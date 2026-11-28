package router

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/config"
)

//go:embed testdata/contract_response_cases.yaml
var contractResponseYAML []byte

type contractResponseFixtures struct {
	Selected  []string `yaml:"selected"`
	SelfTests []struct {
		Name     string `yaml:"name"`
		Mutation string `yaml:"mutation"`
		Want     string `yaml:"want"`
	} `yaml:"self_tests"`
}

var requiredContractResponseCases = []string{
	"health-no-pool", "logout", "empty-organization-search", "organization-visibility-updated",
	"create-api-key", "list-api-keys", "update-discoverability", "list-tags",
	"public-organizations", "project-page", "set-project-name", "list-annotations", "list-commits",
}

func contractResponseFixturesFor(t *testing.T) contractResponseFixtures {
	t.Helper()
	f, err := decodeSingleYAMLDocument[contractResponseFixtures](contractResponseYAML)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Selected, requiredContractResponseCases) {
		t.Fatalf("contract response cases = %v; want %v", f.Selected, requiredContractResponseCases)
	}
	return f
}

// contractResponse checks the operation, status, required headers and response
// body against the same document that the server serves.
func contractResponse(method, requestPath string, rec *httptest.ResponseRecorder) error {
	requestPath, _, _ = strings.Cut(requestPath, "?")
	raw := schema.VillageAPISpecJSON()
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse served contract: %w", err)
	}
	var matched string
	if operations, ok := doc.Paths[requestPath]; ok {
		if _, ok := operations[strings.ToLower(method)]; ok {
			matched = requestPath
		}
	}
	for path, operations := range doc.Paths {
		if matched == requestPath {
			break
		}
		if _, ok := operations[strings.ToLower(method)]; !ok {
			continue
		}
		pattern := regexp.MustCompile(`\\\{[^/]+\\\}`).ReplaceAllString(regexp.QuoteMeta(path), `[^/]+`)
		if regexp.MustCompile("^" + pattern + "$").MatchString(requestPath) {
			if matched != "" {
				return fmt.Errorf("ambiguous operation %s %s", method, requestPath)
			}
			matched = path
		}
	}
	if matched == "" {
		return fmt.Errorf("undeclared operation %s %s", method, requestPath)
	}
	var operation struct {
		Responses map[string]struct {
			Headers map[string]struct {
				Required bool `json:"required"`
			} `json:"headers"`
			Content map[string]struct {
				Schema json.RawMessage `json:"schema"`
			} `json:"content"`
		} `json:"responses"`
	}
	if err := json.Unmarshal(doc.Paths[matched][strings.ToLower(method)], &operation); err != nil {
		return fmt.Errorf("parse operation %s %s: %w", method, matched, err)
	}
	response, ok := operation.Responses[strconv.Itoa(rec.Code)]
	if !ok {
		return fmt.Errorf("undeclared status %d for %s %s", rec.Code, method, matched)
	}
	for name, header := range response.Headers {
		if header.Required && rec.Header().Get(name) == "" {
			return fmt.Errorf("required header %s missing", name)
		}
	}
	if len(response.Content) == 0 {
		if rec.Body.Len() != 0 {
			return fmt.Errorf("unexpected body")
		}
		return nil
	}
	media, _, err := mime.ParseMediaType(rec.Header().Get("Content-Type"))
	if err != nil {
		return fmt.Errorf("invalid content type: %w", err)
	}
	content, ok := response.Content[media]
	if !ok {
		return fmt.Errorf("undeclared media %s", media)
	}
	if media != "application/json" || len(content.Schema) == 0 {
		return fmt.Errorf("unsupported response schema for %s", media)
	}
	var body any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		return fmt.Errorf("decode body: %w", err)
	}
	compiler := schema.NewJSONSchemaCompiler()
	const specURL = "https://village.invalid/openapi.json"
	if err := compiler.AddResource(specURL, bytes.NewReader(raw)); err != nil {
		return fmt.Errorf("register contract: %w", err)
	}
	escape := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }
	location := "#/paths/" + escape(matched) + "/" + strings.ToLower(method) + "/responses/" + strconv.Itoa(rec.Code) + "/content/" + escape(media) + "/schema"
	compiled, err := compiler.Compile(specURL + location)
	if err != nil {
		return fmt.Errorf("compile response schema: %w", err)
	}
	if err := compiled.Validate(body); err != nil {
		return fmt.Errorf("validate body: %w", err)
	}
	return nil
}

func validateSelectedContractResponse(t *testing.T, c routeByteCase, rec *httptest.ResponseRecorder, path string) {
	t.Helper()
	for _, name := range contractResponseFixturesFor(t).Selected {
		if name == c.Name {
			if err := contractResponse(c.Method, path, rec); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
}

func TestContractResponseFixtureCoverage(t *testing.T) {
	cases := routeByteCases(t)
	seen := map[string]bool{}
	for _, c := range cases {
		seen[c.Name] = true
	}
	for _, name := range contractResponseFixturesFor(t).Selected {
		if !seen[name] {
			t.Errorf("missing mounted response case %q", name)
		}
	}
}

func TestContractResponseRejectsInvalidResponses(t *testing.T) {
	f := contractResponseFixturesFor(t)
	seen := map[string]bool{}
	for _, c := range f.SelfTests {
		if seen[c.Name] {
			t.Fatalf("duplicate self-test %q", c.Name)
		}
		seen[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			New(&config.Config{}, nil, nil, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
			path := "/health"
			switch c.Mutation {
			case "status":
				rec.Code = 418
			case "header":
				rec.Header().Del("Cache-Control")
			case "body":
				rec.Body.Reset()
				rec.Body.WriteString(`{"status":42,"serves":[]}`)
			case "unknown-path":
				path = "/not-a-route"
			case "wrong-media":
				rec.Header().Set("Content-Type", "text/plain")
			case "malformed-json":
				rec.Body.Reset()
				rec.Body.WriteString("{")
			default:
				t.Fatalf("unknown mutation %q", c.Mutation)
			}
			if err := contractResponse(http.MethodGet, path, rec); err == nil || !strings.Contains(err.Error(), c.Want) {
				t.Fatalf("error = %v; want %q", err, c.Want)
			}
		})
	}
	required := map[string]bool{"undeclared-status": true, "missing-required-header": true, "invalid-body": true, "undeclared-operation": true, "undeclared-media": true, "invalid-json": true}
	if !reflect.DeepEqual(seen, required) {
		t.Fatalf("self-test names = %v; want %v", seen, required)
	}
}

func TestContractResponseStripsQueryString(t *testing.T) {
	rec := httptest.NewRecorder()
	New(&config.Config{}, nil, nil, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health?probe=1", nil))
	if err := contractResponse(http.MethodGet, "/health?probe=1", rec); err != nil {
		t.Fatalf("contractResponse with query string failed: %v", err)
	}
}
