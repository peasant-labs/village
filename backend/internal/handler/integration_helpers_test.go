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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/sessionorigin"
	"github.com/peasant-labs/village/backend/internal/storage"
)

// rewriteFixtureSourceIdentity obtains the same authenticated source identity
// the mounted read passes to its optional rewrite.
func rewriteFixtureSourceIdentity(t *testing.T, ctx context.Context, blobs storage.TranscriptBlobStore, row sqlc.Transcript) storage.ContentIdentity {
	t.Helper()
	descriptor, err := descriptorFromTranscript(row)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := identityFromTranscript(row)
	if err != nil {
		t.Fatal(err)
	}
	_, identity, err := blobs.Read(ctx, uuidFromPg(row.ID), descriptor, loaded)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

// govTestPool opens a migrated pool or skips when no DB is reachable.
func govTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	cfg, err := database.PoolConfig(pullTestDatabaseURL(t))
	if err != nil {
		t.Skipf("cannot parse the test database URL (%v); set TEST_DATABASE_URL", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
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

// execFixtureIdentity writes a stored identity onto an existing row for a test
// that needs one particular value, including a value no writer could produce.
// The revision transition guard refuses exactly this (an in-place identity
// change), so the statement runs with triggers disabled for its one transaction
// (session_replication_role = replica, which needs the superuser the test
// service uses). Fixture state is not a writer path; the guard has its own
// cases in the database package.
func execFixtureIdentity(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Errorf("execFixtureIdentity: begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL session_replication_role = 'replica'"); err != nil {
		t.Errorf("execFixtureIdentity: disable triggers for the fixture write (the test role must be a superuser): %v", err)
		return
	}
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		t.Errorf("execFixtureIdentity: %s: %v", sql, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("execFixtureIdentity: commit: %v", err)
	}
}

// swapFixtureDescriptor replaces a row's stored descriptor and identity with
// another one, in either direction, for a test that serves stored content a
// writer would never leave behind. The revision transition guard refuses it
// (a different identity must advance the revision, and a revert would lower it),
// so it runs with triggers disabled, as execFixtureIdentity does. It matches the
// row on its current object key and reports a miss.
func swapFixtureDescriptor(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id pgtype.UUID, from, to sqlc.Transcript) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("swapFixtureDescriptor: begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL session_replication_role = 'replica'"); err != nil {
		t.Fatalf("swapFixtureDescriptor: disable triggers for the fixture write (the test role must be a superuser): %v", err)
	}
	tag, err := tx.Exec(ctx, `UPDATE transcripts SET blob_key = $3, wrapped_data_key = $4, encryption_algorithm = $5, key_version = $6,
			content_hash = $7, blob_size_bytes = $8, content_revision = $9,
			segmented_identity_hash = $10, segmented_identity_size = $11
		WHERE id = $1 AND blob_key = $2`,
		id, from.BlobKey, to.BlobKey, to.WrappedDataKey, to.EncryptionAlgorithm, to.KeyVersion,
		to.ContentHash, to.BlobSizeBytes, to.ContentRevision, to.SegmentedIdentityHash, to.SegmentedIdentitySize)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("swapFixtureDescriptor: rows=%d err=%v", tag.RowsAffected(), err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("swapFixtureDescriptor: commit: %v", err)
	}
}

// putTranscriptContent stores body under key in the fake blob store AND records
// body's real identity (content hash and plaintext size) on the transcript row
// that the key backs, as the system actor under the transcript writer fence.
// The production read compares the bytes it loads with the identity the row
// stores, so a fixture that seeds a synthetic hash or size next to different
// content is refused as an integrity failure. Rows seeded with a stand-in
// identity must therefore be given the identity of the content they serve.
func putTranscriptContent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, blobs *fakeBlobStore, transcriptID pgtype.UUID, key string, body []byte) {
	t.Helper()
	blobs.put(key, body)
	execFixtureIdentity(t, ctx, pool,
		`UPDATE transcripts SET content_hash = $2, blob_size_bytes = $3 WHERE id = $1`,
		transcriptID, schema.ComputeTranscriptHash(body), int64(len(body)))
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

// readTranscriptVisibility reads one transcript's stored visibility.
func readTranscriptVisibility(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id pgtype.UUID) string {
	t.Helper()
	var visibility string
	if err := pool.QueryRow(ctx, `SELECT visibility FROM transcripts WHERE id = $1`, id).Scan(&visibility); err != nil {
		t.Fatalf("read the visibility of transcript %s: %v", uuid.UUID(id.Bytes), err)
	}
	return visibility
}

// waitForSessionBlockedBy blocks until PostgreSQL shows a session waiting on the
// given backend, so a race stage is observed on the exact lock the test holds
// rather than on any lock some other session in the cluster is waiting for.
func waitForSessionBlockedBy(t *testing.T, ctx context.Context, pool *pgxpool.Pool, blocker int32) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))`, blocker).Scan(&waiting); err != nil {
			t.Fatalf("look for a session blocked by backend %d: %v", blocker, err)
		}
		if waiting > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no session ever waited on backend %d, so the race this test describes never happened and it would prove nothing", blocker)
}

// backendPID is the PostgreSQL backend a pinned connection talks to.
func backendPID(t *testing.T, ctx context.Context, conn *pgxpool.Conn) int32 {
	t.Helper()
	var pid int32
	if err := conn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("read the backend of the lock-holding connection: %v", err)
	}
	return pid
}
