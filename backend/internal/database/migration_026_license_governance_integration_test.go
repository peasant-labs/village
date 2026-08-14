//go:build integration

package database

// Integration test for migration 026 — licenses reference table, nullable
// transcripts.license_id FK, governance_event_types taxonomy, and the
// FAIL-CLOSED, trigger-written, append-only transcript_governance_events_audit.
//
// Requires a running PostgreSQL instance. Run with:
//
//	TEST_DATABASE_URL="postgres://test:test@localhost:5432/village_test?sslmode=disable" \
//	  go test -tags=integration ./internal/database/...
//
// Mutations run inside a transaction rolled back on cleanup, so the test is
// hermetic (the audit rows written by the triggers roll back with it). Fixture
// INSERTs declare the system actor on this transaction (insertTranscript does it;
// the fail-closed triggers reject anonymous mutations by design). Statements that
// MUST fail run inside savepoints (tx.Begin on a pgx.Tx) so the outer transaction
// survives them.

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/schema"
)

func TestMigration026_AppliesLicenseGovernance(t *testing.T) {
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

	migrateTestDatabaseThrough(t, pool, migrationBoundary026)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback(ctx)

	// (1) The license menu is seeded and every row's FULL obligation tuple is
	// pinned. These village-owned literals drive the future join-consent screen —
	// a wrong boolean is a wrong legal UX. The length check forces this table to
	// grow in the same commit as schema.AllLicenses.
	wantObligations := map[schema.License]struct {
		attribution, shareAlike, commercial bool
	}{
		schema.LicenseCC0:    {attribution: false, shareAlike: false, commercial: true},
		schema.LicenseCCBY:   {attribution: true, shareAlike: false, commercial: true},
		schema.LicenseCCBYSA: {attribution: true, shareAlike: true, commercial: true},
	}
	if len(wantObligations) != len(schema.AllLicenses) {
		t.Fatalf("obligation pinning incomplete: %d rows for %d licenses — add the new license's expected obligations",
			len(wantObligations), len(schema.AllLicenses))
	}
	var n int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM licenses").Scan(&n); err != nil {
		t.Fatalf("count licenses: %v", err)
	}
	if n != len(schema.AllLicenses) {
		t.Fatalf("licenses seeded = %d, want %d", n, len(schema.AllLicenses))
	}
	for lic, want := range wantObligations {
		var attribution, shareAlike, commercial bool
		if err := tx.QueryRow(ctx,
			"SELECT attribution_required, share_alike, commercial_ok FROM licenses WHERE id = $1", string(lic)).
			Scan(&attribution, &shareAlike, &commercial); err != nil {
			t.Fatalf("read %s obligations: %v", lic, err)
		}
		if attribution != want.attribution || shareAlike != want.shareAlike || commercial != want.commercial {
			t.Fatalf("%s obligations = (attribution=%v, share_alike=%v, commercial=%v), want (%v, %v, %v)",
				lic, attribution, shareAlike, commercial, want.attribution, want.shareAlike, want.commercial)
		}
	}
	// permissiveness_rank is gone by design (collective license is decided +
	// consented, never computed from a scalar order).
	var rankCols int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'licenses' AND column_name = 'permissiveness_rank'
	`).Scan(&rankCols); err != nil {
		t.Fatalf("check permissiveness_rank absence: %v", err)
	}
	if rankCols != 0 {
		t.Fatal("licenses.permissiveness_rank must not exist (superseded by decided+consented model)")
	}

	// (1a) Seed id-set == schema.AllLicenses EXACTLY — the cross-repo drift guard.
	licRows, err := tx.Query(ctx, "SELECT id FROM licenses ORDER BY id")
	if err != nil {
		t.Fatalf("read licenses: %v", err)
	}
	var seededLic []string
	for licRows.Next() {
		var id string
		if err := licRows.Scan(&id); err != nil {
			licRows.Close()
			t.Fatalf("scan license id: %v", err)
		}
		seededLic = append(seededLic, id)
	}
	licRows.Close()
	if err := licRows.Err(); err != nil {
		t.Fatalf("iterate licenses: %v", err)
	}
	wantLic := make([]string, len(schema.AllLicenses))
	for i, l := range schema.AllLicenses {
		wantLic[i] = string(l)
	}
	slices.Sort(seededLic)
	slices.Sort(wantLic)
	if !slices.Equal(seededLic, wantLic) {
		t.Fatalf("licenses seed drift: seeded %v, schema.AllLicenses %v", seededLic, wantLic)
	}

	// (1b) Event-type taxonomy seed == AllGovernanceEventTypes EXACTLY.
	rows, err := tx.Query(ctx, "SELECT id FROM governance_event_types ORDER BY id")
	if err != nil {
		t.Fatalf("read governance_event_types: %v", err)
	}
	var seeded []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatalf("scan governance_event_type id: %v", err)
		}
		seeded = append(seeded, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate governance_event_types: %v", err)
	}
	want := make([]string, len(AllGovernanceEventTypes))
	for i, e := range AllGovernanceEventTypes {
		want[i] = string(e)
	}
	slices.Sort(want)
	if !slices.Equal(seeded, want) {
		t.Fatalf("governance_event_types drift: seeded %v, AllGovernanceEventTypes %v", seeded, want)
	}

	// (2) PUBLISH semantics: inserting a transcript (system actor declared by the
	// fixture helper) auto-appends exactly one 'published' snapshot — the audit
	// has NO application writer; the trigger is the only source.
	ownerID := insertUserForLicense(t, ctx, tx, 926001, "user026")
	tid := insertTranscript(t, ctx, tx, ownerID, "local-026", "claude-code", "transcripts/x/y/transcript.json")

	var evType, evActor string
	var evLicense *string
	if err := tx.QueryRow(ctx, `
		SELECT event_type, license_id, changed_by::text
		FROM transcript_governance_events_audit WHERE transcript_id = $1 ORDER BY seq DESC LIMIT 1
	`, tid).Scan(&evType, &evLicense, &evActor); err != nil {
		t.Fatalf("read published event: %v", err)
	}
	if evType != "published" || evLicense != nil {
		t.Fatalf("publish audit = (event=%q, license=%v), want (published, NULL)", evType, evLicense)
	}
	if evActor != SystemActorID {
		t.Fatalf("publish changed_by = %q, want system actor %q", evActor, SystemActorID)
	}

	var initial *string
	if err := tx.QueryRow(ctx, "SELECT license_id FROM transcripts WHERE id = $1", tid).Scan(&initial); err != nil {
		t.Fatalf("read initial license_id: %v", err)
	}
	if initial != nil {
		t.Fatalf("new transcript license_id = %q, want NULL", *initial)
	}

	// (3) UPDATE classification: license-only → license_changed; visibility-only →
	// visibility_changed; BOTH in one statement → ONE governance_changed row with
	// the full post-change snapshot; title-only → WHEN-false, no row at all.
	countEvents := func() int {
		t.Helper()
		var c int
		if err := tx.QueryRow(ctx,
			"SELECT count(*) FROM transcript_governance_events_audit WHERE transcript_id = $1", tid).Scan(&c); err != nil {
			t.Fatalf("count audit events: %v", err)
		}
		return c
	}
	latest := func() (event string, license *string, visibility string) {
		t.Helper()
		if err := tx.QueryRow(ctx, `
			SELECT event_type, license_id, visibility FROM transcript_governance_events_audit
			WHERE transcript_id = $1 ORDER BY seq DESC LIMIT 1
		`, tid).Scan(&event, &license, &visibility); err != nil {
			t.Fatalf("read latest audit event: %v", err)
		}
		return
	}

	if _, err := tx.Exec(ctx, "UPDATE transcripts SET license_id = 'CC-BY-4.0' WHERE id = $1", tid); err != nil {
		t.Fatalf("license-only update: %v", err)
	}
	if ev, lic, _ := latest(); ev != "license_changed" || lic == nil || *lic != "CC-BY-4.0" {
		t.Fatalf("license-only update audit = (%q, %v), want (license_changed, CC-BY-4.0)", ev, lic)
	}
	if _, err := tx.Exec(ctx, "UPDATE transcripts SET visibility = 'public' WHERE id = $1", tid); err != nil {
		t.Fatalf("visibility-only update: %v", err)
	}
	if ev, _, vis := latest(); ev != "visibility_changed" || vis != "public" {
		t.Fatalf("visibility-only update audit = (%q, %q), want (visibility_changed, public)", ev, vis)
	}
	before := countEvents()
	if _, err := tx.Exec(ctx,
		"UPDATE transcripts SET license_id = 'CC0-1.0', visibility = 'shared' WHERE id = $1", tid); err != nil {
		t.Fatalf("both-axes update: %v", err)
	}
	if got := countEvents(); got != before+1 {
		t.Fatalf("both-axes update must append exactly ONE row (governance_changed): %d -> %d", before, got)
	}
	if ev, lic, vis := latest(); ev != "governance_changed" || lic == nil || *lic != "CC0-1.0" || vis != "shared" {
		t.Fatalf("both-axes audit = (%q, %v, %q), want (governance_changed, CC0-1.0, shared)", ev, lic, vis)
	}
	before = countEvents()
	if _, err := tx.Exec(ctx, "UPDATE transcripts SET title = 'renamed' WHERE id = $1", tid); err != nil {
		t.Fatalf("title-only update: %v", err)
	}
	if got := countEvents(); got != before {
		t.Fatalf("title-only update must be WHEN-false (no audit row): %d -> %d", before, got)
	}

	// (3b) Same-instant appends: seq is the order, not effective_at (txn time).
	// Direct INSERTs into the audit table remain legal (append-only ≠ insert-blocked).
	var seq1, seq2 int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO transcript_governance_events_audit (transcript_id, event_type, license_id, visibility, changed_by)
		VALUES ($1, 'license_changed', 'CC-BY-4.0', 'private', $2) RETURNING seq
	`, tid, ownerID).Scan(&seq1); err != nil {
		t.Fatalf("append second same-instant event: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO transcript_governance_events_audit (transcript_id, event_type, license_id, visibility, changed_by)
		VALUES ($1, 'visibility_changed', 'CC-BY-SA-4.0', 'shared', $2) RETURNING seq
	`, tid, ownerID).Scan(&seq2); err != nil {
		t.Fatalf("append third same-instant event (UNIQUE(transcript_id, effective_at) regression?): %v", err)
	}
	if seq2 <= seq1 {
		t.Fatalf("seq must be monotonic across appends: got seq1=%d, seq2=%d", seq1, seq2)
	}

	// (4) RETRACT semantics — the deletion-survival keystone. The deleting txn's
	// GUC actor is stamped; the row PERSISTS after the transcript is gone.
	tid2 := insertTranscript(t, ctx, tx, ownerID, "local-026-del", "claude-code", "transcripts/x/y/del.json")
	if _, err := tx.Exec(ctx, "UPDATE transcripts SET license_id = 'CC-BY-4.0', visibility = 'public' WHERE id = $1", tid2); err != nil {
		t.Fatalf("set tid2 governance state: %v", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM transcripts WHERE id = $1", tid2); err != nil {
		t.Fatalf("delete tid2: %v", err)
	}
	var retEvent, retVis, retChangedBy string
	var retLicense *string
	if err := tx.QueryRow(ctx, `
		SELECT event_type, license_id, visibility, changed_by::text
		FROM transcript_governance_events_audit WHERE transcript_id = $1 ORDER BY seq DESC LIMIT 1
	`, tid2).Scan(&retEvent, &retLicense, &retVis, &retChangedBy); err != nil {
		t.Fatalf("read auto-retracted event: %v", err)
	}
	if retEvent != "retracted" || retVis != "public" || retLicense == nil || *retLicense != string(schema.LicenseCCBY) {
		t.Fatalf("trigger retracted snapshot = (event=%q, license=%v, visibility=%q), want (retracted, CC-BY-4.0, public)",
			retEvent, retLicense, retVis)
	}
	if retChangedBy != SystemActorID {
		t.Fatalf("retracted changed_by = %q, want the txn GUC actor %q", retChangedBy, SystemActorID)
	}

	// (4b) Explicit per-action actor: overriding the GUC mid-txn attributes
	// subsequent events to the new actor (the inTxAs path per authenticated user).
	const actor = "00000000-0000-0000-0000-0000000000aa"
	tid3 := insertTranscript(t, ctx, tx, ownerID, "local-026-del2", "claude-code", "transcripts/x/y/del2.json")
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true)", actor); err != nil {
		t.Fatalf("set app.actor_id: %v", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM transcripts WHERE id = $1", tid3); err != nil {
		t.Fatalf("delete tid3: %v", err)
	}
	var changedBy3 string
	if err := tx.QueryRow(ctx, `
		SELECT changed_by::text FROM transcript_governance_events_audit
		WHERE transcript_id = $1 AND event_type = 'retracted'
	`, tid3).Scan(&changedBy3); err != nil {
		t.Fatalf("read tid3 retracted event: %v", err)
	}
	if changedBy3 != actor {
		t.Fatalf("retracted changed_by (explicit GUC) = %q, want %q", changedBy3, actor)
	}

	// (5) DeleteAccount cascade: deleting the OWNING USER cascades the transcripts
	// (migration 010) with no per-transcript application code — the trigger stamps
	// 'retracted' for each, attributed to the deleting txn's actor, and the rows
	// outlive both the transcripts AND the user (no FK anywhere on the audit).
	if _, err := tx.Exec(ctx, "DELETE FROM users WHERE id = $1", ownerID); err != nil {
		t.Fatalf("delete owning user (cascade): %v", err)
	}
	var cascadeRet int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM transcript_governance_events_audit
		WHERE transcript_id = $1 AND event_type = 'retracted'
	`, tid).Scan(&cascadeRet); err != nil {
		t.Fatalf("count cascade-retracted events: %v", err)
	}
	if cascadeRet != 1 {
		t.Fatalf("account-deletion cascade must stamp exactly one retracted event for tid: got %d", cascadeRet)
	}

	// (6) FAIL-CLOSED attribution: with the actor GUC CLEARED, every
	// governance-relevant mutation aborts — INSERT (publish trigger has no WHEN
	// clause), governance-axis UPDATE, and DELETE. A title-only UPDATE still
	// succeeds (WHEN-false: the trigger never runs, so no actor is needed) —
	// asserting the suppression is structural, not actor-dependent. Each failing
	// statement runs in a savepoint so the outer tx survives.
	owner2 := insertUserForLicense(t, ctx, tx, 926002, "user026b")
	tid4 := insertTranscript(t, ctx, tx, owner2, "local-026-fc", "claude-code", "transcripts/x/y/fc.json")
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', '', true)"); err != nil {
		t.Fatalf("clear app.actor_id: %v", err)
	}
	expectFailClosed := func(name, sql string, args ...any) {
		t.Helper()
		sp, err := tx.Begin(ctx)
		if err != nil {
			t.Fatalf("%s: savepoint: %v", name, err)
		}
		_, err = sp.Exec(ctx, sql, args...)
		_ = sp.Rollback(ctx)
		if err == nil {
			t.Fatalf("%s: expected fail-closed abort without app.actor_id, got success", name)
		}
		if !strings.Contains(err.Error(), "governance audit requires app.actor_id") {
			t.Fatalf("%s: expected the fail-closed error, got: %v", name, err)
		}
	}
	expectFailClosed("anonymous INSERT",
		`INSERT INTO transcripts (owner_id, local_id, title, model_provider, model_name, blob_key, schema_version)
		 VALUES ($1, 'local-026-anon', 't', 'claude-code', 'm', 'transcripts/x/y/anon.json', '2')`, owner2)
	// NB: the update must actually MOVE the axis — new transcripts default to
	// visibility='private', and a private→private update is WHEN-false (no
	// trigger, no actor needed). This confirms the fail-closed path against the live trigger.
	expectFailClosed("anonymous governance-axis UPDATE",
		"UPDATE transcripts SET visibility = 'public' WHERE id = $1", tid4)
	expectFailClosed("anonymous DELETE",
		"DELETE FROM transcripts WHERE id = $1", tid4)
	if _, err := tx.Exec(ctx, "UPDATE transcripts SET title = 'still-anonymous-ok' WHERE id = $1", tid4); err != nil {
		t.Fatalf("title-only update must not require an actor (WHEN-false): %v", err)
	}

	// (7) APPEND-ONLY enforcement: UPDATE/DELETE on the audit table are blocked
	// for everyone — including the table owner this test connects as — unless the
	// transaction deliberately sets the maintenance escape.
	expectBlocked := func(name, sql string, args ...any) {
		t.Helper()
		sp, err := tx.Begin(ctx)
		if err != nil {
			t.Fatalf("%s: savepoint: %v", name, err)
		}
		_, err = sp.Exec(ctx, sql, args...)
		_ = sp.Rollback(ctx)
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("%s: expected append-only block, got: %v", name, err)
		}
	}
	expectBlocked("audit UPDATE",
		"UPDATE transcript_governance_events_audit SET visibility = 'private' WHERE transcript_id = $1", tid)
	expectBlocked("audit DELETE",
		"DELETE FROM transcript_governance_events_audit WHERE transcript_id = $1", tid)
	// Sanctioned maintenance inside a savepoint: escape works, then rolls back.
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("maintenance savepoint: %v", err)
	}
	if _, err := sp.Exec(ctx, "SET LOCAL app.audit_maintenance = 'on'"); err != nil {
		t.Fatalf("set maintenance escape: %v", err)
	}
	if _, err := sp.Exec(ctx,
		"DELETE FROM transcript_governance_events_audit WHERE transcript_id = $1", tid); err != nil {
		t.Fatalf("sanctioned maintenance DELETE must succeed: %v", err)
	}
	if err := sp.Rollback(ctx); err != nil {
		t.Fatalf("rollback maintenance savepoint: %v", err)
	}

	// (8) The license_id FK enforces the closed menu (savepoint: the violation
	// aborts only the subtransaction). Restore an actor first so the statement
	// reaches the FK check rather than the fail-closed gate.
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true)", SystemActorID); err != nil {
		t.Fatalf("restore actor: %v", err)
	}
	spFK, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("fk savepoint: %v", err)
	}
	if _, err := spFK.Exec(ctx, "UPDATE transcripts SET license_id = 'NOPE-1.0' WHERE id = $1", tid4); err == nil {
		t.Fatal("expected FK violation setting license_id to an unknown license, got nil")
	}
	_ = spFK.Rollback(ctx)
}

// insertUserForLicense inserts a distinct throwaway user per (githubID, name).
// provider_user_id is NOT NULL (migration 015); github_id is BIGINT UNIQUE.
func insertUserForLicense(t *testing.T, ctx context.Context, tx pgx.Tx, githubID int64, name string) string {
	t.Helper()
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id)
		VALUES ($1, $2, $3) RETURNING id::text
	`, githubID, name, name).Scan(&id)
	if err != nil {
		t.Fatalf("insert user %s: %v", name, err)
	}
	return id
}
