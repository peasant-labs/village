//go:build integration

package handler

// PATCH /groups/{id}/shares decides a whole selection of pending submissions in
// one action. This is the review counterpart of the batch SUBMIT route: it
// moves open attempts to 'approved' or 'rejected' and touches nothing else.
//
// Every case is driven through the real HTTP handler against a real PostgreSQL,
// on the contributeWorld harness. Submissions are opened by the shipped
// single-share handler and any pre-existing decision is made by the shipped
// single-review handler, so a case's starting state is built by production
// write paths rather than written behind them.

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/config"
)

// ---------------------------------------------------------------------------
// Fixture shapes
// ---------------------------------------------------------------------------

type batchReviewCase struct {
	Name                 string                       `yaml:"name"`
	Why                  string                       `yaml:"why"`
	Transcripts          []identityListTranscriptSpec `yaml:"transcripts"`
	Submit               []string                     `yaml:"submit"`
	SubmitForeign        []string                     `yaml:"submit_foreign"`
	PreDecide            map[string]string            `yaml:"pre_decide"`
	Actor                string                       `yaml:"actor"`
	Status               string                       `yaml:"status"`
	Request              []string                     `yaml:"request"`
	ExpectStatus         int                          `yaml:"expect_status"`
	ExpectDecided        []string                     `yaml:"expect_decided"`
	ExpectAlreadyDecided []string                     `yaml:"expect_already_decided"`
	ExpectDerived        map[string]string            `yaml:"expect_derived"`
	ExpectForeignDerived map[string]string            `yaml:"expect_foreign_derived"`
}

// requiredBatchReviewCases names the shapes that must stay covered. Each one
// exists because losing it hides a distinct real failure: a route that only
// works within one project, one that refuses a whole batch because a single
// row went stale, one that only works in the approve direction, one that
// writes before checking the caller is a reviewer, one that a collective's
// owner could use to reach another collective's submissions, and one that
// turns an empty selection into an error the UI has to explain.
var requiredBatchReviewCases = []string{
	"mixed_projects_one_action",
	"partial_already_decided",
	"reject_selection",
	"non_owner_refused",
	"cross_collective_id_not_decided",
	"empty_selection_is_a_no_op",
}

//go:embed testdata/groups-batch-review.yaml
var groupsBatchReviewYAML []byte

func loadBatchReviewCases(t *testing.T) []batchReviewCase {
	t.Helper()
	cases, err := decodeFixtureRows[batchReviewCase](groupsBatchReviewYAML)
	if err != nil {
		t.Fatalf("load the batch-review corpus: %v", err)
	}
	present := map[string]bool{}
	for _, testCase := range cases {
		if testCase.Name == "" {
			t.Fatalf("a batch-review case has no name; every case is addressed by name")
		}
		if present[testCase.Name] {
			t.Fatalf("the batch-review corpus repeats case %q", testCase.Name)
		}
		present[testCase.Name] = true
		if testCase.Why == "" {
			t.Fatalf("case %q states no reason it exists; a case nobody can justify cannot be maintained", testCase.Name)
		}
		if testCase.ExpectStatus == 0 {
			t.Fatalf("case %q declares no expect_status", testCase.Name)
		}
		if len(testCase.Transcripts) == 0 {
			t.Fatalf("case %q builds no transcript, so it asks the route nothing", testCase.Name)
		}
		if testCase.Actor != "owner" && testCase.Actor != "member" {
			t.Fatalf("case %q names actor %q; a batch review is sent either by the collective's owner or by a plain member",
				testCase.Name, testCase.Actor)
		}
	}
	for _, name := range requiredBatchReviewCases {
		if !present[name] {
			t.Fatalf("the batch-review corpus no longer contains %q. That case exists because losing it hides a real "+
				"failure; restore it rather than deleting it from this manifest.", name)
		}
	}
	return cases
}

// ---------------------------------------------------------------------------
// World extensions
// ---------------------------------------------------------------------------

// secondCollective creates another curated collective owned by the SAME
// moderator, so a case can ask whether a batch scoped to one collective can
// reach a submission that lives in another one. Sharing the owner is the point:
// a scope that leaked would leak for a caller who genuinely is an owner.
func (w *contributeWorld) secondCollective(t *testing.T, ctx context.Context) pgtype.UUID {
	t.Helper()
	var other pgtype.UUID
	if err := w.pool.QueryRow(ctx, `
		INSERT INTO groups (name, created_by, acceptance_mode) VALUES ($1, $2, 'curated') RETURNING id
	`, "contribute-other-"+w.suffix, w.moderator).Scan(&other); err != nil {
		t.Fatalf("create the second collective: %v", err)
	}
	shareAddMember(t, ctx, w.pool, other, w.moderator, "owner")
	shareAddMember(t, ctx, w.pool, other, w.member, "member")
	return other
}

// submitTo offers one transcript to a NAMED collective through the shipped
// single-share handler.
func (w *contributeWorld) submitTo(t *testing.T, h *Handler, key string, group pgtype.UUID) {
	t.Helper()
	body, err := json.Marshal(map[string][]string{"group_ids": {uuid.UUID(group.Bytes).String()}})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ShareTranscript(rec, w.request(http.MethodPost, "/api/v1/transcripts/share", body,
		map[string]string{"id": uuid.UUID(w.transcripts[key].Bytes).String()}))
	if rec.Code != http.StatusOK {
		t.Fatalf("preparing the case: the single share of %q answered %d, want 200 (body: %s)",
			key, rec.Code, rec.Body.String())
	}
}

