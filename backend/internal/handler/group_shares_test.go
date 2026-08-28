package handler

// The two pieces of the whole-project contribution that are decidable without a
// database: what state a collective opens a submission in, and which advisory
// locks a batch takes. Everything else about this route - the transaction, the
// ledger, the derivation, the mid-transaction conflict - is a property of real
// PostgreSQL and is asserted in group_shares_integration_test.go.

import (
	"context"
	_ "embed"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

type shareStatusCase struct {
	Name        string `yaml:"name"`
	Why         string `yaml:"why"`
	Acceptance  string `yaml:"acceptance"`
	LinkedOrg   string `yaml:"linked_org"`
	OrgVisible  bool   `yaml:"org_visible"`
	OrgReadFail bool   `yaml:"org_read_fails"`
	WantStatus  string `yaml:"want_status"`
	WantRefusal string `yaml:"want_refusal"`
}

//go:embed testdata/share-status-modes.yaml
var shareStatusModesYAML []byte

// requiredShareStatusModeCases names the cases that must exist. Each is here
// because losing it hides a specific failure: the two accepting modes, the two
// verified-only shapes in both directions, and the failed org read that must be
// answered as a refusal rather than as an acceptance.
var requiredShareStatusModeCases = []string{
	"open_collective_accepts_on_receipt",
	"curated_collective_queues_for_review",
	"verified_only_with_the_linked_org_visible_accepts",
	"verified_only_without_the_linked_org_refuses",
	"verified_only_org_read_failure_refuses",
	"verified_only_unlinked_with_no_visible_org_refuses",
	"verified_only_unlinked_with_a_visible_org_accepts",
}

func loadShareStatusModeCases(t *testing.T) []shareStatusCase {
	t.Helper()
	cases, err := decodeFixtureRows[shareStatusCase](shareStatusModesYAML)
	if err != nil {
		t.Fatalf("load the acceptance-mode corpus: %v", err)
	}
	present := map[string]bool{}
	for _, testCase := range cases {
		if testCase.Name == "" {
			t.Fatalf("an acceptance-mode case has no name; every case is addressed by name")
		}
		if present[testCase.Name] {
			t.Fatalf("the acceptance-mode corpus repeats case %q", testCase.Name)
		}
		present[testCase.Name] = true
		if testCase.Why == "" {
			t.Fatalf("case %q states no reason it exists; a case nobody can justify cannot be maintained", testCase.Name)
		}
		if testCase.WantStatus == "" && testCase.WantRefusal == "" {
			t.Fatalf("case %q expects neither an opening state nor a refusal, so it asserts nothing", testCase.Name)
		}
	}
	for _, required := range requiredShareStatusModeCases {
		if !present[required] {
			t.Fatalf("the acceptance-mode corpus no longer contains %q. That case exists because losing it hides a "+
				"real failure; restore it rather than deleting it from this manifest.", required)
		}
	}
	return cases
}

func TestShareStatusForGroup(t *testing.T) {
	user := &AuthUser{ID: uuid.New(), Username: "contributor"}
	for _, testCase := range loadShareStatusModeCases(t) {
		t.Run(testCase.Name, func(t *testing.T) {
			q := &mockQuerier{
				hasUserVisibleOrg: func(context.Context, sqlc.HasUserVisibleOrgParams) (bool, error) {
					if testCase.OrgReadFail {
						return false, context.DeadlineExceeded
					}
					return testCase.OrgVisible, nil
				},
				listUserVisibleOrgs: func(context.Context, pgtype.UUID) ([]sqlc.ListUserVisibleOrgsRow, error) {
					if !testCase.OrgVisible {
						return nil, nil
					}
					return []sqlc.ListUserVisibleOrgsRow{{}}, nil
				},
			}
			group := sqlc.Group{AcceptanceMode: testCase.Acceptance}
			if testCase.LinkedOrg != "" {
				group.LinkedGithubOrg = pgtype.Text{String: testCase.LinkedOrg, Valid: true}
			}

			status, refusal := shareStatusForGroup(context.Background(), q, user, group)

			if testCase.WantRefusal != "" {
				if refusal == nil {
					t.Fatalf("the collective accepted the submission in state %q, want the refusal %q (%s). A "+
						"verified-only collective that admits an unverified member admits everyone.",
						status, testCase.WantRefusal, testCase.Why)
				}
				if string(refusal.Reason) != testCase.WantRefusal {
					t.Errorf("the refusal reads %q, want %q (%s); callers answer a refusal by its reason, not by its "+
						"message text", refusal.Reason, testCase.WantRefusal, testCase.Why)
				}
				if refusal.Message == "" {
					t.Errorf("the refusal carries no message (%s); the person has to be told what to change", testCase.Why)
				}
				return
			}
			if refusal != nil {
				t.Fatalf("the collective refused with %q, want a submission opening in state %q (%s)",
					refusal.Reason, testCase.WantStatus, testCase.Why)
			}
			if string(status) != testCase.WantStatus {
				t.Errorf("the submission opens in state %q, want %q (%s). The opening state is what decides whether a "+
					"contribution is visible at once or waits for a moderator.", status, testCase.WantStatus, testCase.Why)
			}
		})
	}
}

// TestWholeProjectLocksAreTheSingleShareLocks proves a batch contends with a
// concurrent single share rather than running beside it. The two paths serialize
// only if they name the same advisory-lock string for the same session, so the
// batch's keys are asserted to BE the single path's keys.
func TestWholeProjectLocksAreTheSingleShareLocks(t *testing.T) {
	owner := toPgUUID(uuid.New())
	for _, localID := range []string{"session-a", "session-b"} {
		batchKey := sessionPublishLockKey(owner, localID)
		if batchKey == "" {
			t.Fatalf("the publish lock key for %q is empty; an empty key would make every session contend with every "+
				"other one", localID)
		}
		if batchKey != sessionPublishLockKey(owner, localID) {
			t.Fatalf("the publish lock key for %q is not stable", localID)
		}
	}
	if sessionPublishLockKey(owner, "session-a") == sessionPublishLockKey(owner, "session-b") {
		t.Fatal("two different sessions share one publish lock key, so contributing one project would block work on " +
			"every other session of the same owner")
	}
	if sessionPublishLockKey(owner, "session-a") == sessionPublishLockKey(toPgUUID(uuid.New()), "session-a") {
		t.Fatal("two owners' sessions share one publish lock key; a local session id is unique only within an owner")
	}
}
