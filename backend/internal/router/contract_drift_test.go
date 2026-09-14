package router

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/handler"
)

// TestContractEnforcedOperations_AreMounted proves each operation the
// handlers validate bodies for is served at exactly that method and path, so
// an operation constant cannot drift from the router.
func TestContractEnforcedOperations_AreMounted(t *testing.T) {
	mounted := map[string]bool{}
	for _, r := range mountedAPIRoutes(t) {
		mounted[routeKey(r.Method, r.Path)] = true
	}
	for _, op := range handler.ContractEnforcedOperations() {
		if !mounted[routeKey(op.Method, op.Path)] {
			t.Errorf("enforced operation %s is not mounted at that method and path", op)
		}
	}
}

//go:embed testdata/contract_drift_cases.yaml
var contractDriftCasesYAML []byte

//go:embed testdata/undocumented_routes.yaml
var undocumentedRoutesYAML []byte

//go:embed testdata/api_prefix_cases.yaml
var apiPrefixCasesYAML []byte

type apiPrefixCase struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
	API  bool   `yaml:"api"`
}

var requiredAPIPrefixCases = []string{
	"the bare prefix is an API route",
	"a route below the prefix is an API route",
	"the prefix with a trailing slash is an API route",
	"a path that merely starts with the prefix text is not an API route",
	"a path outside the prefix is not an API route",
}

func TestUnderAPIPrefix_Fixture(t *testing.T) {
	cases, err := decodeSingleYAMLDocument[[]apiPrefixCase](apiPrefixCasesYAML)
	if err != nil {
		t.Fatalf("load the API prefix fixture: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		present[c.Name] = true
	}
	for _, required := range requiredAPIPrefixCases {
		if !present[required] {
			t.Fatalf("the API prefix fixture omits required case %q", required)
		}
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if got := underAPIPrefix(c.Path); got != c.API {
				t.Fatalf("underAPIPrefix(%q) = %v, want %v", c.Path, got, c.API)
			}
		})
	}
}

type manifestRoute struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
	Reason string `yaml:"reason"`
}

type undocumentedRoutesFile struct {
	Routes []manifestRoute `yaml:"routes"`
}

func loadUndocumentedRoutes(t *testing.T) []route {
	t.Helper()
	file, err := decodeSingleYAMLDocument[undocumentedRoutesFile](undocumentedRoutesYAML)
	if err != nil {
		t.Fatalf("load the undocumented-routes manifest: %v", err)
	}
	routes := make([]route, 0, len(file.Routes))
	for _, r := range file.Routes {
		if strings.TrimSpace(r.Reason) == "" {
			t.Fatalf("%s %s in the undocumented-routes manifest has no reason; say why the contract does not declare it yet", r.Method, r.Path)
		}
		routes = append(routes, route{Method: r.Method, Path: r.Path})
	}
	return routes
}

// productionRouter builds the production router with no database, blob store,
// or GitHub App (construction touches none of them).
func productionRouter(t *testing.T) chi.Router {
	t.Helper()
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct the title pipeline: %v", err)
	}
	handler := New(&config.Config{FrontendURL: "https://app.example.com"}, nil, nil, titles)
	routes, ok := handler.(chi.Router)
	if !ok {
		t.Fatalf("router is %T, not chi.Router", handler)
	}
	return routes
}

