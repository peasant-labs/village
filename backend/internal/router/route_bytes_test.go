package router

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/google/uuid"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/config"
)

//go:embed testdata/route_bytes.yaml
var routeBytesYAML []byte

type routeByteCase struct {
	Name                 string            `yaml:"name"`
	Method               string            `yaml:"method"`
	Path                 string            `yaml:"path"`
	Headers              map[string]string `yaml:"headers"`
	Status               int               `yaml:"status"`
	Want                 map[string]string `yaml:"want_headers"`
	Body                 string            `yaml:"body"`
	Repeat               int               `yaml:"repeat"`
	Database             bool              `yaml:"database"`
	RequestBody          string            `yaml:"request_body"`
	OnlyOutcomeReason    string            `yaml:"only_outcome_reason"`
	BodyFromPinnedSchema bool              `yaml:"body_from_pinned_schema"`
	Configured           bool              `yaml:"configured"`
	OAuthState           bool              `yaml:"oauth_state"`
	// Cookies are sent with the request; their values are templates.
	Cookies map[string]string `yaml:"cookies"`
	// Providers routes the request's outbound OAuth traffic to
	// fakeProviders instead of the real sign-in providers.
	Providers bool `yaml:"providers"`
	// As names the session the request carries: empty for the main account,
	// "anonymous" for none, or another seeded account.
	As routeByteCaller `yaml:"as"`
	// Capture names the values read back after the request, from the
	// response or the database, before the body is compared.
	Capture []string `yaml:"capture"`
	// SharedRows marks a global list that other tests may add rows to: the
	// body must be a JSON array, framed exactly as the encoder writes it,
	// holding one element whose bytes equal the golden.
	SharedRows bool `yaml:"shared_rows"`
	// HeaderPatterns are regular expressions a header's value must match,
	// for values the server derives from the clock, such as the limiter's
	// reset time.
	HeaderPatterns map[string]string `yaml:"header_patterns"`
	// AbsentHeaders names headers the response must not carry at all.
	AbsentHeaders []string `yaml:"absent_headers"`
	// ForbidBodySubstrings are templates the body must not contain, such as
	// a secret the route must never echo.
	ForbidBodySubstrings []string `yaml:"forbid_body_substrings"`
	// SignedIn sends a valid session token on a no-database case, whose
	// account the server cannot confirm.
	SignedIn bool `yaml:"signed_in"`
	// FailingStorage sends a database case to a configured router whose
	// object storage refuses every deletion.
	FailingStorage bool `yaml:"failing_storage"`
}

// fakeProviders answers the outbound requests the OAuth callbacks and the
// GitHub App client make, so no test reaches a real provider. A token request
// without a code is refused, as the providers refuse it. users reports the
// identity each provider signs in.
type fakeProviders struct {
	users         func(provider string) (int64, string)
	installations func(id int64) (string, int64, bool)
}

// fakeProviderTokenURLs are the token endpoints of the five providers.
var fakeProviderTokenURLs = map[string]bool{
	"github.com/login/oauth/access_token":   true,
	"gitlab.com/oauth/token":                true,
	"huggingface.co/oauth/token":            true,
	"codeberg.org/login/oauth/access_token": true,
	"meta.sr.ht/oauth2/access-token":        true,
}

