//go:build integration

package handler

// The owner-only ledger-pairs endpoint, driven through the REAL HTTP handler
// against a REAL PostgreSQL.
//
// It has to be a real database. The distinction the endpoint exists to make -
// a pair that still has a current-state row versus one whose last event was a
// withdrawal and therefore has none - is produced by the derivation trigger,
// so a mock-backed test computing its own expected rows would pass against a
// listing that read the projection, which is the exact bug.
//
// It reuses collectiveWorld from collectives_visibility_integration_test.go so
// the ledger is built by the shipped share handlers rather than written behind
// them.

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/peasant-labs/village/backend/internal/config"
)

//go:embed testdata/owner-collective-submissions.yaml
var ownerCollectiveSubmissionsYAML []byte

type ocsExpectedPair struct {
	Transcript int    `yaml:"transcript"`
	Status     string `yaml:"status"`
	EventNum   int32  `yaml:"event_num"`
}

type ocsCase struct {
	Name         string            `yaml:"name"`
	Why          string            `yaml:"why"`
	Transcripts  []cvTranscript    `yaml:"transcripts"`
	Viewer       string            `yaml:"viewer"`
	ExpectStatus int               `yaml:"expect_status"`
	Pairs        []ocsExpectedPair `yaml:"pairs"`
}

// requiredOwnerCollectiveSubmissionCases names the cases that must exist. Each
// is here because losing it hides a specific failure: the ordinary live pair,
// the two shapes of withdrawal that leave no current-state row, the exact
// history the acceptance test found, the open submission, the mixed listing
// that a projection-backed read would silently shorten instead of emptying,
// and the three boundaries that make owner-only a property of the route.
var requiredOwnerCollectiveSubmissionCases = []string{
	"pair_with_a_live_current_state_row",
	"fully_withdrawn_pair_is_still_listed",
	"revoked_pair_is_listed_as_revoked_not_retracted",
	"refused_three_times_then_withdrawn_pair_is_still_listed",
	"pair_whose_latest_event_is_pending",
	"live_and_fully_withdrawn_pairs_are_both_listed",
	"non_owner_signed_in_collective_member_gets_404",
	"other_signed_in_caller_gets_404",
	"anonymous_caller_gets_404",
}

func loadOwnerCollectiveSubmissionCases(t *testing.T) []ocsCase {
	t.Helper()
	cases, err := decodeFixtureRows[ocsCase](ownerCollectiveSubmissionsYAML)
	if err != nil {
		t.Fatalf("load the owner-submissions corpus: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" {
			t.Fatalf("an owner-submissions case has no name; every case is addressed by name")
		}
		if present[c.Name] {
			t.Fatalf("the owner-submissions corpus repeats case %q", c.Name)
		}
		present[c.Name] = true
		if c.Why == "" {
			t.Fatalf("case %q states no reason it exists; a case nobody can justify cannot be maintained", c.Name)
		}
		if c.ExpectStatus == 0 {
			t.Fatalf("case %q declares no expect_status", c.Name)
		}
		if len(c.Transcripts) == 0 {
			t.Fatalf("case %q builds no transcript, so it asks the endpoint nothing", c.Name)
		}
		assertReachableSubmissionHistory(t, c)
	}
	for _, required := range requiredOwnerCollectiveSubmissionCases {
		if !present[required] {
			t.Fatalf("the owner-submissions corpus no longer contains %q. That case exists because losing it hides a "+
				"real failure; restore it rather than deleting it from this manifest.", required)
		}
	}
	return cases
}

// assertReachableSubmissionHistory refuses a case whose history the write paths
// could never produce. Every collective in this corpus is curated, so the
// reachable states of one pair are: nothing yet, one OPEN attempt, or a closed
// attempt that was approved, rejected, retracted or revoked. A partial unique
// index refuses a second open attempt, only an open attempt can be decided or
// withdrawn, and only an ACCEPTED contribution can be removed by the
// collective. A case describing a state production cannot reach could not fail
// for a reason a real regression would.
func assertReachableSubmissionHistory(t *testing.T, c ocsCase) {
	t.Helper()
	const (
		none     = "no attempt yet"
		pending  = "an open submission"
		approved = "an accepted contribution"
		closed   = "a closed attempt"
	)
	for i, transcript := range c.Transcripts {
		state := none
		for j, step := range transcript.Steps {
			refuse := func(reason string) {
				t.Helper()
				t.Fatalf("case %q, transcript %d, step %d performs %q against %s. %s, so no write path can reach this "+
					"history and the case could not fail for a reason a real regression would.", c.Name, i, j, step, state, reason)
			}
			switch step {
			case "submit":
				if state == pending {
					refuse("a partial unique index refuses a second open attempt")
				}
				if state == approved {
					refuse("an accepted contribution is already there and offering it again is a duplicate")
				}
				state = pending
			case "approve":
				if state != pending {
					refuse("only an open submission can be decided")
				}
				state = approved
			case "reject":
				if state != pending {
					refuse("only an open submission can be decided")
				}
				state = closed
			case "unshare":
				if state != pending && state != approved {
					refuse("the owner withdraws either an open submission or an accepted contribution, and nothing else is live")
				}
				state = closed
			case "remove":
				if state != approved {
					refuse("the collective removes an ACCEPTED contribution")
				}
				state = closed
			default:
				t.Fatalf("case %q, transcript %d, step %d asks for %q; this corpus drives submit, approve, reject, "+
					"unshare and remove", c.Name, i, j, step)
			}
		}
	}
}

func TestOwnerCollectiveSubmissions(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	h := New(&config.Config{}, pool, nil)

	for _, testCase := range loadOwnerCollectiveSubmissionCases(t) {
		t.Run(testCase.Name, func(t *testing.T) {
			world := newCollectiveWorld(t, ctx, pool, collectiveWorldSpec{
				caseName: testCase.Name,
				// Public, so collective visibility can never be the reason a
				// row is missing, and CURATED, so a submission opens an
				// attempt the corpus can then decide or withdraw. The only
				// variables are the ledger and the caller's identity.
				dataAccess:     "public",
				acceptanceMode: "curated",
				discoverable:   true,
				visibility:     "public",
				transcripts:    len(testCase.Transcripts),
			})
			defer world.cleanup(t, ctx)

			for i, transcript := range testCase.Transcripts {
				for _, step := range transcript.Steps {
					world.step(t, h, i, step)
				}
			}

			actor, _ := world.viewer(t, testCase.Viewer)
			rec := httptest.NewRecorder()
			h.ListMyCollectiveSubmissions(rec, world.request(actor, http.MethodGet,
				"/api/v1/users/me/collectives/"+world.groupID()+"/submissions", nil,
				map[string]string{"groupId": world.groupID()}))

			if rec.Code == http.StatusForbidden {
				t.Fatalf("the submissions surface refused with 403. A refusal confirms the history exists and is being "+
					"withheld, which is the disclosure 404 exists to avoid (%s)", testCase.Why)
			}
			if rec.Code != testCase.ExpectStatus {
				t.Fatalf("the submissions surface answered %d, want %d (%s) (body: %s)",
					rec.Code, testCase.ExpectStatus, testCase.Why, rec.Body.String())
			}
			if testCase.ExpectStatus != http.StatusOK {
				return
			}

			var got []OwnerCollectiveSubmission
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode the submissions body: %v (body: %s)", err, rec.Body.String())
			}
			world.assertSubmissionPairs(t, got, testCase)
		})
	}
}

