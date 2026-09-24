package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

const installTestSecret = "install-handshake-test-secret"

func installRequest(h *Handler, path string, userID uuid.UUID) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Get("/integrations/github/install", h.GitHubInstall)
	r.Get("/integrations/github/callback", h.GitHubInstallCallback)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &AuthUser{ID: userID, Username: "owner"}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func installHandler(t *testing.T, f *fakeGitHub, mq *mockQuerier) *Handler {
	t.Helper()
	if f == nil {
		f = &fakeGitHub{}
	}
	newFakeGitHub(t, f)
	h := newRepoHandler(t, mq, f)
	h.cfg.JWTSecret = installTestSecret
	h.cfg.FrontendURL = "https://app.example.com"
	h.cfg.GitHubAppSlug = "village-app"
	return h
}

// ownerGroupRow is one ListUserGroups row a callback test can return.
func ownerGroupRow(groupID, linkedOrg string) sqlc.ListUserGroupsRow {
	return sqlc.ListUserGroupsRow{
		ID:              toPgUUID(uuid.MustParse(groupID)),
		Role:            "owner",
		LinkedGithubOrg: pgtype.Text{String: linkedOrg, Valid: linkedOrg != ""},
	}
}

// TestGitHubInstall_RedirectsToInstallPage proves an owner gets a redirect to
// the App's install page carrying a state token bound to them and the collective.
func TestGitHubInstall_RedirectsToInstallPage(t *testing.T) {
	h := installHandler(t, nil, &mockQuerier{getGroupMember: memberStub("owner")})
	userID := uuid.New()

	w := installRequest(h, "/integrations/github/install?group_id="+testGroupID, userID)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (body: %s)", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://github.com/apps/village-app/installations/new?state=") {
		t.Fatalf("redirect = %q, want the App install page with state", loc)
	}
	parsed, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := auth.ValidateInstallState(installTestSecret, parsed.Query().Get("state"))
	if err != nil {
		t.Fatalf("state did not validate: %v", err)
	}
	if claims.UserID != userID.String() || claims.GroupID != testGroupID {
		t.Fatalf("state bound to %s/%s, want %s/%s", claims.UserID, claims.GroupID, userID, testGroupID)
	}
}

func TestGitHubInstall_NotConfigured(t *testing.T) {
	h := installHandler(t, nil, &mockQuerier{getGroupMember: memberStub("owner")})
	h.cfg.GitHubAppSlug = ""
	w := installRequest(h, "/integrations/github/install?group_id="+testGroupID, uuid.New())
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 when no App slug is configured", w.Code)
	}
}

func TestGitHubInstall_NonOwnerRejected(t *testing.T) {
	h := installHandler(t, nil, &mockQuerier{getGroupMember: memberStub("member")})
	w := installRequest(h, "/integrations/github/install?group_id="+testGroupID, uuid.New())
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a non-owner", w.Code)
	}
}

// TestGitHubInstallCallback_RecordsInstallationForBoundCollective proves the
// callback reads the installation with the App credentials, records it, and
// returns the owner to the collective bound to that account.
func TestGitHubInstallCallback_RecordsInstallationForBoundCollective(t *testing.T) {
	f := &fakeGitHub{installationID: 555, installationAccount: "acme"}
	var upserted *sqlc.UpsertGitHubAppInstallationParams
	mq := &mockQuerier{
		listUserGroups: func(context.Context, pgtype.UUID) ([]sqlc.ListUserGroupsRow, error) {
			return []sqlc.ListUserGroupsRow{ownerGroupRow(testGroupID, "acme")}, nil
		},
		upsertGitHubAppInstallation: func(_ context.Context, arg sqlc.UpsertGitHubAppInstallationParams) error {
			upserted = &arg
			return nil
		},
	}
	h := installHandler(t, f, mq)
	userID := uuid.New()

	w := installRequest(h, "/integrations/github/callback?installation_id=555&setup_action=install", userID)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (body: %s)", w.Code, w.Body.String())
	}
	if upserted == nil || upserted.InstallationID != 555 || upserted.AccountLogin != "acme" {
		t.Fatalf("installation not recorded: %+v", upserted)
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "/groups/"+testGroupID+"/settings") {
		t.Fatalf("redirect = %q, want the bound collective's settings page", loc)
	}
}

