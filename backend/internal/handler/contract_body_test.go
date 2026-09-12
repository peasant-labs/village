package handler

import (
	"context"
	_ "embed"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/contract_body_operations.yaml
var contractBodyOperationsYAML []byte

type contractBodyMalformed struct {
	Name   string `yaml:"name"`
	Body   string `yaml:"body"`
	Expect string `yaml:"expect"`
}

type contractBodyOperationCase struct {
	Name       string                  `yaml:"name"`
	Method     string                  `yaml:"method"`
	Path       string                  `yaml:"path"`
	Conforming string                  `yaml:"conforming"`
	Malformed  []contractBodyMalformed `yaml:"malformed"`
}

// requiredContractBodyOperations names the operations the fixture must cover,
// keyed by method and path. It lives here so deleting fixture rows cannot
// delete the manifest that protects them.
var requiredContractBodyOperations = []string{
	"POST /api/v1/groups",
	"PATCH /api/v1/groups/{id}",
	"POST /api/v1/groups/{id}/members",
	"PATCH /api/v1/groups/{id}/members/{userID}/role",
	"POST /api/v1/groups/{id}/repositories",
	"POST /api/v1/groups/{id}/shares",
	"PATCH /api/v1/groups/{id}/shares",
	"PATCH /api/v1/groups/{id}/shares/{transcriptID}",
	"POST /api/v1/transcripts/{id}/share",
}

func loadContractBodyOperations(t *testing.T) []contractBodyOperationCase {
	t.Helper()
	cases, err := decodeFixtureRows[contractBodyOperationCase](contractBodyOperationsYAML)
	if err != nil {
		t.Fatalf("load the contract body fixture: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		key := c.Method + " " + c.Path
		if present[key] {
			t.Fatalf("the contract body fixture repeats operation %q", key)
		}
		if len(c.Malformed) == 0 {
			t.Fatalf("operation %q has no malformed body; every enforced operation needs at least one", key)
		}
		if strings.TrimSpace(c.Conforming) == "" {
			t.Fatalf("operation %q has no conforming body", key)
		}
		for _, m := range c.Malformed {
			if strings.TrimSpace(m.Expect) == "" {
				t.Fatalf("operation %q malformed case %q expects nothing; name the violation substring", key, m.Name)
			}
		}
		present[key] = true
	}
	for _, required := range requiredContractBodyOperations {
		if !present[required] {
			t.Fatalf("the contract body fixture omits required operation %q; restore it rather than removing it from the manifest", required)
		}
	}
	return cases
}

func TestContractBody_FixtureMatchesEnforcedOperations(t *testing.T) {
	inFixture := map[string]bool{}
	for _, c := range loadContractBodyOperations(t) {
		inFixture[c.Method+" "+c.Path] = true
	}
	for _, op := range ContractEnforcedOperations() {
		if !inFixture[op.String()] {
			t.Errorf("enforced operation %s has no fixture row", op)
		}
	}
	if got, want := len(ContractEnforcedOperations()), len(inFixture); got != want {
		t.Errorf("enforced operations = %d, fixture rows = %d; the two lists must name the same operations", got, want)
	}
}

func TestContractBody_ValidatorRejectsMalformedAndAcceptsConforming(t *testing.T) {
	v := moduleValidator{}
	for _, c := range loadContractBodyOperations(t) {
		t.Run(c.Name, func(t *testing.T) {
			if err := v.ValidateBody(c.Method, c.Path, []byte(c.Conforming)); err != nil {
				t.Fatalf("conforming body rejected: %v", err)
			}
			for _, m := range c.Malformed {
				err := v.ValidateBody(c.Method, c.Path, []byte(m.Body))
				if err == nil {
					t.Errorf("%s: malformed body accepted", m.Name)
					continue
				}
				if !errors.Is(err, ErrSchemaInvalid) {
					t.Errorf("%s: error is not ErrSchemaInvalid: %v", m.Name, err)
				}
				if !strings.Contains(err.Error(), m.Expect) {
					t.Errorf("%s: violation %q does not contain %q", m.Name, err.Error(), m.Expect)
				}
				for _, forbidden := range []string{"file://", "contract://", "village-api", "/Users/", "\\"} {
					if strings.Contains(err.Error(), forbidden) {
						t.Errorf("%s: violation leaks a location: %q", m.Name, err.Error())
					}
				}
			}
		})
	}
}

func TestContractBody_UndeclaredOperationFailsClosed(t *testing.T) {
	v := moduleValidator{}
	err := v.ValidateBody(http.MethodPost, "/api/v1/groups/{id}/join", []byte(`{}`))
	if !errors.Is(err, ErrContractBodyUndeclared) {
		t.Fatalf("expected ErrContractBodyUndeclared, got %v", err)
	}
	err = v.ValidateBody(http.MethodGet, "/api/v1/groups", []byte(`{}`))
	if !errors.Is(err, ErrContractBodyUndeclared) {
		t.Fatalf("expected ErrContractBodyUndeclared for an operation without a body, got %v", err)
	}
}

func TestContractBody_InvalidJSONIsSchemaInvalid(t *testing.T) {
	v := moduleValidator{}
	err := v.ValidateBody(http.MethodPost, "/api/v1/groups", []byte(`{"name":`))
	if !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("expected ErrSchemaInvalid for invalid JSON, got %v", err)
	}
}

//go:embed testdata/contract_body_aliases.yaml
var contractBodyAliasesYAML []byte

type contractBodyAliasCase struct {
	Name   string `yaml:"name"`
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
	Body   string `yaml:"body"`
	Expect string `yaml:"expect"`
}

func loadContractBodyAliases(t *testing.T) []contractBodyAliasCase {
	t.Helper()
	cases, err := decodeFixtureRows[contractBodyAliasCase](contractBodyAliasesYAML)
	if err != nil {
		t.Fatalf("load the contract body alias fixture: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		key := c.Method + " " + c.Path
		if present[key] {
			t.Fatalf("the contract body alias fixture repeats operation %q", key)
		}
		if strings.TrimSpace(c.Expect) == "" {
			t.Fatalf("alias case %q expects nothing; name the refused alias", c.Name)
		}
		present[key] = true
	}
	for _, required := range requiredContractBodyOperations {
		if !present[required] {
			t.Fatalf("the contract body alias fixture omits required operation %q; every enforced operation needs an alias case", required)
		}
	}
	return cases
}

// TestContractBody_ValidatorRefusesCaseVariantKeys: a key that differs from a
// declared field only in case is refused by the validator itself, so the
// decoded object can never differ from the validated one.
func TestContractBody_ValidatorRefusesCaseVariantKeys(t *testing.T) {
	v := moduleValidator{}
	for _, c := range loadContractBodyAliases(t) {
		t.Run(c.Name, func(t *testing.T) {
			err := v.ValidateBody(c.Method, c.Path, []byte(c.Body))
			if err == nil {
				t.Fatalf("alias body accepted")
			}
			if !errors.Is(err, ErrSchemaInvalid) {
				t.Fatalf("error is not ErrSchemaInvalid: %v", err)
			}
			if !strings.Contains(err.Error(), c.Expect) {
				t.Fatalf("violation %q does not contain %q", err.Error(), c.Expect)
			}
		})
	}
}

// contractBodyTestUser is the signed-in caller every handler-level case uses.
var contractBodyTestUser = uuid.MustParse("7d5c2a10-9b3e-4c8f-a1d2-3e4f5a6b7c8d")

// contractBodyRouter mounts the nine enforced handlers at their production
// patterns under /api/v1 so chi.URLParam works as in production. Every
// database lookup that precedes the body decode is stubbed to succeed as an
// owner; every lookup after the decode is left unstubbed, so the mock panics
// (and the test fails) if a malformed body reaches the database.
func contractBodyRouter(t *testing.T) http.Handler {
	t.Helper()
	q := &mockQuerier{
		getGroupMember: memberStub("owner"),
		getTranscriptByID: func(ctx context.Context, id pgtype.UUID) (sqlc.Transcript, error) {
			return sqlc.Transcript{ID: id, OwnerID: pgtype.UUID{Bytes: contractBodyTestUser, Valid: true}, LocalID: "local"}, nil
		},
	}
	fake := &fakeGitHub{}
	newFakeGitHub(t, fake)
	h := newRepoHandler(t, q, fake)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(withUserID(req.Context(), contractBodyTestUser)))
		})
	})
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/groups", h.CreateGroup)
		r.Patch("/groups/{id}", h.UpdateGroup)
		r.Post("/groups/{id}/members", h.AddGroupMember)
		r.Patch("/groups/{id}/members/{userID}/role", h.PromoteMember)
		r.Post("/groups/{id}/repositories", h.LinkRepository)
		r.Post("/groups/{id}/shares", h.BatchShareProject)
		r.Patch("/groups/{id}/shares", h.BatchReviewShares)
		r.Patch("/groups/{id}/shares/{transcriptID}", h.ReviewShare)
		r.Post("/transcripts/{id}/share", h.ShareTranscript)
	})
	return r
}

