//go:build integration

package handler

// Integration test for GET /groups (ListUserGroups) gaining member_count /
// transcript_count aggregates.
//
// member_count on THIS endpoint deliberately EXCLUDES role='pending' members
// (matching ListGroupMembers, the members roster), while the sibling list
// surfaces (ListAllGroups / SearchCollectives / ListCollectivesByGitHubOrg)
// INCLUDE pending members. This file pins that intentional per-surface split
// against silent drift rather than asserting cross-surface equality.
//
// transcript_count is approved-only (transcript_shares.status = 'approved')
// and IS expected to stay identical across every surface.
//
// Drives the REAL ListGroups handler (not the sqlc query directly, except for
// the roster-consistency case) against a real Postgres, and asserts the
// PARSED JSON HTTP body.
//
//	TEST_DATABASE_URL="postgres://test:test@localhost:5432/village_test?sslmode=disable" \
//	  go test -tags=integration -race ./internal/handler/...

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

func countsTestDatabaseURL(t *testing.T) string {
	t.Helper()
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	return "postgres://test:test@localhost:5432/village_test?sslmode=disable"
}

// countsGroupRow mirrors the JSON shape ListGroups writes directly from
// []sqlc.ListUserGroupsRow (no handler/DTO reshaping) — decoding into this
// struct pins that the wire body actually carries these keys.
type countsGroupRow struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Role            string  `json:"role"`
	MemberSince     *string `json:"member_since"`
	MemberCount     *int32  `json:"member_count"`
	TranscriptCount *int32  `json:"transcript_count"`
}

func countsInsertUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, githubID int64, username string, discoverable bool) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id, is_discoverable)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, githubID, username, username, discoverable).Scan(&id); err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}
	return id
}

func countsInsertGroup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner pgtype.UUID, name string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO groups (name, created_by) VALUES ($1, $2) RETURNING id
	`, name, owner).Scan(&id); err != nil {
		t.Fatalf("insert group %s: %v", name, err)
	}
	return id
}

func countsAddMember(t *testing.T, ctx context.Context, pool *pgxpool.Pool, group, user pgtype.UUID, role string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, $3)
	`, group, user, role); err != nil {
		t.Fatalf("add member: %v", err)
	}
}

func countsInsertTranscript(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner pgtype.UUID, localID string) pgtype.UUID {
	t.Helper()
	// The publish audit trigger (migration 026) is fail-closed: the INSERT and
	// the app.actor_id GUC must share one transaction.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin insert-transcript tx: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", database.SystemActorID); err != nil {
		t.Fatalf("set system actor: %v", err)
	}
	id := toPgUUID(uuid.New())
	hash := schema.ComputeTranscriptHash([]byte(localID))
	if err := tx.QueryRow(ctx, `
		INSERT INTO transcripts (id, owner_id, local_id, title, visibility, model_provider, model_name, blob_key, blob_size_bytes, schema_version, content_hash, wrapped_data_key, encryption_algorithm, key_version)
		VALUES ($1, $2, $3, $4, 'shared', $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id
	`, id, owner, localID, "t-"+localID, "claude-code", "m-"+localID, "blob/"+localID, int64(len(localID)), "0.1.0", hash, []byte("fixture-wrapped-data-key"), "aes-256-gcm-random-nonce-v1", 1).Scan(&id); err != nil {
		t.Fatalf("insert transcript %s: %v", localID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit insert transcript %s: %v", localID, err)
	}
	return id
}

