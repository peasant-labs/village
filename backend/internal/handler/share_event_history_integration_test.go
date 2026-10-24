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
	"time"

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

// shareEventHistoryLegacyAttempt is a row installed in the shape migration 036's
// backfill left it: a status carried over from the pre-ledger transcript_shares
// table with no decision time, because that table never recorded one. It is data
// only; the read under test is still the production handler.
type shareEventHistoryLegacyAttempt struct {
	EventNum   int    `yaml:"event_num"`
	Status     string `yaml:"status"`
	RecordedAt string `yaml:"recorded_at"`
}

type shareEventHistoryCase struct {
	Name           string                           `yaml:"name"`
	Why            string                           `yaml:"why"`
	LegacyAttempts []shareEventHistoryLegacyAttempt `yaml:"legacy_attempts"`
	Steps          []shareAttemptStep               `yaml:"steps"`
	Viewer         string                           `yaml:"viewer"`
	ExpectStatus   int                              `yaml:"expect_status"`
	Events         []shareEventHistoryExpectedEvent `yaml:"events"`
}

// requiredShareEventHistoryCases names the cases that must exist. Each one is
// here because losing it would hide a specific failure: the mixed-state
// ordering, the pending event's absent actor, the never-a-raw-user-id
// guarantee on the two actor-less write paths, and the two 404 boundaries
// that make owner-only a route property rather than a predicate a caller
// could bypass.
var requiredShareEventHistoryCases = []string{
	"full_history_all_five_states_in_order",
	"legacy_rejected_null_decision_keeps_whole_history",
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
		for i, legacy := range c.LegacyAttempts {
			if legacy.EventNum != i+1 {
				t.Fatalf("case %q legacy attempt %d declares event_num %d; legacy rows are installed in ordinal order "+
					"from 1 so the case describes the state migration 036's backfill produced", c.Name, i, legacy.EventNum)
			}
			if legacy.Status != "rejected" {
				t.Fatalf("case %q legacy attempt %d declares status %q; only a pre-capture rejection carries a null "+
					"decision time, which is the compatibility case this fixture exists for", c.Name, i, legacy.Status)
			}
			if _, err := time.Parse(time.RFC3339, legacy.RecordedAt); err != nil {
				t.Fatalf("case %q legacy attempt %d has recorded_at %q, which is not RFC 3339: %v",
					c.Name, i, legacy.RecordedAt, err)
			}
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

			world.seedLegacyAttempts(t, ctx, tc.LegacyAttempts)

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

			body := rec.Body.Bytes()
			var got []ShareEventHistoryEntry
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			// The contract's decided_at is REQUIRED and nullable. Decoding into
			// the typed shape cannot tell an explicit null from an omitted
			// member, so keep the raw object alongside it: an unknown decision
			// time must travel as null, never as an absent field.
			var raw []map[string]json.RawMessage
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Fatalf("decode raw response objects: %v", err)
			}
			if len(got) != len(tc.Events) || len(raw) != len(tc.Events) {
				t.Fatalf("got %d events (%d raw), want %d (%s)", len(got), len(raw), len(tc.Events), tc.Why)
			}
			for i, want := range tc.Events {
				if int(got[i].EventNum) != want.EventNum {
					t.Errorf("event %d: event_num = %d, want %d (%s)", i, got[i].EventNum, want.EventNum, tc.Why)
				}
				if string(got[i].Status) != want.Status {
					t.Errorf("event %d: status = %q, want %q (%s)", i, got[i].Status, want.Status, tc.Why)
				}
				decidedRaw, decidedPresent := raw[i]["decided_at"]
				if !decidedPresent {
					t.Errorf("event %d omits decided_at; the wire member is required and nullable, so an unknown "+
						"decision time must be an explicit JSON null (case %s)", i, tc.Name)
				} else if !want.Decided && string(decidedRaw) != "null" {
					t.Errorf("event %d: decided_at = %s, want JSON null. A decision time this row never recorded "+
						"is unknown evidence and must not be derived from recorded_at or the event ordinal (case %s)",
						i, decidedRaw, tc.Name)
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

// seedLegacyAttempts installs the pre-capture rejection shape directly, because
// no current write path can produce it: the production decide path always
// records a decision time. Migration 036's backfill copied the old
// transcript_shares status into attempt 1 and left decided_at and decided_by
// null, so this statement is the same shape the shipped migration wrote. The
// attempt ledger has no audit actor requirement, and the derivation trigger
// still owns the current-state row.
func (w *shareWorld) seedLegacyAttempts(t *testing.T, ctx context.Context, attempts []shareEventHistoryLegacyAttempt) {
	t.Helper()
	for _, attempt := range attempts {
		recordedAt, err := time.Parse(time.RFC3339, attempt.RecordedAt)
		if err != nil {
			t.Fatalf("parse legacy recorded_at %q: %v", attempt.RecordedAt, err)
		}
		if _, err := w.pool.Exec(ctx, `
			INSERT INTO transcript_share_attempts
				(transcript_id, group_id, event_num, status, recorded_at, decided_at, decided_by)
			VALUES ($1, $2, $3, $4, $5, NULL, NULL)
		`, w.transcript, w.group, attempt.EventNum, attempt.Status, recordedAt); err != nil {
			t.Fatalf("install legacy attempt %d (%s): %v", attempt.EventNum, attempt.Status, err)
		}
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
