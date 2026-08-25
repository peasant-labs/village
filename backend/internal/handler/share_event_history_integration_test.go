//go:build integration

package handler

// The owner-only share-event history endpoint, driven through the REAL HTTP
// handler against a REAL PostgreSQL.
//
// It reuses shareWorld's step vocabulary (submit/decide/unshare/remove) from
// share_attempts_integration_test.go to build a genuine attempt ledger, then
// reads it back through ListShareEventHistory as three different viewers:
// the owner (the only caller this route ever permits), a different,
// signed-in member of the SAME collective (proves owner-only is enforced by
// the route even for a legitimate collective member), and no authenticated
// caller at all (the handler's defensive fallback).

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/config"
)

//go:embed testdata/share-event-history.yaml
var shareEventHistoryYAML []byte

type shareEventHistoryExpectedEvent struct {
	EventNum int    `yaml:"event_num"`
	Status   string `yaml:"status"`
	Decided  bool   `yaml:"decided"`
	Actor    string `yaml:"actor"`
}

type shareEventHistoryCase struct {
	Name         string                           `yaml:"name"`
	Why          string                           `yaml:"why"`
	Steps        []shareAttemptStep               `yaml:"steps"`
	Viewer       string                           `yaml:"viewer"`
	ExpectStatus int                              `yaml:"expect_status"`
	Events       []shareEventHistoryExpectedEvent `yaml:"events"`
}

// requiredShareEventHistoryCases names the cases that must exist. Each one is
// here because losing it would hide a specific failure: the mixed-state
// ordering, the pending event's absent actor, the never-a-raw-user-id
// guarantee on the two actor-less write paths, and the two 404 boundaries
// that make owner-only a route property rather than a predicate a caller
// could bypass.
var requiredShareEventHistoryCases = []string{
	"full_history_all_five_states_in_order",
	"pending_event_carries_no_decided_at_and_no_actor",
	"retracted_and_revoked_history_never_names_a_raw_user",
	"non_owner_signed_in_collective_member_gets_404",
	"anonymous_caller_gets_404",
}

func loadShareEventHistoryCases(t *testing.T) []shareEventHistoryCase {
	t.Helper()
	cases, err := decodeFixtureRows[shareEventHistoryCase](shareEventHistoryYAML)
	if err != nil {
		t.Fatalf("load the share-event-history fixture: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if present[c.Name] {
			t.Fatalf("the share-event-history fixture repeats case %q", c.Name)
		}
		present[c.Name] = true
		if c.Why == "" {
			t.Fatalf("case %q states no reason it exists; a case nobody can justify cannot be maintained", c.Name)
		}
		if c.ExpectStatus == 0 {
			t.Fatalf("case %q declares no expect_status", c.Name)
		}
	}
	for _, required := range requiredShareEventHistoryCases {
		if !present[required] {
			t.Fatalf("the share-event-history fixture no longer contains %q. That case exists because losing it hides a "+
				"real failure; restore it rather than removing it from this manifest.", required)
		}
	}
	return cases
}

func TestShareEventHistory(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	h := New(&config.Config{}, pool, nil)

	for _, tc := range loadShareEventHistoryCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			world := newShareWorld(t, ctx, pool, tc.Name, "curated")
			defer world.cleanup(t, ctx)

			other := shareInsertUser(t, ctx, pool, "share-other-"+world.ownerName)
			shareAddMember(t, ctx, pool, world.group, other, "member")
			defer func() {
				if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", other); err != nil {
					t.Errorf("delete the other-member fixture user: %v", err)
				}
			}()

			for i, step := range tc.Steps {
				world.run(t, ctx, h, i, step)
			}

			rec := httptest.NewRecorder()
			h.ListShareEventHistory(rec, world.eventsRequest(t, tc.Viewer, other))

			if rec.Result().StatusCode != tc.ExpectStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Result().StatusCode, tc.ExpectStatus, tc.Why)
			}
			if tc.ExpectStatus != http.StatusOK {
				return
			}

			var got []ShareEventHistoryEntry
			if err := json.NewDecoder(rec.Result().Body).Decode(&got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if len(got) != len(tc.Events) {
				t.Fatalf("got %d events, want %d (%s)", len(got), len(tc.Events), tc.Why)
			}
			for i, want := range tc.Events {
				if int(got[i].EventNum) != want.EventNum {
					t.Errorf("event %d: event_num = %d, want %d (%s)", i, got[i].EventNum, want.EventNum, tc.Why)
				}
				if got[i].Status != want.Status {
					t.Errorf("event %d: status = %q, want %q (%s)", i, got[i].Status, want.Status, tc.Why)
				}
				if (got[i].DecidedAt != nil) != want.Decided {
					t.Errorf("event %d: decided_at present = %v, want %v (%s)", i, got[i].DecidedAt != nil, want.Decided, tc.Why)
				}
				if string(got[i].DecidedByActor) != want.Actor {
					t.Errorf("event %d: decided_by_actor = %q, want %q (%s)", i, got[i].DecidedByActor, want.Actor, tc.Why)
				}
			}
		})
	}
}

// eventsRequest builds a request against ListShareEventHistory as the named
// viewer: owner (the transcript's real owner), other_member (a different,
// signed-in member of the same collective, identified by other), or
// anonymous (no authenticated caller placed in context at all).
func (w *shareWorld) eventsRequest(t *testing.T, viewer string, other pgtype.UUID) *http.Request {
	t.Helper()
	route := chi.NewRouteContext()
	route.URLParams.Add("groupId", uuid.UUID(w.group.Bytes).String())
	route.URLParams.Add("transcriptId", uuid.UUID(w.transcript.Bytes).String())
	target := "/api/v1/users/me/collectives/" + uuid.UUID(w.group.Bytes).String() +
		"/transcripts/" + uuid.UUID(w.transcript.Bytes).String() + "/events"

	reqCtx := context.WithValue(context.Background(), chi.RouteCtxKey, route)
	switch viewer {
	case "owner":
		reqCtx = context.WithValue(reqCtx, UserContextKey, &AuthUser{ID: uuid.UUID(w.owner.Bytes), Username: w.ownerName})
	case "other_member":
		reqCtx = context.WithValue(reqCtx, UserContextKey, &AuthUser{ID: uuid.UUID(other.Bytes), Username: "other-member"})
	case "anonymous":
		// Deliberately no UserContextKey: simulates a caller reaching the
		// handler with no authenticated identity in context.
	default:
		t.Fatalf("fixture names viewer %q; the viewers this test drives are owner, other_member and anonymous", viewer)
	}
	return httptest.NewRequest(http.MethodGet, target, nil).WithContext(reqCtx)
}
