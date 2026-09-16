package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testKeyPEM generates a fresh RSA private key encoded as PKCS#1 PEM, the format
// GitHub hands out for App private keys.
func testKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}
	return string(pem.EncodeToMemory(block))
}

// testKeyPEMPKCS8 generates a fresh RSA key in PKCS#8 PEM, covering the openssl
// re-encoded variant our parser must also accept.
func testKeyPEMPKCS8(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	return string(pem.EncodeToMemory(block))
}

func TestNewClient_NotConfigured(t *testing.T) {
	cases := []Config{
		{},
		{AppID: "123"},
		{PrivateKeyPEM: "x"},
		{AppID: "  ", PrivateKeyPEM: "  "},
	}
	for i, cfg := range cases {
		if _, err := NewClient(cfg); err != ErrNotConfigured {
			t.Errorf("case %d: err = %v, want ErrNotConfigured", i, err)
		}
	}
}

func TestNewClient_InvalidKey(t *testing.T) {
	_, err := NewClient(Config{AppID: "123", PrivateKeyPEM: "-----BEGIN RSA PRIVATE KEY-----\nnonsense\n-----END RSA PRIVATE KEY-----"})
	if err == nil || err == ErrNotConfigured {
		t.Fatalf("expected a parse error, got %v", err)
	}
}

func TestNewClient_AcceptsPKCS1AndPKCS8(t *testing.T) {
	for _, pemStr := range []string{testKeyPEM(t), testKeyPEMPKCS8(t)} {
		if _, err := NewClient(Config{AppID: "123", PrivateKeyPEM: pemStr}); err != nil {
			t.Errorf("NewClient: unexpected error: %v", err)
		}
	}
}

// newAppServer returns an httptest server that emulates the two GitHub
// endpoints we use, plus counters so tests can assert how often each was hit.
type appServer struct {
	srv              *httptest.Server
	tokenCalls       int32
	commitCalls      int32
	repoCalls        int32
	tokenExpiry      time.Time
	commitsETag      string
	commitsBody      string
	commitsPage2Body string
	repoBody         string
	notModified      bool // when true, /commits returns 304
	failToken        bool
	failRepo         bool
	// Pull-request commit listing. pullPath records the last path served so a
	// test can prove the endpoint is the one that was called; pullPage2Body
	// makes the first page advertise a second page through a Link header.
	pullCalls       int32
	pullPath        string
	pullAuthHeader  string
	pullPerPage     string
	pullBody        string
	pullETag        string
	pullNotModified bool
	pullPage2Body   string
	failPull        bool
	// Writes: every check-run and comment request is recorded so a test can
	// assert the method, path, headers, and body GitHub would have received.
	requests     []recordedRequest
	checkRunBody string
	commentBody  string
	failWrite    bool
}

// recordedRequest is one request the fake GitHub received.
type recordedRequest struct {
	Method     string
	Path       string
	Body       string
	AuthHeader string
}

func (a *appServer) recordRequest(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	a.requests = append(a.requests, recordedRequest{
		Method:     r.Method,
		Path:       r.URL.Path,
		Body:       string(body),
		AuthHeader: r.Header.Get("Authorization"),
	})
}

func (a *appServer) lastRequest() recordedRequest {
	if len(a.requests) == 0 {
		return recordedRequest{}
	}
	return a.requests[len(a.requests)-1]
}

