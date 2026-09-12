package handler

import (
	_ "embed"
	"errors"
	"net/http"
	"strings"
	"testing"
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
