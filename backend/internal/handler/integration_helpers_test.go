//go:build integration

package handler

// Shared infrastructure for the handler package's integration tests. It lives in
// its own file (not inside any one suite) because every governance-era test needs
// the same three things:
//
//  1. a migrated pool (govTestPool);
//  2. WRITE fixtures that satisfy the migration-026 FAIL-CLOSED audit triggers —
//     every INSERT / governance-axis UPDATE / DELETE on transcripts must declare
//     app.actor_id or the statement aborts, so fixtures attribute to the reserved
//     SYSTEM actor;
//  3. teardown that works against an APPEND-ONLY audit table — audit rows have no
//     FK cascade, and bare DELETEs on them are blocked by the migration-026
//     block-trigger, so hermetic cleanup must collect transcript ids first,
//     delete the transcripts as the system actor, and purge the audit rows in a
//     transaction that sets the sanctioned app.audit_maintenance escape.
//     SET LOCAL only lives inside an explicit transaction, and pgxpool routes
//     separate Execs to arbitrary conns — the GUC and the statement MUST share
//     one tx. Teardown errors are LOUD (t.Errorf), never swallowed.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/sessionorigin"
)

// govTestPool opens a migrated pool or skips when no DB is reachable.
func govTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, pullTestDatabaseURL(t))
	if err != nil {
		t.Skipf("cannot create pool (%v); set TEST_DATABASE_URL", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("cannot reach test database (%v); set TEST_DATABASE_URL", err)
	}
	if err := database.RunMigrations(pool); err != nil {
		pool.Close()
		t.Fatalf("RunMigrations: %v", err)
	}
	return pool
}

// execAsSystem runs one statement in a transaction attributed to the SYSTEM
// actor — the sanctioned identity for test fixtures and teardown (lifecycle §7).
func execAsSystem(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Errorf("execAsSystem: begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", database.SystemActorID); err != nil {
		t.Errorf("execAsSystem: declare fixed system actor and transcript writer: %v", err)
		return
	}
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		t.Errorf("execAsSystem: %s: %v", sql, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("execAsSystem: commit: %v", err)
	}
}

// purgeAuditRows is the ONLY sanctioned audit-row cleaner: one explicit
// transaction carrying the append-only escape. It doubles as the escape's
// positive test — if the escape breaks, every teardown fails loudly.
func purgeAuditRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, transcriptIDs []pgtype.UUID) {
	t.Helper()
	if len(transcriptIDs) == 0 {
		return
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Errorf("purgeAuditRows: begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL app.audit_maintenance = 'on'"); err != nil {
		t.Errorf("purgeAuditRows: set maintenance escape: %v", err)
		return
	}
	if _, err := tx.Exec(ctx,
		"DELETE FROM transcript_governance_events_audit WHERE transcript_id = ANY($1)", transcriptIDs); err != nil {
		t.Errorf("purgeAuditRows: delete: %v", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("purgeAuditRows: commit: %v", err)
	}
}

// cleanupOwners is the standard governance-test teardown, ordered for the
// fail-closed + append-only world: collect the owners' transcript ids, delete
// the transcripts as the system actor (feeding the retract trigger), delete the
// users (no transcripts remain, so the cascade fires no trigger), and purge the
// audit rows LAST (the deletes just re-appended retracted rows).
func cleanupOwners(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owners ...pgtype.UUID) {
	t.Helper()
	rows, err := pool.Query(ctx, "SELECT id FROM transcripts WHERE owner_id = ANY($1)", owners)
	if err != nil {
		t.Errorf("cleanupOwners: collect transcript ids: %v", err)
		return
	}
	var tids []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Errorf("cleanupOwners: scan transcript id: %v", err)
			return
		}
		tids = append(tids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Errorf("cleanupOwners: iterate transcript ids: %v", err)
		return
	}
	if len(tids) > 0 {
		execAsSystem(t, ctx, pool, "DELETE FROM transcripts WHERE id = ANY($1)", tids)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1)", owners); err != nil {
		t.Errorf("cleanupOwners: delete users: %v", err)
	}
	purgeAuditRows(t, ctx, pool, tids)
}

func completeEncryptedFixtureParams(params sqlc.CreateTranscriptParams) sqlc.CreateTranscriptParams {
	params.ID = toPgUUID(uuid.New())
	params.WrappedDataKey = []byte("fixture-wrapped-data-key")
	params.EncryptionAlgorithm = "aes-256-gcm-random-nonce-v1"
	params.KeyVersion = 1
	params.ContentHash = pgtype.Text{String: schema.ComputeTranscriptHash([]byte(params.LocalID)), Valid: true}
	return params
}

// govStore creates one transcript through the REAL publish-create path
// (inTxAs + CreateTranscript; the migration-026 trigger writes its 'published'
// snapshot), attributed to the owner.
func govStore(t *testing.T, ctx context.Context, h *Handler, owner pgtype.UUID, localID string, lic schema.License) sqlc.Transcript {
	t.Helper()
	req := schema.PublishRequest{
		License:  lic,
		Identity: schema.SessionIdentity{SessionID: schema.SessionID(localID), SchemaVersion: 2},
		Model:    schema.ModelInfo{Harness: "claude-code", Model: "m"},
	}
	params := schemaToTranscriptParams(req, "blob/"+localID, 1, "2", sessionorigin.Unknown)
	params.OwnerID = owner
	params.LocalID = localID
	params = completeEncryptedFixtureParams(params)
	var tr sqlc.Transcript
	err := h.inTxAs(ctx, owner, func(q Querier) error {
		var txErr error
		tr, txErr = q.CreateTranscript(ctx, params)
		return txErr
	})
	if err != nil {
		t.Fatalf("govStore create(%s): %v", localID, err)
	}
	return tr
}