// preDecide makes a decision through the shipped SINGLE-review handler, which
// is how a case builds the "another moderator already decided this" state
// without writing behind the production path.
func (w *contributeWorld) preDecide(t *testing.T, h *Handler, key, status string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"status": status})
	if err != nil {
		t.Fatal(err)
	}
	transcriptID := uuid.UUID(w.transcripts[key].Bytes).String()
	rec := httptest.NewRecorder()
	h.ReviewShare(rec, shareRequestAs(w.moderator, "contrib-mod-"+w.suffix, http.MethodPatch,
		"/api/v1/groups/"+w.groupID()+"/shares/"+transcriptID, body,
		map[string]string{"id": w.groupID(), "transcriptID": transcriptID}))
	if rec.Code != http.StatusOK {
		t.Fatalf("preparing the case: the single review of %q answered %d, want 200 (body: %s)",
			key, rec.Code, rec.Body.String())
	}
}

// derivedStatusIn reads the derived share row for a (transcript, collective)
// pair, answering "<none>" when the pair has no derived row at all.
func (w *contributeWorld) derivedStatusIn(t *testing.T, ctx context.Context, key string, group pgtype.UUID) string {
	t.Helper()
	var status string
	err := w.pool.QueryRow(ctx,
		"SELECT status FROM transcript_shares WHERE transcript_id = $1 AND group_id = $2",
		w.transcripts[key], group).Scan(&status)
	if err != nil {
		return "<none>"
	}
	return status
}

// ---------------------------------------------------------------------------
// PATCH /groups/{id}/shares
// ---------------------------------------------------------------------------

func TestBatchReviewShares(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	for _, testCase := range loadBatchReviewCases(t) {
		t.Run(testCase.Name, func(t *testing.T) {
			h := New(&config.Config{}, pool, nil)
			world := newContributeWorld(t, ctx, pool, testCase.Name, "curated", true)
			defer world.cleanup(t, ctx)

			for ordinal, transcript := range testCase.Transcripts {
				world.insertTranscript(t, ctx, ordinal, transcript.Key, transcript.Project, "shared", "", transcript.Parent)
			}
			for _, key := range testCase.Submit {
				world.submitTo(t, h, key, world.group)
			}
			var foreign pgtype.UUID
			if len(testCase.SubmitForeign) > 0 {
				foreign = world.secondCollective(t, ctx)
				defer func() {
					if _, err := pool.Exec(ctx, "DELETE FROM groups WHERE id = $1", foreign); err != nil {
						t.Errorf("cleanup the second collective: %v", err)
					}
				}()
				for _, key := range testCase.SubmitForeign {
					world.submitTo(t, h, key, foreign)
				}
			}
			for key, status := range testCase.PreDecide {
				world.preDecide(t, h, key, status)
			}

			ids := make([]string, 0, len(testCase.Request))
			for _, key := range testCase.Request {
				ids = append(ids, uuid.UUID(world.transcripts[key].Bytes).String())
			}
			body, err := json.Marshal(map[string]any{"transcript_ids": ids, "status": testCase.Status})
			if err != nil {
				t.Fatal(err)
			}

			actor, actorName := world.moderator, "contrib-mod-"+world.suffix
			if testCase.Actor == "member" {
				actor, actorName = world.member, world.memberName
			}
			rec := httptest.NewRecorder()
			h.BatchReviewShares(rec, shareRequestAs(actor, actorName, http.MethodPatch,
				"/api/v1/groups/"+world.groupID()+"/shares", body, map[string]string{"id": world.groupID()}))

			if rec.Code != testCase.ExpectStatus {
				t.Fatalf("the batch review answered %d, want %d (%s) (body: %s)",
					rec.Code, testCase.ExpectStatus, testCase.Why, rec.Body.String())
			}

			if rec.Code == http.StatusOK {
				var got struct {
					Decided        []string `json:"decided"`
					AlreadyDecided []string `json:"already_decided"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatalf("decode the batch-review response: %v (body: %s)", err, rec.Body.String())
				}
				world.assertIDSet(t, "decided", got.Decided, testCase.ExpectDecided, testCase.Why)
				world.assertIDSet(t, "already_decided", got.AlreadyDecided, testCase.ExpectAlreadyDecided, testCase.Why)
			}

			// The persisted rows are the real answer: a response body that
			// claims a decision the database did not make would pass every
			// assertion above.
			for key, want := range testCase.ExpectDerived {
				if got := world.derivedStatusIn(t, ctx, key, world.group); got != want {
					t.Errorf("submission %q is %q in this collective, want %q (%s)", key, got, want, testCase.Why)
				}
			}
			for key, want := range testCase.ExpectForeignDerived {
				if got := world.derivedStatusIn(t, ctx, key, foreign); got != want {
					t.Errorf("submission %q is %q in the OTHER collective, want %q (%s). A batch is scoped to the "+
						"collective in its URL; reaching another collective's submission would let one owner decide "+
						"work that was never offered to them.", key, got, want, testCase.Why)
				}
			}
		})
	}
}
