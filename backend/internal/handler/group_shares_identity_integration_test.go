//go:build integration

package handler

// The two /groups/{id}/pending and /groups/{id}/my-shares list routes have to
// carry enough identity for a consuming frontend to fold a submitted child
// session under the parent that spawned it. local_id is only unique per
// owner (transcripts has UNIQUE (owner_id, local_id)), so the fold key is the
// pair (owner_id, local_id), and every row needs owner_id, local_id, and
// parent_session_id to answer it.
//
// Both routes are driven through the real HTTP handlers against a real
// PostgreSQL, reusing the contributeWorld harness from
// group_shares_integration_test.go: the pending queue's contents are decided
// by the acceptance-mode + attempt-ledger rules exercised by the real
// ShareTranscript handler, not recomputed in Go.

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
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// pgTextPtr converts a nullable pgtype.Text into a *string, mirroring the
// nil/non-nil shape the frontend contract expects for an optional field.
func pgTextPtr(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

// ---------------------------------------------------------------------------
// Fixture shapes
// ---------------------------------------------------------------------------

type identityListTranscriptSpec struct {
	Key     string `yaml:"key"`
	Project string `yaml:"project"`
	Parent  string `yaml:"parent"`
}

type identityListCase struct {
	Name           string                       `yaml:"name"`
	Why            string                       `yaml:"why"`
	Transcripts    []identityListTranscriptSpec `yaml:"transcripts"`
	Submit         []string                     `yaml:"submit"`
	ExpectStatus   int                          `yaml:"expect_status"`
	ExpectRows     []string                     `yaml:"expect_rows"`
	ExpectParentOf map[string]string            `yaml:"expect_parent_of"`
}

// requiredGroupsPendingSharesCases and requiredGroupsMySharesCases each name
// the two shapes that matter for the parent/child identity fold: a parent and
// child both present in the same response, and a lone child whose parent was
// never offered to this collective. Losing either hides the exact failure
// this coverage exists to catch: a listing that carries a row's title but not
// enough identity for the frontend to place it under its parent.
var requiredGroupsPendingSharesCases = []string{
	"parent_and_child_pending",
	"lone_child_parent_absent",
}

var requiredGroupsMySharesCases = []string{
	"parent_and_child_shared",
	"lone_child_parent_absent",
}

func loadIdentityListCases(t *testing.T, data []byte, required []string, corpusName string) []identityListCase {
	t.Helper()
	cases, err := decodeFixtureRows[identityListCase](data)
	if err != nil {
		t.Fatalf("load the %s corpus: %v", corpusName, err)
	}
	present := map[string]bool{}
	for _, testCase := range cases {
		if testCase.Name == "" {
			t.Fatalf("a %s case has no name; every case is addressed by name", corpusName)
		}
		if present[testCase.Name] {
			t.Fatalf("the %s corpus repeats case %q", corpusName, testCase.Name)
		}
		present[testCase.Name] = true
		if testCase.Why == "" {
			t.Fatalf("case %q states no reason it exists; a case nobody can justify cannot be maintained", testCase.Name)
		}
		if len(testCase.Transcripts) == 0 {
			t.Fatalf("case %q builds no transcript, so it asks the route nothing", testCase.Name)
		}
	}
	for _, name := range required {
		if !present[name] {
			t.Fatalf("the %s corpus no longer contains %q. That case exists because losing it hides a real failure; "+
				"restore it rather than deleting it from this manifest.", corpusName, name)
		}
	}
	return cases
}

//go:embed testdata/groups-pending-shares.yaml
var groupsPendingSharesYAML []byte

//go:embed testdata/groups-my-shares.yaml
var groupsMySharesYAML []byte

// moderatorRequest builds a request authenticated as the world's collective
// owner (the "contrib-mod-<suffix>" user newContributeWorld always creates),
// which is what ListPendingShares requires of its caller.
func (w *contributeWorld) moderatorRequest(method, target string, params map[string]string) *http.Request {
	return shareRequestAs(w.moderator, "contrib-mod-"+w.suffix, method, target, nil, params)
}

// submitCurated offers one transcript through the real single-share handler
// to a curated collective, where every submission lands as 'pending' rather
// than being auto-approved - the shape ListPendingShares exists to list.
func (w *contributeWorld) submitCurated(t *testing.T, h *Handler, key string) {
	t.Helper()
	w.submitSingle(t, h, key)
}

// ---------------------------------------------------------------------------
// GET /groups/{id}/pending
// ---------------------------------------------------------------------------

func TestListPendingSharesCarriesParentIdentity(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	for _, testCase := range loadIdentityListCases(t, groupsPendingSharesYAML, requiredGroupsPendingSharesCases, "pending-shares identity") {
		t.Run(testCase.Name, func(t *testing.T) {
			h := New(&config.Config{}, pool, nil)
			world := newContributeWorld(t, ctx, pool, testCase.Name, "curated", true)
			defer world.cleanup(t, ctx)

			for ordinal, transcript := range testCase.Transcripts {
				world.insertTranscript(t, ctx, ordinal, transcript.Key, transcript.Project, "shared", "", transcript.Parent)
			}
			for _, key := range testCase.Submit {
				world.submitCurated(t, h, key)
			}

			rec := httptest.NewRecorder()
			h.ListPendingShares(rec, world.moderatorRequest(http.MethodGet,
				"/api/v1/groups/"+world.groupID()+"/pending", map[string]string{"id": world.groupID()}))

			if rec.Code != http.StatusOK {
				t.Fatalf("the pending-shares listing answered %d, want 200 (%s) (body: %s)",
					rec.Code, testCase.Why, rec.Body.String())
			}

			var rows []sqlc.ListPendingGroupSharesRow
			if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
				t.Fatalf("decode the pending-shares listing: %v (body: %s)", err, rec.Body.String())
			}
			world.assertIdentityRows(t, testCase, len(rows), func(id string) (identityRow, bool) {
				for _, row := range rows {
					if uuid.UUID(row.TranscriptID.Bytes).String() == id {
						return identityRow{
							ownerID:         uuid.UUID(row.OwnerID.Bytes).String(),
							localID:         row.LocalID,
							parentSessionID: pgTextPtr(row.ParentSessionID),
						}, true
					}
				}
				return identityRow{}, false
			})
		})
	}
}

