package handler

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	gh "github.com/peasant-labs/village/backend/internal/github"
)

// ----------------------------------------------------------------------------
// Test scaffolding: a fake GitHub App API server + a handler with the client
// pointed at it. We never hit real GitHub.
// ----------------------------------------------------------------------------

type fakeGitHub struct {
	srv         *httptest.Server
	tokenCalls  int32
	commitCalls int32
	repoBody    string // JSON for GET /repos/{owner}/{name}
	repoStatus  int    // status for repo endpoint (default 200)
	commitsBody string // JSON for GET /repos/.../commits
	commitsETag string
	notModified bool
	// installationID is the id returned by GET /repos/{owner}/{name}/installation.
	installationID     int64
	installationStatus int // status for that endpoint (default 200)
	// installationAccount is the account login returned by GET /app/installations/{id}.
	installationAccount string
	// installationsBody is the JSON for GET /app/installations (the App's list).
	installationsBody string
	// installationReposBody is the JSON for GET /installation/repositories.
	installationReposBody string
}

func newFakeGitHub(t *testing.T, f *fakeGitHub) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, f.installationsBody)
	})
	mux.HandleFunc("/installation/repositories", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, f.installationReposBody)
	})
	mux.HandleFunc("/app/installations/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprintf(w, `{"id":%d,"account":{"login":%q,"id":1,"type":"Organization"}}`, f.installationID, f.installationAccount)
			return
		}
		atomic.AddInt32(&f.tokenCalls, 1)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"token":"ghs_x","expires_at":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	})
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/commits") {
			atomic.AddInt32(&f.commitCalls, 1)
			if f.notModified && r.Header.Get("If-None-Match") == f.commitsETag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			if f.commitsETag != "" {
				w.Header().Set("ETag", f.commitsETag)
			}
			fmt.Fprint(w, f.commitsBody)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/installation") {
			status := f.installationStatus
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			if status == http.StatusOK {
				fmt.Fprintf(w, `{"id":%d}`, f.installationID)
			}
			return
		}
		status := f.repoStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		fmt.Fprint(w, f.repoBody)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
}

func testAppPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	return string(pem.EncodeToMemory(block))
}

// newRepoHandler builds a *Handler with a GitHub client pointed at f (or no
// client when f is nil, to exercise the not-configured 501 path).
func newRepoHandler(t *testing.T, q Querier, f *fakeGitHub) *Handler {
	t.Helper()
	h := newTestHandler(q, nil)
	if f != nil {
		client, err := gh.NewClient(gh.Config{AppID: "123", PrivateKeyPEM: testAppPEM(t)}, gh.WithBaseURL(f.srv.URL))
		if err != nil {
			t.Fatalf("gh.NewClient: %v", err)
		}
		h.gh = client
	}
	return h
}

// ownerMember returns a stub that reports the caller as the given role.
func memberStub(role string) func(ctx context.Context, arg sqlc.GetGroupMemberParams) (sqlc.GroupMember, error) {
	return func(ctx context.Context, arg sqlc.GetGroupMemberParams) (sqlc.GroupMember, error) {
		return sqlc.GroupMember{GroupID: arg.GroupID, UserID: arg.UserID, Role: role}, nil
	}
}

// notAMember returns a stub that reports the caller is not in the group.
func notAMember() func(ctx context.Context, arg sqlc.GetGroupMemberParams) (sqlc.GroupMember, error) {
	return func(ctx context.Context, arg sqlc.GetGroupMemberParams) (sqlc.GroupMember, error) {
		return sqlc.GroupMember{}, errors.New("no rows")
	}
}

// routeRequest dispatches req through a chi router so chi.URLParam works exactly
// as in production (path params like {id}, {owner}, {name}).
func routeRequest(h *Handler, method, target string, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Get("/groups/{id}/repositories", h.ListRepositories)
	r.Post("/groups/{id}/repositories", h.LinkRepository)
	r.Delete("/groups/{id}/repositories/{owner}/{name}", h.UnlinkRepository)
	r.Get("/groups/{id}/repositories/{owner}/{name}/commits", h.ListRepositoryCommits)

	var reqBody *strings.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	} else {
		reqBody = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, reqBody)
	req = req.WithContext(withTestUser(req.Context()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

const testGroupID = "11111111-1111-1111-1111-111111111111"

// ----------------------------------------------------------------------------
// Not-configured path: every endpoint that needs the App returns 501.
// ----------------------------------------------------------------------------

func TestLinkRepository_NotConfigured(t *testing.T) {
	// Owner caller, but no GitHub client wired -> 501 before any DB work.
	mq := &mockQuerier{getGroupMember: memberStub("owner")}
	h := newRepoHandler(t, mq, nil) // nil fake => h.gh stays nil

	w := routeRequest(h, http.MethodPost, "/groups/"+testGroupID+"/repositories",
		`{"owner":"acme","name":"repo","installation_id":42}`)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 (body: %s)", w.Code, w.Body.String())
	}
}

func TestRefreshCommits_NotConfigured(t *testing.T) {
	mq := &mockQuerier{
		getGroupMember: memberStub("owner"),
		getCollectiveRepository: func(ctx context.Context, arg sqlc.GetCollectiveRepositoryParams) (sqlc.CollectiveRepository, error) {
			return sqlc.CollectiveRepository{GroupID: arg.GroupID, Owner: "acme", Name: "repo", InstallationID: 42}, nil
		},
		getGitHubAppInstallation: acmeInstallation(),
		getUserByID:              githubLogin("acme-member"),
		listUserAllOrgs:          callerOrgs("acme"),
	}
	h := newRepoHandler(t, mq, nil)

	w := routeRequest(h, http.MethodGet, "/groups/"+testGroupID+"/repositories/acme/repo/commits?refresh=true", "")
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 (body: %s)", w.Code, w.Body.String())
	}
}