// contractBodyTarget turns a contract path into a concrete request target.
func contractBodyTarget(path string) string {
	replacer := strings.NewReplacer(
		"{id}", testGroupID,
		"{userID}", "5a6b7c8d-9e0f-4a1b-8c2d-3e4f5a6b7c8d",
		"{transcriptID}", "3f0d8b1e-2c4a-4f6e-9a1b-7c2d3e4f5a6b",
	)
	return replacer.Replace(path)
}

func TestContractBody_HandlersAnswer400BeforeTouchingTheDatabase(t *testing.T) {
	router := contractBodyRouter(t)
	for _, c := range loadContractBodyOperations(t) {
		for _, m := range c.Malformed {
			t.Run(c.Name+"/"+m.Name, func(t *testing.T) {
				req := httptest.NewRequest(c.Method, contractBodyTarget(c.Path), strings.NewReader(m.Body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
				}
				body := rec.Body.String()
				if !strings.Contains(body, "request body failed contract validation: ") {
					t.Fatalf("body lacks the contract prefix: %s", body)
				}
				if !strings.Contains(body, strings.ReplaceAll(m.Expect, `"`, `\"`)) && !strings.Contains(body, m.Expect) {
					t.Fatalf("body %q does not name the violation %q", body, m.Expect)
				}
			})
		}
	}
}

// TestContractBody_HandlersRefuseCaseVariantKeysBeforeDecode drives each alias
// body through the production handler. The mock panics on any lookup after the
// decode and the share route's publish lock needs a pool the test does not
// give it, so a 400 here proves the refused value reached neither a write nor
// a lock.
func TestContractBody_HandlersRefuseCaseVariantKeysBeforeDecode(t *testing.T) {
	router := contractBodyRouter(t)
	for _, c := range loadContractBodyAliases(t) {
		t.Run(c.Name, func(t *testing.T) {
			req := httptest.NewRequest(c.Method, contractBodyTarget(c.Path), strings.NewReader(c.Body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, strings.ReplaceAll(c.Expect, `"`, `\"`)) {
				t.Fatalf("body %q does not name the alias %q", body, c.Expect)
			}
		})
	}
}

func TestContractBody_TrailingBytesAnswer400(t *testing.T) {
	router := contractBodyRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/groups", strings.NewReader(`{"name":"x"} trailing`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Invalid request body") {
		t.Fatalf("status = %d body = %s; want 400 Invalid request body", rec.Code, rec.Body.String())
	}
}

// TestContractBody_UndeclaredOperationAnswers500 mounts a handler that names
// an operation the served contract gives no body, the wiring mistake the 500
// exists for, and proves the answer through the HTTP boundary.
func TestContractBody_UndeclaredOperationAnswers500(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)
	r := chi.NewRouter()
	r.Post("/api/v1/groups/{id}/join", func(w http.ResponseWriter, req *http.Request) {
		var body struct{}
		if !h.decodeContractBody(w, req, ContractOperation{Method: "POST", Path: "/api/v1/groups/{id}/join"}, &body) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/groups/"+testGroupID+"/join", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "contract wiring error") {
		t.Fatalf("status = %d body = %s; want 500 contract wiring error", rec.Code, rec.Body.String())
	}
}

func TestContractBody_HandlersKeepInvalidJSONWording(t *testing.T) {
	router := contractBodyRouter(t)
	for _, c := range loadContractBodyOperations(t) {
		t.Run(c.Name, func(t *testing.T) {
			req := httptest.NewRequest(c.Method, contractBodyTarget(c.Path), strings.NewReader(`{"name":`))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Invalid request body") {
				t.Fatalf("status = %d body = %s; want 400 Invalid request body", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestContractBody_OversizedBodyAnswers413(t *testing.T) {
	router := contractBodyRouter(t)
	huge := `{"name":"` + strings.Repeat("x", maxContractBodyBytes) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/groups", strings.NewReader(huge))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body: %s)", rec.Code, rec.Body.String())
	}
}

// brokenValidator stands in for a validator whose served contract failed to
// compile: every call reports the compile error rather than a body verdict.
type brokenValidator struct{ err error }

func (b brokenValidator) ValidatePublish([]byte) error              { return b.err }
func (b brokenValidator) ValidateAnnotation([]byte) error           { return b.err }
func (b brokenValidator) ValidateBody(string, string, []byte) error { return b.err }

func TestContractBody_ValidatorFailureFailsClosedWithoutInternalText(t *testing.T) {
	orig := payloadValidator
	internal := errors.New("compile the POST /api/v1/groups request body schema: contract://village-api/openapi.json#/paths: boom")
	payloadValidator = func() PayloadValidator { return brokenValidator{err: internal} }
	t.Cleanup(func() { payloadValidator = orig })
	router := contractBodyRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/groups", strings.NewReader(`{"name":"x"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body: %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "contract://") || strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("body leaks internal text: %s", rec.Body.String())
	}
}

func TestContractBody_NilValidatorFailsClosed(t *testing.T) {
	orig := payloadValidator
	payloadValidator = func() PayloadValidator { return nil }
	t.Cleanup(func() { payloadValidator = orig })
	router := contractBodyRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/groups", strings.NewReader(`{"name":"x"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body: %s)", rec.Code, rec.Body.String())
	}
}
