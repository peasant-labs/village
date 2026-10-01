package handler

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/user-settings-patch.yaml
var userSettingsPatchYAML []byte

type userSettingsChoices struct {
	PreviewBeforeAttach    *bool `yaml:"preview_before_attach"`
	AutoAttachPullRequests *bool `yaml:"auto_attach_pull_requests"`
}

type userSettingsPatchCase struct {
	Name        string               `yaml:"name"`
	Stored      userSettingsChoices  `yaml:"stored"`
	Body        string               `yaml:"body"`
	WantStatus  int                  `yaml:"want_status"`
	WantWritten *userSettingsChoices `yaml:"want_written"`
	Want        *userSettingsChoices `yaml:"want"`
}

// requiredUserSettingsPatchCases names every row: each choice on its own in
// both directions, both together, an empty body, and a body the contract
// refuses.
var requiredUserSettingsPatchCases = []string{
	"automatic_linking_turns_on",
	"automatic_linking_turns_off",
	"preview_alone_keeps_automatic_linking",
	"both_choices_change_together",
	"an_empty_body_reads_back_and_writes_nothing",
	"a_value_that_is_not_a_boolean_is_refused",
}

func loadUserSettingsPatchCases(t *testing.T) []userSettingsPatchCase {
	t.Helper()
	cases, err := decodeFixtureRows[userSettingsPatchCase](userSettingsPatchYAML)
	if err != nil {
		t.Fatalf("load testdata/user-settings-patch.yaml: %v", err)
	}
	present := map[string]struct{}{}
	for _, c := range cases {
		if _, repeated := present[c.Name]; c.Name == "" || repeated {
			t.Fatalf("user-settings-patch row name %q is empty or repeated", c.Name)
		}
		present[c.Name] = struct{}{}
		if c.Stored.PreviewBeforeAttach == nil || c.Stored.AutoAttachPullRequests == nil {
			t.Fatalf("row %q must state both stored choices", c.Name)
		}
		if c.WantStatus == http.StatusOK && (c.Want == nil || c.Want.PreviewBeforeAttach == nil || c.Want.AutoAttachPullRequests == nil) {
			t.Fatalf("row %q answers 200 and must state both choices it reports", c.Name)
		}
	}
	assertExactTitleFixtureNames(t, "user-settings-patch", present, requiredUserSettingsPatchCases)
	return cases
}

// TestUpdateUserSettingsCarriesEitherChoice drives every row of
// testdata/user-settings-patch.yaml through the PATCH handler and its contract
// check. The mock stands in for the one UPDATE statement, which leaves a NULL
// column as it is; the statement itself runs against PostgreSQL in
// TestUserSettingsRoundTrip.
func TestUpdateUserSettingsCarriesEitherChoice(t *testing.T) {
	for _, c := range loadUserSettingsPatchCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			stored := sqlc.User{PreviewBeforeAttach: *c.Stored.PreviewBeforeAttach, AutoAttachPullRequests: *c.Stored.AutoAttachPullRequests}
			var written *sqlc.UpdateUserAttachSettingsParams
			q := &mockQuerier{
				getUserByID: func(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
					stored.ID = id
					return stored, nil
				},
				updateUserAttachSettings: func(_ context.Context, arg sqlc.UpdateUserAttachSettingsParams) (sqlc.User, error) {
					written = &arg
					row := stored
					row.ID = arg.ID
					if arg.PreviewBeforeAttach.Valid {
						row.PreviewBeforeAttach = arg.PreviewBeforeAttach.Bool
					}
					if arg.AutoAttachPullRequests.Valid {
						row.AutoAttachPullRequests = arg.AutoAttachPullRequests.Bool
					}
					return row, nil
				},
			}
			h := newTestHandler(q, nil)
			r := httptest.NewRequest(http.MethodPatch, "/api/v1/users/me/settings", strings.NewReader(c.Body))
			r.Header.Set("Content-Type", "application/json")
			r = r.WithContext(withTestUser(r.Context()))
			w := httptest.NewRecorder()
			h.UpdateUserSettings(w, r)

			if w.Code != c.WantStatus {
				t.Fatalf("status = %d (%s), want %d", w.Code, w.Body.String(), c.WantStatus)
			}
			switch {
			case c.WantWritten == nil && written != nil:
				t.Fatalf("the PATCH wrote %+v, want nothing written", *written)
			case c.WantWritten != nil && written == nil:
				t.Fatal("the PATCH wrote nothing, want a write")
			case c.WantWritten != nil:
				assertWrittenChoice(t, "preview_before_attach", c.WantWritten.PreviewBeforeAttach, written.PreviewBeforeAttach)
				assertWrittenChoice(t, "auto_attach_pull_requests", c.WantWritten.AutoAttachPullRequests, written.AutoAttachPullRequests)
			}
			if c.WantStatus != http.StatusOK {
				return
			}
			var settings schema.VillageUserSettings
			if err := json.Unmarshal(w.Body.Bytes(), &settings); err != nil {
				t.Fatalf("decode settings: %v", err)
			}
			if settings.PreviewBeforeAttach != *c.Want.PreviewBeforeAttach || settings.AutoAttachPullRequests != *c.Want.AutoAttachPullRequests {
				t.Fatalf("settings = %+v, want preview %t and automatic linking %t", settings, *c.Want.PreviewBeforeAttach, *c.Want.AutoAttachPullRequests)
			}
		})
	}
}

// assertWrittenChoice pins one column of the update: a stated value is written
// as that value, and an omitted one as NULL, which leaves the column alone.
func assertWrittenChoice(t *testing.T, column string, want *bool, got pgtype.Bool) {
	t.Helper()
	if want == nil {
		if got.Valid {
			t.Fatalf("%s written as %t, want NULL so the stored value stays", column, got.Bool)
		}
		return
	}
	if !got.Valid || got.Bool != *want {
		t.Fatalf("%s written as %+v, want %t", column, got, *want)
	}
}