// enumerateAPIRoutes returns every route mounted under /api/v1. chi.Walk
// reports a method handler that shares a mount node with a subrouter, such as
// a handler registered on the parent at the mount prefix (chi v5.3.0 and
// later); a mount stub with no handler of its own stays hidden.
func enumerateAPIRoutes(t *testing.T, routes chi.Routes) []route {
	t.Helper()
	var out []route
	err := chi.Walk(routes, func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if underAPIPrefix(path) {
			out = append(out, route{Method: method, Path: path})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the router: %v", err)
	}
	return out
}

// mountedAPIRoutes builds the production router and returns its API routes.
func mountedAPIRoutes(t *testing.T) []route {
	t.Helper()
	return enumerateAPIRoutes(t, productionRouter(t))
}

// underAPIPrefix reports whether a mounted path belongs to the /api/v1
// surface, the bare prefix included.
func underAPIPrefix(path string) bool {
	return path == "/api/v1" || strings.HasPrefix(path, "/api/v1/")
}

// declaredContractRoutes reads the operations from the same bytes the server
// serves at /api/v1/openapi.json.
func declaredContractRoutes(t *testing.T) []route {
	t.Helper()
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(schema.VillageAPISpecJSON(), &doc); err != nil {
		t.Fatalf("parse the served contract: %v", err)
	}
	var out []route
	for path, operations := range doc.Paths {
		for method := range operations {
			switch strings.ToUpper(method) {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
				out = append(out, route{Method: strings.ToUpper(method), Path: path})
			}
		}
	}
	return out
}

// TestContractDriftGate_MountedRoutesAreDeclaredOrListed is the CI gate: every
// route the server mounts under /api/v1 is either declared by the served
// contract or listed, with a reason, in the undocumented-routes manifest. The
// manifest can only shrink: a row for a route the contract now declares, or
// for a route no longer mounted, is itself a failure.
func TestContractDriftGate_MountedRoutesAreDeclaredOrListed(t *testing.T) {
	mounted := mountedAPIRoutes(t)
	declared := declaredContractRoutes(t)
	manifest := loadUndocumentedRoutes(t)
	for _, finding := range contractDriftFindings(mounted, declared, manifest) {
		t.Error(finding)
	}
	mountedSet := map[string]bool{}
	for _, r := range mounted {
		mountedSet[routeKey(r.Method, r.Path)] = true
	}
	for _, r := range declared {
		if !mountedSet[routeKey(r.Method, r.Path)] {
			t.Logf("declared but not mounted (informational): %s %s", r.Method, r.Path)
		}
	}
}

// TestContractDriftGate_SeesAHandlerAtTheMountPrefix pins the enumeration
// behavior the gate depends on: a method handler registered on the parent
// router at the exact /api/v1 mount prefix is a mounted route. The handler is
// live, and the gate reports it as undeclared, so the proof is not vacuous.
func TestContractDriftGate_SeesAHandlerAtTheMountPrefix(t *testing.T) {
	router := productionRouter(t)
	router.Get("/api/v1", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	live := httptest.NewRecorder()
	router.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/api/v1", nil))
	if live.Code != http.StatusNoContent {
		t.Fatalf("the handler at /api/v1 answered %d; the regression would be vacuous", live.Code)
	}

	findings := contractDriftFindings(enumerateAPIRoutes(t, router), declaredContractRoutes(t), loadUndocumentedRoutes(t))
	want := "GET /api/v1 is mounted but the served contract does not declare it; declare it in the schema module and re-pin, or add it to the undocumented-routes manifest with a reason"
	for _, finding := range findings {
		if finding == want {
			return
		}
	}
	t.Fatalf("the gate misses a live handler at the mount prefix; findings = %v", findings)
}

// route is one method and path pair. Path parameter names are erased before
// comparison: the router's {id} and the contract's {groupId} at the same
// segment name the same route.
type route struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
}

type contractDriftCase struct {
	Name     string   `yaml:"name"`
	Why      string   `yaml:"why"`
	Mounted  []route  `yaml:"mounted"`
	Declared []route  `yaml:"declared"`
	Manifest []route  `yaml:"manifest"`
	Findings []string `yaml:"findings"`
}

// requiredContractDriftCases names the cases that must exist. It lives here
// rather than in the fixture so deleting fixture rows cannot delete the
// manifest that protects them.
var requiredContractDriftCases = []string{
	"mounted route absent from the contract and the manifest is a finding",
	"manifest row no longer mounted is a finding",
	"manifest row the contract now declares is a finding",
	"duplicate manifest row is a finding",
	"mounted route the contract declares passes",
	"manifest row still mounted and still undeclared passes",
	"path parameter names do not matter",
	"declared route that is not mounted is not a finding",
	"attachment route mounted without a declaration is a finding",
}

func decodeSingleYAMLDocument[T any](data []byte) (T, error) {
	var out T
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&out); err != nil {
		return out, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return out, fmt.Errorf("fixture must contain exactly one YAML document")
	}
	return out, nil
}

func loadContractDriftCases(t *testing.T) []contractDriftCase {
	t.Helper()
	cases, err := decodeSingleYAMLDocument[[]contractDriftCase](contractDriftCasesYAML)
	if err != nil {
		t.Fatalf("load the contract drift fixture: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if present[c.Name] {
			t.Fatalf("the contract drift fixture repeats case %q", c.Name)
		}
		present[c.Name] = true
	}
	for _, required := range requiredContractDriftCases {
		if !present[required] {
			t.Fatalf("the contract drift fixture omits required case %q; restore it rather than removing it from the manifest", required)
		}
	}
	return cases
}

func TestContractDriftFindings_Fixture(t *testing.T) {
	for _, c := range loadContractDriftCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			got := contractDriftFindings(c.Mounted, c.Declared, c.Manifest)
			want := append([]string{}, c.Findings...)
			sort.Strings(want)
			if len(got) == 0 && len(want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("findings mismatch\n got: %s\nwant: %s", strings.Join(got, "\n      "), strings.Join(want, "\n      "))
			}
		})
	}
}

var pathParameter = regexp.MustCompile(`\{[^}]*\}`)

// routeKey identifies a route by method and by path with parameter names erased.
func routeKey(method, path string) string {
	return strings.ToUpper(method) + " " + pathParameter.ReplaceAllString(path, "{}")
}

// contractDriftFindings compares the routes the server mounts with the routes
// the served contract declares and with the manifest of routes known to be
// undeclared. Each finding is one sentence naming the route and the fix. The
// result is sorted so fixtures can state it exactly.
func contractDriftFindings(mounted, declared, manifest []route) []string {
	declaredSet := map[string]bool{}
	for _, r := range declared {
		declaredSet[routeKey(r.Method, r.Path)] = true
	}
	mountedSet := map[string]bool{}
	for _, r := range mounted {
		mountedSet[routeKey(r.Method, r.Path)] = true
	}
	manifestSet := map[string]bool{}
	var findings []string
	for _, r := range manifest {
		key := routeKey(r.Method, r.Path)
		if manifestSet[key] {
			findings = append(findings, fmt.Sprintf("%s %s appears twice in the undocumented-routes manifest; keep one row", r.Method, r.Path))
			continue
		}
		manifestSet[key] = true
		if !mountedSet[key] {
			findings = append(findings, fmt.Sprintf("%s %s is in the undocumented-routes manifest but the server no longer mounts it; delete the row", r.Method, r.Path))
		}
		if declaredSet[key] {
			findings = append(findings, fmt.Sprintf("%s %s is in the undocumented-routes manifest but the served contract now declares it; delete the row", r.Method, r.Path))
		}
	}
	for _, r := range mounted {
		key := routeKey(r.Method, r.Path)
		if !declaredSet[key] && !manifestSet[key] {
			findings = append(findings, fmt.Sprintf("%s %s is mounted but the served contract does not declare it; declare it in the schema module and re-pin, or add it to the undocumented-routes manifest with a reason", r.Method, r.Path))
		}
	}
	sort.Strings(findings)
	return findings
}
