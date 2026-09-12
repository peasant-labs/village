package router

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/contract_drift_cases.yaml
var contractDriftCasesYAML []byte

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
