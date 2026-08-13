//go:build integration

package handler

// Integration tests for the governance audit as the HANDLER package experiences
// it after migration 026: there is NO application audit writer — the DB triggers
// write every event — so what these tests pin is the handler-side plumbing and
// semantics on real Postgres:
//
//   - inTxAs carries the authenticated actor into every mutation (fail-closed
//     triggers make forgetting it an error, not a mis-attribution);
//   - the publish-create path yields exactly one 'published' snapshot;
//   - applyMetadataPatch resolves partial patches against the LOCKED narrow
//     pre-image (lost-update fix) and the trigger classifies each transition;
//   - re-publish pins visibility and preserves an absent license (clobber fix);
//   - the share flip and the delete/account-cascade paths are audited end to end.
//
//	TEST_DATABASE_URL="postgres://test:test@localhost:5432/village_test?sslmode=disable" \
//	  go test -tags=integration ./internal/handler/...

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

func TestPublishCreate_WritesPublishedEvent_RealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	owner := pullInsertUser(t, ctx, pool, 980201, "gov-pub-owner")
	defer cleanupOwners(t, ctx, pool, owner)

	h := &Handler{pool: pool, queries: sqlc.New(pool)}
	tr := govStore(t, ctx, h, owner, "gov-pub-1", schema.LicenseCCBY)

	// Exactly one event was written, atomically with the transcript (same txn:
	// the trigger runs inside inTxAs's transaction by construction).
	var count int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM transcript_governance_events_audit WHERE transcript_id = $1", tr.ID).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 1 {
		t.Fatalf("published event count = %d, want exactly 1 (atomic with create)", count)
	}

	// And it is the 'published' snapshot of the persisted row (license +
	// visibility), stamped with the publisher as the actor.
	var event, vis, changedBy string
	var lic *string
	if err := pool.QueryRow(ctx, `
		SELECT event_type, license_id, visibility, changed_by::text
		FROM transcript_governance_events_audit WHERE transcript_id = $1
	`, tr.ID).Scan(&event, &lic, &vis, &changedBy); err != nil {
		t.Fatalf("read published event: %v", err)
	}
	if event != string(database.EventPublished) {
		t.Errorf("event_type = %q, want %q", event, database.EventPublished)
	}
	if lic == nil || *lic != string(schema.LicenseCCBY) {
		t.Errorf("published license = %v, want %q", lic, schema.LicenseCCBY)
	}
	if vis != DefaultVisibility {
		t.Errorf("published visibility = %q, want %q (create default)", vis, DefaultVisibility)
	}
	if want := uuid.UUID(owner.Bytes).String(); changedBy != want {
		t.Errorf("published changed_by = %q, want publisher %q", changedBy, want)
	}
}

