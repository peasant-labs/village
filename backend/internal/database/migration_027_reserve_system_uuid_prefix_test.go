//go:build integration

package database

// Integration test for migration 027 — the CHECK that fences users.id out of the
// reserved system-identity range 00000000-0000-0000-0000-* (ReservedSystemUUIDPrefix).
//
// Requires a running PostgreSQL instance. Run with:
//
//	TEST_DATABASE_URL="postgres://test:test@localhost:55432/village_test?sslmode=disable" \
//	  go test -tags=integration ./internal/database/...
//
// Every mutation runs inside a transaction rolled back on cleanup, so the test is
// hermetic. The reserved-prefix insert aborts its own transaction (a CHECK
// violation), so non-persistence is confirmed with a separate autocommit pool
// query rather than a further statement on the aborted transaction.

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigration027_ReservesSystemUUIDPrefix(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, migration023TestDatabaseURL(t))
	if err != nil {
		t.Skipf("no test database available: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("test database not reachable: %v", err)
	}
	migrateTestDatabaseThrough(t, pool, migrationBoundary027)

	const reservedID = "00000000-0000-0000-0000-000000000001"

	// (1) must-reject: an explicit id inside the reserved system prefix violates
	// the CHECK, and the row is never persisted.
	t.Run("reserved-prefix id rejected", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}
		defer tx.Rollback(ctx)
		_, err = tx.Exec(ctx, `
			INSERT INTO users (id, github_id, github_username, provider_user_id)
			VALUES ($1, $2, $3, $4)
		`, reservedID, int64(927101), "user027-reserved", "927101")
		if err == nil {
			t.Fatal("expected a CHECK violation inserting a user id in the reserved system prefix, got success")
		}
		if !strings.Contains(err.Error(), "users_id_not_system_reserved") {
			t.Fatalf("expected the users_id_not_system_reserved CHECK violation, got: %v", err)
		}
		_ = tx.Rollback(ctx)

		// Independent autocommit conn: the reserved id must not exist anywhere.
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE id = $1", reservedID).Scan(&n); err != nil {
			t.Fatalf("count reserved-id rows: %v", err)
		}
		if n != 0 {
			t.Fatalf("reserved-prefix user id must not be persisted, found %d row(s)", n)
		}
	})

	// (2) must-accept (default): a normal gen_random_uuid() id is v4 — structurally
	// outside the reserved prefix — and inserts fine.
	t.Run("default gen_random_uuid id accepted", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}
		defer tx.Rollback(ctx)
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO users (github_id, github_username, provider_user_id)
			VALUES ($1, $2, $3) RETURNING id::text
		`, int64(927102), "user027-default", "927102").Scan(&id); err != nil {
			t.Fatalf("insert default-id user: %v", err)
		}
		if strings.HasPrefix(strings.ToLower(id), ReservedSystemUUIDPrefix) {
			t.Fatalf("gen_random_uuid() minted an id in the reserved prefix: %q", id)
		}
	})

	// (3) must-accept (explicit non-reserved): the real explicit-id path
	// (scripts/seed.sql uses a0000000-* ids) is outside the reserved prefix and
	// inserts fine — the CHECK fences only the reserved prefix, not all explicit ids.
	t.Run("explicit non-reserved id accepted", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}
		defer tx.Rollback(ctx)
		const explicitID = "a0000000-0000-0000-0000-000000000027"
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO users (id, github_id, github_username, provider_user_id)
			VALUES ($1, $2, $3, $4) RETURNING id::text
		`, explicitID, int64(927103), "user027-explicit", "927103").Scan(&id); err != nil {
			t.Fatalf("insert explicit non-reserved id user: %v", err)
		}
		if id != explicitID {
			t.Fatalf("explicit id round-trip = %q, want %q", id, explicitID)
		}
	})

	// (4) system stays valid (defensive, low falsification): the reservation fences
	// users.id, NOT system-actor governance writes. A transcript published while
	// attributed to database.SystemActorID still succeeds and its audit row carries
	// the system actor — SystemActorID is not a users row, and the audit changed_by
	// column has no CHECK. This guards against a mis-scoped CHECK landing on the
	// audit table by mistake; it is not a strong falsifier of the users.id fence.
	t.Run("system-actor governance write still valid", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}
		defer tx.Rollback(ctx)
		ownerID := insertUserForLicense(t, ctx, tx, 927104, "user027-owner")
		tid := insertTranscript(t, ctx, tx, ownerID, "local-027", "claude-code", "transcripts/x/y/027.json")
		var changedBy string
		if err := tx.QueryRow(ctx, `
			SELECT changed_by::text FROM transcript_governance_events_audit
			WHERE transcript_id = $1 ORDER BY seq DESC LIMIT 1
		`, tid).Scan(&changedBy); err != nil {
			t.Fatalf("read published audit row for system-actor write: %v", err)
		}
		if changedBy != SystemActorID {
			t.Fatalf("system-actor publish changed_by = %q, want %q", changedBy, SystemActorID)
		}
	})
}
