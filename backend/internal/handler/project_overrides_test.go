package handler

// The owner-correction routes. See testdata/project-overrides-validation.yaml for
// why each case is there.

import (
	"context"
	_ "embed"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/projectname"
)

//go:embed testdata/project-overrides-validation.yaml
var projectOverridesValidationYAML []byte

type projectOverrideCase struct {
	Name    string `yaml:"name"`
	Why     string `yaml:"why"`
	Method  string `yaml:"method"`
	Project string `yaml:"project_hash"`
	Body    string `yaml:"body"`
	// BodyDisplayNameRepeat builds the body from a display name of exactly this
	// many characters, so the length boundary cases stay readable in the fixture.
	BodyDisplayNameRepeat int      `yaml:"body_display_name_repeat"`
	OwnedTranscripts      int64    `yaml:"owned_transcripts"`
	OverrideExists        bool     `yaml:"override_exists"`
	WantStatus            int      `yaml:"want_status"`
	WantOverrideWritten   bool     `yaml:"want_override_written"`
	WantOverrideDeleted   bool     `yaml:"want_override_deleted"`
	WantMessageContains   []string `yaml:"want_message_contains"`
}

// requiredProjectOverrideCases names the cases that must exist. Each is here
// because losing it hides a specific failure: the name-keyed rename defect this
// route was built to fix, a correction stored against someone else's project, a
// malformed key stored into an untyped TEXT column that no read will ever match,
// a name no surface can render, and the difference between clearing a chosen name
// and there being nothing to clear.
var requiredProjectOverrideCases = []string{
	"hash_keyed_rename_matches_the_owners_project",
	"rename_of_a_project_the_caller_never_published_into",
	"rename_keyed_on_a_hash_that_is_too_short",
	"rename_keyed_on_an_uppercase_hash",
	"rename_keyed_on_a_non_hexadecimal_string",
	"rename_to_an_empty_name",
	"rename_to_a_whitespace_only_name",
	"rename_to_a_name_longer_than_the_surfaces_can_render",
	"rename_to_a_name_at_the_longest_allowed_length",
	"rename_with_a_body_that_is_not_an_object",
	"clear_a_name_the_owner_had_chosen",
	"clear_when_no_name_was_ever_chosen",
	"clear_keyed_on_a_malformed_hash",
}

func loadProjectOverrideCases(t *testing.T) []projectOverrideCase {
	t.Helper()
	cases, err := decodeFixtureRows[projectOverrideCase](projectOverridesValidationYAML)
	if err != nil {
		t.Fatalf("load the project-override fixture: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if present[c.Name] {
			t.Fatalf("the project-override fixture repeats case %q", c.Name)
		}
		present[c.Name] = true
		if c.Why == "" {
			t.Fatalf("case %q states no reason it exists; a case nobody can justify cannot be maintained", c.Name)
		}
		if c.Method != http.MethodPatch && c.Method != http.MethodDelete {
			t.Fatalf("case %q drives method %q; the correction routes are PATCH and DELETE", c.Name, c.Method)
		}
		if c.Project == "" {
			t.Fatalf("case %q names no project key, so it drives neither route", c.Name)
		}
	}
	for _, required := range requiredProjectOverrideCases {
		if !present[required] {
			t.Fatalf("the project-override fixture no longer contains %q. That case exists because losing it hides a "+
				"real failure; restore it rather than removing it from this manifest.", required)
		}
	}
	return cases
}

