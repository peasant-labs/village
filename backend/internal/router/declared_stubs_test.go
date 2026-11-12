package router

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/config"
)

//go:embed testdata/declared_stub_routes.yaml
var declaredStubRoutesYAML []byte

type declaredStubRoute struct {
	Name      string `yaml:"name"`
	Method    string `yaml:"method"`
	Route     string `yaml:"route"`
	Path      string `yaml:"path"`
	Anonymous int    `yaml:"anonymous"`
	SignedIn  int    `yaml:"signed_in"`
}

type declaredStubRoutesFile struct {
	Routes []declaredStubRoute `yaml:"routes"`
}

// requiredDeclaredStubRoutes names the rows that must exist. It lives here so
// deleting a fixture row cannot delete the check with it; landing a real
// handler removes the row and its name together.
var requiredDeclaredStubRoutes = []string{
	"the segment directory is declared and not served yet",
	"one segment is declared and not served yet",
	"the window around an anchor is declared and not served yet",
}

func loadDeclaredStubRoutes(t *testing.T) []declaredStubRoute {
	t.Helper()
	file, err := decodeSingleYAMLDocument[declaredStubRoutesFile](declaredStubRoutesYAML)
	if err != nil {
		t.Fatalf("load the declared stub routes fixture: %v", err)
	}
	present := map[string]bool{}
	for _, r := range file.Routes {
		if r.Name == "" || present[r.Name] {
			t.Fatalf("the declared stub routes fixture has an empty or repeated name %q", r.Name)
		}
		present[r.Name] = true
		// A request for the row must be a request for its route: same path with
		// the parameters filled in, so a row cannot pin one route and probe another.
		requestPath, _, _ := strings.Cut(r.Path, "?")
		pattern, request := strings.Split(r.Route, "/"), strings.Split(requestPath, "/")
		matches := len(pattern) == len(request)
		for i := 0; matches && i < len(pattern); i++ {
			matches = pattern[i] == request[i] || strings.HasPrefix(pattern[i], "{") && request[i] != ""
		}
		if !matches {
			t.Fatalf("row %q requests %s, which is not an instance of its route %s", r.Name, r.Path, r.Route)
		}
	}
	if len(present) != len(requiredDeclaredStubRoutes) {
		t.Fatalf("the declared stub routes fixture has %d rows but %d required names; name every row in requiredDeclaredStubRoutes", len(present), len(requiredDeclaredStubRoutes))
	}
	for _, required := range requiredDeclaredStubRoutes {
		if !present[required] {
			t.Fatalf("the declared stub routes fixture omits required row %q; delete the name only when the real handler lands", required)
		}
	}
	return file.Routes
}

// TestDeclaredStubRoutes_AnswerNotImplemented drives every stub through the
// production router with no database or object store: a stub that read
// anything would fail instead of answering 501. It pins each route's status
// without and with a session.
func TestDeclaredStubRoutes_AnswerNotImplemented(t *testing.T) {
	cfg := &config.Config{JWTSecret: "declared-stub-test-only-secret", FrontendURL: "https://example.test"}
	routes := New(cfg, nil, nil, nil)
	token, err := auth.CreateToken(cfg.JWTSecret, uuid.New(), "declared-stub")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range loadDeclaredStubRoutes(t) {
		t.Run(c.Name, func(t *testing.T) {
			for _, caller := range []struct {
				name  string
				token string
				want  int
			}{{"anonymous", "", c.Anonymous}, {"signed in", token, c.SignedIn}} {
				r := httptest.NewRequest(c.Method, c.Path, nil)
				if caller.token != "" {
					r.Header.Set("Authorization", "Bearer "+caller.token)
				}
				w := httptest.NewRecorder()
				routes.ServeHTTP(w, r)
				if w.Code != caller.want {
					t.Fatalf("%s %s %s answered %d (%s), want %d", caller.name, c.Method, c.Path, w.Code, w.Body.String(), caller.want)
				}
				if w.Code != http.StatusNotImplemented {
					continue
				}
				var response struct {
					Error string `json:"error"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Error == "" {
					t.Fatalf("%s %s %s answered 501 without the error body (%s): %v", caller.name, c.Method, c.Path, w.Body.String(), err)
				}
			}
		})
	}
}

// TestDeclaredStubRoutes_AreMountedAndDeclared proves each stub is a route the
// served contract declares and the server mounts. A stub for a route the
// contract does not declare belongs in the undocumented-routes manifest
// instead, or nowhere.
func TestDeclaredStubRoutes_AreMountedAndDeclared(t *testing.T) {
	mounted := map[string]bool{}
	for _, r := range mountedAPIRoutes(t) {
		mounted[routeKey(r.Method, r.Path)] = true
	}
	declared := map[string]bool{}
	for _, r := range declaredContractRoutes(t) {
		declared[routeKey(r.Method, r.Path)] = true
	}
	for _, c := range loadDeclaredStubRoutes(t) {
		key := routeKey(c.Method, c.Route)
		if !mounted[key] {
			t.Errorf("%s %s is pinned as a stub but the server does not mount it; mount it or delete the row", c.Method, c.Route)
		}
		if !declared[key] {
			t.Errorf("%s %s is pinned as a stub but the served contract does not declare it; pin a schema version that declares it, or delete the row and the route", c.Method, c.Route)
		}
	}
}
