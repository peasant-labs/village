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

	"github.com/peasant-labs/schema"
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
}

// fakeProviders answers the outbound requests the OAuth callbacks and the
// GitHub App client make, so no test reaches a real provider. A token request
// without a code is refused, as the providers refuse it.
type fakeProviders struct {
	githubUser    func() (int64, string)
	gitlabUser    func() (int64, string)
	installations func(id int64) (string, int64, bool)
}

func (f fakeProviders) RoundTrip(r *http.Request) (*http.Response, error) {
	answer := func(status int, body string) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}
	switch r.URL.Host + r.URL.Path {
	case "github.com/login/oauth/access_token", "gitlab.com/oauth/token":
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		if form.Get("code") == "" {
			return answer(http.StatusBadRequest, `{"error":"invalid_grant"}`)
		}
		return answer(http.StatusOK, `{"access_token":"disposable","token_type":"bearer"}`)
	case "api.github.com/user":
		id, login := f.githubUser()
		return answer(http.StatusOK, fmt.Sprintf(`{"id":%d,"login":%q}`, id, login))
	case "api.github.com/user/orgs":
		return answer(http.StatusOK, `[]`)
	case "gitlab.com/api/v4/user":
		id, login := f.gitlabUser()
		return answer(http.StatusOK, fmt.Sprintf(`{"id":%d,"username":%q,"name":"Golden Lab","avatar_url":"https://example.test/lab.png"}`, id, login))
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
	"health-no-pool", "logout", "empty-organization-search",
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
			req := httptest.NewRequest(c.Method, c.Path, nil)
			for name, value := range c.Headers {
				req.Header.Set(name, value)
			}
			for name, value := range c.Cookies {
				req.AddCookie(&http.Cookie{Name: name, Value: value})
			}
			if c.Providers {
				req = req.WithContext(context.WithValue(req.Context(), oauth2.HTTPClient, &http.Client{Transport: fakeProviders{}}))
			}
			var w *httptest.ResponseRecorder
			for i := 0; i <= c.Repeat; i++ {
				w = httptest.NewRecorder()
				routes.ServeHTTP(w, req.Clone(req.Context()))
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
			for name, value := range c.Want {
				value = strings.ReplaceAll(value, "{{state}}", state)
				if got := strings.Join(w.Header().Values(name), ", "); got != value {
					t.Errorf("header %s = %q, want %q", name, got, value)
				}
			}
		})
	}
}

// routeByteCaller names the session a route-byte case sends: empty for the
// main account, routeByteCallerAnonymous for none, or another seeded account.
type routeByteCaller string

const routeByteCallerAnonymous routeByteCaller = "anonymous"