// TestMetadataPatch_WritesChangeEvents_RealPostgres exercises the PATCH
// resolution path end to end against Postgres: each axis transition yields the
// right trigger-written event, a both-axes change is ONE governance_changed row,
// and a no-op writes nothing (the trigger's WHEN clause).
func TestMetadataPatch_WritesChangeEvents_RealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	owner := pullInsertUser(t, ctx, pool, 980401, "gov-patch-owner")
	defer cleanupOwners(t, ctx, pool, owner)
	h := &Handler{pool: pool, queries: sqlc.New(pool)}
	tr := govStore(t, ctx, h, owner, "patch-1", schema.LicenseCC0) // published; private; CC0

	patch := func(vis string, lic pgtype.Text) {
		t.Helper()
		v := vis
		if err := h.inTxAs(ctx, owner, func(q Querier) error {
			_, txErr := applyMetadataPatch(ctx, q, tr.ID, metadataPatch{
				Visibility: &v, License: lic, LicenseProvided: true,
			})
			return txErr
		}); err != nil {
			t.Fatalf("metadata patch: %v", err)
		}
	}
	latest := func() string {
		t.Helper()
		var ev string
		if err := pool.QueryRow(ctx, `
			SELECT event_type FROM transcript_governance_events_audit
			WHERE transcript_id = $1 ORDER BY seq DESC LIMIT 1
		`, tr.ID).Scan(&ev); err != nil {
			t.Fatalf("read latest event: %v", err)
		}
		return ev
	}
	count := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx,
			"SELECT count(*) FROM transcript_governance_events_audit WHERE transcript_id = $1", tr.ID).Scan(&n); err != nil {
			t.Fatalf("count events: %v", err)
		}
		return n
	}

	cc0 := pgtype.Text{String: string(schema.LicenseCC0), Valid: true}
	ccby := pgtype.Text{String: string(schema.LicenseCCBY), Valid: true}
	null := pgtype.Text{Valid: false}

	if c := count(); c != 1 {
		t.Fatalf("after create: %d events, want 1 (published)", c)
	}

	// license only: CC0 → CC-BY (visibility unchanged)
	patch("private", ccby)
	if ev := latest(); ev != string(database.EventLicenseChanged) {
		t.Errorf("license-only change: latest=%q, want license_changed", ev)
	}

	// visibility only: private → public (license unchanged)
	patch("public", ccby)
	if ev := latest(); ev != string(database.EventVisibilityChanged) {
		t.Errorf("visibility-only change: latest=%q, want visibility_changed", ev)
	}

	// both axes in one update → ONE governance_changed row
	patch("shared", cc0)
	if ev := latest(); ev != string(database.EventGovernanceChanged) {
		t.Errorf("both-axes change: latest=%q, want governance_changed", ev)
	}

	// no-op: same values ⇒ no new event (WHEN-clause suppression)
	before := count()
	patch("shared", cc0)
	if after := count(); after != before {
		t.Errorf("no-op update wrote %d extra event(s); want none", after-before)
	}

	// clearing a GRANTED license is BLOCKED (O1 FIX-NOW: CC grants are
	// irrevocable) — the patch errors, writes no event, and leaves the license.
	beforeClear := count()
	if err := h.inTxAs(ctx, owner, func(q Querier) error {
		_, txErr := applyMetadataPatch(ctx, q, tr.ID, metadataPatch{
			Visibility: func() *string { v := "shared"; return &v }(), License: null, LicenseProvided: true,
		})
		return txErr
	}); err == nil || !strings.Contains(err.Error(), "granted license is irrevocable") {
		t.Fatalf("license clear must be blocked with the irrevocability error, got: %v", err)
	}
	if after := count(); after != beforeClear {
		t.Errorf("blocked un-license wrote %d event(s); want none", after-beforeClear)
	}

	// The successor owner-update contract cannot represent shared. Narrow through
	// the same owner-attributed mutation path before exercising omitted fields.
	patch("private", cc0)
	if ev := latest(); ev != string(database.EventVisibilityChanged) {
		t.Errorf("shared-to-private narrowing: latest=%q, want visibility_changed", ev)
	}
	var narrowingActor pgtype.UUID
	if err := pool.QueryRow(ctx, `
		SELECT changed_by FROM transcript_governance_events_audit
		WHERE transcript_id=$1 ORDER BY seq DESC LIMIT 1
	`, tr.ID).Scan(&narrowingActor); err != nil {
		t.Fatalf("read narrowing audit actor: %v", err)
	}
	if narrowingActor != owner {
		t.Errorf("shared-to-private narrowing actor=%v, want owner %v", narrowingActor, owner)
	}

	// A title-only patch (visibility + license OMITTED) resolves the omitted
	// fields from the LIVE locked row — preserving them and writing no governance
	// event. This is the lost-update fix: omitted fields are never reverted to a
	// stale read. (License is still CC0 and visibility is now private.)
	beforeTitle := count()
	newTitle := "renamed-after-clear"
	if err := h.inTxAs(ctx, owner, func(q Querier) error {
		_, txErr := applyMetadataPatch(ctx, q, tr.ID, metadataPatch{Title: &newTitle})
		return txErr
	}); err != nil {
		t.Fatalf("title-only patch: %v", err)
	}
	if after := count(); after != beforeTitle {
		t.Errorf("title-only patch wrote %d governance event(s); want none", after-beforeTitle)
	}
	var vis string
	var lic *string
	if err := pool.QueryRow(ctx, "SELECT visibility, license_id FROM transcripts WHERE id = $1", tr.ID).Scan(&vis, &lic); err != nil {
		t.Fatalf("read post title-only state: %v", err)
	}
	if vis != "private" || lic == nil || *lic != string(schema.LicenseCC0) {
		t.Errorf("title-only patch changed governance state: visibility=%q license=%v, want private / CC0-1.0", vis, lic)
	}
}