func (f fakeProviders) RoundTrip(r *http.Request) (*http.Response, error) {
	answer := func(status int, body string) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}
	endpoint := r.URL.Host + r.URL.Path
	if fakeProviderTokenURLs[endpoint] {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		if form.Get("code") == "" {
			return answer(http.StatusBadRequest, `{"error":"invalid_grant"}`)
		}
		return answer(http.StatusOK, `{"access_token":"disposable","token_type":"bearer"}`)
	}
	switch endpoint {
	case "api.github.com/user":
		id, login := f.users("github")
		return answer(http.StatusOK, fmt.Sprintf(`{"id":%d,"login":%q}`, id, login))
	case "api.github.com/user/orgs":
		return answer(http.StatusOK, `[]`)
	case "gitlab.com/api/v4/user":
		id, login := f.users("gitlab")
		return answer(http.StatusOK, fmt.Sprintf(`{"id":%d,"username":%q,"name":"Golden Lab","avatar_url":"https://example.test/lab.png"}`, id, login))
	case "huggingface.co/oauth/userinfo", "codeberg.org/login/oauth/userinfo":
		id, login := f.users(map[string]string{"huggingface.co": "huggingface", "codeberg.org": "codeberg"}[r.URL.Host])
		return answer(http.StatusOK, fmt.Sprintf(`{"sub":"%d","preferred_username":%q,"name":"Golden Hub","picture":"https://example.test/hub.png"}`, id, login))
	case "meta.sr.ht/query":
		id, login := f.users("sourcehut")
		return answer(http.StatusOK, fmt.Sprintf(`{"data":{"me":{"id":%d,"username":%q,"canonicalName":"~%s","url":"https://sr.ht/~%s"}}}`, id, login, login, login))
	}
	if id, ok := strings.CutPrefix(r.URL.Path, "/app/installations/"); ok && r.URL.Host == "api.github.com" && f.installations != nil {
		n, _ := strconv.ParseInt(id, 10, 64)
		if login, account, found := f.installations(n); found {
			return answer(http.StatusOK, fmt.Sprintf(`{"id":%d,"account":{"login":%q,"id":%d,"type":"User"}}`, n, login, account))
		}
		return answer(http.StatusNotFound, `{"message":"Not Found"}`)
	}
	return nil, fmt.Errorf("no fake provider answers %s %s", r.Method, r.URL)
}

// assertRouteHeaders compares the pinned headers, after replace fills their
// templates, and checks the headers that must be absent.
func assertRouteHeaders(t *testing.T, c routeByteCase, got http.Header, replace func(string) string) {
	t.Helper()
	for name, value := range c.Want {
		if have := strings.Join(got.Values(name), ", "); have != replace(value) {
			t.Errorf("header %s = %q, want %q", name, have, replace(value))
		}
	}
	for name, pattern := range c.HeaderPatterns {
		if have := strings.Join(got.Values(name), ", "); !regexp.MustCompile(pattern).MatchString(have) {
			t.Errorf("header %s = %q, want a match for %q", name, have, pattern)
		}
	}
	for _, name := range c.AbsentHeaders {
		if values := got.Values(name); len(values) != 0 {
			t.Errorf("header %s = %q, want it absent", name, values)
		}
	}
}

// assertRouteBodyOmits checks the substrings a body must never contain.
func assertRouteBodyOmits(t *testing.T, c routeByteCase, body string, replace func(string) string) {
	t.Helper()
	for _, forbidden := range c.ForbidBodySubstrings {
		if f := replace(forbidden); f == "" || strings.Contains(body, f) {
			t.Errorf("body %q contains forbidden %q (or the template is empty)", body, f)
		}
	}
}

// assertRouteBody compares a response body with its golden. A shared-rows
// golden is one element of a JSON array other tests may add rows to.
func assertRouteBody(t *testing.T, c routeByteCase, got, want string) {
	t.Helper()
	if !c.SharedRows {
		if got != want {
			t.Fatalf("body = %q, want %q", got, want)
		}
		return
	}
	var rows []json.RawMessage
	if err := json.Unmarshal([]byte(got), &rows); err != nil {
		t.Fatalf("body %q is not a JSON array: %v", got, err)
	}
	parts := make([]string, 0, len(rows))
	found := false
	for _, row := range rows {
		parts = append(parts, string(row))
		found = found || string(row)+"\n" == want
	}
	if framed := "[" + strings.Join(parts, ",") + "]\n"; framed != got {
		t.Fatalf("body %q is not framed as the encoder writes an array (%q)", got, framed)
	}
	if !found {
		t.Fatalf("body %q holds no element equal to %q", got, want)
	}
}

