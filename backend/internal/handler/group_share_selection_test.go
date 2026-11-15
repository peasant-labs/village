package handler

// The selection write's answer to the ledger refusing one of its attempts,
// driven through the production handler with its dependencies stubbed. Every
// other behaviour of the route is covered against a real PostgreSQL in
// contribution_selection_write_integration_test.go; this one is here because
// the real database cannot be made to raise it through the application's own
// writers (see testdata/contribution-selection-conflict.yaml).

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/contribution-selection-conflict.yaml
var contributionSelectionConflictYAML []byte

type selectionConflictCase struct {
	Name         string `yaml:"name"`
	Why          string `yaml:"why"`
	SQLState     string `yaml:"sqlstate"`
	Constraint   string `yaml:"constraint"`
	ExpectStatus int    `yaml:"expect_status"`
	ExpectCode   string `yaml:"expect_code"`
}

// requiredSelectionConflictCases names the cases that must exist: both ledger
// constraints the classifier knows, and the two failures it must not mistake
// for one of them.
var requiredSelectionConflictCases = []string{
	"an_open_attempt_refused_by_the_partial_unique_index_is_a_conflict",
	"an_event_ordering_violation_is_a_conflict",
	"an_unrelated_unique_violation_is_a_server_error",
	"a_failure_that_is_not_a_unique_violation_is_a_server_error",
}

func loadSelectionConflictCases(t *testing.T) []selectionConflictCase {
	t.Helper()
	cases, err := decodeFixtureRows[selectionConflictCase](contributionSelectionConflictYAML)
	if err != nil {
		t.Fatalf("load the selection conflict corpus: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || c.Why == "" || c.SQLState == "" || c.Constraint == "" || c.ExpectStatus == 0 {
			t.Fatalf("selection conflict case %+v is missing its name, reason, failure or expected status", c)
		}
		if present[c.Name] {
			t.Fatalf("the selection conflict corpus repeats case %q", c.Name)
		}
		present[c.Name] = true
	}
	for _, required := range requiredSelectionConflictCases {
		if !present[required] {
			t.Fatalf("the selection conflict corpus no longer contains %q; restore it rather than deleting it from "+
				"this manifest", required)
		}
	}
	return cases
}

func selectionConflictConstraint(name string) string {
	switch name {
	case "open_attempt_index":
		return openShareAttemptIndex
	case "event_num_constraint":
		return shareAttemptEventNumConstraint
	default:
		return name
	}
}

func TestBatchShareSelectionAnswersALedgerRefusal(t *testing.T) {
	const projectHash = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	groupID := uuid.MustParse("5b0c7e1a-3d2f-4a8b-9c6d-1e2f3a4b5c6d")
	first := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	second := uuid.MustParse("99999999-2222-4333-8444-555555555555")

	for _, c := range loadSelectionConflictCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			var inserted []uuid.UUID
			q := &mockQuerier{
				getGroupMember: memberStub("member"),
				getGroupByID: func(_ context.Context, id pgtype.UUID) (sqlc.Group, error) {
					return sqlc.Group{ID: id, AcceptanceMode: "open"}, nil
				},
				listOwnerSelectionShareStates: func(_ context.Context, arg sqlc.ListOwnerSelectionShareStatesParams) ([]sqlc.ListOwnerSelectionShareStatesRow, error) {
					var rows []sqlc.ListOwnerSelectionShareStatesRow
					for _, id := range arg.TranscriptIds {
						rows = append(rows, sqlc.ListOwnerSelectionShareStatesRow{
							ID: id, LocalID: "local-" + uuid.UUID(id.Bytes).String(), ProjectHash: projectHash,
							Visibility: dbVisibilityPublic, IsRoot: true,
						})
					}
					return rows, nil
				},
				getTranscriptGovernanceForUpdate: func(_ context.Context, id pgtype.UUID) (sqlc.GetTranscriptGovernanceForUpdateRow, error) {
					return sqlc.GetTranscriptGovernanceForUpdateRow{ID: id, Visibility: dbVisibilityPublic}, nil
				},
				shareTranscriptWithStatus: func(_ context.Context, arg sqlc.ShareTranscriptWithStatusParams) error {
					inserted = append(inserted, uuid.UUID(arg.TranscriptID.Bytes))
					if uuid.UUID(arg.TranscriptID.Bytes) == second {
						return &pgconn.PgError{Code: c.SQLState, ConstraintName: selectionConflictConstraint(c.Constraint)}
					}
					return nil
				},
			}
			h := newTestHandler(q, nil)
			r := chi.NewRouter()
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					next.ServeHTTP(w, req.WithContext(withUserID(req.Context(), contractBodyTestUser)))
				})
			})
			r.Post("/api/v1/collectives/{id}/shares/selection", h.BatchShareSelection)

			body := `{"project_hash":"` + projectHash + `","transcript_ids":["` + second.String() + `","` + first.String() +
				`"],"private_approvals":[]}`
			req := httptest.NewRequest(http.MethodPost, "/api/v1/collectives/"+groupID.String()+"/shares/selection",
				strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != c.ExpectStatus {
				t.Fatalf("status = %d, want %d (%s): %s", rec.Code, c.ExpectStatus, c.Why, rec.Body.String())
			}
			if len(inserted) != 2 || inserted[0] != first || inserted[1] != second {
				t.Fatalf("attempts were opened for %v, want %v then %v: the failure must come from the second insert, "+
					"after the first succeeded, so the answer is about rolling a partial write back", inserted, first, second)
			}

			var resp schema.VillageErrorResponse
			decoder := json.NewDecoder(strings.NewReader(rec.Body.String()))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&resp); err != nil {
				t.Fatalf("the refusal does not decode into the contract's VillageErrorResponse: %v\nbody: %s", err, rec.Body.String())
			}
			if c.ExpectCode == "" {
				if resp.Code != nil || len(resp.Details) != 0 {
					t.Fatalf("a server error carries code %v and details %v; only a refusal is typed", resp.Code, resp.Details)
				}
				if !strings.Contains(resp.Error, "rolled back") {
					t.Fatalf("the server error %q does not say the contribution was rolled back", resp.Error)
				}
				return
			}
			if resp.Code == nil || string(*resp.Code) != c.ExpectCode {
				t.Fatalf("code = %v, want %q", resp.Code, c.ExpectCode)
			}
			if len(resp.Details) != 1 || resp.Details[0].TranscriptID == nil ||
				string(*resp.Details[0].TranscriptID) != second.String() || string(resp.Details[0].Code) != c.ExpectCode ||
				resp.Details[0].CurrentProjectHash != nil {
				t.Fatalf("details = %+v, want exactly one naming %s with code %s", resp.Details, second, c.ExpectCode)
			}
			for _, want := range []string{"1 of the 2 sessions", "submitted by another request at the same moment", "nothing was written"} {
				if !strings.Contains(resp.Error, want) {
					t.Fatalf("the refusal %q does not say %q", resp.Error, want)
				}
			}
		})
	}
}
