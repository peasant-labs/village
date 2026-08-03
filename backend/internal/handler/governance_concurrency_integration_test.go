//go:build integration

package handler

// Concurrency + basics for the governance audit, against real Postgres under -race.
// These pin the invariants the FOR UPDATE / one-txn-per-mutation design exists to
// guarantee: under concurrent writers the live transcripts row and its audit trail
// never diverge, seq stays monotonic, every create/delete is accounted for exactly
// once, and no goroutine deadlocks or errors.
//
//	TEST_DATABASE_URL="postgres://test:test@localhost:5432/village_test?sslmode=disable" \
//	  go test -tags=integration -race ./internal/handler/...

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// samePtr compares two *string (NULL-as-nil) for equality.
func samePtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// TestConcurrentPatches_StayConsistent_RealPostgres hammers ONE transcript with N
// concurrent edits. The FOR UPDATE pre-image lock must serialise them so: (a) no
// writer errors/deadlocks, (b) the live row equals the latest-by-seq audit snapshot
// (cache and audit never disagree), and (c) seq is strictly increasing.
func TestConcurrentPatches_StayConsistent_RealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	owner := pullInsertUser(t, ctx, pool, 990001, "conc-patch-owner")
	defer cleanupOwners(t, ctx, pool, owner)
	h := &Handler{pool: pool, queries: sqlc.New(pool)}
	tr := govStore(t, ctx, h, owner, "conc-patch", schema.LicenseCC0) // published; private; CC0

	const N = 8
	lics := []pgtype.Text{
		{String: string(schema.LicenseCC0), Valid: true},
		{String: string(schema.LicenseCCBY), Valid: true},
		{String: string(schema.LicenseCCBYSA), Valid: true},
	}
	viss := []string{"private", "public", "shared"}

	var wg sync.WaitGroup
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v := viss[i%len(viss)]
			errs <- h.inTxAs(ctx, owner, func(q Querier) error {
				_, txErr := applyMetadataPatch(ctx, q, tr.ID, metadataPatch{
					Visibility: &v, License: lics[i%len(lics)], LicenseProvided: true,
				})
				return txErr
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent patch errored (deadlock / serialization failure?): %v", err)
		}
	}

	// (b) live row == latest-by-seq audit snapshot.
	var liveLic *string
	var liveVis string
	if err := pool.QueryRow(ctx, "SELECT license_id, visibility FROM transcripts WHERE id = $1", tr.ID).Scan(&liveLic, &liveVis); err != nil {
		t.Fatalf("read live row: %v", err)
	}
	var auditLic *string
	var auditVis string
	if err := pool.QueryRow(ctx, `
		SELECT license_id, visibility FROM transcript_governance_events_audit
		WHERE transcript_id = $1 ORDER BY seq DESC LIMIT 1
	`, tr.ID).Scan(&auditLic, &auditVis); err != nil {
		t.Fatalf("read latest audit: %v", err)
	}
	if liveVis != auditVis || !samePtr(liveLic, auditLic) {
		t.Errorf("live row (lic=%v vis=%s) != latest audit snapshot (lic=%v vis=%s) — cache/audit diverged under concurrency",
			liveLic, liveVis, auditLic, auditVis)
	}

	// (c) seq strictly increasing, no duplicates.
	rows, err := pool.Query(ctx, "SELECT seq FROM transcript_governance_events_audit WHERE transcript_id = $1 ORDER BY seq", tr.ID)
	if err != nil {
		t.Fatalf("read seqs: %v", err)
	}
	defer rows.Close()
	var prev int64 = -1
	count := 0
	for rows.Next() {
		var s int64
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan seq: %v", err)
		}
		if s <= prev {
			t.Errorf("seq not strictly increasing: %d followed %d", s, prev)
		}
		prev = s
		count++
	}
	if count < 1 {
		t.Errorf("expected at least the published event, got %d", count)
	}
	t.Logf("concurrent patches survived: %d audit rows, final state lic=%v vis=%s", count, liveLic, liveVis)
}

// TestConcurrentCreates_EachGetsOnePublished_RealPostgres creates N distinct
// transcripts concurrently and asserts each lands with EXACTLY one published event —
// no lost, duplicated, or cross-wired events across the concurrent txns.
func TestConcurrentCreates_EachGetsOnePublished_RealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	owner := pullInsertUser(t, ctx, pool, 990002, "conc-create-owner")
	defer cleanupOwners(t, ctx, pool, owner)
	h := &Handler{pool: pool, queries: sqlc.New(pool)}

	const N = 8
	var wg sync.WaitGroup
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			local := fmt.Sprintf("conc-create-%d", i)
			req := schema.PublishRequest{
				License:  schema.LicenseCCBY,
				Identity: schema.SessionIdentity{SessionID: schema.SessionID(local), SchemaVersion: 2},
				Model:    schema.ModelInfo{Harness: "claude-code", Model: "m"},
			}
			params := schemaToTranscriptParams(req, "blob/"+local, 1, "2")
			params.OwnerID = owner
			params.LocalID = local
			params = completeEncryptedFixtureParams(params)
			errs <- h.inTxAs(ctx, owner, func(q Querier) error {
				_, txErr := q.CreateTranscript(ctx, params)
				return txErr
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent create errored: %v", err)
		}
	}

	var transcripts, published, dupes int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM transcripts WHERE owner_id = $1", owner).Scan(&transcripts); err != nil {
		t.Fatalf("count transcripts: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM transcript_governance_events_audit a
		JOIN transcripts t ON t.id = a.transcript_id
		WHERE t.owner_id = $1 AND a.event_type = 'published'
	`, owner).Scan(&published); err != nil {
		t.Fatalf("count published: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT a.transcript_id FROM transcript_governance_events_audit a
			JOIN transcripts t ON t.id = a.transcript_id
			WHERE t.owner_id = $1 AND a.event_type = 'published'
			GROUP BY a.transcript_id HAVING count(*) > 1
		) x
	`, owner).Scan(&dupes); err != nil {
		t.Fatalf("count dupes: %v", err)
	}
	if transcripts != N {
		t.Errorf("transcripts = %d, want %d", transcripts, N)
	}
	if published != N {
		t.Errorf("published events = %d, want %d (exactly one per create)", published, N)
	}
	if dupes != 0 {
		t.Errorf("%d transcript(s) got more than one published event", dupes)
	}
}

