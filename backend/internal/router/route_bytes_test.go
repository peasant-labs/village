package router

import (
	_ "embed"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/peasant-labs/village/backend/internal/config"
)

//go:embed testdata/route_bytes.yaml
var routeBytesYAML []byte

type routeByteCase struct {
	Name    string            `yaml:"name"`
	Method  string            `yaml:"method"`
	Path    string            `yaml:"path"`
	Headers map[string]string `yaml:"headers"`
	Status  int               `yaml:"status"`
	Want    map[string]string `yaml:"want_headers"`
	Body    string            `yaml:"body"`
	Repeat  int               `yaml:"repeat"`
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
}

func TestRouteBytes(t *testing.T) {
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
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			routes := New(&config.Config{JWTSecret: "route-bytes-test-only-secret", FrontendURL: "https://example.test"}, nil, nil, nil)
			req := httptest.NewRequest(c.Method, c.Path, nil)
			for name, value := range c.Headers {
				req.Header.Set(name, value)
			}
			var w *httptest.ResponseRecorder
			for i := 0; i <= c.Repeat; i++ {
				w = httptest.NewRecorder()
				routes.ServeHTTP(w, req.Clone(req.Context()))
			}
			if w.Code != c.Status || w.Body.String() != c.Body {
				t.Fatalf("got status %d body %q, want status %d body %q", w.Code, w.Body.String(), c.Status, c.Body)
			}
			for name, value := range c.Want {
				if got := strings.Join(w.Header().Values(name), ", "); got != value {
					t.Errorf("header %s = %q, want %q", name, got, value)
				}
			}
		})
	}
}
