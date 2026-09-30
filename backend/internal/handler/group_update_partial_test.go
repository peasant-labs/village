package handler

import (
	"context"
	_ "embed"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/group-update-partial.yaml
var groupUpdatePartialYAML []byte

// groupUpdateRow is the collective row as the fixture states it: the stored
// row a case starts from, and the row the update must write.
type groupUpdateRow struct {
	Name                     string  `yaml:"name"`
	Description              *string `yaml:"description"`
	DataAccess               string  `yaml:"data_access"`
	AcceptanceMode           string  `yaml:"acceptance_mode"`
	LinkedGithubOrg          *string `yaml:"linked_github_org"`
	DisplayMembers           bool    `yaml:"display_members"`
	TranscriptDeletionPolicy string  `yaml:"transcript_deletion_policy"`
	PostPromptsCheck         bool    `yaml:"post_prompts_check"`
	PromptsCheckMode         string  `yaml:"prompts_check_mode"`
}

type groupUpdatePartialCase struct {
	Name string         `yaml:"name"`
	Why  string         `yaml:"why"`
	Body string         `yaml:"body"`
	Want groupUpdateRow `yaml:"want"`
}

type groupUpdatePartialFixture struct {
	Stored groupUpdateRow           `yaml:"stored"`
	Cases  []groupUpdatePartialCase `yaml:"cases"`
}

// requiredGroupUpdatePartialCases is the deletion guard: every name must stay
// in the fixture, and the fixture may not carry a case this list omits.
var requiredGroupUpdatePartialCases = []string{
	"the-pull-request-switch-alone-keeps-the-name-and-description",
	"a-new-name-alone-keeps-the-description",
	"a-new-description-alone-keeps-the-name",
	"an-empty-description-clears-it",
	"a-read-policy-alone-keeps-everything-else",
	"a-leave-policy-alone-keeps-everything-else",
}

func optionalText(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}

func (row groupUpdateRow) group(id pgtype.UUID) sqlc.Group {
	return sqlc.Group{
		ID:                       id,
		Name:                     row.Name,
		Description:              optionalText(row.Description),
		DataAccess:               row.DataAccess,
		AcceptanceMode:           row.AcceptanceMode,
		LinkedGithubOrg:          optionalText(row.LinkedGithubOrg),
		DisplayMembers:           row.DisplayMembers,
		TranscriptDeletionPolicy: row.TranscriptDeletionPolicy,
		PostPromptsCheck:         row.PostPromptsCheck,
		PromptsCheckMode:         row.PromptsCheckMode,
	}
}

func (row groupUpdateRow) params(id pgtype.UUID) sqlc.UpdateGroupParams {
	g := row.group(id)
	return sqlc.UpdateGroupParams{
		ID:                       id,
		Name:                     g.Name,
		Description:              g.Description,
		DataAccess:               g.DataAccess,
		AcceptanceMode:           g.AcceptanceMode,
		LinkedGithubOrg:          g.LinkedGithubOrg,
		DisplayMembers:           g.DisplayMembers,
		TranscriptDeletionPolicy: g.TranscriptDeletionPolicy,
		PostPromptsCheck:         g.PostPromptsCheck,
		PromptsCheckMode:         g.PromptsCheckMode,
	}
}

// TestUpdateGroupKeepsEveryOmittedField drives the production handler with one
// field per body, as the settings page sends it, and asserts the whole row the
// update writes: the named field changes and every omitted field keeps the
// stored value. The database write itself is covered by the integration test
// in group_prompt_check_settings_integration_test.go.
func TestUpdateGroupKeepsEveryOmittedField(t *testing.T) {
	fixture, err := decodeFixtureDocument[groupUpdatePartialFixture](groupUpdatePartialYAML)
	if err != nil {
		t.Fatalf("load group-update-partial.yaml: %v", err)
	}
	names := make([]string, 0, len(fixture.Cases))
	for _, c := range fixture.Cases {
		names = append(names, c.Name)
	}
	required := append([]string(nil), requiredGroupUpdatePartialCases...)
	sort.Strings(names)
	sort.Strings(required)
	if strings.Join(names, "\n") != strings.Join(required, "\n") {
		t.Fatalf("fixture cases = %v, want exactly %v", names, required)
	}

	id := toPgUUID(uuid.MustParse(testGroupID))
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if strings.TrimSpace(c.Why) == "" {
				t.Fatalf("case states no reason it exists")
			}
			var written *sqlc.UpdateGroupParams
			q := &mockQuerier{
				getGroupMember: memberStub("owner"),
				getGroupByID: func(ctx context.Context, got pgtype.UUID) (sqlc.Group, error) {
					return fixture.Stored.group(got), nil
				},
				updateGroup: func(ctx context.Context, arg sqlc.UpdateGroupParams) (sqlc.Group, error) {
					written = &arg
					return sqlc.Group{ID: arg.ID, Name: arg.Name}, nil
				},
			}
			h := newTestHandler(q, nil)
			r := chi.NewRouter()
			r.Patch("/groups/{id}", h.UpdateGroup)
			req := httptest.NewRequest(http.MethodPatch, "/groups/"+testGroupID, strings.NewReader(c.Body))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(withTestUser(req.Context()))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("PATCH %s = %d (%s), want 200", c.Body, w.Code, w.Body.String())
			}
			if written == nil {
				t.Fatalf("PATCH %s answered 200 without writing the collective", c.Body)
			}
			if want := c.Want.params(id); *written != want {
				t.Fatalf("PATCH %s wrote\n  %+v\nwant\n  %+v", c.Body, *written, want)
			}
		})
	}
}