// assertSubmissionPairs matches the answer against the declared pairs by
// TRANSCRIPT, not by position: the response is ordered by recorded_at, and two
// events recorded in the same clock tick would make a positional assertion
// flake without saying anything about the endpoint.
func (w *collectiveWorld) assertSubmissionPairs(t *testing.T, got []OwnerCollectiveSubmission, testCase ocsCase) {
	t.Helper()
	byTranscript := map[string]OwnerCollectiveSubmission{}
	for _, row := range got {
		id := uuid.UUID(row.TranscriptID.Bytes).String()
		if _, repeated := byTranscript[id]; repeated {
			t.Fatalf("transcript %s appears twice in the answer; the listing serves the LATEST event of each pair, one "+
				"row per pair (%s)", id, testCase.Why)
		}
		byTranscript[id] = row
	}
	if len(got) != len(testCase.Pairs) {
		t.Fatalf("the submissions surface returned %d pair(s), want %d. A listing that reads the derived current-state "+
			"row instead of the attempt ledger loses exactly the withdrawn pairs, which shortens this answer rather "+
			"than emptying it (%s)", len(got), len(testCase.Pairs), testCase.Why)
	}
	for _, want := range testCase.Pairs {
		id := uuid.UUID(w.transcripts[want.Transcript].Bytes).String()
		row, ok := byTranscript[id]
		if !ok {
			t.Fatalf("the submissions surface omits transcript %d, whose latest event is %q. A pair whose last event was "+
				"a withdrawal has NO current-state row, and it must still be listed (%s)", want.Transcript, want.Status, testCase.Why)
		}
		if row.Status != want.Status {
			t.Errorf("transcript %d: status = %q, want %q; the row carries the LATEST ledger event, and retracted and "+
				"revoked stay distinct because the surface names the actor (%s)", want.Transcript, row.Status, want.Status, testCase.Why)
		}
		if row.EventNum != want.EventNum {
			t.Errorf("transcript %d: event_num = %d, want %d; it is the ordinal of the latest event, so a listing that "+
				"served an earlier one would be reporting stale state (%s)", want.Transcript, row.EventNum, want.EventNum, testCase.Why)
		}
		if uuid.UUID(row.GroupID.Bytes).String() != w.groupID() {
			t.Errorf("transcript %d: group_id = %s, want %s; the listing is scoped to one collective",
				want.Transcript, uuid.UUID(row.GroupID.Bytes).String(), w.groupID())
		}
		if row.RecordedAt.IsZero() {
			t.Errorf("transcript %d: recorded_at is the zero time; every ledger event carries when it was recorded (%s)",
				want.Transcript, testCase.Why)
		}
		if !row.Title.Valid || row.Title.String == "" {
			t.Errorf("transcript %d: the row carries no title, so the listing can only name the transcript by its id (%s)",
				want.Transcript, testCase.Why)
		}
	}
}