// ----------------------------------------------------------------------------
// Link: happy path + admin-only enforcement + access validation.
// ----------------------------------------------------------------------------

func TestLinkRepository_OwnerSucceeds(t *testing.T) {
	f := &fakeGitHub{repoBody: `{"name":"repo","private":true,"owner":{"login":"acme"}}`}
	newFakeGitHub(t, f)

	var linked *sqlc.LinkCollectiveRepositoryParams
	mq := &mockQuerier{
		getGroupMember: memberStub("owner"),
		linkCollectiveRepository: func(ctx context.Context, arg sqlc.LinkCollectiveRepositoryParams) (sqlc.CollectiveRepository, error) {
			linked = &arg
			return sqlc.CollectiveRepository{
				ID: toPgUUID(uuid.New()), GroupID: arg.GroupID,
				Owner: arg.Owner, Name: arg.Name, InstallationID: arg.InstallationID, IsPrivate: arg.IsPrivate,
			}, nil
		},
	}
	h := newRepoHandler(t, mq, f)

	w := routeRequest(h, http.MethodPost, "/groups/"+testGroupID+"/repositories",
		`{"owner":"acme","name":"repo","installation_id":42}`)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", w.Code, w.Body.String())
	}
	if linked == nil {
		t.Fatal("LinkCollectiveRepository was not called")
	}
	if linked.Owner != "acme" || linked.Name != "repo" || linked.InstallationID != 42 {
		t.Errorf("persisted link wrong: %+v", linked)
	}
	// Privacy is derived from the validated repo metadata, not the request.
	if !linked.IsPrivate {
		t.Error("expected is_private=true derived from repo metadata")
	}
}

