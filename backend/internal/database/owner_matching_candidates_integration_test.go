//go:build integration

package database

import (
	"bytes"
	"context"
	_ "embed"
	"io"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/owner_matching_candidates.yaml
var ownerMatchingCandidatesYAML []byte

// requiredOwnerCandidateRowNames is the name manifest for the candidate
// fixture: every row exists to pin one arm of the owner/remote boundary, so
// losing one must name itself rather than shrink a count.
var requiredOwnerCandidateRowNames = []string{
	"a-empty-remote",
	"a-no-session-start",
	"a-null-remote",
	"a-repo-middle-other-project",
	"a-repo-newest",
	"a-repo-oldest",
	"b-other-owner",
}

type candidateOwnerFixture struct {
	Key      string `yaml:"key"`
	GithubID int64  `yaml:"github_id"`
	Username string `yaml:"username"`
}

type candidateRowFixture struct {
	Name         string     `yaml:"name"`
	Owner        string     `yaml:"owner"`
	LocalID      string     `yaml:"local_id"`
	GitRemote    *string    `yaml:"git_remote"`
	GitBranch    string     `yaml:"git_branch"`
	SessionStart *time.Time `yaml:"session_start"`
}

type candidateExpectation struct {
	Order    []string          `yaml:"order"`
	Branches map[string]string `yaml:"branches"`
}

type candidateFixture struct {
	Owners []candidateOwnerFixture         `yaml:"owners"`
	Rows   []candidateRowFixture           `yaml:"rows"`
	Expect map[string]candidateExpectation `yaml:"expect"`
}

func loadCandidateFixture(t *testing.T) candidateFixture {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(ownerMatchingCandidatesYAML))
	decoder.KnownFields(true)
	var fixture candidateFixture
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode strict candidate fixture: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("candidate fixture must contain exactly one YAML document: %v", err)
	}

	seen := map[string]bool{}
	for _, row := range fixture.Rows {
		if row.Name == "" {
			t.Fatal("candidate fixture has a row with an empty name")
		}
		if seen[row.Name] {
			t.Fatalf("candidate fixture repeats row name %q", row.Name)
		}
		seen[row.Name] = true
	}
	declared := map[string]bool{}
	for _, name := range requiredOwnerCandidateRowNames {
		declared[name] = true
	}
	var missing, undeclared []string
	for name := range declared {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	for name := range seen {
		if !declared[name] {
			undeclared = append(undeclared, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(undeclared)
	if len(missing) > 0 {
		t.Fatalf("testdata/owner_matching_candidates.yaml no longer carries %v, which its manifest declares: each "+
			"row guards an arm of the owner/remote boundary. Restore the row under its exact name.", missing)
	}
	if len(undeclared) > 0 {
		t.Fatalf("testdata/owner_matching_candidates.yaml carries %v, which its manifest does not declare: an "+
			"undeclared row is unprotected, so add each new name to the manifest in the same change.", undeclared)
	}
	return fixture
}

// TestListOwnerTranscriptsForMatchingOwnerAndRemoteBoundary proves the query the
// matcher's candidate pool rests on: it returns one owner's rows and no other
// owner's, it drops rows with an empty or missing remote, and it returns them in
// session-start order with unknown last.
//
// Losing the owner predicate would silently widen matching to every user's
// transcripts and nothing else in the suite would notice, which is why this
// runs against real PostgreSQL rather than a stub.
func TestListOwnerTranscriptsForMatchingOwnerAndRemoteBoundary(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)
	queries := sqlc.New(pool)

	fixture := loadCandidateFixture(t)
	ownerIDs := seedCandidateOwners(t, ctx, pool, fixture.Owners)
	rowIDs := seedCandidateTranscripts(t, ctx, pool, fixture, ownerIDs)

	for _, owner := range fixture.Owners {
		want, ok := fixture.Expect[owner.Key]
		if !ok {
			t.Fatalf("candidate fixture declares no expectation for owner %q", owner.Key)
		}

		rows, err := queries.ListOwnerTranscriptsForMatching(ctx, ownerIDs[owner.Key])
		if err != nil {
			t.Fatalf("list candidates for %s: %v", owner.Key, err)
		}

		var gotOrder []string
		for _, row := range rows {
			localID := rowIDs[pgtype.UUID{Bytes: row.ID.Bytes, Valid: row.ID.Valid}]
			gotOrder = append(gotOrder, localID)
			if !row.GitRemote.Valid || row.GitRemote.String == "" {
				t.Errorf("owner %s returned %s with an empty remote; the query must drop those", owner.Key, localID)
			}
			wantBranch, ok := want.Branches[localID]
			if !ok {
				t.Errorf("owner %s returned unexpected row %s", owner.Key, localID)
				continue
			}
			gotBranch := ""
			if row.GitBranch.Valid {
				gotBranch = row.GitBranch.String
			}
			if gotBranch != wantBranch {
				t.Errorf("row %s branch = %q, want %q", localID, gotBranch, wantBranch)
			}
		}

		if len(gotOrder) != len(want.Order) {
			t.Fatalf("owner %s candidates = %v, want %v", owner.Key, gotOrder, want.Order)
		}
		for i := range gotOrder {
			if gotOrder[i] != want.Order[i] {
				t.Fatalf("owner %s candidates = %v, want %v (session start ascending, unknown last)", owner.Key, gotOrder, want.Order)
			}
		}
	}
}

func seedCandidateOwners(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owners []candidateOwnerFixture) map[string]pgtype.UUID {
	t.Helper()
	ids := map[string]pgtype.UUID{}
	for _, owner := range owners {
		var id pgtype.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (github_id, github_username, provider_user_id)
			VALUES ($1, $2, $1::bigint::text) RETURNING id
		`, owner.GithubID, owner.Username).Scan(&id); err != nil {
			t.Fatalf("seed owner %s: %v", owner.Key, err)
		}
		ids[owner.Key] = id
	}
	return ids
}

func seedCandidateTranscripts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture candidateFixture, ownerIDs map[string]pgtype.UUID) map[pgtype.UUID]string {
	t.Helper()
	localIDs := map[pgtype.UUID]string{}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin seed transaction: %v", err)
	}
	defer tx.Rollback(ctx)
	// Transcript inserts are audited by a fail-closed trigger that requires a
	// transaction-local actor, and encrypted storage mutations require the
	// writer-version marker.
	if _, err := tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','1',true), set_config('app.actor_id',$1,true)", SystemActorID); err != nil {
		t.Fatalf("declare system actor and writer version: %v", err)
	}

	for _, row := range fixture.Rows {
		ownerID, ok := ownerIDs[row.Owner]
		if !ok {
			t.Fatalf("row %s names owner %q, which the fixture does not declare", row.Name, row.Owner)
		}
		remote := pgtype.Text{}
		if row.GitRemote != nil {
			remote = pgtype.Text{String: *row.GitRemote, Valid: true}
		}
		branch := pgtype.Text{}
		if row.GitBranch != "" {
			branch = pgtype.Text{String: row.GitBranch, Valid: true}
		}
		sessionStart := pgtype.Timestamptz{}
		if row.SessionStart != nil {
			sessionStart = pgtype.Timestamptz{Time: *row.SessionStart, Valid: true}
		}

		var id pgtype.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO transcripts (owner_id, local_id, model_provider, blob_key, schema_version, project_hash,
			                         wrapped_data_key, encryption_algorithm, key_version, git_remote, git_branch, session_start)
			VALUES ($1, $2, 'claude-code', $3, '2', 'c4e19a2f0b73',
			        decode('01','hex'), 'aes-256-gcm-random-nonce-v1', 1, $4, $5, $6) RETURNING id
		`, ownerID, row.LocalID, "transcripts/"+uuid.NewString()+".bin", remote, branch, sessionStart).Scan(&id); err != nil {
			t.Fatalf("seed transcript %s: %v", row.Name, err)
		}
		localIDs[id] = row.LocalID
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit seed transaction: %v", err)
	}
	return localIDs
}
