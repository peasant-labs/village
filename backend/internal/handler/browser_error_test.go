package handler

import (
	_ "embed"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type browserErrorCase struct {
	Name      string   `yaml:"name"`
	Route     string   `yaml:"route"`
	Query     string   `yaml:"query"`
	Accept    string   `yaml:"accept"`
	Status    int      `yaml:"status"`
	HTML      []string `yaml:"html"`
	JSONError string   `yaml:"jsonError"`
}

//go:embed testdata/browser-errors.yaml
var browserErrorsFixture []byte

// browserErrorRequiredCases keeps each route's tab and API answers covered.
var browserErrorRequiredCases = []string{
	"github-callback-lost-state-tab",
	"github-callback-lost-state-api",
	"github-callback-lost-state-fetch",
	"cli-login-incomplete-tab",
	"cli-login-incomplete-api",
	"gitlab-callback-unconfigured-tab",
	"install-callback-unconfigured-tab",
	"install-callback-unconfigured-api",
}

// TestBrowserRouteErrors drives each failing browser route through its
// production handler and checks both answers: the same status, a styled page
// for a tab and the unchanged JSON error for any other client.
func TestBrowserRouteErrors(t *testing.T) {
	cases, err := decodeFixtureRows[browserErrorCase](browserErrorsFixture)
	if err != nil {
		t.Fatalf("load browser-errors fixture: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		seen[c.Name] = true
	}
	for _, name := range browserErrorRequiredCases {
		if !seen[name] {
			t.Fatalf("browser-errors fixture lost required case %q", name)
		}
	}

	h := newTestHandler(&mockQuerier{}, nil)
	routes := map[string]struct {
		path   string
		handle http.HandlerFunc
	}{
		"github-callback":         {"/api/v1/auth/github/callback", h.GitHubCallback},
		"cli-login":               {"/api/v1/auth/cli/login", h.CLILogin},
		"gitlab-callback":         {"/api/v1/auth/gitlab/callback", h.GitLabCallback},
		"github-install-callback": {"/api/v1/integrations/github/callback", h.GitHubInstallCallback},
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			route, ok := routes[c.Route]
			if !ok {
				t.Fatalf("fixture names unknown route %q", c.Route)
			}
			target := route.path
			if c.Query != "" {
				target += "?" + c.Query
			}
			req := httptest.NewRequest(http.MethodGet, target, nil)
			if c.Accept != "" {
				req.Header.Set("Accept", c.Accept)
			}
			req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "the-cookie"})
			rec := httptest.NewRecorder()
			route.handle(rec, req)

			if rec.Code != c.Status {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, c.Status, rec.Body.String())
			}
			body := rec.Body.String()
			if c.JSONError != "" {
				if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
					t.Fatalf("Content-Type = %q, want application/json", ct)
				}
				if got := decodeError(t, rec.Body.Bytes()); got != c.JSONError {
					t.Fatalf("error = %q, want %q", got, c.JSONError)
				}
				return
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Fatalf("Content-Type = %q, want text/html", ct)
			}
			if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
				t.Fatalf("Content-Security-Policy = %q, want a default-src 'none' policy", csp)
			}
			if strings.Contains(body, `"error"`) {
				t.Fatalf("page carries the JSON error:\n%s", body)
			}
			for _, fragment := range c.HTML {
				if !strings.Contains(body, fragment) {
					t.Errorf("page is missing %q:\n%s", fragment, body)
				}
			}
		})
	}
}
