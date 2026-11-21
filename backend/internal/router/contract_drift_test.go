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
	schemaRoute "github.com/peasant-labs/schema/route"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/handler"
)

// TestContractEnforcedOperations_AreMounted proves each operation the
// handlers validate bodies for is served at exactly that method and path, so
// an operation constant cannot drift from the router.
func TestContractEnforcedOperations_AreMounted(t *testing.T) {
	mounted := map[string]bool{}
	for _, r := range mountedRoutes(t) {
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

// enumerateRoutes returns every mounted route. chi.Walk
// reports a method handler that shares a mount node with a subrouter, such as
// a handler registered on the parent at the mount prefix (chi v5.3.0 and
// later); a mount stub with no handler of its own stays hidden.
func enumerateRoutes(t *testing.T, routes chi.Routes) []route {
	t.Helper()
	var out []route
	err := chi.Walk(routes, func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out = append(out, route{Method: method, Path: path})
		return nil
	})
	if err != nil {
		t.Fatalf("walk the router: %v", err)
	}
	return out
}

// mountedRoutes builds the production router and returns all its routes.
func mountedRoutes(t *testing.T) []route {
	t.Helper()
	return enumerateRoutes(t, productionRouter(t))
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
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodTrace:
				out = append(out, route{Method: strings.ToUpper(method), Path: path})
			}
		}
	}
	return out
}

// TestContractDriftGate compares every mounted route with the served contract.
func TestContractDriftGate_MountedRoutesAreDeclared(t *testing.T) {
	mounted := mountedRoutes(t)
	declared := declaredContractRoutes(t)
	for _, finding := range contractDriftFindings(mounted, declared) {
		t.Error(finding)
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

	findings := contractDriftFindings(enumerateRoutes(t, router), declaredContractRoutes(t))
	want := "GET /api/v1 is mounted but the served contract does not declare it; declare it in the schema module and re-pin"
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
	Findings []string `yaml:"findings"`
}

// requiredContractDriftCases names the cases that must exist. It lives here
// rather than in the fixture so deleting fixture rows cannot delete the
// manifest that protects them.
var requiredContractDriftCases = []string{
	"mounted route absent from the contract is a finding",
	"mounted route the contract declares passes",
	"path parameter names do not matter",
	"declared route that is not mounted is a finding",
	"attachment route mounted without a declaration is a finding",
	"a root route the contract declares passes",
	"a root route the contract does not declare is a finding",
	"a route declared at the root but mounted under the API prefix is a finding",
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
			got := contractDriftFindings(c.Mounted, c.Declared)
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
// the served contract declares . Each finding is one sentence naming the route and the fix. The
// result is sorted so fixtures can state it exactly.
func contractDriftFindings(mounted, declared []route) []string {
	declaredSet := map[string]bool{}
	for _, r := range declared {
		declaredSet[routeKey(r.Method, r.Path)] = true
	}
	mountedSet := map[string]bool{}
	for _, r := range mounted {
		mountedSet[routeKey(r.Method, r.Path)] = true
	}
	var findings []string
	for _, r := range mounted {
		key := routeKey(r.Method, r.Path)
		if !declaredSet[key] {
			findings = append(findings, fmt.Sprintf("%s %s is mounted but the served contract does not declare it; declare it in the schema module and re-pin", r.Method, r.Path))
		}
	}
	for _, r := range declared {
		if !mountedSet[routeKey(r.Method, r.Path)] {
			findings = append(findings, fmt.Sprintf("%s %s is declared by the served contract but the server does not mount it; mount it from its schema route member, or as a 501 stub listed in the declared-stub-routes manifest until its handler lands", r.Method, r.Path))
		}
	}
	sort.Strings(findings)
	return findings
}

// TestRootRoutesAreMountedAtTheirPattern checks every contract route outside the API group.
func TestRootRoutesAreMountedAtTheirPattern(t *testing.T) {
	mounted := mountedRoutes(t)
	for _, r := range schemaRoute.AllVillage {
		if strings.HasPrefix(r.Pattern(), handler.APIPrefix+"/") {
			continue
		}
		found := false
		for _, m := range mounted {
			if m.Method == string(r.Method()) && m.Path == r.Pattern() {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("root route %s %s is not mounted", r.Method(), r.Pattern())
		}
	}
}