// TestGitHubInstallCallback_DoesNotDependOnState proves the callback works when
// GitHub does not forward the install URL's state: the bound collective is
// resolved from the session and the installation's account instead.
func TestGitHubInstallCallback_DoesNotDependOnState(t *testing.T) {
	f := &fakeGitHub{installationID: 555, installationAccount: "acme"}
	mq := &mockQuerier{
		listUserGroups: func(context.Context, pgtype.UUID) ([]sqlc.ListUserGroupsRow, error) {
			return []sqlc.ListUserGroupsRow{ownerGroupRow(testGroupID, "acme")}, nil
		},
	}
	h := installHandler(t, f, mq)

	// A garbage state must not break the handshake.
	w := installRequest(h, "/integrations/github/callback?installation_id=555&state=not-a-token", uuid.New())
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 even with an unusable state (body: %s)", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "/groups/"+testGroupID+"/settings") {
		t.Fatalf("redirect = %q, want the bound collective's settings page", loc)
	}
}

// TestGitHubInstallCallback_NoBoundCollectiveLandsOnGroups proves the install is
// still recorded when the caller owns no collective bound to that account.
func TestGitHubInstallCallback_NoBoundCollectiveLandsOnGroups(t *testing.T) {
	f := &fakeGitHub{installationID: 555, installationAccount: "acme"}
	var recorded bool
	mq := &mockQuerier{
		listUserGroups: func(context.Context, pgtype.UUID) ([]sqlc.ListUserGroupsRow, error) {
			return []sqlc.ListUserGroupsRow{ownerGroupRow(testGroupID, "other-org")}, nil
		},
		upsertGitHubAppInstallation: func(context.Context, sqlc.UpsertGitHubAppInstallationParams) error {
			recorded = true
			return nil
		},
	}
	h := installHandler(t, f, mq)

	w := installRequest(h, "/integrations/github/callback?installation_id=555", uuid.New())
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if !recorded {
		t.Fatal("the installation was not recorded")
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "/groups?github_installed=1") {
		t.Fatalf("redirect = %q, want the collectives list", loc)
	}
}

func TestGitHubInstallCallback_RequiresInstallationID(t *testing.T) {
	h := installHandler(t, nil, &mockQuerier{})
	w := installRequest(h, "/integrations/github/callback?state=x", uuid.New())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 without an installation_id", w.Code)
	}
}

// TestGitHubInstallCallback_BindsAnUnlinkedCollective is the path a fresh
// collective takes: the owner connects the App before anything says which
// account the collective belongs to, and the callback records it from the
// installation. Without this the collective can never be bound, because the
// linked org had to be set by hand first and nothing could set it.
func TestGitHubInstallCallback_BindsAnUnlinkedCollective(t *testing.T) {
	f := &fakeGitHub{installationID: 555, installationAccount: "acme"}
	var bound *sqlc.SetGroupLinkedGitHubOrgParams
	mq := &mockQuerier{
		listUserGroups: func(context.Context, pgtype.UUID) ([]sqlc.ListUserGroupsRow, error) {
			return []sqlc.ListUserGroupsRow{ownerGroupRow(testGroupID, "")}, nil
		},
		setGroupLinkedGitHubOrg: func(_ context.Context, arg sqlc.SetGroupLinkedGitHubOrgParams) error {
			bound = &arg
			return nil
		},
	}
	h := installHandler(t, f, mq)
	userID := uuid.New()
	state, err := auth.CreateInstallState(installTestSecret, userID.String(), testGroupID)
	if err != nil {
		t.Fatal(err)
	}

	w := installRequest(h, "/integrations/github/callback?installation_id=555&state="+url.QueryEscape(state), userID)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (body: %s)", w.Code, w.Body.String())
	}
	if bound == nil {
		t.Fatal("the collective was not bound to the installation's account")
	}
	if got := uuid.UUID(bound.ID.Bytes).String(); got != testGroupID {
		t.Fatalf("bound collective = %s, want %s", got, testGroupID)
	}
	if !bound.LinkedGithubOrg.Valid || bound.LinkedGithubOrg.String != "acme" {
		t.Fatalf("linked org = %+v, want acme", bound.LinkedGithubOrg)
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "/groups/"+testGroupID+"/settings") {
		t.Fatalf("redirect = %q, want the bound collective's settings page", loc)
	}
}

