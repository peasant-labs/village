//go:build integration

package handler

// Integration coverage for pull authorization and SQL equivalence.
//
// The approved-only predicate is enforced entirely in unmocked SQL:
// ListApprovedTranscriptShareGroups filters to approved shares for the Go
// predicate, while both pull-list membership queries filter their joins to
// approved shares. CountPullableTranscripts must remain consistent with the
// full-row list.
//
// This drives the real queries (sqlc.New(pool)) and the real canPullTranscript
// policy fn against a real Postgres. A regression dropping the status='approved'
// clause (re-admitting pending shares) or the public-exclusion would FAIL here
// even though every mock-querier handler test would stay green.
//
//	TEST_DATABASE_URL="postgres://test:test@localhost:55432/village_test?sslmode=disable" \
//	  go test -tags=integration ./internal/handler/...

import (
	"context"
	"os"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

func pullTestDatabaseURL(t *testing.T) string {
	t.Helper()
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	return "postgres://test:test@localhost:55432/village_test?sslmode=disable"
}

func pullInsertUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, githubID int64, username string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id)
		VALUES ($1, $2, $3) RETURNING id
	`, githubID, username, username).Scan(&id); err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}
	return id
}

func pullInsertTranscript(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner pgtype.UUID, localID, visibility string) pgtype.UUID {
	t.Helper()
	// The migration-026 publish trigger is fail-closed on app.actor_id, so the
	// fixture INSERT declares the SYSTEM actor in its own txn (SET LOCAL scope).
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("insert transcript %s: begin: %v", localID, err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", database.SystemActorID); err != nil {
		t.Fatalf("insert transcript %s: declare system actor: %v", localID, err)
	}
	id := toPgUUID(uuid.New())
	hash := schema.ComputeTranscriptHash([]byte(localID))
	if err := tx.QueryRow(ctx, `
		INSERT INTO transcripts (id, owner_id, local_id, title, visibility, model_provider, model_name, blob_key, blob_size_bytes, schema_version, content_hash, wrapped_data_key, encryption_algorithm, key_version, project_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15) RETURNING id
	`, id, owner, localID, "t-"+localID, visibility, "claude-code", "m-"+localID, "blob/"+localID, int64(len(localID)), "0.1.0", hash, []byte("fixture-wrapped-data-key"), "aes-256-gcm-random-nonce-v1", 1, fixtureProjectHash(localID)).Scan(&id); err != nil {
		t.Fatalf("insert transcript %s: %v", localID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("insert transcript %s: commit: %v", localID, err)
	}
	return id
}

func pullInsertGroup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner pgtype.UUID, name string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO groups (name, created_by) VALUES ($1, $2) RETURNING id
	`, name, owner).Scan(&id); err != nil {
		t.Fatalf("insert group %s: %v", name, err)
	}
	return id
}

func pullAddMember(t *testing.T, ctx context.Context, pool *pgxpool.Pool, group, user pgtype.UUID, role string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, $3)
	`, group, user, role); err != nil {
		t.Fatalf("add member: %v", err)
	}
}

// pullShare submits the transcript to the collective and leaves it in the
// requested state. transcript_shares is derived, never written directly, so the
// fixture opens an attempt and lets the derivation produce the current-state
// row the pull authorization reads.
func pullShare(t *testing.T, ctx context.Context, pool *pgxpool.Pool, transcript, group pgtype.UUID, status string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status)
		VALUES ($1, $2, 1, $3)
	`, transcript, group, status); err != nil {
		t.Fatalf("share: %v", err)
	}
}

