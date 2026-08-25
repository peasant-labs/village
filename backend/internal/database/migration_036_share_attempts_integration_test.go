//go:build integration

package database

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These cases run against a real PostgreSQL because every one of them is a
// property of the database, not of Go: a share that existed before the attempt
// model must survive as its first attempt, and the writer fence must refuse
// each of the three verbs it claims to cover. A Go test computing its own
// expected values would pass with no trigger installed at all.

// TestShareAttemptBackfillPreservesExistingShares proves a share published
// before the attempt model becomes attempt 1 carrying its own status, so
// counters are correct for pre-existing data from the first deploy.
func TestShareAttemptBackfillPreservesExistingShares(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	migrateTestDatabaseThrough(t, pool, migrationBoundary035)

	owner := insertShareFixtureOwner(t, ctx, pool)
	group := insertShareFixtureGroup(t, ctx, pool, owner)

	// One share per status the pre-attempt table could hold.
	existing := map[string]string{}
	for _, status := range []string{"pending", "approved", "rejected"} {
		transcript := insertShareFixtureTranscript(t, ctx, pool, owner)
		existing[status] = transcript
		if _, err := pool.Exec(ctx,
			`INSERT INTO transcript_shares (transcript_id, group_id, status) VALUES ($1,$2,$3)`,
			transcript, group, status); err != nil {
			t.Fatalf("seed a pre-attempt share in state %q: %v", status, err)
		}
	}

	if err := runMigration(pool, requireMigrationVersion(t, 36)); err != nil {
		t.Fatalf("apply the share-attempt migration over the boundary before it: %v", err)
	}

	for status, transcript := range existing {
		var attemptNo int
		var backfilled string
		if err := pool.QueryRow(ctx,
			`SELECT attempt_no, status FROM transcript_share_attempts WHERE transcript_id=$1 AND group_id=$2`,
			transcript, group).Scan(&attemptNo, &backfilled); err != nil {
			t.Fatalf("read back the backfilled attempt for a %q share: %v", status, err)
		}
		if attemptNo != 1 || backfilled != status {
			t.Fatalf("a pre-existing %q share became attempt %d in state %q, want attempt 1 in state %q; "+
				"pre-attempt history would otherwise be lost or misreported", status, attemptNo, backfilled, status)
		}
		// The current-state row must be untouched by the backfill: it was
		// already correct, and the derivation has nothing to change.
		var derived string
		if err := pool.QueryRow(ctx,
			`SELECT status FROM transcript_shares WHERE transcript_id=$1 AND group_id=$2`,
			transcript, group).Scan(&derived); err != nil {
			t.Fatalf("read back the current-state row for a %q share: %v", status, err)
		}
		if derived != status {
			t.Fatalf("the backfill changed a %q share's current-state row to %q", status, derived)
		}
	}
}

// TestTranscriptSharesWriterFenceRefusesEveryVerb proves the fail-closed guard
// against a direct INSERT, a direct UPDATE and a direct DELETE separately. A
// single-verb proof would pass a trigger accidentally scoped to that one verb.
func TestTranscriptSharesWriterFenceRefusesEveryVerb(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)

	owner := insertShareFixtureOwner(t, ctx, pool)
	group := insertShareFixtureGroup(t, ctx, pool, owner)
	transcript := insertShareFixtureTranscript(t, ctx, pool, owner)

	// A row to attack with UPDATE and DELETE, produced the only sanctioned
	// way: by opening an attempt and letting the derivation write it.
	if _, err := pool.Exec(ctx,
		`INSERT INTO transcript_share_attempts (transcript_id, group_id, attempt_no, status) VALUES ($1,$2,1,'approved')`,
		transcript, group); err != nil {
		t.Fatalf("open the attempt whose derivation produces the current-state row: %v", err)
	}

	other := insertShareFixtureTranscript(t, ctx, pool, owner)
	for _, probe := range []struct {
		verb      string
		statement string
		args      []any
	}{
		{"INSERT", `INSERT INTO transcript_shares (transcript_id, group_id, status) VALUES ($1,$2,'approved')`, []any{other, group}},
		{"UPDATE", `UPDATE transcript_shares SET status='rejected' WHERE transcript_id=$1 AND group_id=$2`, []any{transcript, group}},
		{"DELETE", `DELETE FROM transcript_shares WHERE transcript_id=$1 AND group_id=$2`, []any{transcript, group}},
	} {
		_, err := pool.Exec(ctx, probe.statement, probe.args...)
		if err == nil {
			t.Errorf("a direct %s on transcript_shares succeeded; the writer fence must refuse all three verbs, and a guard "+
				"scoped to only some of them leaves the derived row writable behind the attempt history", probe.verb)
			continue
		}
		if !strings.Contains(err.Error(), "transcript_shares is derived, not written") {
			t.Errorf("a direct %s failed with %v, which is not the writer fence; the refusal must name what to do instead", probe.verb, err)
		}
	}

	// The one non-derivation write that must still succeed: a foreign-key
	// cascade. Deleting the transcript removes its share rows with no
	// application code in the loop, and blocking that would block account
	// deletion.
	if _, err := pool.Exec(ctx, `SELECT set_config('app.actor_id',$1,true)`, SystemActorID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)`, SystemActorID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM transcripts WHERE id=$1`, transcript); err != nil {
		t.Fatalf("deleting a transcript must still cascade its share rows away, but it was refused: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the cascade proof: %v", err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcript_shares WHERE transcript_id=$1`, transcript).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("the cascade left %d share rows behind", remaining)
	}
}

func insertShareFixtureOwner(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	handle := "share-fixture-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (github_id, github_username, provider_user_id) VALUES ($1,$2,$3) RETURNING id::text`,
		int64(uuid.New().ID()), handle, handle).Scan(&id); err != nil {
		t.Fatalf("insert share fixture owner: %v", err)
	}
	return id
}

func insertShareFixtureGroup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO groups (name, created_by) VALUES ($1,$2) RETURNING id::text`,
		"share-fixture-"+uuid.NewString(), owner).Scan(&id); err != nil {
		t.Fatalf("insert share fixture collective: %v", err)
	}
	return id
}

// insertShareFixtureTranscript writes a transcript the way production does:
// inside one transaction carrying the actor and writer-version markers the
// audit and encryption fences require.
func insertShareFixtureTranscript(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner string) string {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)`, SystemActorID); err != nil {
		t.Fatal(err)
	}
	local := uuid.NewString()
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO transcripts (owner_id, local_id, visibility, model_provider, blob_key, schema_version,
		                         wrapped_data_key, encryption_algorithm, key_version, project_hash)
		VALUES ($1,$2,'shared','claude-code',$3,'1',decode('01','hex'),'aes-256-gcm-random-nonce-v1',1,$4)
		RETURNING id::text`, owner, local, "transcripts/"+local+".bin", "a1b2c3d4e5f6").Scan(&id); err != nil {
		t.Fatalf("insert share fixture transcript: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}