// TestRepublish_LicenseChangeAndVisibilityPreserved_RealPostgres covers the
// re-publish path: a re-push with a new --license yields license_changed, a
// re-push with no license preserves the existing one (no event), and — the
// clobber fix — re-publish NEVER resets visibility.
func TestRepublish_LicenseChangeAndVisibilityPreserved_RealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	owner := pullInsertUser(t, ctx, pool, 980501, "gov-republish-owner")
	defer cleanupOwners(t, ctx, pool, owner)
	h := &Handler{pool: pool, queries: sqlc.New(pool)}
	tr := govStore(t, ctx, h, owner, "republish-1", schema.LicenseCC0) // published; private; CC0

	// Make it public via the dedicated edit path, so we can prove re-publish preserves it.
	pub := "public"
	if err := h.inTxAs(ctx, owner, func(q Querier) error {
		_, txErr := applyMetadataPatch(ctx, q, tr.ID, metadataPatch{
			Visibility: &pub, License: pgtype.Text{String: string(schema.LicenseCC0), Valid: true}, LicenseProvided: true,
		})
		return txErr
	}); err != nil {
		t.Fatalf("set public via PATCH: %v", err)
	}

	republish := func(lic pgtype.Text) {
		t.Helper()
		params := sqlc.UpdateTranscriptByOwnerAndLocalIDParams{
			OwnerID: owner, LocalID: "republish-1",
			ModelProvider: "claude-code", BlobKey: "blob/republish-1", SchemaVersion: "2",
			LicenseID: lic, BlobSizeBytes: tr.BlobSizeBytes, ContentHash: tr.ContentHash,
			WrappedDataKey: tr.WrappedDataKey, EncryptionAlgorithm: tr.EncryptionAlgorithm, KeyVersion: tr.KeyVersion,
		}
		if err := h.inTxAs(ctx, owner, func(q Querier) error {
			if err := pinRepublishGovernance(ctx, q, tr.ID, &params); err != nil {
				return err
			}
			_, txErr := q.UpdateTranscriptByOwnerAndLocalID(ctx, params)
			return txErr
		}); err != nil {
			t.Fatalf("re-publish: %v", err)
		}
	}
	count := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx,
			"SELECT count(*) FROM transcript_governance_events_audit WHERE transcript_id = $1", tr.ID).Scan(&n); err != nil {
			t.Fatalf("count events: %v", err)
		}
		return n
	}

	// Re-publish carrying a new license: CC0 → CC-BY ⇒ license_changed, visibility kept.
	republish(pgtype.Text{String: string(schema.LicenseCCBY), Valid: true})
	var vis, latest string
	if err := pool.QueryRow(ctx, "SELECT visibility FROM transcripts WHERE id = $1", tr.ID).Scan(&vis); err != nil {
		t.Fatalf("read visibility: %v", err)
	}
	if vis != "public" {
		t.Errorf("re-publish clobbered visibility to %q, want public (preserved)", vis)
	}
	if err := pool.QueryRow(ctx, `
		SELECT event_type FROM transcript_governance_events_audit
		WHERE transcript_id = $1 ORDER BY seq DESC LIMIT 1
	`, tr.ID).Scan(&latest); err != nil {
		t.Fatalf("read latest event: %v", err)
	}
	if latest != string(database.EventLicenseChanged) {
		t.Errorf("re-publish with new license: latest=%q, want license_changed", latest)
	}

	// Re-publish carrying NO license ⇒ preserve CC-BY, write nothing.
	before := count()
	republish(pgtype.Text{Valid: false})
	if after := count(); after != before {
		t.Errorf("empty-license re-publish wrote %d extra event(s); want none", after-before)
	}
	var lic *string
	if err := pool.QueryRow(ctx, "SELECT license_id FROM transcripts WHERE id = $1", tr.ID).Scan(&lic); err != nil {
		t.Fatalf("read license: %v", err)
	}
	if lic == nil || *lic != string(schema.LicenseCCBY) {
		t.Errorf("empty-license re-publish changed license to %v, want CC-BY-4.0 (preserved)", lic)
	}
}