// TestConcurrentDeletes_EachGetsOneRetracted_RealPostgres deletes N transcripts
// concurrently (each through inTxAs, its own txn + app.actor_id) and asserts
// every one produced exactly one surviving, actor-stamped retracted event — proving
// the trigger + per-txn GUC are correct under concurrency.
func TestConcurrentDeletes_EachGetsOneRetracted_RealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	owner := pullInsertUser(t, ctx, pool, 990003, "conc-del-owner")
	defer cleanupOwners(t, ctx, pool, owner)
	h := &Handler{pool: pool, queries: sqlc.New(pool)}

	const N = 8
	ids := make([]pgtype.UUID, N)
	for i := 0; i < N; i++ {
		ids[i] = govStore(t, ctx, h, owner, fmt.Sprintf("conc-del-%d", i), schema.LicenseCC0).ID
	}
	// The test deletes every transcript itself, so cleanupOwners collects no
	// surviving ids — purge the audit rows for the known ids explicitly or each
	// run leaks 2N rows (published + retracted) on the shared database.
	defer purgeAuditRows(t, ctx, pool, ids)

	var wg sync.WaitGroup
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(id pgtype.UUID) {
			defer wg.Done()
			errs <- h.inTxAs(ctx, owner, func(q Querier) error {
				return q.DeleteTranscript(ctx, id)
			})
		}(ids[i])
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent delete errored: %v", err)
		}
	}

	var left, retracted, wrongActor int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM transcripts WHERE owner_id = $1", owner).Scan(&left); err != nil {
		t.Fatalf("count transcripts: %v", err)
	}
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM transcript_governance_events_audit WHERE event_type = 'retracted' AND changed_by = $1", owner).Scan(&retracted); err != nil {
		t.Fatalf("count retracted: %v", err)
	}
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM transcript_governance_events_audit WHERE event_type = 'retracted' AND changed_by <> $1 AND transcript_id = ANY($2)", owner, ids).Scan(&wrongActor); err != nil {
		t.Fatalf("count wrong-actor retracted: %v", err)
	}
	if left != 0 {
		t.Errorf("transcripts left = %d, want 0", left)
	}
	if retracted != N {
		t.Errorf("retracted events = %d, want %d (one surviving per delete)", retracted, N)
	}
	if wrongActor != 0 {
		t.Errorf("%d retracted rows had the wrong actor (app.actor_id leaked across txns?)", wrongActor)
	}
}

// TestPublishedEvent_NoLicense_RealPostgres is the clearly-defined basic for an
// unlicensed publish: the published snapshot records a NULL license (not "").
func TestPublishedEvent_NoLicense_RealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	owner := pullInsertUser(t, ctx, pool, 990004, "nolicense-owner")
	defer cleanupOwners(t, ctx, pool, owner)
	h := &Handler{pool: pool, queries: sqlc.New(pool)}

	req := schema.PublishRequest{
		// no License field
		Identity: schema.SessionIdentity{SessionID: "no-license-1", SchemaVersion: 2},
		Model:    schema.ModelInfo{Harness: "claude-code", Model: "m"},
	}
	params := schemaToTranscriptParams(req, "blob/no-license-1", 1, "2")
	params.OwnerID = owner
	params.LocalID = "no-license-1"
	params = completeEncryptedFixtureParams(params)
	var tr sqlc.Transcript
	if err := h.inTxAs(ctx, owner, func(q Querier) error {
		var txErr error
		tr, txErr = q.CreateTranscript(ctx, params)
		return txErr
	}); err != nil {
		t.Fatalf("publish create via inTxAs: %v", err)
	}

	var event string
	var lic *string
	if err := pool.QueryRow(ctx, `
		SELECT event_type, license_id FROM transcript_governance_events_audit WHERE transcript_id = $1
	`, tr.ID).Scan(&event, &lic); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if event != string(database.EventPublished) {
		t.Errorf("event = %q, want published", event)
	}
	if lic != nil {
		t.Errorf("published license = %q, want NULL (no license supplied)", *lic)
	}
}