func TestProjectDisplayNameCorrectionRoutes(t *testing.T) {
	for _, testCase := range loadProjectOverrideCases(t) {
		t.Run(testCase.Name, func(t *testing.T) {
			var upserted []sqlc.UpsertOwnerOverrideParams
			var deleted []sqlc.DeleteOwnerOverrideParams
			// Every transcript write path is deliberately absent from this mock. If
			// a correction route ever starts writing transcripts.project_name, it
			// panics here rather than passing quietly.
			q := &mockQuerier{
				countOwnerTranscriptsInProject: func(_ context.Context, arg sqlc.CountOwnerTranscriptsInProjectParams) (int64, error) {
					if arg.ProjectHash != testCase.Project {
						t.Errorf("ownership was probed for project %q, want the requested %q", arg.ProjectHash, testCase.Project)
					}
					return testCase.OwnedTranscripts, nil
				},
				upsertOwnerOverride: func(_ context.Context, arg sqlc.UpsertOwnerOverrideParams) (sqlc.OwnerOverride, error) {
					upserted = append(upserted, arg)
					return sqlc.OwnerOverride{Value: arg.Value}, nil
				},
				deleteOwnerOverride: func(_ context.Context, arg sqlc.DeleteOwnerOverrideParams) (int64, error) {
					deleted = append(deleted, arg)
					if testCase.OverrideExists {
						return 1, nil
					}
					return 0, nil
				},
				listOwnerProjectIdentities: func(_ context.Context, _ sqlc.ListOwnerProjectIdentitiesParams) ([]sqlc.ListOwnerProjectIdentitiesRow, error) {
					return nil, nil
				},
			}
			h := newTestHandler(q, nil)

			r := httptest.NewRequest(testCase.Method, "/api/v1/users/me/projects/"+testCase.Project,
				strings.NewReader(projectOverrideBody(testCase)))
			r = r.WithContext(withTestUser(r.Context()))
			r = withChiURLParam(r, "projectHash", testCase.Project)
			// withChiURLParam replaces the request context, so the caller identity
			// is re-attached after it.
			r = r.WithContext(withTestUser(r.Context()))
			w := httptest.NewRecorder()

			if testCase.Method == http.MethodPatch {
				h.SetProjectDisplayName(w, r)
			} else {
				h.ClearProjectDisplayName(w, r)
			}

			if w.Code != testCase.WantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, testCase.WantStatus, w.Body.String())
			}
			for _, want := range testCase.WantMessageContains {
				if !strings.Contains(w.Body.String(), want) {
					t.Errorf("the response does not contain %q.\nbody: %s", want, w.Body.String())
				}
			}

			if testCase.Method == http.MethodPatch {
				if got := len(upserted) > 0; got != testCase.WantOverrideWritten {
					t.Fatalf("a correction was written = %v, want %v", got, testCase.WantOverrideWritten)
				}
				for _, arg := range upserted {
					assertProjectDisplayNameOverride(t, arg.TargetKind, arg.TargetKey, arg.Field, testCase.Project)
				}
				return
			}
			if got := len(deleted) > 0; got != testCase.WantOverrideDeleted {
				t.Fatalf("a correction removal was attempted = %v, want %v", got, testCase.WantOverrideDeleted)
			}
			for _, arg := range deleted {
				assertProjectDisplayNameOverride(t, arg.TargetKind, arg.TargetKey, arg.Field, testCase.Project)
			}
		})
	}
}

// assertProjectDisplayNameOverride proves a correction addressed the one writable
// pair, keyed on the requested project hash.
func assertProjectDisplayNameOverride(t *testing.T, kind, key, field, wantKey string) {
	t.Helper()
	if overrideTargetKind(kind) != overrideTargetProject || overrideField(field) != overrideFieldDisplayName {
		t.Errorf("the correction addressed (%s, %s); the only writable pair is (%s, %s)",
			kind, field, overrideTargetProject, overrideFieldDisplayName)
	}
	if key != wantKey {
		t.Errorf("the correction was keyed on %q, want the project hash %q. Keying a correction on anything other than "+
			"the project hash is the defect this route replaced.", key, wantKey)
	}
}

func projectOverrideBody(testCase projectOverrideCase) string {
	if testCase.BodyDisplayNameRepeat > 0 {
		return `{"display_name":"` + strings.Repeat("n", testCase.BodyDisplayNameRepeat) + `"}`
	}
	return testCase.Body
}

// TestWritableOverridePairs_RefusesReservedPairs proves the closed set is enforced
// in Go rather than by a second database CHECK. The table reserves wider menus so
// a later field is a code change; until such a field is implemented, a route may
// not write one.
func TestWritableOverridePairs_RefusesReservedPairs(t *testing.T) {
	if err := validateOverridePair(overrideTargetProject, overrideFieldDisplayName); err != nil {
		t.Fatalf("the implemented pair (%s, %s) was refused: %v", overrideTargetProject, overrideFieldDisplayName, err)
	}
	// Reserved by the table's CHECK constraints, implemented by no route.
	for _, reserved := range []struct {
		kind  overrideTargetKind
		field overrideField
	}{
		{"transcript", "title"},
		{"redaction_span", "redaction_decision"},
		{overrideTargetProject, "title"},
		{"transcript", overrideFieldDisplayName},
	} {
		err := validateOverridePair(reserved.kind, reserved.field)
		if err == nil {
			t.Errorf("the reserved pair (%s, %s) was accepted; no route implements it", reserved.kind, reserved.field)
			continue
		}
		if !strings.Contains(err.Error(), "reserved but not implemented") {
			t.Errorf("the refusal of (%s, %s) does not say why: %v", reserved.kind, reserved.field, err)
		}
	}
}

