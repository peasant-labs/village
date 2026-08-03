package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	srv         *httptest.Server
	tokenCalls  int32
	commitCalls int32
	repoCalls   int32
	tokenExpiry time.Time
	commitsETag string
	commitsBody string
	repoBody    string
	notModified bool // when true, /commits returns 304
	failToken   bool
	failRepo    bool
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
		// /repos/{owner}/{name}            -> repo metadata
		// /repos/{owner}/{name}/commits    -> commit list
		if len(r.URL.Path) > len("/commits") && r.URL.Path[len(r.URL.Path)-len("/commits"):] == "/commits" {
			atomic.AddInt32(&a.commitCalls, 1)
			if a.notModified && r.Header.Get("If-None-Match") == a.commitsETag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			if a.commitsETag != "" {
				w.Header().Set("ETag", a.commitsETag)
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