var requiredRouteByteCases = []string{
	"health-no-pool", "logout", "empty-organization-search", "unsubscribe-invalid", "unsubscribe-rate-limited",
	"delete-account-anonymous", "update-discoverability-anonymous",
	"set-username-anonymous", "create-api-key-anonymous", "list-api-keys-anonymous",
	"revoke-api-key-anonymous", "organization-visibility-anonymous",
	"delete-transcript-anonymous", "create-attestation-anonymous",
	"delete-attestation-anonymous", "create-annotation-anonymous",
	"batch-publish-anonymous", "set-project-name-anonymous", "clear-project-name-anonymous",
	"github-login-sixth-request",
	"create-api-key", "list-api-keys", "peasant-revoke-api-key",
	"batch-publish-signed-in", "organization-search-null-avatar", "organization-search-avatar",
	"openapi-document",
	"gitlab-entry-unconfigured-json",
	"gitlab-entry-unconfigured-html",
	"gitlab-callback-unconfigured-json",
	"gitlab-callback-unconfigured-html",
	"huggingface-entry-unconfigured-json",
	"huggingface-entry-unconfigured-html",
	"huggingface-callback-unconfigured-json",
	"huggingface-callback-unconfigured-html",
	"codeberg-entry-unconfigured-json",
	"codeberg-entry-unconfigured-html",
	"codeberg-callback-unconfigured-json",
	"codeberg-callback-unconfigured-html",
	"sourcehut-entry-unconfigured-json",
	"sourcehut-entry-unconfigured-html",
	"sourcehut-callback-unconfigured-json",
	"sourcehut-callback-unconfigured-html",

	"github-entry", "gitlab-entry", "huggingface-entry", "codeberg-entry", "sourcehut-entry",
	"github-callback-state-json", "gitlab-callback-state-json", "huggingface-callback-state-json",
	"codeberg-callback-state-json", "sourcehut-callback-state-json",
	"github-callback-state-html", "gitlab-callback-state-html", "huggingface-callback-state-html",
	"codeberg-callback-state-html", "sourcehut-callback-state-html",
	"list-attestations-invalid-id", "list-annotations-invalid-id", "list-commits-invalid-id",
	"github-install-anonymous", "github-install-callback-anonymous",
	"github-install-unconfigured", "github-install-callback-unconfigured",
	"organization-visibility-updated", "organization-visibility-invalid-json",
	"delete-missing-attestation", "delete-attestation-invalid-id",
	"public-profile-missing", "public-organizations-missing",

	"gitlab-callback-missing-code-json", "gitlab-callback-missing-code-html",
	"github-callback-web", "github-callback-cli", "gitlab-callback-web",
	"github-callback-missing-code-json", "github-callback-missing-code-html",
	"github-install", "github-install-missing-group-json", "github-install-missing-group-html",
	"github-install-not-owner", "github-install-callback-bound", "github-install-callback-unbound",
	"github-install-callback-invalid-id-json", "github-install-callback-invalid-id-html",
	"github-install-callback-unreadable",
	"update-discoverability", "update-discoverability-missing",
	"set-username", "set-username-taken", "set-username-invalid",
	"list-tags", "popular-tags", "public-profile", "public-organizations",
	"project-page", "project-page-no-visible-transcripts", "project-page-missing", "project-page-invalid-hash",
	"set-project-name", "set-project-name-unowned", "set-project-name-empty",
	"clear-project-name", "clear-project-name-absent",
	"list-attestations-empty", "create-attestation", "create-attestation-duplicate",
	"create-attestation-not-member", "create-attestation-invalid-type", "list-attestations",
	"list-annotations-empty", "create-annotation", "create-annotation-missing-entry", "list-annotations",
	"list-commits", "list-commits-hidden",
	"delete-transcript-without-blob-store", "delete-transcript", "delete-transcript-missing",
	"delete-account",

	"cli-login", "cli-exchange", "cli-exchange-used-code", "cli-exchange-unknown-code",
	"huggingface-callback-web", "codeberg-callback-web", "sourcehut-callback-web",
	"github-install-unconfigured-html", "github-install-callback-unconfigured-html",
	"revoke-api-key-invalid-id", "clear-project-name-invalid-hash", "list-attestations-hidden",
	"list-annotations-hidden", "create-attestation-hidden", "create-annotation-hidden",
	"create-attestation-invalid-id", "create-annotation-invalid-id", "delete-transcript-invalid-id",
	"delete-transcript-not-owner", "delete-transcript-storage-failure", "cli-login-missing-port-json", "cli-login-missing-port-html",
	"cli-login-missing-state-json", "cli-login-missing-state-html", "cli-login-invalid-port-json",
	"cli-login-invalid-port-html", "cli-login-port-below-range-json",
	"cli-login-port-below-range-html", "cli-login-port-above-range-json",
	"cli-login-port-above-range-html", "cli-login-sixth-request", "cli-exchange-invalid-json",
	"cli-exchange-missing-code", "cli-exchange-missing-state", "cli-exchange-sixth-request",
	"gitlab-login-sixth-request", "huggingface-login-sixth-request", "codeberg-login-sixth-request",
	"sourcehut-login-sixth-request", "huggingface-callback-missing-code-json",
	"huggingface-callback-missing-code-html", "codeberg-callback-missing-code-json",
	"codeberg-callback-missing-code-html", "sourcehut-callback-missing-code-json",
	"sourcehut-callback-missing-code-html", "list-api-keys-session-unconfirmed",
	"create-api-key-session-unconfirmed", "revoke-api-key-session-unconfirmed",
	"delete-account-session-unconfirmed", "update-discoverability-session-unconfirmed",
	"set-username-session-unconfirmed", "organization-visibility-session-unconfirmed",
	"github-install-session-unconfirmed", "github-install-callback-session-unconfirmed",
	"batch-publish-session-unconfirmed", "delete-transcript-session-unconfirmed",
	"create-annotation-session-unconfirmed", "create-attestation-session-unconfirmed",
	"delete-attestation-session-unconfirmed", "set-project-name-session-unconfirmed",
	"clear-project-name-session-unconfirmed",
}

