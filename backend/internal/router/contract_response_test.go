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
	schemaRoute "github.com/peasant-labs/schema/route"
	"github.com/peasant-labs/village/backend/internal/config"
)

//go:embed testdata/contract_response_cases.yaml
var contractResponseYAML []byte

type contractGap struct {
	Name   string `yaml:"name"`
	Reason string `yaml:"reason"`
}

type contractResponseFixtures struct {
	Selected          []string      `yaml:"selected"`
	KnownContractGaps []contractGap `yaml:"known_contract_gaps"`
	SelfTests         []struct {
		Name     string `yaml:"name"`
		Mutation string `yaml:"mutation"`
		Want     string `yaml:"want"`
	} `yaml:"self_tests"`
}

var requiredContractResponseCases = []string{
	"health-no-pool", "openapi-document", "logout", "empty-organization-search",
	"list-attestations-invalid-id", "list-annotations-invalid-id", "list-commits-invalid-id",
	"github-login-sixth-request", "delete-account-anonymous", "update-discoverability-anonymous",
	"set-username-anonymous", "create-api-key-anonymous", "list-api-keys-anonymous",
	"revoke-api-key-anonymous", "organization-visibility-anonymous", "delete-transcript-anonymous",
	"create-attestation-anonymous", "delete-attestation-anonymous", "create-annotation-anonymous",
	"batch-publish-anonymous", "set-project-name-anonymous", "clear-project-name-anonymous",
	"github-install-anonymous", "github-install-callback-anonymous", "github-install-unconfigured",
	"github-install-callback-unconfigured", "organization-visibility-updated",
	"organization-visibility-invalid-json", "delete-missing-attestation",
	"delete-attestation-invalid-id", "public-profile-missing", "public-organizations-missing",
	"create-api-key", "list-api-keys", "peasant-revoke-api-key", "batch-publish-signed-in",
	"organization-search-null-avatar", "organization-search-avatar", "github-entry",
	"github-callback-state-json", "github-callback-state-html", "gitlab-entry",
	"gitlab-callback-state-json", "gitlab-callback-state-html", "huggingface-entry",
	"huggingface-callback-state-json", "huggingface-callback-state-html", "codeberg-entry",
	"codeberg-callback-state-json", "codeberg-callback-state-html", "sourcehut-entry",
	"sourcehut-callback-state-json", "sourcehut-callback-state-html",
	"gitlab-entry-unconfigured-json", "gitlab-entry-unconfigured-html",
	"gitlab-callback-unconfigured-json", "gitlab-callback-unconfigured-html",
	"huggingface-entry-unconfigured-json", "huggingface-entry-unconfigured-html",
	"huggingface-callback-unconfigured-json", "huggingface-callback-unconfigured-html",
	"codeberg-entry-unconfigured-json", "codeberg-entry-unconfigured-html",
	"codeberg-callback-unconfigured-json", "codeberg-callback-unconfigured-html",
	"sourcehut-entry-unconfigured-json", "sourcehut-entry-unconfigured-html",
	"sourcehut-callback-unconfigured-json", "sourcehut-callback-unconfigured-html",
	"gitlab-callback-missing-code-json", "gitlab-callback-missing-code-html", "github-callback-web",
	"github-callback-cli", "gitlab-callback-web", "github-callback-missing-code-json",
	"github-callback-missing-code-html", "github-install", "github-install-missing-group-json",
	"github-install-missing-group-html", "github-install-not-owner", "github-install-callback-bound",
	"github-install-callback-unbound", "github-install-callback-invalid-id-json",
	"github-install-callback-invalid-id-html", "github-install-callback-unreadable",
	"update-discoverability", "update-discoverability-missing", "set-username", "set-username-taken",
	"set-username-invalid", "list-tags", "popular-tags", "public-profile", "public-organizations",
	"project-page", "project-page-no-visible-transcripts", "project-page-missing",
	"project-page-invalid-hash", "set-project-name", "set-project-name-unowned",
	"set-project-name-empty", "clear-project-name", "clear-project-name-absent",
	"list-attestations-empty", "create-attestation", "create-attestation-duplicate",
	"create-attestation-not-member", "create-attestation-invalid-type", "list-attestations",
	"list-annotations-empty", "create-annotation", "create-annotation-missing-entry",
	"list-annotations", "list-commits", "list-commits-hidden", "cli-login", "cli-exchange",
	"cli-exchange-used-code", "cli-exchange-unknown-code", "huggingface-callback-web",
	"codeberg-callback-web", "sourcehut-callback-web", "github-install-unconfigured-html",
	"github-install-callback-unconfigured-html", "revoke-api-key-invalid-id",
	"clear-project-name-invalid-hash", "list-attestations-hidden", "list-annotations-hidden",
	"create-attestation-hidden", "create-annotation-hidden", "create-attestation-invalid-id",
	"create-annotation-invalid-id", "delete-transcript-invalid-id", "delete-transcript-not-owner",
	"delete-transcript-without-blob-store", "delete-transcript", "delete-transcript-missing",
	"delete-account", "cli-login-missing-port-json", "cli-login-missing-port-html",
	"cli-login-missing-state-json", "cli-login-missing-state-html", "cli-login-invalid-port-json",
	"cli-login-invalid-port-html", "cli-login-port-below-range-json",
	"cli-login-port-below-range-html", "cli-login-port-above-range-json",
	"cli-login-port-above-range-html", "cli-login-sixth-request", "cli-exchange-invalid-json",
	"cli-exchange-missing-code", "cli-exchange-missing-state", "cli-exchange-sixth-request",
	"gitlab-login-sixth-request", "huggingface-login-sixth-request", "codeberg-login-sixth-request",
	"sourcehut-login-sixth-request", "huggingface-callback-missing-code-json",
	"huggingface-callback-missing-code-html", "codeberg-callback-missing-code-json",
	"codeberg-callback-missing-code-html", "sourcehut-callback-missing-code-json",
	"sourcehut-callback-missing-code-html", "list-api-keys-session-unconfirmed",
	"create-api-key-session-unconfirmed", "revoke-api-key-session-unconfirmed",
	"delete-account-session-unconfirmed", "update-discoverability-session-unconfirmed",
	"set-username-session-unconfirmed", "organization-visibility-session-unconfirmed",
	"github-install-session-unconfirmed", "github-install-callback-session-unconfirmed",
	"batch-publish-session-unconfirmed", "delete-transcript-session-unconfirmed",
	"create-annotation-session-unconfirmed", "create-attestation-session-unconfirmed",
	"delete-attestation-session-unconfirmed", "set-project-name-session-unconfirmed",
	"clear-project-name-session-unconfirmed", "delete-transcript-storage-failure",
	"unsubscribe-invalid", "unsubscribe-rate-limited",
}

