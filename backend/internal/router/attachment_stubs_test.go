package router

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/peasant-labs/redact"

	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/config"
)

//go:embed testdata/attachment_stub_routes.yaml
var attachmentStubRoutesYAML []byte

type attachmentStubFixture struct {
	UnauthenticatedError string                `yaml:"unauthenticated_error"`
	NotImplementedError  string                `yaml:"not_implemented_error"`
	Routes               []attachmentStubRoute `yaml:"routes"`
}

type attachmentStubRoute struct {
	Name          string                   `yaml:"name"`
	Method        string                   `yaml:"method"`
	Path          string                   `yaml:"path"`
	Target        string                   `yaml:"target"`
	Body          string                   `yaml:"body"`
	Anonymous     int                      `yaml:"anonymous"`
	Authenticated int                      `yaml:"authenticated"`
	Malformed     *attachmentStubMalformed `yaml:"malformed"`
}

type attachmentStubMalformed struct {
	Body          string `yaml:"body"`
	Status        int    `yaml:"status"`
	ErrorContains string `yaml:"error_contains"`
}

// requiredAttachmentStubRoutes names the rows the fixture must carry. It lives
// here so deleting fixture rows cannot delete the manifest that protects them;
// landing a real handler removes the route from both places in one change.
var requiredAttachmentStubRoutes = []string{
	"receive github webhook",
	"get pull request attachment",
	"confirm pull request attachment",
	"detach pull request attachment",
	"list my prompt requests",
	"get my settings",
	"update my settings",
}

// loadAttachmentStubRoutes decodes and validates the attachment stub fixture.
// Every row here names a route the contract declares before its handler
// exists, so the checks below are deliberately strict: a stub that starts
// answering something other than 501 signed in, or a row that goes missing
// without its name leaving the required list, is a fixture defect, not a
// passing test.
func loadAttachmentStubRoutes(t *testing.T) attachmentStubFixture {
	t.Helper()
	fixture, err := decodeSingleYAMLDocument[attachmentStubFixture](attachmentStubRoutesYAML)
	if err != nil {
		t.Fatalf("load the attachment stub fixture: %v", err)
	}
	if strings.TrimSpace(fixture.UnauthenticatedError) == "" {
		t.Fatalf("the attachment stub fixture has no unauthenticated_error")
	}
	if strings.TrimSpace(fixture.NotImplementedError) == "" {
		t.Fatalf("the attachment stub fixture has no not_implemented_error")
	}

	names := map[string]bool{}
	pairs := map[string]bool{}
	for _, r := range fixture.Routes {
		if names[r.Name] {
			t.Fatalf("the attachment stub fixture repeats route %q", r.Name)
		}
		names[r.Name] = true

		key := routeKey(r.Method, r.Path)
		if pairs[key] {
			t.Fatalf("the attachment stub fixture repeats %s %s", r.Method, r.Path)
		}
		pairs[key] = true

		if strings.TrimSpace(r.Target) == "" {
			t.Fatalf("%q has no target", r.Name)
		}
		if r.Anonymous != http.StatusUnauthorized && r.Anonymous != http.StatusNotImplemented {
			t.Fatalf("%s %s declares anonymous status %d; it must be 401 or 501", r.Method, r.Path, r.Anonymous)
		}
		if r.Authenticated != http.StatusNotImplemented {
			t.Fatalf("%s %s answers %d signed in; a stub row must answer 501, and a route with a real handler deletes its row", r.Method, r.Path, r.Authenticated)
		}
		if r.Malformed != nil {
			if r.Malformed.Status != http.StatusBadRequest {
				t.Fatalf("%s %s malformed block declares status %d; it must be 400", r.Method, r.Path, r.Malformed.Status)
			}
			if strings.TrimSpace(r.Malformed.ErrorContains) == "" {
				t.Fatalf("%s %s malformed block has no error_contains", r.Method, r.Path)
			}
		}
	}

	for _, required := range requiredAttachmentStubRoutes {
		if !names[required] {
			t.Fatalf("the attachment stub fixture omits required route %q; a route with a real handler is removed from the required list in the same change", required)
		}
	}

	return fixture
}