func countsShare(t *testing.T, ctx context.Context, pool *pgxpool.Pool, transcript, group pgtype.UUID, status string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO transcript_shares (transcript_id, group_id, status) VALUES ($1, $2, $3)
	`, transcript, group, status); err != nil {
		t.Fatalf("share: %v", err)
	}
}

// countsNewPool connects, migrates (idempotent), and returns a pool + skip
// helper consistent with the other integration suites in this package.
func countsNewPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, countsTestDatabaseURL(t))
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

// countsGetGroups drives the REAL ListGroups handler as the given caller and
// decodes the parsed JSON HTTP body.
func countsGetGroups(t *testing.T, h *Handler, caller pgtype.UUID) []countsGroupRow {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/groups", nil)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &AuthUser{ID: uuid.UUID(caller.Bytes), Username: "counts-caller"}))
	w := httptest.NewRecorder()
	h.ListGroups(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /groups status: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var rows []countsGroupRow
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode GET /groups body: %v", err)
	}
	return rows
}

func countsFindGroup(t *testing.T, rows []countsGroupRow, groupID pgtype.UUID) countsGroupRow {
	t.Helper()
	want := uuid.UUID(groupID.Bytes).String()
	for _, r := range rows {
		if r.ID == want {
			return r
		}
	}
	t.Fatalf("group %s not present in GET /groups response (got %d groups)", want, len(rows))
	return countsGroupRow{}
}

// TestListGroups_Counts_NonZero: owner + N non-pending members, M approved
// shares -> member_count==N, transcript_count==M.
func TestListGroups_Counts_NonZero(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := countsNewPool(t)
	defer pool.Close()

	owner := countsInsertUser(t, ctx, pool, 990101, "counts-nz-owner", true)
	memberA := countsInsertUser(t, ctx, pool, 990102, "counts-nz-a", true)
	memberB := countsInsertUser(t, ctx, pool, 990103, "counts-nz-b", true)
	users := []pgtype.UUID{owner, memberA, memberB}
	defer cleanupOwners(t, ctx, pool, users...)

	group := countsInsertGroup(t, ctx, pool, owner, "counts-nz-group")
	countsAddMember(t, ctx, pool, group, owner, "owner")
	countsAddMember(t, ctx, pool, group, memberA, "member")
	countsAddMember(t, ctx, pool, group, memberB, "member")

	tr1 := countsInsertTranscript(t, ctx, pool, owner, "counts-nz-t1")
	tr2 := countsInsertTranscript(t, ctx, pool, owner, "counts-nz-t2")
	countsShare(t, ctx, pool, tr1, group, "approved")
	countsShare(t, ctx, pool, tr2, group, "approved")

	h := &Handler{pool: pool, queries: sqlc.New(pool)}
	row := countsFindGroup(t, countsGetGroups(t, h, owner), group)

	if row.MemberCount == nil || *row.MemberCount != 3 {
		t.Errorf("member_count: got %v, want 3 (owner + 2 non-pending members)", row.MemberCount)
	}
	if row.TranscriptCount == nil || *row.TranscriptCount != 2 {
		t.Errorf("transcript_count: got %v, want 2 (2 approved shares)", row.TranscriptCount)
	}
}

// TestListGroups_Counts_ApprovedOnly: pending + rejected shares are EXCLUDED
// from transcript_count.
func TestListGroups_Counts_ApprovedOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := countsNewPool(t)
	defer pool.Close()

	owner := countsInsertUser(t, ctx, pool, 990201, "counts-ao-owner", true)
	users := []pgtype.UUID{owner}
	defer cleanupOwners(t, ctx, pool, users...)

	group := countsInsertGroup(t, ctx, pool, owner, "counts-ao-group")
	countsAddMember(t, ctx, pool, group, owner, "owner")

	trApproved := countsInsertTranscript(t, ctx, pool, owner, "counts-ao-approved")
	trPending := countsInsertTranscript(t, ctx, pool, owner, "counts-ao-pending")
	trRejected := countsInsertTranscript(t, ctx, pool, owner, "counts-ao-rejected")
	countsShare(t, ctx, pool, trApproved, group, "approved")
	countsShare(t, ctx, pool, trPending, group, "pending")
	countsShare(t, ctx, pool, trRejected, group, "rejected")

	h := &Handler{pool: pool, queries: sqlc.New(pool)}
	row := countsFindGroup(t, countsGetGroups(t, h, owner), group)

	if row.TranscriptCount == nil || *row.TranscriptCount != 1 {
		t.Errorf("transcript_count: got %v, want 1 (only the approved share counts; pending+rejected excluded)", row.TranscriptCount)
	}
}

// TestListGroups_Counts_PendingMemberExcluded is the PRIMARY drift pin for
// the exclude-pending semantics: 1 owner + 1
// role='pending' member -> member_count==1, NOT 2. The realistic drift this
// catches is accidentally dropping "AND role != 'pending'" (or reverting to
// the sibling include-pending form), which would yield 2 here.
func TestListGroups_Counts_PendingMemberExcluded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := countsNewPool(t)
	defer pool.Close()

	owner := countsInsertUser(t, ctx, pool, 990301, "counts-pend-owner", true)
	pendingUser := countsInsertUser(t, ctx, pool, 990302, "counts-pend-user", true)
	users := []pgtype.UUID{owner, pendingUser}
	defer cleanupOwners(t, ctx, pool, users...)

	group := countsInsertGroup(t, ctx, pool, owner, "counts-pend-group")
	countsAddMember(t, ctx, pool, group, owner, "owner")
	countsAddMember(t, ctx, pool, group, pendingUser, "pending")

	h := &Handler{pool: pool, queries: sqlc.New(pool)}
	row := countsFindGroup(t, countsGetGroups(t, h, owner), group)

	if row.MemberCount == nil || *row.MemberCount != 1 {
		t.Errorf("member_count: got %v, want 1 (pending member must be EXCLUDED, not 2)", row.MemberCount)
	}
}

// TestListGroups_Counts_SoloZero: caller alone, no approved shares ->
// member_count==1, transcript_count==0, and both are PRESENT as concrete
// integers (not null/omitted) in the parsed body — the exact regression that
// motivated this whole effort (cards silently omitting the fields).
func TestListGroups_Counts_SoloZero(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := countsNewPool(t)
	defer pool.Close()

	owner := countsInsertUser(t, ctx, pool, 990401, "counts-solo-owner", true)
	users := []pgtype.UUID{owner}
	defer cleanupOwners(t, ctx, pool, users...)

	group := countsInsertGroup(t, ctx, pool, owner, "counts-solo-group")
	countsAddMember(t, ctx, pool, group, owner, "owner")

	h := &Handler{pool: pool, queries: sqlc.New(pool)}
	row := countsFindGroup(t, countsGetGroups(t, h, owner), group)

	if row.MemberCount == nil {
		t.Fatal("member_count: absent/null in HTTP body, want present concrete integer 1")
	}
	if *row.MemberCount != 1 {
		t.Errorf("member_count: got %d, want 1 (solo owner)", *row.MemberCount)
	}
	if row.TranscriptCount == nil {
		t.Fatal("transcript_count: absent/null in HTTP body, want present concrete integer 0")
	}
	if *row.TranscriptCount != 0 {
		t.Errorf("transcript_count: got %d, want 0 (no shares)", *row.TranscriptCount)
	}
}

// TestListGroups_Counts_RosterConsistency pins that member_count equals the
// number of rows ListGroupMembers returns for the SAME group, fetched AS
// OWNER (viewer_is_owner=true bypasses the "is_discoverable OR
// viewer_is_owner" filter, isolating the role-filter equivalence rather than
// discoverability). The fixture is owner + >=1 non-owner member + 1 pending
// member, so the two cases together pin BOTH "pending excluded" AND "every
// non-pending role counted" (a role-narrowing regression, e.g. counting only
// owners, would also give 1 in the bare pending-exclusion test above but
// would diverge from the roster row count here).
//
// Deliberately NOT asserted: member_count == a sibling surface's
// member_count. That invariant is intentionally broken by the exclude-pending
// semantics (see the query's in-code comment); asserting it would fail by
// design.
func TestListGroups_Counts_RosterConsistency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := countsNewPool(t)
	defer pool.Close()

	owner := countsInsertUser(t, ctx, pool, 990501, "counts-roster-owner", true)
	member := countsInsertUser(t, ctx, pool, 990502, "counts-roster-member", true)
	pendingUser := countsInsertUser(t, ctx, pool, 990503, "counts-roster-pending", true)
	users := []pgtype.UUID{owner, member, pendingUser}
	defer cleanupOwners(t, ctx, pool, users...)

	group := countsInsertGroup(t, ctx, pool, owner, "counts-roster-group")
	countsAddMember(t, ctx, pool, group, owner, "owner")
	countsAddMember(t, ctx, pool, group, member, "member")
	countsAddMember(t, ctx, pool, group, pendingUser, "pending")

	h := &Handler{pool: pool, queries: sqlc.New(pool)}
	row := countsFindGroup(t, countsGetGroups(t, h, owner), group)

	rosterRows, err := h.queries.ListGroupMembers(ctx, sqlc.ListGroupMembersParams{
		GroupID:       group,
		ViewerIsOwner: true, // bypasses discoverability filter -> isolates the role-filter equivalence
	})
	if err != nil {
		t.Fatalf("ListGroupMembers: %v", err)
	}

	if row.MemberCount == nil {
		t.Fatal("member_count: absent/null in HTTP body")
	}
	if int(*row.MemberCount) != len(rosterRows) {
		t.Errorf("member_count (%d) != ListGroupMembers roster row count (%d) fetched as owner; they must agree (owner + non-pending member, pending excluded)", *row.MemberCount, len(rosterRows))
	}
	if len(rosterRows) != 2 {
		t.Fatalf("test fixture sanity: roster should have 2 rows (owner + member, pending excluded), got %d", len(rosterRows))
	}
}

// TestListGroups_Counts_IndependentOfDiscoverability pins that member_count
// counts ALL non-pending members regardless of is_discoverable -- a count is
// a membership-SIZE quantity, not a per-viewer roster-visibility filter. A
// non-discoverable non-pending member fetched AS NON-OWNER must still be
// counted (else a future accidental discoverability filter on the count
// aggregate would go uncaught, even though it would visibly shrink
// ListGroupMembers for non-owner viewers).
func TestListGroups_Counts_IndependentOfDiscoverability(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := countsNewPool(t)
	defer pool.Close()

	owner := countsInsertUser(t, ctx, pool, 990601, "counts-disc-owner", true)
	hiddenMember := countsInsertUser(t, ctx, pool, 990602, "counts-disc-hidden", false) // is_discoverable=false
	viewerMember := countsInsertUser(t, ctx, pool, 990603, "counts-disc-viewer", true)  // non-owner caller
	users := []pgtype.UUID{owner, hiddenMember, viewerMember}
	defer cleanupOwners(t, ctx, pool, users...)

	group := countsInsertGroup(t, ctx, pool, owner, "counts-disc-group")
	countsAddMember(t, ctx, pool, group, owner, "owner")
	countsAddMember(t, ctx, pool, group, hiddenMember, "member")
	countsAddMember(t, ctx, pool, group, viewerMember, "member")

	h := &Handler{pool: pool, queries: sqlc.New(pool)}
	// Fetch as the non-owner viewer -- ListGroupMembers would hide the
	// non-discoverable member for this viewer, but member_count must not.
	row := countsFindGroup(t, countsGetGroups(t, h, viewerMember), group)

	if row.MemberCount == nil || *row.MemberCount != 3 {
		t.Errorf("member_count: got %v, want 3 (owner + hidden non-discoverable member + viewer, all non-pending); discoverability must not filter the count", row.MemberCount)
	}
}

// TestListGroups_Counts_Scoping pins that counts reflect the WHOLE group
// (not just the caller's own rows), that a non-member's group does not
// appear in their GET /groups response at all, and (regression) that
// role/member_since are still present and correct alongside the new fields.
func TestListGroups_Counts_Scoping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := countsNewPool(t)
	defer pool.Close()

	owner := countsInsertUser(t, ctx, pool, 990701, "counts-scope-owner", true)
	member := countsInsertUser(t, ctx, pool, 990702, "counts-scope-member", true)
	outsider := countsInsertUser(t, ctx, pool, 990703, "counts-scope-outsider", true)
	users := []pgtype.UUID{owner, member, outsider}
	defer cleanupOwners(t, ctx, pool, users...)

	group := countsInsertGroup(t, ctx, pool, owner, "counts-scope-group")
	countsAddMember(t, ctx, pool, group, owner, "owner")
	countsAddMember(t, ctx, pool, group, member, "member")

	tr := countsInsertTranscript(t, ctx, pool, owner, "counts-scope-t1")
	countsShare(t, ctx, pool, tr, group, "approved")

	h := &Handler{pool: pool, queries: sqlc.New(pool)}

	// The non-owner member sees the SAME group-wide counts as the owner
	// (counts are not scoped down to "rows the caller can see").
	memberRow := countsFindGroup(t, countsGetGroups(t, h, member), group)
	if memberRow.MemberCount == nil || *memberRow.MemberCount != 2 {
		t.Errorf("member (non-owner) view member_count: got %v, want 2 (whole group, not just caller's row)", memberRow.MemberCount)
	}
	if memberRow.TranscriptCount == nil || *memberRow.TranscriptCount != 1 {
		t.Errorf("member (non-owner) view transcript_count: got %v, want 1", memberRow.TranscriptCount)
	}
	if memberRow.Role != "member" {
		t.Errorf("regression: role got %q, want %q", memberRow.Role, "member")
	}
	if memberRow.MemberSince == nil || *memberRow.MemberSince == "" {
		t.Error("regression: member_since is absent/empty, want a timestamp")
	}

	// A non-member (outsider) does not see this group at all.
	outsiderRows := countsGetGroups(t, h, outsider)
	for _, r := range outsiderRows {
		if r.ID == uuid.UUID(group.Bytes).String() {
			t.Errorf("outsider (non-member) unexpectedly sees group %s in GET /groups", r.ID)
		}
	}
}