var requiredKnownContractGaps = map[string]string{}

func contractResponseFixturesFor(t *testing.T) contractResponseFixtures {
	t.Helper()
	f, err := decodeSingleYAMLDocument[contractResponseFixtures](contractResponseYAML)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Selected, requiredContractResponseCases) {
		t.Fatalf("contract response cases = %v; want %v", f.Selected, requiredContractResponseCases)
	}
	gotGaps := map[string]string{}
	for _, g := range f.KnownContractGaps {
		if g.Name == "" || strings.TrimSpace(g.Reason) == "" {
			t.Fatalf("known contract gap %+v must name both case and reason", g)
		}
		if _, dup := gotGaps[g.Name]; dup {
			t.Fatalf("duplicate known contract gap %q", g.Name)
		}
		gotGaps[g.Name] = g.Reason
	}
	if !reflect.DeepEqual(gotGaps, requiredKnownContractGaps) {
		t.Fatalf("known contract gaps = %v; want %v", gotGaps, requiredKnownContractGaps)
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
	if media == "text/html" {
		// text/html responses have no JSON body schema in the served contract;
		// status and declared headers were validated above.
		return nil
	}
	if (media != "application/json" && media != "text/plain") || len(content.Schema) == 0 {
		return fmt.Errorf("unsupported response schema for %s", media)
	}
	var body any
	if media == "text/plain" {
		body = rec.Body.String()
	} else {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			return fmt.Errorf("decode body: %w", err)
		}
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
	f := contractResponseFixturesFor(t)
	for _, name := range f.Selected {
		if name == c.Name {
			if err := contractResponse(c.Method, path, rec); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	for _, gap := range f.KnownContractGaps {
		if gap.Name == c.Name {
			err := contractResponse(c.Method, path, rec)
			if err == nil || !strings.Contains(err.Error(), gap.Reason) {
				t.Fatalf("known contract gap %q error = %v; want %q", gap.Name, err, gap.Reason)
			}
			return
		}
	}
	t.Fatalf("route byte case %q is in neither selected nor known_contract_gaps", c.Name)
}

func TestContractResponseFixtureCoverage(t *testing.T) {
	cases := routeByteCases(t)
	seen := map[string]bool{}
	for _, c := range cases {
		seen[c.Name] = true
	}
	f := contractResponseFixturesFor(t)
	covered := map[string]bool{}
	for _, name := range f.Selected {
		if !seen[name] {
			t.Errorf("missing mounted response case %q", name)
		}
		if covered[name] {
			t.Errorf("duplicate selected response case %q", name)
		}
		covered[name] = true
	}
	for _, gap := range f.KnownContractGaps {
		if !seen[gap.Name] {
			t.Errorf("missing mounted response case %q in known_contract_gaps", gap.Name)
		}
		if covered[gap.Name] {
			t.Errorf("case %q appears in both selected and known_contract_gaps", gap.Name)
		}
		covered[gap.Name] = true
	}
	for _, c := range cases {
		if !covered[c.Name] {
			t.Errorf("route byte case %q is neither in selected nor known_contract_gaps", c.Name)
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
			case "unexpected-body":
				var err error
				path, err = schemaRoute.VillageGetTranscriptHeaderPath("00000000-0000-0000-0000-000000000001")
				if err != nil {
					t.Fatal(err)
				}
				rec = httptest.NewRecorder()
				rec.Code = http.StatusNotModified
				rec.Body.WriteString("unexpected")
			default:
				t.Fatalf("unknown mutation %q", c.Mutation)
			}
			if err := contractResponse(http.MethodGet, path, rec); err == nil || !strings.Contains(err.Error(), c.Want) {
				t.Fatalf("error = %v; want %q", err, c.Want)
			}
		})
	}
	required := map[string]bool{"undeclared-status": true, "missing-required-header": true, "invalid-body": true, "undeclared-operation": true, "undeclared-media": true, "invalid-json": true, "unexpected-body": true}
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
