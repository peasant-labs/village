package router

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
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
	"batch publish",
	"list-contribution-projects",
	"list-contribution-projects-deprecated",
	"batch-share-selection",
	"batch-share-selection-deprecated",
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
// without and with a session. With no database the session's account cannot be
// confirmed, so an optional-auth stub serves the signed-in caller anonymously
// and an auth-required stub would answer 503 instead of reaching the stub.
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

// declaredStubFindings checks every pinned stub row against its auth-status
// invariant, its route pattern, the mounted router, and the served contract.
// Results are sorted so negative fixtures can assert them exactly.
func declaredStubFindings(routes []declaredStubRoute, mounted, declared []route) []string {
	mountedSet := map[string]bool{}
	for _, r := range mounted {
		mountedSet[routeKey(r.Method, r.Path)] = true
	}
	declaredSet := map[string]bool{}
	for _, r := range declared {
		declaredSet[routeKey(r.Method, r.Path)] = true
	}
	var findings []string
	for _, r := range routes {
		if r.Anonymous != http.StatusUnauthorized && r.Anonymous != http.StatusNotImplemented {
			findings = append(findings, fmt.Sprintf("row %q pins anonymous %d; a declared stub must answer 401 (auth-required) or 501 (optional-auth)", r.Name, r.Anonymous))
		}
		// With no database a session cannot be confirmed: an optional-auth stub
		// serves the signed-in caller anonymously, and an auth-required stub
		// refuses it as unconfirmed.
		wantSignedIn := r.Anonymous
		if r.Anonymous == http.StatusUnauthorized {
			wantSignedIn = http.StatusServiceUnavailable
		}
		if r.SignedIn != wantSignedIn {
			findings = append(findings, fmt.Sprintf("row %q pins signed_in %d with anonymous %d; with no database signed_in must equal anonymous on an optional-auth stub and be 503 on an auth-required one", r.Name, r.SignedIn, r.Anonymous))
		}
		// A request for the row must be a request for its route: same path with
		// the parameters filled in, so a row cannot pin one route and probe another.
		requestPath, _, _ := strings.Cut(r.Path, "?")
		pattern, request := strings.Split(r.Route, "/"), strings.Split(requestPath, "/")
		matches := len(pattern) == len(request)
		for i := 0; matches && i < len(pattern); i++ {
			matches = pattern[i] == request[i] || strings.HasPrefix(pattern[i], "{") && request[i] != ""
		}
		if !matches {
			findings = append(findings, fmt.Sprintf("row %q requests %s, which is not an instance of its route %s", r.Name, r.Path, r.Route))
		}
		key := routeKey(r.Method, r.Route)
		if !mountedSet[key] {
			findings = append(findings, fmt.Sprintf("%s %s is pinned as a stub but the server does not mount it; mount it or delete the row", r.Method, r.Route))
		}
		if !declaredSet[key] {
			findings = append(findings, fmt.Sprintf("%s %s is pinned as a stub but the served contract does not declare it; pin a schema version that declares it, or delete the row and the route", r.Method, r.Route))
		}
	}
	sort.Strings(findings)
	return findings
}

// TestDeclaredStubRoutes_AreMountedAndDeclared proves each stub satisfies its
// status invariants, matches its route pattern, and is a route the served
// contract declares and the server mounts.
func TestDeclaredStubRoutes_AreMountedAndDeclared(t *testing.T) {
	for _, finding := range declaredStubFindings(loadDeclaredStubRoutes(t), mountedRoutes(t), declaredContractRoutes(t)) {
		t.Error(finding)
	}
}

//go:embed testdata/declared_stub_negative_cases.yaml
var declaredStubNegativeCasesYAML []byte

type declaredStubNegativeCase struct {
	Name     string              `yaml:"name"`
	Routes   []declaredStubRoute `yaml:"routes"`
	Mounted  []route             `yaml:"mounted"`
	Declared []route             `yaml:"declared"`
	Findings []string            `yaml:"findings"`
}

var requiredDeclaredStubNegativeCases = []string{
	"valid auth-required stub passes",
	"wrong anonymous status is a finding",
	"wrong signed-in status is a finding",
	"route and path mismatch is a finding",
	"stub not mounted is a finding",
	"stub not declared by the contract is a finding",
}

func TestDeclaredStubFindings_Fixture(t *testing.T) {
	cases, err := decodeSingleYAMLDocument[[]declaredStubNegativeCase](declaredStubNegativeCasesYAML)
	if err != nil {
		t.Fatalf("load declared stub negative cases: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if present[c.Name] {
			t.Fatalf("duplicate negative case %q", c.Name)
		}
		present[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			got := declaredStubFindings(c.Routes, c.Mounted, c.Declared)
			want := append([]string{}, c.Findings...)
			sort.Strings(want)
			if len(got) == 0 && len(want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("findings mismatch\n got: %v\nwant: %v", got, want)
			}
		})
	}
	for _, required := range requiredDeclaredStubNegativeCases {
		if !present[required] {
			t.Fatalf("missing required negative case %q", required)
		}
	}
	if len(present) != len(requiredDeclaredStubNegativeCases) {
		t.Fatalf("negative fixture has %d rows, want %d", len(present), len(requiredDeclaredStubNegativeCases))
	}
}