// attachmentStubRouter builds the production router with a fixed JWT secret,
// so a signed-in request can be minted here, and no database, blob store, or
// GitHub App.
func attachmentStubRouter(t *testing.T) (http.Handler, *config.Config) {
	t.Helper()
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct the title pipeline: %v", err)
	}
	cfg := &config.Config{JWTSecret: "attachment-stub-test-secret", FrontendURL: "https://app.example.com"}
	return New(cfg, nil, nil, titles), cfg
}

// serveAttachmentStub drives one request through the router: a non-empty body
// gets a JSON content type, and a non-empty token an Authorization header.
func serveAttachmentStub(t *testing.T, handler http.Handler, method, target, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// stubError reads the one field every stub and contract-violation body here
// carries.
func stubError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the response body %q: %v", rec.Body.String(), err)
	}
	return body.Error
}

// TestAttachmentStubRoutes_AreDeclaredAndMounted proves each stub route is
// both declared by the served contract and mounted by the router, so the
// fixture cannot name a route the contract does not know or the server does
// not answer.
func TestAttachmentStubRoutes_AreDeclaredAndMounted(t *testing.T) {
	fixture := loadAttachmentStubRoutes(t)

	declared := map[string]bool{}
	for _, r := range declaredContractRoutes(t) {
		declared[routeKey(r.Method, r.Path)] = true
	}
	mounted := map[string]bool{}
	for _, r := range mountedAPIRoutes(t) {
		mounted[routeKey(r.Method, r.Path)] = true
	}

	for _, row := range fixture.Routes {
		key := routeKey(row.Method, row.Path)
		if !declared[key] {
			t.Errorf("%s %s is not declared by the served contract", row.Method, row.Path)
		}
		if !mounted[key] {
			t.Errorf("%s %s is not mounted by the router", row.Method, row.Path)
		}
	}
}

// TestAttachmentStubRoutes_AnswerAsFixtured drives every row through the
// production router with no database: anonymous, signed in, and, where the
// row carries one, with a malformed body signed in.
func TestAttachmentStubRoutes_AnswerAsFixtured(t *testing.T) {
	fixture := loadAttachmentStubRoutes(t)
	handler, cfg := attachmentStubRouter(t)

	for _, row := range fixture.Routes {
		t.Run(row.Name, func(t *testing.T) {
			anon := serveAttachmentStub(t, handler, row.Method, row.Target, row.Body, "")
			if anon.Code != row.Anonymous {
				t.Fatalf("anonymous %s %s answered %d, want %d", row.Method, row.Target, anon.Code, row.Anonymous)
			}
			switch row.Anonymous {
			case http.StatusUnauthorized:
				if got := stubError(t, anon); got != fixture.UnauthenticatedError {
					t.Fatalf("anonymous %s %s error = %q, want %q", row.Method, row.Target, got, fixture.UnauthenticatedError)
				}
			case http.StatusNotImplemented:
				if got := stubError(t, anon); got != fixture.NotImplementedError {
					t.Fatalf("anonymous %s %s error = %q, want %q", row.Method, row.Target, got, fixture.NotImplementedError)
				}
			}

			token, err := auth.CreateToken(cfg.JWTSecret, uuid.New(), "attachment-stub")
			if err != nil {
				t.Fatalf("mint a signed-in token: %v", err)
			}

			signedIn := serveAttachmentStub(t, handler, row.Method, row.Target, row.Body, token)
			if signedIn.Code != row.Authenticated {
				t.Fatalf("signed-in %s %s answered %d, want %d", row.Method, row.Target, signedIn.Code, row.Authenticated)
			}
			if got := stubError(t, signedIn); got != fixture.NotImplementedError {
				t.Fatalf("signed-in %s %s error = %q, want %q", row.Method, row.Target, got, fixture.NotImplementedError)
			}

			if row.Malformed == nil {
				return
			}
			malformed := serveAttachmentStub(t, handler, row.Method, row.Target, row.Malformed.Body, token)
			if malformed.Code != row.Malformed.Status {
				t.Fatalf("malformed %s %s answered %d, want %d", row.Method, row.Target, malformed.Code, row.Malformed.Status)
			}
			got := stubError(t, malformed)
			if !strings.Contains(got, "request body failed contract validation: ") {
				t.Fatalf("malformed %s %s error = %q, want the contract validation prefix", row.Method, row.Target, got)
			}
			if !strings.Contains(got, row.Malformed.ErrorContains) {
				t.Fatalf("malformed %s %s error = %q, want it to contain %q", row.Method, row.Target, got, row.Malformed.ErrorContains)
			}
		})
	}
}