// ---------------------------------------------------------------------------
// GET /groups/{id}/my-shares
// ---------------------------------------------------------------------------

func TestListMyGroupSharesCarriesParentIdentity(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	for _, testCase := range loadIdentityListCases(t, groupsMySharesYAML, requiredGroupsMySharesCases, "my-shares identity") {
		t.Run(testCase.Name, func(t *testing.T) {
			h := New(&config.Config{}, pool, nil)
			world := newContributeWorld(t, ctx, pool, testCase.Name, "open", true)
			defer world.cleanup(t, ctx)

			for ordinal, transcript := range testCase.Transcripts {
				world.insertTranscript(t, ctx, ordinal, transcript.Key, transcript.Project, "shared", "", transcript.Parent)
			}
			for _, key := range testCase.Submit {
				world.submitSingle(t, h, key)
			}

			rec := httptest.NewRecorder()
			h.ListMyGroupShares(rec, world.request(http.MethodGet,
				"/api/v1/groups/"+world.groupID()+"/my-shares", nil, map[string]string{"id": world.groupID()}))

			if rec.Code != http.StatusOK {
				t.Fatalf("the my-shares listing answered %d, want 200 (%s) (body: %s)",
					rec.Code, testCase.Why, rec.Body.String())
			}

			var rows []sqlc.ListUserSharesInGroupRow
			if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
				t.Fatalf("decode the my-shares listing: %v (body: %s)", err, rec.Body.String())
			}
			world.assertIdentityRows(t, testCase, len(rows), func(id string) (identityRow, bool) {
				for _, row := range rows {
					if uuid.UUID(row.ID.Bytes).String() == id {
						return identityRow{
							ownerID:         uuid.UUID(row.OwnerID.Bytes).String(),
							localID:         row.LocalID,
							parentSessionID: pgTextPtr(row.ParentSessionID),
						}, true
					}
				}
				return identityRow{}, false
			})
		})
	}
}

// ---------------------------------------------------------------------------
// Shared assertions
// ---------------------------------------------------------------------------

type identityRow struct {
	ownerID         string
	localID         string
	parentSessionID *string
}

func (w *contributeWorld) assertIdentityRows(t *testing.T, testCase identityListCase, gotCount int, lookup func(id string) (identityRow, bool)) {
	t.Helper()
	if gotCount != len(testCase.ExpectRows) {
		t.Fatalf("the listing holds %d row(s), want %d (%s)", gotCount, len(testCase.ExpectRows), testCase.Why)
	}
	for _, key := range testCase.ExpectRows {
		id := uuid.UUID(w.transcripts[key].Bytes).String()
		row, ok := lookup(id)
		if !ok {
			t.Fatalf("the listing omits transcript %q (%s)", key, testCase.Why)
		}
		wantOwner := uuid.UUID(w.member.Bytes).String()
		if row.ownerID != wantOwner {
			t.Errorf("transcript %q carries owner_id %q, want %q (%s). Without the true owner_id the frontend cannot "+
				"key its (owner_id, local_id) fold, since local_id alone is only unique per owner.",
				key, row.ownerID, wantOwner, testCase.Why)
		}
		if row.localID != w.localIDs[key] {
			t.Errorf("transcript %q carries local_id %q, want %q (%s)", key, row.localID, w.localIDs[key], testCase.Why)
		}
	}
	for childKey, parentKey := range testCase.ExpectParentOf {
		id := uuid.UUID(w.transcripts[childKey].Bytes).String()
		row, ok := lookup(id)
		if !ok {
			t.Fatalf("the listing omits transcript %q, so its parent identity cannot be checked (%s)", childKey, testCase.Why)
		}
		wantParent := w.localIDs[parentKey]
		if row.parentSessionID == nil || *row.parentSessionID != wantParent {
			t.Errorf("transcript %q reports parent_session_id %v, want %q (%s). Without it the frontend cannot fold "+
				"this row under its parent - including a parent that is not itself present in this same listing - and "+
				"would render it as an unrelated root session.",
				childKey, row.parentSessionID, wantParent, testCase.Why)
		}
	}
}