// TestGitHubInstallCallback_KeepsACollectiveAlreadyBoundToTheAccount proves a
// re-connect does not rewrite the org a collective already records.
func TestGitHubInstallCallback_KeepsACollectiveAlreadyBoundToTheAccount(t *testing.T) {
	f := &fakeGitHub{installationID: 555, installationAccount: "acme"}
	wrote := false
	mq := &mockQuerier{
		listUserGroups: func(context.Context, pgtype.UUID) ([]sqlc.ListUserGroupsRow, error) {
			return []sqlc.ListUserGroupsRow{ownerGroupRow(testGroupID, "acme")}, nil
		},
		setGroupLinkedGitHubOrg: func(context.Context, sqlc.SetGroupLinkedGitHubOrgParams) error {
			wrote = true
			return nil
		},
	}
	h := installHandler(t, f, mq)

	w := installRequest(h, "/integrations/github/callback?installation_id=555", uuid.New())
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if wrote {
		t.Fatal("a collective already bound to the account was rewritten")
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "/groups/"+testGroupID+"/settings") {
		t.Fatalf("redirect = %q, want the bound collective's settings page", loc)
	}
}

// TestGitHubInstallCallback_StateRebindsADifferentOrg proves the handshake's
// signed state is the owner's explicit choice: starting it from a collective
// that records another org moves that collective to the connected account.
func TestGitHubInstallCallback_StateRebindsADifferentOrg(t *testing.T) {
	f := &fakeGitHub{installationID: 555, installationAccount: "acme"}
	var bound *sqlc.SetGroupLinkedGitHubOrgParams
	mq := &mockQuerier{
		listUserGroups: func(context.Context, pgtype.UUID) ([]sqlc.ListUserGroupsRow, error) {
			return []sqlc.ListUserGroupsRow{ownerGroupRow(testGroupID, "other-org")}, nil
		},
		setGroupLinkedGitHubOrg: func(_ context.Context, arg sqlc.SetGroupLinkedGitHubOrgParams) error {
			bound = &arg
			return nil
		},
	}
	h := installHandler(t, f, mq)
	userID := uuid.New()
	state, err := auth.CreateInstallState(installTestSecret, userID.String(), testGroupID)
	if err != nil {
		t.Fatal(err)
	}

	w := installRequest(h, "/integrations/github/callback?installation_id=555&state="+url.QueryEscape(state), userID)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if bound == nil || bound.LinkedGithubOrg.String != "acme" {
		t.Fatalf("bound = %+v, want the collective moved to acme", bound)
	}
}

// TestGitHubInstallCallback_LeavesSeveralUnlinkedCollectivesAlone proves the
// callback will not guess. With more than one owned collective carrying no org
// and no state naming one, binding either would be arbitrary, so the
// installation is recorded and the owner is sent to their collectives.
func TestGitHubInstallCallback_LeavesSeveralUnlinkedCollectivesAlone(t *testing.T) {
	f := &fakeGitHub{installationID: 555, installationAccount: "acme"}
	wrote := false
	mq := &mockQuerier{
		listUserGroups: func(context.Context, pgtype.UUID) ([]sqlc.ListUserGroupsRow, error) {
			return []sqlc.ListUserGroupsRow{
				ownerGroupRow(testGroupID, ""),
				ownerGroupRow(uuid.NewString(), ""),
			}, nil
		},
		setGroupLinkedGitHubOrg: func(context.Context, sqlc.SetGroupLinkedGitHubOrgParams) error {
			wrote = true
			return nil
		},
	}
	h := installHandler(t, f, mq)

	w := installRequest(h, "/integrations/github/callback?installation_id=555", uuid.New())
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if wrote {
		t.Fatal("a collective was bound without knowing which one the owner meant")
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "/groups?github_installed=1") {
		t.Fatalf("redirect = %q, want the collectives list", loc)
	}
}