// TestResolveProjectIdentities_OnePageOneStatement proves a page of transcripts
// spanning many owners and many projects resolves its display names in ONE
// statement. A per-row read would be invisible to every other assertion here and
// would only show up as a slow list in production.
func TestResolveProjectIdentities_OnePageOneStatement(t *testing.T) {
	q := &mockQuerier{
		listOwnerProjectIdentities: func(_ context.Context, arg sqlc.ListOwnerProjectIdentitiesParams) ([]sqlc.ListOwnerProjectIdentitiesRow, error) {
			if len(arg.OwnerIds) != 2 {
				t.Errorf("the identity read asked for %d owners, want the 2 distinct owners on the page", len(arg.OwnerIds))
			}
			if len(arg.ProjectHashes) != 2 {
				t.Errorf("the identity read asked for %d projects, want the 2 distinct projects on the page", len(arg.ProjectHashes))
			}
			return nil, nil
		},
	}
	h := newTestHandler(q, nil)

	ownerA := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	ownerB := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	hashA := strings.Repeat("a", 64)
	hashB := strings.Repeat("b", 64)
	keys := []projectIdentityKey{
		{OwnerID: ownerA, ProjectHash: hashA},
		{OwnerID: ownerA, ProjectHash: hashA},
		{OwnerID: ownerA, ProjectHash: hashB},
		{OwnerID: ownerB, ProjectHash: hashB},
	}

	resolved := h.resolveProjectIdentities(context.Background(), keys)

	if q.listOwnerProjectIdentitiesCalls != 1 {
		t.Fatalf("the page issued %d identity reads, want exactly 1", q.listOwnerProjectIdentitiesCalls)
	}
	// Every pair still gets an answer: with no stored evidence the resolver falls
	// back to the label derived from the hash, so no caller has to invent one.
	for _, key := range keys {
		got, ok := resolved[key]
		if !ok {
			t.Fatalf("project %s of owner %v got no resolved identity", key.ProjectHash[:12], key.OwnerID.Bytes[0])
		}
		if got.DisplayName == "" || got.DisplayName == "Other" {
			t.Fatalf("resolved display name = %q; it may never be empty or the literal \"Other\"", got.DisplayName)
		}
	}
}

// TestEvidenceFromIdentityRow_ClassifiesNamesInGo proves the split between a
// consented project name and Peasant's privacy label happens through
// projectname.IsPrivacyLabel and NOT through a second copy of that rule in SQL.
// The query returns the names as ONE ordered array precisely so this stays true.
func TestEvidenceFromIdentityRow_ClassifiesNamesInGo(t *testing.T) {
	row := sqlc.ListOwnerProjectIdentitiesRow{
		ProjectHash:  strings.Repeat("a", 64),
		OverrideName: "",
		ProjectNames: []string{"project-abc123def456", "ledger-service", "project-000000000000"},
		GitRemote:    "git@github.com:peasant-labs/ledger.git",
	}

	evidence := evidenceFromIdentityRow(row)

	if evidence.PrivacyLabel != "project-abc123def456" {
		t.Errorf("privacy label = %q, want the first name matching the privacy shape", evidence.PrivacyLabel)
	}
	if evidence.ConsentedName != "ledger-service" {
		t.Errorf("consented name = %q, want the first name that is not a privacy label", evidence.ConsentedName)
	}
	if !projectname.IsPrivacyLabel(evidence.PrivacyLabel) {
		t.Errorf("the classified privacy label %q is not one", evidence.PrivacyLabel)
	}
	if projectname.IsPrivacyLabel(evidence.ConsentedName) {
		t.Errorf("the classified consented name %q is a privacy label", evidence.ConsentedName)
	}
	if evidence.GitRemote != row.GitRemote {
		t.Errorf("git remote = %q, want the raw remote %q; formatting it is the label seam's job", evidence.GitRemote, row.GitRemote)
	}
}