func newAppServer(t *testing.T, a *appServer) {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/app/installations/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&a.tokenCalls, 1)
		if a.failToken {
			http.Error(w, `{"message":"bad jwt"}`, http.StatusUnauthorized)
			return
		}
		exp := a.tokenExpiry
		if exp.IsZero() {
			exp = time.Now().Add(time.Hour)
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"token":"ghs_installtoken","expires_at":%q}`, exp.UTC().Format(time.RFC3339))
	})

	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		// /repos/{owner}/{name}/check-runs[/{id}]        -> check run write
		// /repos/{owner}/{name}/issues/{n}/comments      -> comment create
		// /repos/{owner}/{name}/issues/comments/{id}     -> comment edit/delete
		if strings.Contains(r.URL.Path, "/check-runs") {
			a.recordRequest(r)
			if a.failWrite {
				http.Error(w, `{"message":"Validation Failed"}`, http.StatusUnprocessableEntity)
				return
			}
			if r.Method == http.MethodPatch {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusCreated)
			}
			if a.checkRunBody != "" {
				fmt.Fprint(w, a.checkRunBody)
				return
			}
			fmt.Fprint(w, `{"id":77,"html_url":"https://example.test/check/77","status":"completed","conclusion":"success"}`)
			return
		}
		if strings.Contains(r.URL.Path, "/issues/") && strings.Contains(r.URL.Path, "/comments") {
			a.recordRequest(r)
			if a.failWrite {
				http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
				return
			}
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if r.Method == http.MethodPatch {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusCreated)
			}
			if a.commentBody != "" {
				fmt.Fprint(w, a.commentBody)
				return
			}
			fmt.Fprint(w, `{"id":555,"html_url":"https://example.test/comment/555","body":"posted"}`)
			return
		}
		// /repos/{owner}/{name}                     -> repo metadata
		// /repos/{owner}/{name}/commits             -> commit list
		// /repos/{owner}/{name}/pulls/{n}/commits   -> pull request commit list
		if strings.HasSuffix(r.URL.Path, "/commits") && strings.Contains(r.URL.Path, "/pulls/") {
			atomic.AddInt32(&a.pullCalls, 1)
			a.pullPath = r.URL.Path
			a.pullAuthHeader = r.Header.Get("Authorization")
			a.pullPerPage = r.URL.Query().Get("per_page")
			if a.failPull {
				http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
				return
			}
			if a.pullNotModified && a.pullETag != "" && r.Header.Get("If-None-Match") == a.pullETag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			if a.pullETag != "" {
				w.Header().Set("ETag", a.pullETag)
			}
			if r.URL.Query().Get("page") == "2" {
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, a.pullPage2Body)
				return
			}
			if a.pullPage2Body != "" {
				w.Header().Set("Link", fmt.Sprintf(`<%s%s?page=2>; rel="next"`, a.srv.URL, r.URL.Path))
			}
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, a.pullBody)
			return
		}
		if len(r.URL.Path) > len("/commits") && r.URL.Path[len(r.URL.Path)-len("/commits"):] == "/commits" {
			atomic.AddInt32(&a.commitCalls, 1)
			if a.notModified && r.Header.Get("If-None-Match") == a.commitsETag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			if a.commitsETag != "" {
				w.Header().Set("ETag", a.commitsETag)
			}
			if a.commitsPage2Body != "" {
				w.Header().Set("Link", fmt.Sprintf(`<%s%s?page=2>; rel="next"`, a.srv.URL, r.URL.Path))
			}
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, a.commitsBody)
			return
		}
		atomic.AddInt32(&a.repoCalls, 1)
		if a.failRepo {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, a.repoBody)
	})

	a.srv = httptest.NewServer(mux)
	t.Cleanup(a.srv.Close)
}

func newTestClient(t *testing.T, baseURL string, opts ...Option) *Client {
	t.Helper()
	all := append([]Option{WithBaseURL(baseURL)}, opts...)
	c, err := NewClient(Config{AppID: "123", PrivateKeyPEM: testKeyPEM(t)}, all...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestGetRepository_ValidatesAccessAndPrivacy(t *testing.T) {
	a := &appServer{
		repoBody: `{"name":"repo","private":true,"owner":{"login":"acme"}}`,
	}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	repo, err := c.GetRepository(context.Background(), 42, "acme", "repo")
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}
	if repo.Owner != "acme" || repo.Name != "repo" || !repo.Private {
		t.Errorf("repo = %+v, want acme/repo private", repo)
	}
	if a.tokenCalls != 1 {
		t.Errorf("token minted %d times, want 1", a.tokenCalls)
	}
}

func TestGetRepository_NoAccessReturnsError(t *testing.T) {
	a := &appServer{failRepo: true}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	if _, err := c.GetRepository(context.Background(), 42, "acme", "secret"); err == nil {
		t.Fatal("expected error for inaccessible repo")
	}
}

func TestListCommits_FetchesAndNormalizes(t *testing.T) {
	a := &appServer{
		commitsETag: `"etag-v1"`,
		commitsBody: `[
			{"sha":"aaa","commit":{"message":"first","author":{"name":"Alice","email":"a@x.io","date":"2024-01-01T10:00:00Z"},"committer":{"date":"2024-01-01T10:05:00Z"}}},
			{"sha":"bbb","commit":{"message":"second","author":{"name":"Bob","email":"b@x.io","date":"2024-01-02T10:00:00Z"},"committer":{"date":"2024-01-02T10:05:00Z"}}}
		]`,
	}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	res, err := c.ListCommits(context.Background(), 42, "acme", "repo", ListCommitsOptions{PerPage: 100})
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	if res.NotModified {
		t.Fatal("unexpected NotModified")
	}
	if len(res.Commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(res.Commits))
	}
	if res.Commits[0].SHA != "aaa" || res.Commits[0].AuthorName != "Alice" || res.Commits[0].Message != "first" {
		t.Errorf("commit[0] = %+v", res.Commits[0])
	}
	if res.Commits[0].AuthoredAt.IsZero() || res.Commits[0].CommittedAt.IsZero() {
		t.Errorf("commit[0] timestamps not parsed: %+v", res.Commits[0])
	}
	if res.ETag != `"etag-v1"` {
		t.Errorf("ETag = %q, want \"etag-v1\"", res.ETag)
	}
}

func TestListCommits_ConditionalNotModified(t *testing.T) {
	a := &appServer{
		commitsETag: `"etag-v1"`,
		notModified: true,
		commitsBody: `[{"sha":"aaa","commit":{"message":"x","author":{"name":"A"}}}]`,
	}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	res, err := c.ListCommits(context.Background(), 42, "acme", "repo", ListCommitsOptions{ETag: `"etag-v1"`})
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	if !res.NotModified {
		t.Fatal("expected NotModified=true on matching ETag")
	}
	if len(res.Commits) != 0 {
		t.Errorf("expected no commits on 304, got %d", len(res.Commits))
	}
	if res.ETag != `"etag-v1"` {
		t.Errorf("ETag should be preserved on 304, got %q", res.ETag)
	}
}

func TestListPullRequestCommits_FetchesFromThePullRequestEndpoint(t *testing.T) {
	a := &appServer{
		pullBody: `[
			{"sha":"aaa","commit":{"message":"first","author":{"name":"Alice","email":"a@x.io","date":"2024-01-01T10:00:00Z"},"committer":{"date":"2024-01-01T10:05:00Z"}}}
		]`,
	}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	res, err := c.ListPullRequestCommits(context.Background(), 42, "acme", "repo", 7, ListCommitsOptions{})
	if err != nil {
		t.Fatalf("ListPullRequestCommits: %v", err)
	}
	if a.pullPath != "/repos/acme/repo/pulls/7/commits" {
		t.Errorf("called %q, want the pull request commits endpoint", a.pullPath)
	}
	if len(res.Commits) != 1 || res.Commits[0].SHA != "aaa" || res.Commits[0].AuthorName != "Alice" {
		t.Fatalf("commits = %+v, want one normalized commit aaa from Alice", res.Commits)
	}
	if !res.Complete {
		t.Error("a walk that ran out of pages must report the list as complete")
	}
	// M1: the same installation token, header, and page size the repository
	// walk uses, so a difference cannot hide in the shared implementation.
	if a.tokenCalls != 1 {
		t.Errorf("token minted %d times, want 1", a.tokenCalls)
	}
	if a.pullAuthHeader != "token ghs_installtoken" {
		t.Errorf("Authorization = %q, want the installation token header", a.pullAuthHeader)
	}
	if a.pullPerPage != strconv.Itoa(maxGitHubPerPage) {
		t.Errorf("per_page = %q, want %d so completeness is not lost to a small page", a.pullPerPage, maxGitHubPerPage)
	}
}

func TestListPullRequestCommits_Paginates(t *testing.T) {
	a := &appServer{
		pullBody:      `[{"sha":"aaa","commit":{"message":"first","author":{"name":"Alice"}}}]`,
		pullPage2Body: `[{"sha":"bbb","commit":{"message":"second","author":{"name":"Bob"}}}]`,
	}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	res, err := c.ListPullRequestCommits(context.Background(), 42, "acme", "repo", 7, ListCommitsOptions{})
	if err != nil {
		t.Fatalf("ListPullRequestCommits: %v", err)
	}
	if len(res.Commits) != 2 || res.Commits[0].SHA != "aaa" || res.Commits[1].SHA != "bbb" {
		t.Fatalf("commits = %+v, want both pages in order", res.Commits)
	}
	if a.pullCalls != 2 {
		t.Errorf("endpoint called %d times, want 2 (Link rel=next followed)", a.pullCalls)
	}
	if !res.Complete {
		t.Error("following every page must report the list as complete")
	}
}

// TestListPullRequestCommits_PageCapLeavesTheListIncomplete is the B1
// reproduction: a page cap reached while a next page is still advertised must
// never be reported as a complete list, because the matcher would then resolve
// an abbreviation against a set that is missing commits.
func TestListPullRequestCommits_PageCapLeavesTheListIncomplete(t *testing.T) {
	a := &appServer{
		pullBody:      `[{"sha":"aaa","commit":{"message":"first","author":{"name":"Alice"}}}]`,
		pullPage2Body: `[{"sha":"abc1234000000000000000000000000000000002","commit":{"message":"colliding","author":{"name":"Bob"}}}]`,
	}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	res, err := c.ListPullRequestCommits(context.Background(), 42, "acme", "repo", 7, ListCommitsOptions{MaxPages: 1})
	if err != nil {
		t.Fatalf("ListPullRequestCommits: %v", err)
	}
	if a.pullCalls != 1 {
		t.Fatalf("endpoint called %d times, want 1 for a one-page cap", a.pullCalls)
	}
	if res.Complete {
		t.Fatal("a truncated walk reported the list as complete; an omitted page could hold a colliding commit")
	}
}

// TestListPullRequestCommits_CapOf250IsIncomplete pins the endpoint's documented
// maximum: at the cap we cannot tell whether more commits exist, so the list is
// not provably complete even though no page was left to follow.
func TestListPullRequestCommits_CapOf250IsIncomplete(t *testing.T) {
	var body strings.Builder
	body.WriteString("[")
	for i := 0; i < maxPullRequestCommits; i++ {
		if i > 0 {
			body.WriteString(",")
		}
		fmt.Fprintf(&body, `{"sha":"%040x","commit":{"message":"c","author":{"name":"A"}}}`, i)
	}
	body.WriteString("]")

	a := &appServer{pullBody: body.String()}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	res, err := c.ListPullRequestCommits(context.Background(), 42, "acme", "repo", 7, ListCommitsOptions{})
	if err != nil {
		t.Fatalf("ListPullRequestCommits: %v", err)
	}
	if len(res.Commits) != maxPullRequestCommits {
		t.Fatalf("got %d commits, want %d", len(res.Commits), maxPullRequestCommits)
	}
	if res.Complete {
		t.Fatalf("a list at the %d-commit endpoint cap reported itself complete", maxPullRequestCommits)
	}
}

// TestListPullRequestCommits_ConditionalNotModifiedIsNotEmpty pins that a 304
// refers to the caller's cached set rather than to an empty pull request: no
// commits and never a complete list.
func TestListPullRequestCommits_ConditionalNotModifiedIsNotEmpty(t *testing.T) {
	a := &appServer{
		pullBody:        `[{"sha":"aaa","commit":{"message":"first","author":{"name":"Alice"}}}]`,
		pullETag:        `"pr-etag-v1"`,
		pullNotModified: true,
	}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	res, err := c.ListPullRequestCommits(context.Background(), 42, "acme", "repo", 7, ListCommitsOptions{ETag: `"pr-etag-v1"`})
	if err != nil {
		t.Fatalf("ListPullRequestCommits: %v", err)
	}
	if !res.NotModified {
		t.Fatal("expected NotModified on a matching ETag")
	}
	if len(res.Commits) != 0 {
		t.Fatalf("got %d commits on a 304, want none", len(res.Commits))
	}
	if res.Complete {
		t.Fatal("a 304 is not an empty complete list; the caller must reuse its cached set")
	}
	if res.ETag != `"pr-etag-v1"` {
		t.Errorf("ETag = %q, want the offered ETag preserved", res.ETag)
	}
}

func TestListPullRequestCommits_ErrorReportsStatus(t *testing.T) {
	a := &appServer{failPull: true}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	_, err := c.ListPullRequestCommits(context.Background(), 42, "acme", "repo", 7, ListCommitsOptions{})
	if err == nil {
		t.Fatal("expected an error when GitHub answers non-200")
	}
	if !strings.Contains(err.Error(), "status 404") {
		t.Errorf("error = %v, want it to report the status the way ListCommits does", err)
	}
}

func TestListPullRequestCommits_TokenErrorPropagates(t *testing.T) {
	a := &appServer{failToken: true}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	if _, err := c.ListPullRequestCommits(context.Background(), 42, "acme", "repo", 7, ListCommitsOptions{}); err == nil {
		t.Fatal("expected the token error to propagate")
	}
	if a.pullCalls != 0 {
		t.Errorf("endpoint called %d times after a failed token mint, want 0", a.pullCalls)
	}
}

func TestListPullRequestCommits_RejectsNonPositiveNumberBeforeCalling(t *testing.T) {
	a := &appServer{}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	if _, err := c.ListPullRequestCommits(context.Background(), 42, "acme", "repo", 0, ListCommitsOptions{}); err == nil {
		t.Fatal("expected an error for a non-positive pull request number")
	}
	if a.pullCalls != 0 || a.tokenCalls != 0 {
		t.Errorf("refused request still called GitHub: pull=%d token=%d", a.pullCalls, a.tokenCalls)
	}
}

// TestListCommits_TruncationIsReportedIncomplete covers the repository walk as
// well: its bounded default now says so instead of truncating silently.
func TestListCommits_TruncationIsReportedIncomplete(t *testing.T) {
	a := &appServer{
		commitsBody:      `[{"sha":"aaa","commit":{"message":"first","author":{"name":"Alice"}}}]`,
		commitsETag:      `"etag-v1"`,
		commitsPage2Body: `[{"sha":"bbb","commit":{"message":"second","author":{"name":"Bob"}}}]`,
	}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	res, err := c.ListCommits(context.Background(), 42, "acme", "repo", ListCommitsOptions{})
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	if res.Complete {
		t.Fatal("the default one-page repository walk must report truncation, not claim completeness")
	}
}

func TestInstallationToken_CachedUntilExpiry(t *testing.T) {
	a := &appServer{
		tokenExpiry: time.Now().Add(time.Hour),
		repoBody:    `{"name":"repo","private":false,"owner":{"login":"acme"}}`,
	}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	for i := 0; i < 3; i++ {
		if _, err := c.GetRepository(context.Background(), 7, "acme", "repo"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	// Token endpoint must have been hit exactly once across three repo calls.
	if a.tokenCalls != 1 {
		t.Errorf("token minted %d times, want 1 (should be cached)", a.tokenCalls)
	}
}

func TestInstallationToken_RefreshesAfterExpiry(t *testing.T) {
	a := &appServer{
		tokenExpiry: time.Now().Add(30 * time.Second), // within the expiry guard window after our clock jump
		repoBody:    `{"name":"repo","private":false,"owner":{"login":"acme"}}`,
	}
	newAppServer(t, a)

	// Controllable clock: starts now, then jumps past expiry on the second call.
	current := time.Now()
	c := newTestClient(t, a.srv.URL, WithClock(func() time.Time { return current }))

	if _, err := c.GetRepository(context.Background(), 7, "acme", "repo"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Advance the clock beyond the cached token's (guarded) expiry.
	current = current.Add(2 * time.Hour)
	if _, err := c.GetRepository(context.Background(), 7, "acme", "repo"); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if a.tokenCalls != 2 {
		t.Errorf("token minted %d times, want 2 (refresh after expiry)", a.tokenCalls)
	}
}

func TestInstallationToken_FailurePropagates(t *testing.T) {
	a := &appServer{failToken: true}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	if _, err := c.GetRepository(context.Background(), 7, "acme", "repo"); err == nil {
		t.Fatal("expected error when token exchange fails")
	}
}

func TestNextPageURL(t *testing.T) {
	link := `<https://api.github.com/repositories/1/commits?page=2>; rel="next", <https://api.github.com/repositories/1/commits?page=9>; rel="last"`
	got := nextPageURL(link)
	want := "https://api.github.com/repositories/1/commits?page=2"
	if got != want {
		t.Errorf("nextPageURL = %q, want %q", got, want)
	}
	if nextPageURL("") != "" {
		t.Error("nextPageURL(\"\") should be empty")
	}
	if nextPageURL(`<https://x>; rel="last"`) != "" {
		t.Error("nextPageURL with no next should be empty")
	}
}
