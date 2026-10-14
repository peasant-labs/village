package handler

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// A person's linked organizations are read through the SAME visibility boundary
// as their profile. Driving the boundary rather than the query is the point:
// the query never sees a request the profile read would have refused.

//go:embed testdata/user-orgs-visibility.yaml
var userOrgsVisibilityYAML []byte

type userOrgsVisibilityOrg struct {
	OrgLogin  string  `yaml:"org_login"`
	AvatarURL *string `yaml:"avatar_url"`
}

type userOrgsVisibilityCase struct {
	Name         string                  `yaml:"name"`
	Why          string                  `yaml:"why"`
	Discoverable bool                    `yaml:"discoverable"`
	Viewer       string                  `yaml:"viewer"`
	UserExists   bool                    `yaml:"user_exists"`
	WantStatus   int                     `yaml:"want_status"`
	Orgs         []userOrgsVisibilityOrg `yaml:"orgs"`
}

// requiredUserOrgsVisibilityCases names every row: a discoverable profile read
// by another person, a hidden one read by its owner, a hidden one read by a
// signed-in stranger and by an anonymous reader, and an unknown handle.
var requiredUserOrgsVisibilityCases = []string{
	"a_discoverable_profile_answers_with_its_linked_organizations",
	"the_owner_reads_their_own_hidden_organizations",
	"a_hidden_profile_is_not_found_for_another_signed_in_person",
	"a_hidden_profile_is_not_found_for_an_anonymous_reader",
	"an_unknown_handle_is_not_found",
}

func loadUserOrgsVisibilityCases(t *testing.T) []userOrgsVisibilityCase {
	t.Helper()
	cases, err := decodeFixtureRows[userOrgsVisibilityCase](userOrgsVisibilityYAML)
	if err != nil {
		t.Fatalf("load testdata/user-orgs-visibility.yaml: %v", err)
	}
	present := map[string]struct{}{}
	for _, c := range cases {
		if _, repeated := present[c.Name]; c.Name == "" || repeated {
			t.Fatalf("user-orgs-visibility row name %q is empty or repeated", c.Name)
		}
		present[c.Name] = struct{}{}
		if c.WantStatus == http.StatusOK && len(c.Orgs) == 0 {
			t.Fatalf("row %q answers 200 and must state the organizations it reports", c.Name)
		}
		if c.WantStatus != http.StatusOK && len(c.Orgs) != 0 {
			t.Fatalf("row %q is refused and cannot also state organizations", c.Name)
		}
	}
	assertExactTitleFixtureNames(t, "user-orgs-visibility", present, requiredUserOrgsVisibilityCases)
	return cases
}

// TestListUserPublicOrgsHonoursProfileVisibility drives every row of
// testdata/user-orgs-visibility.yaml through the mounted read. Each case asserts
// the status AND whether the organizations were looked up at all: a refused read
// must not have asked, or the refusal would be a filter over an answer the
// server had already fetched.
func TestListUserPublicOrgsHonoursProfileVisibility(t *testing.T) {
	const target = "someone"

	for _, c := range loadUserOrgsVisibilityCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			targetID := uuid.New()
			visibleRead := false

			q := &mockQuerier{
				getUserByUsername: func(_ context.Context, username string) (sqlc.User, error) {
					if username != target {
						t.Fatalf("looked up %q, want %q", username, target)
					}
					if !c.UserExists {
						return sqlc.User{}, pgx.ErrNoRows
					}
					return sqlc.User{
						ID:             pgtype.UUID{Bytes: targetID, Valid: true},
						GithubUsername: target,
						IsDiscoverable: c.Discoverable,
					}, nil
				},
				getUserVisibleOrgsByUsername: func(_ context.Context, username string) ([]sqlc.GetUserVisibleOrgsByUsernameRow, error) {
					visibleRead = true
					rows := make([]sqlc.GetUserVisibleOrgsByUsernameRow, 0, len(c.Orgs))
					for _, org := range c.Orgs {
						row := sqlc.GetUserVisibleOrgsByUsernameRow{OrgLogin: org.OrgLogin}
						if org.AvatarURL != nil {
							row.AvatarUrl = pgtype.Text{String: *org.AvatarURL, Valid: true}
						}
						rows = append(rows, row)
					}
					return rows, nil
				},
			}
			h := newTestHandler(q, nil)

			ctx := context.Background()
			switch c.Viewer {
			case "owner":
				ctx = context.WithValue(ctx, UserContextKey, &AuthUser{ID: targetID, Username: target})
			case "other":
				ctx = context.WithValue(ctx, UserContextKey, &AuthUser{ID: uuid.New(), Username: "someone-else"})
			case "anonymous":
				// AuthOptional: the request carries no viewer at all.
			default:
				t.Fatalf("unknown viewer %q in row %q", c.Viewer, c.Name)
			}

			r := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+target+"/orgs", nil)
			route := chi.NewRouteContext()
			route.URLParams.Add("username", target)
			r = r.WithContext(context.WithValue(ctx, chi.RouteCtxKey, route))
			w := httptest.NewRecorder()

			h.ListUserPublicOrgs(w, r)

			if w.Code != c.WantStatus {
				t.Fatalf("status = %d (%s), want %d (%s)", w.Code, w.Body.String(), c.WantStatus, c.Why)
			}
			if c.WantStatus != http.StatusOK {
				if visibleRead {
					t.Fatalf("the read was refused with %d but the organizations were still looked up", w.Code)
				}
				return
			}

			var got []struct {
				OrgLogin  string  `json:"org_login"`
				AvatarURL *string `json:"avatar_url"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode orgs: %v", err)
			}
			if len(got) != len(c.Orgs) {
				t.Fatalf("served %d organizations, want %d", len(got), len(c.Orgs))
			}
			for i, want := range c.Orgs {
				if got[i].OrgLogin != want.OrgLogin {
					t.Errorf("organization %d is %q, want %q", i, got[i].OrgLogin, want.OrgLogin)
				}
				switch {
				case want.AvatarURL == nil && got[i].AvatarURL != nil:
					t.Errorf("organization %d carries an avatar %q, want none", i, *got[i].AvatarURL)
				case want.AvatarURL != nil && (got[i].AvatarURL == nil || *got[i].AvatarURL != *want.AvatarURL):
					t.Errorf("organization %d avatar = %v, want %q", i, got[i].AvatarURL, *want.AvatarURL)
				}
			}
		})
	}
}