// TestShareTranscript_FlipsVisibilityWithAudit_RealPostgres is the regression
// guard for the share-path audit gap: sharing a PRIVATE transcript flips it to
// 'shared', and that flip must be audited — a visibility_changed event — not a
// silent direct update. Drives the real ShareTranscript handler over real
// Postgres.
func TestShareTranscript_FlipsVisibilityWithAudit_RealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	owner := pullInsertUser(t, ctx, pool, 980601, "gov-share-owner")
	h := &Handler{pool: pool, queries: sqlc.New(pool)}
	tr := govStore(t, ctx, h, owner, "share-1", schema.LicenseCC0) // published; private
	grp := pullInsertGroup(t, ctx, pool, owner, "gov-share-grp")   // acceptance_mode 'open' ⇒ approved
	pullAddMember(t, ctx, pool, grp, owner, "member")
	defer func() {
		// groups are not governance-audited; transcripts + audit go through the
		// standard cleanup.
		_, _ = pool.Exec(ctx, "DELETE FROM groups WHERE created_by = $1", owner)
		cleanupOwners(t, ctx, pool, owner)
	}()

	body, _ := json.Marshal(map[string][]string{"group_ids": {uuid.UUID(grp.Bytes).String()}})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", uuid.UUID(tr.ID.Bytes).String())
	reqCtx := context.WithValue(context.Background(), UserContextKey, &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "gov-share-owner"})
	reqCtx = context.WithValue(reqCtx, chi.RouteCtxKey, rctx)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/share", bytes.NewReader(body)).WithContext(reqCtx)
	w := httptest.NewRecorder()

	h.ShareTranscript(w, r)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("ShareTranscript status = %d, want 200", w.Result().StatusCode)
	}

	var vis string
	if err := pool.QueryRow(ctx, "SELECT visibility FROM transcripts WHERE id = $1", tr.ID).Scan(&vis); err != nil {
		t.Fatalf("read visibility: %v", err)
	}
	if vis != "shared" {
		t.Errorf("post-share visibility = %q, want shared", vis)
	}

	var latest string
	if err := pool.QueryRow(ctx, `
		SELECT event_type FROM transcript_governance_events_audit
		WHERE transcript_id = $1 ORDER BY seq DESC LIMIT 1
	`, tr.ID).Scan(&latest); err != nil {
		t.Fatalf("read latest event: %v", err)
	}
	if latest != string(database.EventVisibilityChanged) {
		t.Errorf("share-induced flip latest event = %q, want visibility_changed (share must be audited)", latest)
	}
}

