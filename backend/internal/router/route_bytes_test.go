package router

import (
	_ "embed"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"

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
