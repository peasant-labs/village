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

// TestGitHubInstallCallback_RecordsInstallation proves a valid state records the
// installation and returns the owner to the collective settings page.
func TestGitHubInstallCallback_RecordsInstallation(t *testing.T) {
	f := &fakeGitHub{installationID: 555, installationAccount: "acme"}
	var upserted *sqlc.UpsertGitHubAppInstallationParams
	mq := &mockQuerier{
		getGroupMember: memberStub("owner"),
		upsertGitHubAppInstallation: func(_ context.Context, arg sqlc.UpsertGitHubAppInstallationParams) error {
			upserted = &arg
			return nil
		},
	}
	h := installHandler(t, f, mq)
	userID := uuid.New()
	state, err := auth.CreateInstallState(installTestSecret, userID.String(), testGroupID)
	if err != nil {
		t.Fatal(err)
	}

	w := installRequest(h, "/integrations/github/callback?installation_id=555&setup_action=install&state="+url.QueryEscape(state), userID)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (body: %s)", w.Code, w.Body.String())
	}
	if upserted == nil || upserted.InstallationID != 555 || upserted.AccountLogin != "acme" {
		t.Fatalf("installation not recorded: %+v", upserted)
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "/groups/"+testGroupID+"/settings") {
		t.Fatalf("redirect = %q, want the collective settings page", loc)
	}
}

func TestGitHubInstallCallback_RejectsBadState(t *testing.T) {
	h := installHandler(t, nil, &mockQuerier{getGroupMember: memberStub("owner")})
	w := installRequest(h, "/integrations/github/callback?installation_id=555&state=not-a-token", uuid.New())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an invalid state", w.Code)
	}
}

func TestGitHubInstallCallback_RejectsMismatchedUser(t *testing.T) {
	h := installHandler(t, nil, &mockQuerier{getGroupMember: memberStub("owner")})
	state, err := auth.CreateInstallState(installTestSecret, uuid.New().String(), testGroupID)
	if err != nil {
		t.Fatal(err)
	}
	w := installRequest(h, "/integrations/github/callback?installation_id=555&state="+url.QueryEscape(state), uuid.New())
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 when the state belongs to another account", w.Code)
	}
}
