package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	gh "github.com/peasant-labs/village/backend/internal/github"
)

// pullReadsGitHub serves a pull request's title and head branch, which the pull
// request pages read from GitHub. It also answers what a login may do in a
// repository, and counts every such question, so a test can prove the pages
// never ask it. Anything it was not told about is a 404, which is what GitHub
// answers for a pull request or account it cannot show.
type pullReadsGitHub struct {
	srv *httptest.Server

	mu sync.Mutex
	// pulls maps "owner/name#number" (lowercased) to its title and head branch.
	pulls map[string][2]string
	// logins maps an account id to its login.
	logins map[string]string
	// permissions maps "login owner/name" (lowercased) to a permission.
	permissions map[string]string
	// failPulls makes pull request reads answer 500, and pullDelay makes them
	// answer late (or not at all, once the caller stops waiting).
	failPulls      bool
	pullDelay      time.Duration
	pullReads      int
	permissionAsks int
	// held makes pull request reads wait until the test releases them (or the
	// caller gives up), and arrivals announces each read as it reaches the fake.
	held     chan struct{}
	arrivals chan struct{}
}

func newPullReadsGitHub(t *testing.T) *pullReadsGitHub {
	t.Helper()
	fake := &pullReadsGitHub{pulls: map[string][2]string{}, logins: map[string]string{}, permissions: map[string]string{}}
	fake.srv = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.srv.Close)
	return fake
}

// client is a GitHub App client pointed at the fake.
func (f *pullReadsGitHub) client(t *testing.T) *gh.Client {
	t.Helper()
	client, err := gh.NewClient(gh.Config{AppID: "123", PrivateKeyPEM: testAppPEM(t)}, gh.WithBaseURL(f.srv.URL))
	if err != nil {
		t.Fatalf("compose the GitHub client: %v", err)
	}
	return client
}

func (f *pullReadsGitHub) setPull(repo string, number int, title, headRef string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pulls[strings.ToLower(repo)+"#"+strconv.Itoa(number)] = [2]string{title, headRef}
}

func (f *pullReadsGitHub) setReader(accountID, login, repo, permission string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logins[accountID] = login
	f.permissions[strings.ToLower(login+" "+repo)] = permission
}

func (f *pullReadsGitHub) failPullReads(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failPulls = fail
}

func (f *pullReadsGitHub) delayPullReads(delay time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pullDelay = delay
}

// holdPullReads holds every pull request read until the test ends.
func (f *pullReadsGitHub) holdPullReads(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	f.held = make(chan struct{})
	f.arrivals = make(chan struct{}, 256)
	held := f.held
	f.mu.Unlock()
	t.Cleanup(func() { close(held) })
}

// awaitPullReads waits until n held pull request reads have reached the fake.
// It is a failure bound, not a timing assumption: the reads it waits for are
// already started, and only a broken list would leave it waiting.
func (f *pullReadsGitHub) awaitPullReads(t *testing.T, n int) {
	f.mu.Lock()
	arrivals := f.arrivals
	f.mu.Unlock()
	for i := 0; i < n; i++ {
		select {
		case <-arrivals:
		case <-time.After(time.Minute):
			t.Errorf("only %d of %d pull request reads reached GitHub within a minute", i, n)
			return
		}
	}
}

func (f *pullReadsGitHub) counts() (pullReads, permissionAsks int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pullReads, f.permissionAsks
}

func (f *pullReadsGitHub) serve(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case len(parts) == 4 && parts[0] == "app" && parts[1] == "installations" && parts[3] == "access_tokens":
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"token":"ghs_pull_reads","expires_at":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	case len(parts) == 5 && parts[0] == "repos" && parts[3] == "pulls":
		f.pullReads++
		if held := f.held; held != nil {
			select {
			case f.arrivals <- struct{}{}:
			default:
			}
			f.mu.Unlock()
			select {
			case <-held:
			case <-r.Context().Done():
			}
			f.mu.Lock()
			if r.Context().Err() != nil {
				return
			}
		}
		if delay := f.pullDelay; delay > 0 {
			f.mu.Unlock()
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
			}
			f.mu.Lock()
			if r.Context().Err() != nil {
				return
			}
		}
		if f.failPulls {
			http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
			return
		}
		number, _ := strconv.Atoi(parts[4])
		detail, ok := f.pulls[strings.ToLower(parts[1]+"/"+parts[2])+"#"+parts[4]]
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		body, _ := json.Marshal(map[string]any{
			"number": number, "title": detail[0], "state": "open",
			"head": map[string]any{"ref": detail[1], "sha": "headsha", "repo": map[string]any{"id": 1, "name": parts[2], "full_name": parts[1] + "/" + parts[2], "owner": map[string]any{"login": parts[1]}}},
			"base": map[string]any{"repo": map[string]any{"id": 1, "name": parts[2], "owner": map[string]any{"login": parts[1]}}},
			"user": map[string]any{"id": 1},
		})
		_, _ = w.Write(body)
	case len(parts) == 2 && parts[0] == "user":
		login, ok := f.logins[parts[1]]
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		fmt.Fprintf(w, `{"login":%q}`, login)
	case len(parts) == 6 && parts[0] == "repos" && parts[3] == "collaborators" && parts[5] == "permission":
		f.permissionAsks++
		permission, ok := f.permissions[strings.ToLower(parts[4]+" "+parts[1]+"/"+parts[2])]
		if !ok {
			permission = "none"
		}
		fmt.Fprintf(w, `{"permission":%q}`, permission)
	default:
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}
}