// TestLinkRepository_ResolvesInstallationFromRepo proves a link with no
// installation_id resolves one from the repository and persists the resolved id.
func TestLinkRepository_ResolvesInstallationFromRepo(t *testing.T) {
	f := &fakeGitHub{repoBody: `{"name":"repo","private":false,"owner":{"login":"acme"}}`, installationID: 99}
	newFakeGitHub(t, f)

	var linked *sqlc.LinkCollectiveRepositoryParams
	mq := &mockQuerier{
		getGroupMember: memberStub("owner"),
		linkCollectiveRepository: func(ctx context.Context, arg sqlc.LinkCollectiveRepositoryParams) (sqlc.CollectiveRepository, error) {
			linked = &arg
			return sqlc.CollectiveRepository{ID: toPgUUID(uuid.New()), GroupID: arg.GroupID, Owner: arg.Owner, Name: arg.Name, InstallationID: arg.InstallationID}, nil
		},
	}
	h := newRepoHandler(t, mq, f)

	w := routeRequest(h, http.MethodPost, "/groups/"+testGroupID+"/repositories", `{"owner":"acme","name":"repo"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", w.Code, w.Body.String())
	}
	if linked == nil || linked.InstallationID != 99 {
		t.Fatalf("resolved installation not persisted: %+v", linked)
	}
}

// TestLinkRepository_NoInstallationCoversRepo proves that when no installation
// covers the repository, the link is refused rather than persisted.
func TestLinkRepository_NoInstallationCoversRepo(t *testing.T) {
	f := &fakeGitHub{repoBody: `{"name":"repo","private":false,"owner":{"login":"acme"}}`, installationStatus: http.StatusNotFound}
	newFakeGitHub(t, f)

	mq := &mockQuerier{getGroupMember: memberStub("owner")}
	h := newRepoHandler(t, mq, f)

	w := routeRequest(h, http.MethodPost, "/groups/"+testGroupID+"/repositories", `{"owner":"acme","name":"repo"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 when no installation covers the repo (body: %s)", w.Code, w.Body.String())
	}
}

// TestLinkRepository_AllowsRepoInCollectiveOrg proves a collective bound to an
// org may link a repo under that org.
func TestLinkRepository_AllowsRepoInCollectiveOrg(t *testing.T) {
	f := &fakeGitHub{repoBody: `{"name":"repo","private":false,"owner":{"login":"acme"}}`, installationID: 99, installationAccount: "acme"}
	newFakeGitHub(t, f)

	var linked *sqlc.LinkCollectiveRepositoryParams
	mq := &mockQuerier{
		getGroupMember: memberStub("owner"),
		getGroupByID: func(_ context.Context, id pgtype.UUID) (sqlc.Group, error) {
			return sqlc.Group{ID: id, LinkedGithubOrg: pgtype.Text{String: "acme", Valid: true}}, nil
		},
		linkCollectiveRepository: func(_ context.Context, arg sqlc.LinkCollectiveRepositoryParams) (sqlc.CollectiveRepository, error) {
			linked = &arg
			return sqlc.CollectiveRepository{ID: toPgUUID(uuid.New()), GroupID: arg.GroupID, Owner: arg.Owner, Name: arg.Name, InstallationID: arg.InstallationID}, nil
		},
	}
	h := newRepoHandler(t, mq, f)

	w := routeRequest(h, http.MethodPost, "/groups/"+testGroupID+"/repositories", `{"owner":"acme","name":"repo"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", w.Code, w.Body.String())
	}
	if linked == nil || linked.InstallationID != 99 {
		t.Fatalf("link not persisted: %+v", linked)
	}
}

// TestLinkRepository_RejectsRepoOutsideCollectiveOrg proves a collective bound
// to one org cannot link a repo whose installation belongs to another.
func TestLinkRepository_RejectsRepoOutsideCollectiveOrg(t *testing.T) {
	f := &fakeGitHub{repoBody: `{"name":"repo","private":false,"owner":{"login":"acme"}}`, installationID: 99, installationAccount: "someone-else"}
	newFakeGitHub(t, f)

	mq := &mockQuerier{
		getGroupMember: memberStub("owner"),
		getGroupByID: func(_ context.Context, id pgtype.UUID) (sqlc.Group, error) {
			return sqlc.Group{ID: id, LinkedGithubOrg: pgtype.Text{String: "acme", Valid: true}}, nil
		},
	}
	h := newRepoHandler(t, mq, f)

	w := routeRequest(h, http.MethodPost, "/groups/"+testGroupID+"/repositories", `{"owner":"acme","name":"repo"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 when the installation is outside the collective's org (body: %s)", w.Code, w.Body.String())
	}
}

func TestLinkRepository_NonOwnerRejected(t *testing.T) {
	f := &fakeGitHub{repoBody: `{"name":"repo","private":false,"owner":{"login":"acme"}}`}
	newFakeGitHub(t, f)

	for _, role := range []string{"member", "contributor", "pending"} {
		t.Run(role, func(t *testing.T) {
			mq := &mockQuerier{
				getGroupMember: memberStub(role),
				linkCollectiveRepository: func(ctx context.Context, arg sqlc.LinkCollectiveRepositoryParams) (sqlc.CollectiveRepository, error) {
					t.Fatal("LinkCollectiveRepository must not be called for non-owner")
					return sqlc.CollectiveRepository{}, nil
				},
			}
			h := newRepoHandler(t, mq, f)
			w := routeRequest(h, http.MethodPost, "/groups/"+testGroupID+"/repositories",
				`{"owner":"acme","name":"repo","installation_id":42}`)
			if w.Code != http.StatusForbidden {
				t.Fatalf("role %s: status = %d, want 403 (body: %s)", role, w.Code, w.Body.String())
			}
		})
	}
}

func TestLinkRepository_NonMemberRejected(t *testing.T) {
	f := &fakeGitHub{repoBody: `{"name":"repo","private":false,"owner":{"login":"acme"}}`}
	newFakeGitHub(t, f)
	mq := &mockQuerier{getGroupMember: notAMember()}
	h := newRepoHandler(t, mq, f)

	w := routeRequest(h, http.MethodPost, "/groups/"+testGroupID+"/repositories",
		`{"owner":"acme","name":"repo","installation_id":42}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body: %s)", w.Code, w.Body.String())
	}
}

func TestLinkRepository_InaccessibleRepoRejected(t *testing.T) {
	// GitHub returns 404 for the repo => installation has no access => 400.
	f := &fakeGitHub{repoStatus: http.StatusNotFound, repoBody: `{"message":"Not Found"}`}
	newFakeGitHub(t, f)
	mq := &mockQuerier{getGroupMember: memberStub("owner")}
	h := newRepoHandler(t, mq, f)

	w := routeRequest(h, http.MethodPost, "/groups/"+testGroupID+"/repositories",
		`{"owner":"acme","name":"secret","installation_id":42}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
}

func TestLinkRepository_MissingFields(t *testing.T) {
	f := &fakeGitHub{repoBody: `{}`}
	newFakeGitHub(t, f)
	mq := &mockQuerier{getGroupMember: memberStub("owner")}
	h := newRepoHandler(t, mq, f)

	w := routeRequest(h, http.MethodPost, "/groups/"+testGroupID+"/repositories",
		`{"owner":"","name":"","installation_id":0}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// ----------------------------------------------------------------------------
// Unlink + list.
// ----------------------------------------------------------------------------

func TestUnlinkRepository_OwnerSucceeds(t *testing.T) {
	mq := &mockQuerier{
		getGroupMember: memberStub("owner"),
		unlinkCollectiveRepository: func(ctx context.Context, arg sqlc.UnlinkCollectiveRepositoryParams) (int64, error) {
			if arg.Lower != "acme" || arg.Lower_2 != "repo" {
				t.Errorf("unlink args = %+v", arg)
			}
			return 1, nil
		},
	}
	h := newRepoHandler(t, mq, nil) // unlink does not need GitHub

	w := routeRequest(h, http.MethodDelete, "/groups/"+testGroupID+"/repositories/acme/repo", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
}

func TestUnlinkRepository_NotLinked404(t *testing.T) {
	mq := &mockQuerier{
		getGroupMember: memberStub("owner"),
		unlinkCollectiveRepository: func(ctx context.Context, arg sqlc.UnlinkCollectiveRepositoryParams) (int64, error) {
			return 0, nil // no rows affected
		},
	}
	h := newRepoHandler(t, mq, nil)

	w := routeRequest(h, http.MethodDelete, "/groups/"+testGroupID+"/repositories/acme/ghost", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", w.Code, w.Body.String())
	}
}

func TestUnlinkRepository_NonOwnerRejected(t *testing.T) {
	mq := &mockQuerier{
		getGroupMember: memberStub("member"),
		unlinkCollectiveRepository: func(ctx context.Context, arg sqlc.UnlinkCollectiveRepositoryParams) (int64, error) {
			t.Fatal("unlink must not be called for non-owner")
			return 0, nil
		},
	}
	h := newRepoHandler(t, mq, nil)

	w := routeRequest(h, http.MethodDelete, "/groups/"+testGroupID+"/repositories/acme/repo", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestListRepositories_MemberSees(t *testing.T) {
	mq := &mockQuerier{
		getGroupMember: memberStub("member"),
		listCollectiveRepositories: func(ctx context.Context, groupID pgtype.UUID) ([]sqlc.CollectiveRepository, error) {
			return []sqlc.CollectiveRepository{
				{Owner: "acme", Name: "repo", InstallationID: 42},
				{Owner: "acme", Name: "tools", InstallationID: 42},
			}, nil
		},
		getGitHubAppInstallation: acmeInstallation(),
		getUserByID:              githubLogin("acme-member"),
		listUserAllOrgs:          callerOrgs("acme"),
	}
	h := newRepoHandler(t, mq, nil)

	w := routeRequest(h, http.MethodGet, "/groups/"+testGroupID+"/repositories", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var resp struct {
		Repositories []repoResponse `json:"repositories"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Repositories) != 2 {
		t.Errorf("got %d repositories, want 2", len(resp.Repositories))
	}
}

func TestListRepositories_NonMemberRejected(t *testing.T) {
	mq := &mockQuerier{getGroupMember: notAMember()}
	h := newRepoHandler(t, mq, nil)
	w := routeRequest(h, http.MethodGet, "/groups/"+testGroupID+"/repositories", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

// ----------------------------------------------------------------------------
// Commits: cache-first read, owner-gated refresh, fetch+cache, 304 no-refetch.
// ----------------------------------------------------------------------------

func linkedRepoStub(etag string) func(ctx context.Context, arg sqlc.GetCollectiveRepositoryParams) (sqlc.CollectiveRepository, error) {
	return func(ctx context.Context, arg sqlc.GetCollectiveRepositoryParams) (sqlc.CollectiveRepository, error) {
		repo := sqlc.CollectiveRepository{GroupID: arg.GroupID, Owner: "acme", Name: "repo", InstallationID: 42}
		if etag != "" {
			repo.CommitsEtag = pgtype.Text{String: etag, Valid: true}
		}
		return repo, nil
	}
}

func TestListCommits_CacheFirstNoRefresh(t *testing.T) {
	f := &fakeGitHub{commitsBody: `[]`}
	newFakeGitHub(t, f)

	mq := &mockQuerier{
		getGroupMember:           memberStub("member"),
		getGitHubAppInstallation: acmeInstallation(),
		getUserByID:              githubLogin("acme-member"),
		listUserAllOrgs:          callerOrgs("acme"),
		getCollectiveRepository:  linkedRepoStub(""),
		listRepositoryCommits: func(ctx context.Context, arg sqlc.ListRepositoryCommitsParams) ([]sqlc.RepositoryCommit, error) {
			return []sqlc.RepositoryCommit{
				{Sha: "cached1"}, {Sha: "cached2"},
			}, nil
		},
	}
	h := newRepoHandler(t, mq, f)

	// No ?refresh -> serve cache, never call GitHub.
	w := routeRequest(h, http.MethodGet, "/groups/"+testGroupID+"/repositories/acme/repo/commits", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if f.commitCalls != 0 {
		t.Errorf("GitHub commits endpoint called %d times, want 0 (cache-first)", f.commitCalls)
	}
	var resp struct {
		Refreshed bool             `json:"refreshed"`
		Commits   []commitResponse `json:"commits"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Refreshed {
		t.Error("refreshed should be false without ?refresh=true")
	}
	if len(resp.Commits) != 2 {
		t.Errorf("got %d cached commits, want 2", len(resp.Commits))
	}
}

func TestListCommits_RefreshFetchesAndCaches(t *testing.T) {
	f := &fakeGitHub{
		commitsETag: `"etag-1"`,
		commitsBody: `[
			{"sha":"aaa","commit":{"message":"first","author":{"name":"Alice","email":"a@x.io","date":"2024-01-01T10:00:00Z"},"committer":{"date":"2024-01-01T10:05:00Z"}}},
			{"sha":"bbb","commit":{"message":"second","author":{"name":"Bob"},"committer":{}}}
		]`,
	}
	newFakeGitHub(t, f)

	var upserted []string
	var savedETag string
	mq := &mockQuerier{
		getGroupMember:           memberStub("owner"),
		getGitHubAppInstallation: acmeInstallation(),
		getUserByID:              githubLogin("acme-member"),
		listUserAllOrgs:          callerOrgs("acme"),
		getCollectiveRepository:  linkedRepoStub(""),
		upsertRepositoryCommit: func(ctx context.Context, arg sqlc.UpsertRepositoryCommitParams) error {
			upserted = append(upserted, arg.Sha)
			return nil
		},
		updateCollectiveRepositorySync: func(ctx context.Context, arg sqlc.UpdateCollectiveRepositorySyncParams) error {
			savedETag = arg.CommitsEtag.String
			return nil
		},
		listRepositoryCommits: func(ctx context.Context, arg sqlc.ListRepositoryCommitsParams) ([]sqlc.RepositoryCommit, error) {
			// After refresh, return what the cache would now hold.
			return []sqlc.RepositoryCommit{{Sha: "aaa"}, {Sha: "bbb"}}, nil
		},
	}
	h := newRepoHandler(t, mq, f)

	w := routeRequest(h, http.MethodGet, "/groups/"+testGroupID+"/repositories/acme/repo/commits?refresh=true", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if f.commitCalls != 1 {
		t.Errorf("GitHub commits called %d times, want 1", f.commitCalls)
	}
	if len(upserted) != 2 || upserted[0] != "aaa" || upserted[1] != "bbb" {
		t.Errorf("upserted SHAs = %v, want [aaa bbb]", upserted)
	}
	if savedETag != `"etag-1"` {
		t.Errorf("saved ETag = %q, want \"etag-1\"", savedETag)
	}
}

func TestListCommits_RefreshNotModifiedSkipsCacheWrite(t *testing.T) {
	f := &fakeGitHub{
		commitsETag: `"etag-1"`,
		notModified: true,
		commitsBody: `[{"sha":"aaa","commit":{"message":"x","author":{"name":"A"}}}]`,
	}
	newFakeGitHub(t, f)

	upsertCalled := false
	mq := &mockQuerier{
		getGroupMember:           memberStub("owner"),
		getGitHubAppInstallation: acmeInstallation(),
		getUserByID:              githubLogin("acme-member"),
		listUserAllOrgs:          callerOrgs("acme"),
		getCollectiveRepository:  linkedRepoStub(`"etag-1"`), // stored ETag matches -> 304
		upsertRepositoryCommit: func(ctx context.Context, arg sqlc.UpsertRepositoryCommitParams) error {
			upsertCalled = true
			return nil
		},
		listRepositoryCommits: func(ctx context.Context, arg sqlc.ListRepositoryCommitsParams) ([]sqlc.RepositoryCommit, error) {
			return []sqlc.RepositoryCommit{{Sha: "aaa"}}, nil
		},
	}
	h := newRepoHandler(t, mq, f)

	w := routeRequest(h, http.MethodGet, "/groups/"+testGroupID+"/repositories/acme/repo/commits?refresh=true", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if f.commitCalls != 1 {
		t.Errorf("GitHub commits called %d times, want 1 (the conditional request itself)", f.commitCalls)
	}
	if upsertCalled {
		t.Error("UpsertRepositoryCommit must NOT be called on 304 Not Modified")
	}
}

func TestListCommits_RefreshNonOwnerRejected(t *testing.T) {
	f := &fakeGitHub{commitsBody: `[]`}
	newFakeGitHub(t, f)
	mq := &mockQuerier{
		getGroupMember:           memberStub("member"),
		getGitHubAppInstallation: acmeInstallation(),
		getUserByID:              githubLogin("acme-member"),
		listUserAllOrgs:          callerOrgs("acme"),
		getCollectiveRepository:  linkedRepoStub(""),
		listRepositoryCommits: func(ctx context.Context, arg sqlc.ListRepositoryCommitsParams) ([]sqlc.RepositoryCommit, error) {
			return nil, nil
		},
	}
	h := newRepoHandler(t, mq, f)

	w := routeRequest(h, http.MethodGet, "/groups/"+testGroupID+"/repositories/acme/repo/commits?refresh=true", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (refresh is owner-only)", w.Code)
	}
	if f.commitCalls != 0 {
		t.Errorf("GitHub must not be called when refresh is rejected; got %d calls", f.commitCalls)
	}
}

func TestListCommits_RepoNotLinked404(t *testing.T) {
	mq := &mockQuerier{
		getGroupMember: memberStub("member"),
		getCollectiveRepository: func(ctx context.Context, arg sqlc.GetCollectiveRepositoryParams) (sqlc.CollectiveRepository, error) {
			return sqlc.CollectiveRepository{}, errors.New("no rows")
		},
	}
	h := newRepoHandler(t, mq, nil)

	w := routeRequest(h, http.MethodGet, "/groups/"+testGroupID+"/repositories/acme/ghost/commits", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

// acmeInstallation is the stub reporting the account a linked repository's
// installation belongs to.
func acmeInstallation() func(context.Context, int64) (sqlc.GithubAppInstallation, error) {
	return func(_ context.Context, id int64) (sqlc.GithubAppInstallation, error) {
		if id != 42 {
			panic(fmt.Sprintf("acmeInstallation asked for installation %d, want 42", id))
		}
		return sqlc.GithubAppInstallation{InstallationID: id, AccountLogin: "acme", AccountID: 1}, nil
	}
}

// TestListRepositories_HidesAnotherAccountsRepositories is the boundary the
// review found: the repositories come from an installation that belongs to an
// organisation, not to the collective, so a member of the collective who has
// nothing to do with that organisation must not read them. Their own
// collectives still list whatever their account does control.
func TestListRepositories_HidesAnotherAccountsRepositories(t *testing.T) {
	mq := &mockQuerier{
		getGroupMember: memberStub("member"),
		listCollectiveRepositories: func(context.Context, pgtype.UUID) ([]sqlc.CollectiveRepository, error) {
			return []sqlc.CollectiveRepository{
				{Owner: "acme", Name: "repo", InstallationID: 42},
				{Owner: "acme", Name: "tools", InstallationID: 42},
			}, nil
		},
		getGitHubAppInstallation: acmeInstallation(),
		getUserByID:              githubLogin("outsider"),
		listUserAllOrgs:          callerOrgs("other-org"),
	}
	h := newRepoHandler(t, mq, nil)

	w := routeRequest(h, http.MethodGet, "/groups/"+testGroupID+"/repositories", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var resp struct {
		Repositories []repoResponse `json:"repositories"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Repositories) != 0 {
		t.Fatalf("got %d repositories, want none for an account the viewer does not control", len(resp.Repositories))
	}
}

// TestListRepositoryCommits_RefusesAnotherAccountsRepository is the same
// boundary on the commit read, which is the surface that carries messages,
// authors and timestamps.
func TestListRepositoryCommits_RefusesAnotherAccountsRepository(t *testing.T) {
	mq := &mockQuerier{
		getGroupMember: memberStub("member"),
		getCollectiveRepository: func(context.Context, sqlc.GetCollectiveRepositoryParams) (sqlc.CollectiveRepository, error) {
			return sqlc.CollectiveRepository{Owner: "acme", Name: "repo", InstallationID: 42}, nil
		},
		getGitHubAppInstallation: acmeInstallation(),
		getUserByID:              githubLogin("outsider"),
		listUserAllOrgs:          callerOrgs("other-org"),
	}
	h := newRepoHandler(t, mq, nil)

	w := routeRequest(h, http.MethodGet, "/groups/"+testGroupID+"/repositories/acme/repo/commits", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: the answer an unlinked repository gets, so the refusal does not confirm the link (body: %s)", w.Code, w.Body.String())
	}
}

// TestListRepositories_KeepsOnlyTheAccountsTheViewerControls proves the filter is
// per account: a member who controls one of the collective's accounts keeps that
// account's repositories and loses the other's.
func TestListRepositories_KeepsOnlyTheAccountsTheViewerControls(t *testing.T) {
	mq := &mockQuerier{
		getGroupMember: memberStub("member"),
		listCollectiveRepositories: func(context.Context, pgtype.UUID) ([]sqlc.CollectiveRepository, error) {
			return []sqlc.CollectiveRepository{
				{Owner: "acme", Name: "repo", InstallationID: 42},
				{Owner: "other-org", Name: "secret", InstallationID: 43},
			}, nil
		},
		getGitHubAppInstallation: func(_ context.Context, id int64) (sqlc.GithubAppInstallation, error) {
			switch id {
			case 42:
				return sqlc.GithubAppInstallation{InstallationID: 42, AccountLogin: "acme", AccountID: 1}, nil
			case 43:
				return sqlc.GithubAppInstallation{InstallationID: 43, AccountLogin: "other-org", AccountID: 2}, nil
			}
			t.Fatalf("unexpected installation %d", id)
			return sqlc.GithubAppInstallation{}, nil
		},
		getUserByID:     githubLogin("acme-member"),
		listUserAllOrgs: callerOrgs("acme"),
	}
	h := newRepoHandler(t, mq, nil)

	w := routeRequest(h, http.MethodGet, "/groups/"+testGroupID+"/repositories", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var resp struct {
		Repositories []repoResponse `json:"repositories"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Repositories) != 1 || resp.Repositories[0].Owner != "acme" || resp.Repositories[0].Name != "repo" {
		t.Fatalf("repositories = %+v, want only acme/repo", resp.Repositories)
	}
}

// TestListRepositories_DoesNotTrustAReusedLogin pins that control is decided on
// the immutable account id. The viewer's organisation login is the one the
// installation's account carries, but their account is a different account, so
// they do not control it.
func TestListRepositories_DoesNotTrustAReusedLogin(t *testing.T) {
	mq := &mockQuerier{
		getGroupMember: memberStub("member"),
		listCollectiveRepositories: func(context.Context, pgtype.UUID) ([]sqlc.CollectiveRepository, error) {
			return []sqlc.CollectiveRepository{{Owner: "acme", Name: "repo", InstallationID: 42}}, nil
		},
		getGitHubAppInstallation: acmeInstallation(),
		getUserByID:              githubLogin("outsider"),
		listUserAllOrgs: func(context.Context, pgtype.UUID) ([]sqlc.ListUserAllOrgsRow, error) {
			return []sqlc.ListUserAllOrgsRow{{OrgLogin: "acme", OrgID: 2}}, nil
		},
	}
	h := newRepoHandler(t, mq, nil)

	w := routeRequest(h, http.MethodGet, "/groups/"+testGroupID+"/repositories", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var resp struct {
		Repositories []repoResponse `json:"repositories"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Repositories) != 0 {
		t.Fatalf("got %d repositories, want none: a login the installation's account carries is not control of it", len(resp.Repositories))
	}
}

// TestListRepositories_DeniesALinkItCannotResolve keeps one unreadable link from
// hiding the viewer's own repositories: that row is denied and the rest survive.
func TestListRepositories_DeniesALinkItCannotResolve(t *testing.T) {
	mq := &mockQuerier{
		getGroupMember: memberStub("member"),
		listCollectiveRepositories: func(context.Context, pgtype.UUID) ([]sqlc.CollectiveRepository, error) {
			return []sqlc.CollectiveRepository{
				{Owner: "acme", Name: "repo", InstallationID: 42},
				{Owner: "gone", Name: "dangling", InstallationID: 43},
			}, nil
		},
		getGitHubAppInstallation: func(_ context.Context, id int64) (sqlc.GithubAppInstallation, error) {
			if id == 43 {
				return sqlc.GithubAppInstallation{}, errors.New("no such installation")
			}
			return sqlc.GithubAppInstallation{InstallationID: 42, AccountLogin: "acme", AccountID: 1}, nil
		},
		getUserByID:     githubLogin("acme-member"),
		listUserAllOrgs: callerOrgs("acme"),
	}
	h := newRepoHandler(t, mq, nil)

	w := routeRequest(h, http.MethodGet, "/groups/"+testGroupID+"/repositories", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var resp struct {
		Repositories []repoResponse `json:"repositories"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Repositories) != 1 || resp.Repositories[0].Owner != "acme" {
		t.Fatalf("repositories = %+v, want only the readable one", resp.Repositories)
	}
}

// TestListRepositories_AdmitsAMemberOfTheInstallationsOrganisation pins the other
// half of the control rule: the caller's own GitHub account need not be the
// account the installation belongs to, as long as their account is in the
// organisation that account is.
func TestListRepositories_AdmitsAMemberOfTheInstallationsOrganisation(t *testing.T) {
	mq := &mockQuerier{
		getGroupMember: memberStub("member"),
		listCollectiveRepositories: func(context.Context, pgtype.UUID) ([]sqlc.CollectiveRepository, error) {
			return []sqlc.CollectiveRepository{{Owner: "acme", Name: "repo", InstallationID: 42}}, nil
		},
		getGitHubAppInstallation: acmeInstallation(),
		// A different account id from the installation's, with the organisation
		// the installation belongs to.
		getUserByID:     githubLogin("outsider"),
		listUserAllOrgs: callerOrgs("acme"),
	}
	h := newRepoHandler(t, mq, nil)

	w := routeRequest(h, http.MethodGet, "/groups/"+testGroupID+"/repositories", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var resp struct {
		Repositories []repoResponse `json:"repositories"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Repositories) != 1 {
		t.Fatalf("got %d repositories, want the one the caller's organisation owns", len(resp.Repositories))
	}
}

// TestListRepositories_ReportsAnUnreadableIdentity keeps the read failing closed
// when the viewer's GitHub identity cannot be read.
func TestListRepositories_ReportsAnUnreadableIdentity(t *testing.T) {
	mq := &mockQuerier{
		getGroupMember: memberStub("member"),
		listCollectiveRepositories: func(context.Context, pgtype.UUID) ([]sqlc.CollectiveRepository, error) {
			return []sqlc.CollectiveRepository{{Owner: "acme", Name: "repo", InstallationID: 42}}, nil
		},
		getGitHubAppInstallation: acmeInstallation(),
		getUserByID: func(context.Context, pgtype.UUID) (sqlc.User, error) {
			return sqlc.User{}, errors.New("read failed")
		},
	}
	h := newRepoHandler(t, mq, nil)

	w := routeRequest(h, http.MethodGet, "/groups/"+testGroupID+"/repositories", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when the viewer's identity cannot be read", w.Code)
	}
}

// TestListRepositories_RefusesANonGitHubCaller covers the caller who signed in
// through another provider: they carry no GitHub identity, so no installation
// account is theirs to control.
func TestListRepositories_RefusesANonGitHubCaller(t *testing.T) {
	mq := &mockQuerier{
		getGroupMember: memberStub("member"),
		listCollectiveRepositories: func(context.Context, pgtype.UUID) ([]sqlc.CollectiveRepository, error) {
			return []sqlc.CollectiveRepository{{Owner: "acme", Name: "repo", InstallationID: 42}}, nil
		},
		getGitHubAppInstallation: acmeInstallation(),
		getUserByID: func(context.Context, pgtype.UUID) (sqlc.User, error) {
			return sqlc.User{Provider: "gitlab", GithubID: 1}, nil
		},
		// A stale org row: only the early return keeps this from granting access.
		listUserAllOrgs: callerOrgs("acme"),
	}
	h := newRepoHandler(t, mq, nil)

	w := routeRequest(h, http.MethodGet, "/groups/"+testGroupID+"/repositories", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var resp struct {
		Repositories []repoResponse `json:"repositories"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Repositories) != 0 {
		t.Fatalf("got %d repositories, want none: a caller without a GitHub identity controls no installation", len(resp.Repositories))
	}
}

// TestListRepositoryCommits_NonMemberGetsTheSameRefusalForALinkedRepository
// pins the order of the checks: membership is answered before the link is read,
// so a caller who is not in the collective cannot tell a linked repository from
// an unlinked one. Reading the link first would answer 404 for the unlinked case
// and 403 for the linked one.
func TestListRepositoryCommits_NonMemberGetsTheSameRefusalForALinkedRepository(t *testing.T) {
	mq := &mockQuerier{
		getGroupMember: notAMember(),
		getCollectiveRepository: func(context.Context, sqlc.GetCollectiveRepositoryParams) (sqlc.CollectiveRepository, error) {
			return sqlc.CollectiveRepository{}, errors.New("not linked")
		},
	}
	h := newRepoHandler(t, mq, nil)

	w := routeRequest(h, http.MethodGet, "/groups/"+testGroupID+"/repositories/acme/repo/commits", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: membership is answered before the link is read (body: %s)", w.Code, w.Body.String())
	}
}
