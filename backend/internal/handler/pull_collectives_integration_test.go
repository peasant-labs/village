//go:build integration

package handler

// Integration coverage for the collectives filter on the pull list.
//
// The filter is enforced entirely in unmocked SQL. ListPullableTranscriptsInGroup
// and CountPullableTranscriptsInGroup must agree with each other, must exclude a
// transcript the requester owns but never shared with a listed collective, and
// must contribute nothing for a collective the requester is not in. A regression
// that reused the unfiltered predicate (owner OR approved share) behind a nullable
// group condition would leak the requester's own unshared transcripts into a
// filtered page; a regression that dropped the group_members join would answer
// for a collective the requester cannot see. Both would pass every mock-querier
// handler test and fail here.
//
//	TEST_DATABASE_URL="postgres://test:test@localhost:55432/village_test?sslmode=disable" \
//	  go test -tags=integration ./internal/handler/...

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// TestPull_CollectivesFilter_RealPostgres seeds the membership matrix and asserts
// the filtered list, its count, the union of two collectives, and the no-oracle
// case (a collective the requester is not in contributes nothing, exactly like one
// that does not exist).
func TestPull_CollectivesFilter_RealPostgres(t *testing.T) {
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

	owner := pullInsertUser(t, ctx, pool, 981001, "collective-owner")
	member := pullInsertUser(t, ctx, pool, 981002, "collective-member")
	stranger := pullInsertUser(t, ctx, pool, 981003, "collective-stranger")
	users := []pgtype.UUID{owner, member, stranger}
	defer func() {
		_, _ = pool.Exec(ctx, "DELETE FROM groups WHERE created_by = ANY($1)", users)
		cleanupOwners(t, ctx, pool, users...)
	}()

	groupA := pullInsertGroup(t, ctx, pool, owner, "pull-collective-A")
	groupB := pullInsertGroup(t, ctx, pool, owner, "pull-collective-B")
	groupH := pullInsertGroup(t, ctx, pool, owner, "pull-collective-H")
	// The member belongs to A and B only. H exists and holds an approved share,
	// but the member is not in it.
	pullAddMember(t, ctx, pool, groupA, member, "member")
	pullAddMember(t, ctx, pool, groupB, member, "member")

	tsOwnedPrivate := pullInsertTranscript(t, ctx, pool, member, "owned-private", dbVisibilityPrivate)
	tsOwnedInA := pullInsertTranscript(t, ctx, pool, member, "owned-in-A", dbVisibilityShared)
	tsApprovedA := pullInsertTranscript(t, ctx, pool, owner, "approved-A", dbVisibilityShared)
	tsApprovedB := pullInsertTranscript(t, ctx, pool, owner, "approved-B", dbVisibilityShared)
	tsApprovedH := pullInsertTranscript(t, ctx, pool, owner, "approved-H", dbVisibilityShared)
	tsPendingA := pullInsertTranscript(t, ctx, pool, owner, "pending-A", dbVisibilityShared)
	tsRejectedA := pullInsertTranscript(t, ctx, pool, owner, "rejected-A", dbVisibilityShared)
	tsPublic := pullInsertTranscript(t, ctx, pool, stranger, "public", dbVisibilityPublic)
	tsMultiAB := pullInsertTranscript(t, ctx, pool, owner, "multi-A-B", dbVisibilityShared)

	pullShare(t, ctx, pool, tsOwnedInA, groupA, "approved")
	pullShare(t, ctx, pool, tsApprovedA, groupA, "approved")
	pullShare(t, ctx, pool, tsApprovedB, groupB, "approved")
	pullShare(t, ctx, pool, tsApprovedH, groupH, "approved")
	pullShare(t, ctx, pool, tsPendingA, groupA, "pending")
	pullShare(t, ctx, pool, tsRejectedA, groupA, "rejected")
	pullShare(t, ctx, pool, tsMultiAB, groupA, "approved")
	pullShare(t, ctx, pool, tsMultiAB, groupB, "approved")

	h := &Handler{pool: pool, queries: sqlc.New(pool)}

	// set runs the real filtered list and count and returns the listed id set.
	listSet := func(name string, collectives ...pgtype.UUID) map[uuid.UUID]bool {
		t.Helper()
		rows, err := h.queries.ListPullableTranscriptsInGroup(ctx, sqlc.ListPullableTranscriptsInGroupParams{
			UserID:      member,
			Collectives: collectives,
			PageLimit:   100,
			PageOffset:  0,
		})
		if err != nil {
			t.Fatalf("%s: ListPullableTranscriptsInGroup: %v", name, err)
		}
		total, err := h.queries.CountPullableTranscriptsInGroup(ctx, sqlc.CountPullableTranscriptsInGroupParams{
			UserID:      member,
			Collectives: collectives,
		})
		if err != nil {
			t.Fatalf("%s: CountPullableTranscriptsInGroup: %v", name, err)
		}
		if int(total) != len(rows) {
			t.Errorf("%s: count %d disagrees with %d listed rows", name, total, len(rows))
		}
		out := make(map[uuid.UUID]bool, len(rows))
		for _, r := range rows {
			id := uuid.UUID(r.ID.Bytes)
			if out[id] {
				t.Errorf("%s: DISTINCT dedup violated: id %s appears twice", name, id)
			}
			out[id] = true
		}
		return out
	}

	// expect asserts the set is EXACTLY the named transcripts. Membership is what
	// the access boundary decides, so the test asserts the closed set, not a count.
	expect := func(name string, got map[uuid.UUID]bool, want ...pgtype.UUID) {
		t.Helper()
		wantSet := make(map[uuid.UUID]bool, len(want))
		for _, w := range want {
			wantSet[uuid.UUID(w.Bytes)] = true
		}
		if len(got) != len(wantSet) {
			t.Fatalf("%s: got %d transcripts %s, want %d", name, len(got), idsOf(got), len(wantSet))
		}
		for id := range wantSet {
			if !got[id] {
				t.Errorf("%s: missing transcript %s (got %s)", name, id, idsOf(got))
			}
		}
	}

	// The single-collective shelf is the approved shares into that collective.
	expect("collectives=[A]", listSet("[A]", groupA), tsOwnedInA, tsApprovedA, tsMultiAB)
	expect("collectives=[B]", listSet("[B]", groupB), tsApprovedB, tsMultiAB)

	// Several collectives: the union, each transcript once.
	expect("collectives=[A,B]", listSet("[A,B]", groupA, groupB), tsOwnedInA, tsApprovedA, tsApprovedB, tsMultiAB)

	// A collective the member is not in contributes nothing, exactly like a
	// collective that does not exist. No error, no rows: no existence oracle.
	expect("collectives=[H] (not a member)", listSet("[H]", groupH))
	expect("collectives=[unknown]", listSet("[unknown]", toPgUUID(uuid.New())))

	// The excluded rows stay excluded from the union. Pending and rejected shares,
	// public visibility, and a collective the member is not in.
	union := listSet("[A,B]", groupA, groupB)
	for _, excluded := range []struct {
		name string
		id   pgtype.UUID
	}{
		{"pending share", tsPendingA},
		{"rejected share", tsRejectedA},
		{"public visibility", tsPublic},
		{"collective the member is not in", tsApprovedH},
	} {
		if union[uuid.UUID(excluded.id.Bytes)] {
			t.Errorf("%s leaked into collectives=[A,B]", excluded.name)
		}
	}

	// The contrast that pins the filtered predicate: the unfiltered list admits the
	// member's own private transcript, the filtered list does not.
	unfiltered, err := h.queries.ListPullableTranscripts(ctx, sqlc.ListPullableTranscriptsParams{
		UserID: member, Limit: 100, Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListPullableTranscripts: %v", err)
	}
	foundOwned := false
	for _, r := range unfiltered {
		if uuid.UUID(r.ID.Bytes) == uuid.UUID(tsOwnedPrivate.Bytes) {
			foundOwned = true
		}
	}
	if !foundOwned {
		t.Errorf("the unfiltered list must include the member's own private transcript %s", uuid.UUID(tsOwnedPrivate.Bytes))
	}
	if union[uuid.UUID(tsOwnedPrivate.Bytes)] {
		t.Errorf("collectives=[A,B] must NOT include the member's own unshared transcript")
	}
}

// idsOf renders a set of transcript ids for a failure message.
func idsOf(set map[uuid.UUID]bool) string {
	out := ""
	for id := range set {
		out += id.String() + " "
	}
	return out
}