// TestDeleteViaInTxAs_StampsRetracted_RealPostgres proves the handler delete
// path feeds the fail-closed BEFORE DELETE trigger its actor: deleting through
// inTxAs stamps the auto 'retracted' event with the supplied actor, and the
// event survives the transcript's removal.
func TestDeleteViaInTxAs_StampsRetracted_RealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	owner := pullInsertUser(t, ctx, pool, 980301, "gov-del-owner")
	h := &Handler{pool: pool, queries: sqlc.New(pool)}
	tr := govStore(t, ctx, h, owner, "del-1", schema.LicenseCC0)
	defer func() {
		cleanupOwners(t, ctx, pool, owner)
		purgeAuditRows(t, ctx, pool, []pgtype.UUID{tr.ID}) // rows outlive the transcript
	}()

	if err := h.inTxAs(ctx, owner, func(q Querier) error {
		return q.DeleteTranscript(ctx, tr.ID)
	}); err != nil {
		t.Fatalf("delete via inTxAs: %v", err)
	}

	var event, changedBy string
	if err := pool.QueryRow(ctx, `
		SELECT event_type, changed_by::text FROM transcript_governance_events_audit
		WHERE transcript_id = $1 AND event_type = 'retracted'
	`, tr.ID).Scan(&event, &changedBy); err != nil {
		t.Fatalf("read retracted event: %v", err)
	}
	if want := uuid.UUID(owner.Bytes).String(); changedBy != want {
		t.Errorf("retracted changed_by = %q, want actor %q", changedBy, want)
	}

	var stillThere bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM transcripts WHERE id = $1)", tr.ID).Scan(&stillThere); err != nil {
		t.Fatalf("check transcript existence: %v", err)
	}
	if stillThere {
		t.Error("transcript should be deleted")
	}
}

// TestDeleteAccountCascade_StampsRetractedForAll_RealPostgres is the legal
// keystone: deleting a USER cascades to all their transcripts (migration 010),
// and the trigger must append a 'retracted' event for EACH — stamped with the
// deleting account as the actor (inTxAs's SET LOCAL is txn-scoped, so the
// cascade sees it) — that SURVIVES the cascade. A mock-querier test cannot
// reach this; only the real DB cascade + trigger exercise it.
func TestDeleteAccountCascade_StampsRetractedForAll_RealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	owner := pullInsertUser(t, ctx, pool, 980302, "gov-acct-owner")
	h := &Handler{pool: pool, queries: sqlc.New(pool)}
	t1 := govStore(t, ctx, h, owner, "acct-del-1", schema.LicenseCCBY)
	t2 := govStore(t, ctx, h, owner, "acct-del-2", "")
	defer purgeAuditRows(t, ctx, pool, []pgtype.UUID{t1.ID, t2.ID})

	// Delete the ACCOUNT (cascades to both transcripts).
	if err := h.inTxAs(ctx, owner, func(q Querier) error {
		return q.DeleteUser(ctx, owner)
	}); err != nil {
		t.Fatalf("account delete via inTxAs: %v", err)
	}

	wantActor := uuid.UUID(owner.Bytes).String()
	for _, tid := range []pgtype.UUID{t1.ID, t2.ID} {
		var changedBy string
		if err := pool.QueryRow(ctx, `
			SELECT changed_by::text FROM transcript_governance_events_audit
			WHERE transcript_id = $1 AND event_type = 'retracted'
		`, tid).Scan(&changedBy); err != nil {
			t.Fatalf("transcript %s: no surviving 'retracted' event after account cascade: %v",
				uuid.UUID(tid.Bytes), err)
		}
		if changedBy != wantActor {
			t.Errorf("transcript %s retracted changed_by = %q, want account %q",
				uuid.UUID(tid.Bytes), changedBy, wantActor)
		}
	}

	// The transcripts (and user) are gone, but the audit survived above.
	var remaining int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM transcripts WHERE owner_id = $1", owner).Scan(&remaining); err != nil {
		t.Fatalf("count remaining transcripts: %v", err)
	}
	if remaining != 0 {
		t.Errorf("transcripts remaining after account delete = %d, want 0", remaining)
	}
}
