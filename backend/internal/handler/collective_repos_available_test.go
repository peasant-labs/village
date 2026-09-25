package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

func availableRequest(t *testing.T, h *Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/groups/{id}/repositories/available", h.ListAvailableRepositories)
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req = req.WithContext(withTestUser(req.Context()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// boundGroupQuery returns a GetGroupByID stub for a collective bound to org.
func boundGroupQuery(org string) func(context.Context, pgtype.UUID) (sqlc.Group, error) {
	return func(_ context.Context, id pgtype.UUID) (sqlc.Group, error) {
		g := sqlc.Group{ID: id}
		if org != "" {
			g.LinkedGithubOrg = pgtype.Text{String: org, Valid: true}
		}
		return g, nil
	}
}

func TestListAvailableRepositories_ReturnsInstallationRepos(t *testing.T) {
	f := &fakeGitHub{
		installationsBody:     `[{"id":99,"account":{"login":"acme","id":1,"type":"Organization"}}]`,
		installationReposBody: `{"total_count":2,"repositories":[{"name":"one","private":false,"owner":{"login":"acme"}},{"name":"two","private":true,"owner":{"login":"acme"}}]}`,
	}
	mq := &mockQuerier{getGroupMember: memberStub("owner"), getGroupByID: boundGroupQuery("acme"), getUserByID: githubLogin("owner"), listUserAllOrgs: callerOrgs("acme")}
	newFakeGitHub(t, f)
	h := newRepoHandler(t, mq, f)

	w := availableRequest(t, h, "/groups/"+testGroupID+"/repositories/available")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var got schema.VillageAvailableRepositoriesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Repositories) != 2 || got.Repositories[1].Name != "two" || !got.Repositories[1].IsPrivate {
		t.Fatalf("repositories = %+v, want the installation's two repos", got.Repositories)
	}
}

func TestListAvailableRepositories_UnboundCollectiveReturnsEmpty(t *testing.T) {
	f := &fakeGitHub{installationsBody: `[{"id":99,"account":{"login":"acme","id":1,"type":"Organization"}}]`}
	mq := &mockQuerier{getGroupMember: memberStub("owner"), getGroupByID: boundGroupQuery("")}
	newFakeGitHub(t, f)
	h := newRepoHandler(t, mq, f)

	w := availableRequest(t, h, "/groups/"+testGroupID+"/repositories/available")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got schema.VillageAvailableRepositoriesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Repositories) != 0 {
		t.Fatalf("unbound collective returned %d repos, want none", len(got.Repositories))
	}
}

func TestListAvailableRepositories_NoInstallationForOrg(t *testing.T) {
	f := &fakeGitHub{installationsBody: `[]`}
	mq := &mockQuerier{getGroupMember: memberStub("owner"), getGroupByID: boundGroupQuery("acme"), getUserByID: githubLogin("owner"), listUserAllOrgs: callerOrgs("acme")}
	newFakeGitHub(t, f)
	h := newRepoHandler(t, mq, f)

	w := availableRequest(t, h, "/groups/"+testGroupID+"/repositories/available")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got schema.VillageAvailableRepositoriesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Repositories) != 0 {
		t.Fatalf("no matching installation returned %d repos, want none", len(got.Repositories))
	}
}

func TestListAvailableRepositories_NonOwnerRejected(t *testing.T) {
	f := &fakeGitHub{}
	mq := &mockQuerier{getGroupMember: memberStub("member"), getGroupByID: boundGroupQuery("acme"), getUserByID: githubLogin("owner"), listUserAllOrgs: callerOrgs("acme")}
	newFakeGitHub(t, f)
	h := newRepoHandler(t, mq, f)

	w := availableRequest(t, h, "/groups/"+testGroupID+"/repositories/available")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a non-owner", w.Code)
	}
}

func TestListAvailableRepositories_NotConfigured(t *testing.T) {
	h := newRepoHandler(t, &mockQuerier{getGroupMember: memberStub("owner")}, nil)
	w := availableRequest(t, h, "/groups/"+testGroupID+"/repositories/available")
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 when the App is not configured", w.Code)
	}
}

// TestListAvailableRepositories_RefusesAnOwnerOutsideTheOrganisation is the
// inventory's own gate: the repositories belong to the organisation that
// installed the App, so an owner whose GitHub account is not in it must not read
// the organisation's repository list.
func TestListAvailableRepositories_RefusesAnOwnerOutsideTheOrganisation(t *testing.T) {
	f := &fakeGitHub{
		installationsBody:     `[{"id":99,"account":{"login":"acme","id":1,"type":"Organization"}}]`,
		installationReposBody: `{"total_count":1,"repositories":[{"name":"secret","private":true,"owner":{"login":"acme"}}]}`,
	}
	mq := &mockQuerier{
		getGroupMember:  memberStub("owner"),
		getGroupByID:    boundGroupQuery("acme"),
		getUserByID:     githubLogin("outsider"),
		listUserAllOrgs: callerOrgs("other-org"),
	}
	newFakeGitHub(t, f)
	h := newRepoHandler(t, mq, f)

	w := availableRequest(t, h, "/groups/"+testGroupID+"/repositories/available")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for an owner outside the organisation (body: %s)", w.Code, w.Body.String())
	}
}