// TestPull_AuthorizationEquivalence_RealPostgres seeds the full divergence table
// and asserts both the real SQL list/count and its agreement with the Go
// canPullTranscript predicate over every seeded case.
func TestPull_AuthorizationEquivalence_RealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, pullTestDatabaseURL(t))
	if err != nil {
		t.Skipf("cannot create pool (%v); set TEST_DATABASE_URL", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("cannot reach test database (%v); set TEST_DATABASE_URL", err)
	}
	if err := database.RunMigrations(pool); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// --- Seed (cleaned up at the end; transcript_shares/group_members cascade) ---
	owner := pullInsertUser(t, ctx, pool, 980001, "pull-owner")
	member := pullInsertUser(t, ctx, pool, 980002, "pull-member")     // the requester under test
	stranger := pullInsertUser(t, ctx, pool, 980003, "pull-stranger") // owns the public/pending transcripts
	users := []pgtype.UUID{owner, member, stranger}
	defer func() {
		// FK order: shares & members cascade from groups/transcripts. Groups are
		// not governance-audited; transcripts + users + audit rows go through the
		// fail-closed-aware standard cleanup.
		_, _ = pool.Exec(ctx, "DELETE FROM groups WHERE created_by = ANY($1)", users)
		cleanupOwners(t, ctx, pool, users...)
	}()

	groupA := pullInsertGroup(t, ctx, pool, owner, "pull-grp-A")
	groupB := pullInsertGroup(t, ctx, pool, owner, "pull-grp-B")
	groupC := pullInsertGroup(t, ctx, pool, owner, "pull-grp-C")
	// The member belongs to A, B, C. Not to any group D (none exists).
	pullAddMember(t, ctx, pool, groupA, member, "member")
	pullAddMember(t, ctx, pool, groupB, member, "member")
	pullAddMember(t, ctx, pool, groupC, member, "member")

	// Divergence-table rows, each a transcript:
	tOwned := pullInsertTranscript(t, ctx, pool, member, "owned", dbVisibilityPrivate)         // owner ⇒ admit
	tPublic := pullInsertTranscript(t, ctx, pool, stranger, "public", dbVisibilityPublic)      // public-but-unshared ⇒ deny
	tApproved := pullInsertTranscript(t, ctx, pool, owner, "approved", dbVisibilityShared)     // approved share to A ⇒ admit
	tPending := pullInsertTranscript(t, ctx, pool, owner, "pending", dbVisibilityShared)       // pending share to B ⇒ deny
	tMulti := pullInsertTranscript(t, ctx, pool, owner, "multi", dbVisibilityShared)           // approved to A AND C ⇒ admit ONCE (DISTINCT)
	tStranger := pullInsertTranscript(t, ctx, pool, stranger, "stranger", dbVisibilityPrivate) // not owned, not shared ⇒ deny

	pullShare(t, ctx, pool, tApproved, groupA, "approved")
	pullShare(t, ctx, pool, tPending, groupB, "pending")
	pullShare(t, ctx, pool, tMulti, groupA, "approved")
	pullShare(t, ctx, pool, tMulti, groupC, "approved")

	h := &Handler{pool: pool, queries: sqlc.New(pool)}
	memberUser := &AuthUser{ID: uuid.UUID(member.Bytes), Username: "pull-member"}

	// Helper: the set of ids the real list SQL admits for the member.
	listSet := func() map[uuid.UUID]bool {
		rows, err := h.queries.ListPullableTranscripts(ctx, sqlc.ListPullableTranscriptsParams{
			UserID: member, Limit: 100, Offset: 0,
		})
		if err != nil {
			t.Fatalf("ListPullableTranscripts: %v", err)
		}
		set := make(map[uuid.UUID]bool, len(rows))
		for _, r := range rows {
			id := uuid.UUID(r.ID.Bytes)
			if set[id] {
				t.Errorf("DISTINCT dedup violated: id %s appears twice in the list", id)
			}
			set[id] = true
		}
		return set
	}

	countTotal := func() int64 {
		c, err := h.queries.CountPullableTranscripts(ctx, member)
		if err != nil {
			t.Fatalf("CountPullableTranscripts: %v", err)
		}
		return c
	}

	// A pending share is denied.
	got := listSet()
	wantAdmit := map[string]uuid.UUID{
		"owned (owner)":          uuid.UUID(tOwned.Bytes),
		"approved-share":         uuid.UUID(tApproved.Bytes),
		"multi-approved (dedup)": uuid.UUID(tMulti.Bytes),
	}
	wantDeny := map[string]uuid.UUID{
		"public-but-unshared": uuid.UUID(tPublic.Bytes),
		"pending-share":       uuid.UUID(tPending.Bytes),
		"stranger-private":    uuid.UUID(tStranger.Bytes),
	}
	for name, id := range wantAdmit {
		if !got[id] {
			t.Errorf("pending-share state: %s (%s) must be admitted but was absent", name, id)
		}
	}
	for name, id := range wantDeny {
		if got[id] {
			t.Errorf("pending-share state: %s (%s) must be denied but was admitted", name, id)
		}
	}
	if c := countTotal(); int(c) != len(got) {
		t.Errorf("pending-share state: count=%d != distinct list size=%d (predicate divergence)", c, len(got))
	}
	if len(got) != 3 {
		t.Errorf("pending-share state: admitted set size = %d, want 3 (owned + approved + multi-once)", len(got))
	}

	// Across every seeded transcript, list membership must equal
	// canPullTranscript(member, transcript). The Go predicate and both SQL
	// membership queries must agree for every divergence-table row.
	allTranscripts := []struct {
		name string
		id   pgtype.UUID
	}{
		{"owned", tOwned}, {"public", tPublic}, {"approved", tApproved},
		{"pending", tPending}, {"multi", tMulti}, {"stranger", tStranger},
	}
	assertEquivalence := func(state string, set map[uuid.UUID]bool) {
		for _, tc := range allTranscripts {
			tr, err := h.queries.GetTranscriptByID(ctx, tc.id)
			if err != nil {
				t.Fatalf("%s GetTranscriptByID(%s): %v", state, tc.name, err)
			}
			goPredicate := h.canPullTranscript(ctx, memberUser, tr)
			inList := set[uuid.UUID(tc.id.Bytes)]
			if goPredicate != inList {
				t.Errorf("%s equivalence diverged for %s: canPullTranscript=%v but listMembership=%v", state, tc.name, goPredicate, inList)
			}
		}
	}

	// byIDsSet: the ids the pull skip-gate's batch query admits for the member,
	// asked over EVERY seeded transcript id at once. This is the skip-gate's
	// pull-scoping predicate; folding its membership through the same
	// assertEquivalence keeps this second SQL membership query aligned with the Go
	// canPullTranscript predicate and the full-row list across
	// the whole divergence table. A non-pullable id is ABSENT (withheld by
	// omission), exactly matching a canPullTranscript=false verdict.
	allIDs := make([]pgtype.UUID, 0, len(allTranscripts))
	for _, tc := range allTranscripts {
		allIDs = append(allIDs, tc.id)
	}
	byIDsSet := func() map[uuid.UUID]bool {
		rows, err := h.queries.ListPullableTranscriptsByIDs(ctx, sqlc.ListPullableTranscriptsByIDsParams{
			UserID:        member,
			TranscriptIds: allIDs,
		})
		if err != nil {
			t.Fatalf("ListPullableTranscriptsByIDs: %v", err)
		}
		set := make(map[uuid.UUID]bool, len(rows))
		for _, r := range rows {
			id := uuid.UUID(r.ID.Bytes)
			if set[id] {
				t.Errorf("DISTINCT dedup violated: id %s appears twice in the by-ids result", id)
			}
			set[id] = true
		}
		return set
	}

	assertEquivalence("pending-share list", got)
	assertEquivalence("pending-share by-ids", byIDsSet())

	// Approving the pending share admits it.
	if err := h.queries.UpdateShareStatus(ctx, sqlc.UpdateShareStatusParams{
		TranscriptID: tPending, GroupID: groupB, Status: "approved", DecidedBy: owner,
	}); err != nil {
		t.Fatalf("UpdateShareStatus pending->approved: %v", err)
	}
	got2 := listSet()
	if !got2[uuid.UUID(tPending.Bytes)] {
		t.Errorf("approved-share state: flipping pending->approved must admit the share, but it is still absent")
	}
	if len(got2) != 4 {
		t.Errorf("approved-share state: admitted set size = %d, want 4 (previous three + newly approved)", len(got2))
	}
	if c := countTotal(); int(c) != len(got2) {
		t.Errorf("approved-share state: count=%d != distinct list size=%d", c, len(got2))
	}
	assertEquivalence("approved-share list", got2)
	assertEquivalence("approved-share by-ids", byIDsSet())

	// Stable, sorted log of the final admitted ids (aids debugging on failure).
	ids := make([]string, 0, len(got2))
	for id := range got2 {
		ids = append(ids, id.String())
	}
	sort.Strings(ids)
	t.Logf("final admitted ids: %v", ids)
}
