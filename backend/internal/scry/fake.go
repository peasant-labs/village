package scry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/peasant-labs/schema"
)

// FakeSecret is the bearer secret a Fake requires.
const FakeSecret = "fake-scry-secret-0123456789abcdef0123456789"

// Fake is an in-process scry for handler tests. Production code never imports
// it.
type Fake struct {
	server *httptest.Server

	mu       sync.Mutex
	version  string
	answers  map[string]schema.ScryCheckResponse
	failWith int
	// failCheckWith fails only check requests, so health still answers.
	failCheckWith int
	requests      []schema.ScryCheckRequest
	// unanswered counts check requests that matched no Answer and so got the
	// default unflagged reply.
	unanswered int
}

// HashOf is the key Answer uses: the hex SHA-256 of the JSON encoding of the
// session detail the fake receives.
func HashOf(detail schema.SessionDetailPayload) string {
	raw, _ := json.Marshal(detail)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// NewFake starts a fake reporting the given scry version. It stops with the
// test.
func NewFake(t testing.TB, version string) *Fake {
	f := &Fake{version: version, answers: map[string]schema.ScryCheckResponse{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", f.health)
	mux.HandleFunc("/v1/check", f.check)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

// URL is the fake's base URL.
func (f *Fake) URL() string { return f.server.URL }

// Settings are the settings a client needs to reach the fake.
func (f *Fake) Settings() Settings { return Settings{URL: f.server.URL, SharedSecret: FakeSecret} }

// Answer sets the response for a transcript whose session detail hashes to
// hash. The key is HashOf(SessionDetail), NOT the stored content_hash: a key
// from the wrong source never matches and the request gets the default
// unflagged reply. A test that answers by key should assert Unanswered() is
// zero so such a miss cannot pass silently.
func (f *Fake) Answer(hash string, resp schema.ScryCheckResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[hash] = resp
}

// Fail makes every call answer with status; zero restores normal answers.
func (f *Fake) Fail(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failWith = status
}

// FailCheck makes every check request answer with status while health keeps
// answering; zero restores normal answers.
func (f *Fake) FailCheck(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failCheckWith = status
}

// Unanswered is the number of check requests that matched no Answer and got
// the default reply. A test that expects its answers to be used asserts zero.
func (f *Fake) Unanswered() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.unanswered
}

// SetVersion changes the scry version the fake reports.
func (f *Fake) SetVersion(v string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.version = v
}

// Requests returns the check requests the fake answered, oldest first. It
// excludes every check that failed before an answer: one refused for its
// secret, one forced to fail by Fail or FailCheck, and one that was not a
// valid request. A test that needs to count refused or failed calls counts
// them in its own checker.
func (f *Fake) Requests() []schema.ScryCheckRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]schema.ScryCheckRequest(nil), f.requests...)
}

// gate answers the bearer check and any forced failure for the check route;
// true means handled.
func (f *Fake) gate(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != "Bearer "+FakeSecret {
		w.WriteHeader(http.StatusUnauthorized)
		return true
	}
	f.mu.Lock()
	status := f.failWith
	f.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		return true
	}
	return false
}

// health needs no secret, like the real service's /healthz; only a forced
// failure stops it.
func (f *Fake) health(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	status := f.failWith
	f.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	f.mu.Lock()
	version := f.version
	f.mu.Unlock()
	_ = json.NewEncoder(w).Encode(schema.ScryHealthResponse{ContractVersion: schema.ScryContractVersion, ScryVersion: version})
}

func (f *Fake) check(w http.ResponseWriter, r *http.Request) {
	if f.gate(w, r) {
		return
	}
	f.mu.Lock()
	status := f.failCheckWith
	f.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	raw, err := io.ReadAll(r.Body)
	var req schema.ScryCheckRequest
	if err != nil || json.Unmarshal(raw, &req) != nil || req.Validate() != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.requests = append(f.requests, req)
	resp, ok := f.answers[HashOf(req.SessionDetail)]
	if !ok {
		f.unanswered++
	}
	version := f.version
	f.mu.Unlock()
	if !ok {
		resp = schema.ScryCheckResponse{ScryVersion: version, Verdict: schema.ScryVerdictChecked, FlaggedTurns: []int{}}
	}
	_ = json.NewEncoder(w).Encode(resp)
}