func routeByteCases(t *testing.T) []routeByteCase {
	t.Helper()
	cases, err := decodeSingleYAMLDocument[[]routeByteCase](routeBytesYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := make(map[string]bool, len(requiredRouteByteCases))
	for _, name := range requiredRouteByteCases {
		if required[name] {
			t.Fatalf("duplicate required name %q", name)
		}
		required[name] = true
	}
	seen := make(map[string]bool, len(cases))
	for _, c := range cases {
		if !required[c.Name] || seen[c.Name] {
			t.Fatalf("unexpected or duplicate case %q", c.Name)
		}
		seen[c.Name] = true
	}
	if !reflect.DeepEqual(required, seen) {
		t.Fatalf("fixture names differ from required names: required %v, got %v", required, seen)
	}
	return cases
}

func TestRouteBytes(t *testing.T) {
	cases := routeByteCases(t)
	for _, c := range cases {
		if c.Database {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			cfg := &config.Config{JWTSecret: "route-bytes-test-only-secret", FrontendURL: "https://example.test"}
			if c.Configured {
				cfg.BaseURL = "https://api.example.test"
				cfg.GitHubClientID, cfg.GitHubClientSecret = "client", "secret"
				cfg.GitLabClientID, cfg.GitLabClientSecret = "client", "secret"
				cfg.HuggingFaceClientID, cfg.HuggingFaceClientSecret = "client", "secret"
				cfg.CodebergClientID, cfg.CodebergClientSecret = "client", "secret"
				cfg.SourceHutClientID, cfg.SourceHutClientSecret = "client", "secret"
			}
			routes := New(cfg, nil, nil, nil)
			session := ""
			if c.SignedIn {
				token, err := auth.CreateToken(cfg.JWTSecret, uuid.New(), "golden-unconfirmed")
				if err != nil {
					t.Fatal(err)
				}
				session = token
			}
			var w *httptest.ResponseRecorder
			for i := 0; i <= c.Repeat; i++ {
				req := httptest.NewRequest(c.Method, c.Path, strings.NewReader(c.RequestBody))
				for name, value := range c.Headers {
					req.Header.Set(name, value)
				}
				if session != "" {
					req.Header.Set("Authorization", "Bearer "+session)
				}
				for name, value := range c.Cookies {
					req.AddCookie(&http.Cookie{Name: name, Value: value})
				}
				if c.Providers {
					req = req.WithContext(context.WithValue(req.Context(), oauth2.HTTPClient, &http.Client{Transport: fakeProviders{}}))
				}
				w = httptest.NewRecorder()
				routes.ServeHTTP(w, req)
			}
			body := c.Body
			if c.BodyFromPinnedSchema {
				body = string(schema.VillageAPISpecJSON())
			}
			state := ""
			if c.OAuthState {
				match := regexp.MustCompile(`^oauth_state=([0-9a-f]{32});`).FindStringSubmatch(w.Header().Get("Set-Cookie"))
				if len(match) != 2 {
					t.Fatalf("invalid state cookie %q", w.Header().Get("Set-Cookie"))
				}
				state = match[1]
				body = strings.ReplaceAll(body, "{{state}}", state)
			}
			if w.Code != c.Status || w.Body.String() != body {
				t.Fatalf("got status %d body %q, want status %d body %q", w.Code, w.Body.String(), c.Status, body)
			}
			replace := func(value string) string { return strings.ReplaceAll(value, "{{state}}", state) }
			assertRouteHeaders(t, c, w.Header(), replace)
			assertRouteBodyOmits(t, c, w.Body.String(), replace)
			validateSelectedContractResponse(t, c, w, c.Path)
		})
	}
}

// routeByteCaller names the session a route-byte case sends: empty for the
// main account, routeByteCallerAnonymous for none, or another seeded account.
type routeByteCaller string

const routeByteCallerAnonymous routeByteCaller = "anonymous"

//go:embed testdata/route_bytes_deferred.yaml
var routeBytesDeferredYAML []byte

// deferredRouteStatus is a declared status no route-byte golden pins.
type deferredRouteStatus struct {
	Name   string `yaml:"name"`
	Method string `yaml:"method"`
	Route  string `yaml:"route"`
	Status int    `yaml:"status"`
	Reason string `yaml:"reason"`
}

// requiredDeferredRouteStatuses is the exact set of deferred status names.
var requiredDeferredRouteStatuses = []string{
	"list-api-keys-500", "create-api-key-500", "revoke-api-key-500", "cli-exchange-500",
	"cli-login-500", "delete-account-500", "update-discoverability-500", "set-username-500",
	"organization-visibility-500", "github-install-callback-500", "github-install-500",
	"organization-search-500", "list-tags-500", "popular-tags-500", "list-annotations-500",
	"create-annotation-500", "list-attestations-500", "create-attestation-500",
	"delete-attestation-500", "list-commits-500", "set-project-name-500", "clear-project-name-500",
	"public-organizations-500", "project-page-500",
}

// TestRouteBytesDeferredStatuses keeps the deferred list honest: every row
// names a mounted route and gives a reason, and no golden already pins it.
func TestRouteBytesDeferredStatuses(t *testing.T) {
	file, err := decodeSingleYAMLDocument[struct {
		DeferredStatuses []deferredRouteStatus `yaml:"deferred_statuses"`
	}](routeBytesDeferredYAML)
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	for _, d := range file.DeferredStatuses {
		key := d.Name
		if present[key] {
			t.Fatalf("deferred status %q is listed twice", key)
		}
		present[key] = true
		if strings.TrimSpace(d.Reason) == "" {
			t.Errorf("deferred status %q gives no reason it cannot be reached", key)
		}
	}
	required := map[string]bool{}
	for _, key := range requiredDeferredRouteStatuses {
		required[key] = true
	}
	if !reflect.DeepEqual(required, present) {
		t.Fatalf("deferred statuses differ from the required set: required %v, got %v", required, present)
	}

	mounted := map[route]bool{}
	for _, r := range mountedRoutes(t) {
		mounted[r] = true
	}
	cases := routeByteCases(t)
	placeholder := regexp.MustCompile(`\{\{[^}]+\}\}`)
	for _, d := range file.DeferredStatuses {
		if !mounted[route{Method: d.Method, Path: d.Route}] {
			t.Errorf("deferred status %s %s %d names a route the router does not mount", d.Method, d.Route, d.Status)
		}
		pattern := regexp.MustCompile("^" + regexp.MustCompile(`\\\{[^/]+\\\}`).ReplaceAllString(regexp.QuoteMeta(d.Route), `[^/]+`) + "$")
		for _, c := range cases {
			path, _, _ := strings.Cut(placeholder.ReplaceAllString(c.Path, "x"), "?")
			if c.Method == d.Method && c.Status == d.Status && pattern.MatchString(path) {
				t.Errorf("deferred status %s %s %d is pinned by case %q; remove the row", d.Method, d.Route, d.Status, c.Name)
			}
		}
	}
}
