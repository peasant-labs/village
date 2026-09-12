package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/peasant-labs/schema"
)

// maxContractBodyBytes caps a JSON request body on the enforced operations.
// A batch of transcript ids is a few dozen bytes per id; a megabyte is far
// beyond any real selection and stops a client from streaming an unbounded
// body into the validator.
const maxContractBodyBytes = 1 << 20

// readContractBody reads the JSON body under the size cap and validates it
// against the served contract's schema for op. On any refusal it has already
// written the response and returns false. Invalid JSON keeps the existing
// "Invalid request body" answer; a contract violation answers 400 with the
// rendered violation; an operation the contract gives no body answers 500
// because that is a wiring error, not a client error.
func (h *Handler) readContractBody(w http.ResponseWriter, r *http.Request, op ContractOperation) ([]byte, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxContractBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("request body exceeds the %d byte limit", maxContractBodyBytes))
			return nil, false
		}
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return nil, false
	}
	if !json.Valid(raw) {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return nil, false
	}
	v := payloadValidator()
	if v == nil {
		writeError(w, http.StatusServiceUnavailable, "request validation unavailable")
		return nil, false
	}
	if err := v.ValidateBody(op.Method, op.Path, raw); err != nil {
		if errors.Is(err, ErrContractBodyUndeclared) {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("contract wiring error: the served contract declares no request body for %s", op))
			return nil, false
		}
		writeError(w, http.StatusBadRequest, "request body failed contract validation: "+strings.TrimPrefix(err.Error(), ErrSchemaInvalid.Error()+": "))
		return nil, false
	}
	return raw, true
}

// decodeContractBody is readContractBody followed by a decode into dst.
func (h *Handler) decodeContractBody(w http.ResponseWriter, r *http.Request, op ContractOperation, dst any) bool {
	raw, ok := h.readContractBody(w, r, op)
	if !ok {
		return false
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return false
	}
	return true
}

// ContractOperation names one operation of the served Village API by method
// and path exactly as the contract spells them. Handlers pass their operation
// explicitly rather than reading a route pattern from the request, because
// the integration suite invokes handlers directly and those requests carry no
// chi route pattern.
type ContractOperation struct {
	Method string
	Path   string
}

func (op ContractOperation) String() string { return op.Method + " " + op.Path }

var (
	opCreateGroup           = ContractOperation{Method: "POST", Path: "/api/v1/groups"}
	opUpdateGroup           = ContractOperation{Method: "PATCH", Path: "/api/v1/groups/{id}"}
	opAddGroupMember        = ContractOperation{Method: "POST", Path: "/api/v1/groups/{id}/members"}
	opUpdateGroupMemberRole = ContractOperation{Method: "PATCH", Path: "/api/v1/groups/{id}/members/{userID}/role"}
	opLinkGroupRepository   = ContractOperation{Method: "POST", Path: "/api/v1/groups/{id}/repositories"}
	opBatchShareProject     = ContractOperation{Method: "POST", Path: "/api/v1/groups/{id}/shares"}
	opBatchReviewShares     = ContractOperation{Method: "PATCH", Path: "/api/v1/groups/{id}/shares"}
	opReviewShare           = ContractOperation{Method: "PATCH", Path: "/api/v1/groups/{id}/shares/{transcriptID}"}
	opShareTranscript       = ContractOperation{Method: "POST", Path: "/api/v1/transcripts/{id}/share"}
)

// ContractEnforcedOperations lists every operation whose request body the
// handlers validate against the served contract. The router drift gate
// asserts each is mounted at exactly this method and path; the handler tests
// assert each has a compiled body schema and a fixture row.
func ContractEnforcedOperations() []ContractOperation {
	return []ContractOperation{
		opCreateGroup,
		opUpdateGroup,
		opAddGroupMember,
		opUpdateGroupMemberRole,
		opLinkGroupRepository,
		opBatchShareProject,
		opBatchReviewShares,
		opReviewShare,
		opShareTranscript,
	}
}

// ErrContractBodyUndeclared reports a lookup for an operation the served
// contract gives no JSON request body. Reaching it from a handler is a wiring
// error, never a client error.
var ErrContractBodyUndeclared = errors.New("the served contract declares no JSON request body for this operation")

// contractSpecURL is the resource name the compiler files the served document
// under. It is not a file URL, so no local path can appear in a violation.
const contractSpecURL = "contract://village-api/openapi.json"

var contractPathParameter = regexp.MustCompile(`\{[^}]*\}`)

// contractOperationKey identifies an operation by method and by path with
// parameter names erased, so a handler's {id} and the contract's {groupId}
// meet.
func contractOperationKey(method, path string) string {
	return strings.ToUpper(method) + " " + contractPathParameter.ReplaceAllString(path, "{}")
}

type contractBodySchemas struct {
	byOperation map[string]*jsonschema.Schema
	err         error
}

var (
	contractBodiesOnce sync.Once
	contractBodies     contractBodySchemas
)

// loadContractBodySchemas parses the served document once and compiles the
// JSON request-body schema of every operation that declares one, through the
// contract module's canonical compiler. Served and enforced are one byte
// source. Multipart bodies (publish) have no JSON schema here.
func loadContractBodySchemas() contractBodySchemas {
	raw := schema.VillageAPISpecJSON()
	var doc struct {
		Paths map[string]map[string]struct {
			RequestBody *struct {
				Content map[string]struct {
					Schema json.RawMessage `json:"schema"`
				} `json:"content"`
			} `json:"requestBody"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return contractBodySchemas{err: fmt.Errorf("parse the served contract: %w", err)}
	}
	compiler := schema.NewJSONSchemaCompiler()
	if err := compiler.AddResource(contractSpecURL, bytes.NewReader(raw)); err != nil {
		return contractBodySchemas{err: fmt.Errorf("register the served contract with the compiler: %w", err)}
	}
	table := map[string]*jsonschema.Schema{}
	for path, operations := range doc.Paths {
		for method, operation := range operations {
			if operation.RequestBody == nil {
				continue
			}
			content, ok := operation.RequestBody.Content["application/json"]
			if !ok || len(content.Schema) == 0 {
				continue
			}
			var ref struct {
				Ref string `json:"$ref"`
			}
			_ = json.Unmarshal(content.Schema, &ref)
			location := ref.Ref
			if location == "" {
				location = "#/paths/" + jsonPointerEscape(path) + "/" + method + "/requestBody/content/application~1json/schema"
			}
			compiled, err := compiler.Compile(contractSpecURL + location)
			if err != nil {
				return contractBodySchemas{err: fmt.Errorf("compile the %s %s request body schema: %w", strings.ToUpper(method), path, err)}
			}
			table[contractOperationKey(method, path)] = compiled
		}
	}
	return contractBodySchemas{byOperation: table}
}

func jsonPointerEscape(segment string) string {
	return strings.ReplaceAll(strings.ReplaceAll(segment, "~", "~0"), "/", "~1")
}

// ValidateBody implements PayloadValidator over the served document's
// request-body schemas.
func (moduleValidator) ValidateBody(method, path string, raw []byte) error {
	contractBodiesOnce.Do(func() { contractBodies = loadContractBodySchemas() })
	if contractBodies.err != nil {
		return contractBodies.err
	}
	compiled, ok := contractBodies.byOperation[contractOperationKey(method, path)]
	if !ok {
		return fmt.Errorf("%w: %s %s", ErrContractBodyUndeclared, strings.ToUpper(method), path)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%w: the body is not valid JSON", ErrSchemaInvalid)
	}
	if err := compiled.Validate(value); err != nil {
		return fmt.Errorf("%w: %s", ErrSchemaInvalid, renderContractViolation(err))
	}
	return nil
}

// renderContractViolation turns the compiler's error tree into
// "<instance pointer>: <message>" per failing leaf, joined by "; ", with the
// root pointer rendered as "/". Schema locations and resource URLs are left
// out so the text names the client's field, never the server's files.
func renderContractViolation(err error) string {
	var violation *jsonschema.ValidationError
	if !errors.As(err, &violation) {
		return err.Error()
	}
	seen := map[string]bool{}
	var leaves []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			where := e.InstanceLocation
			if where == "" {
				where = "/"
			}
			line := where + ": " + e.Message
			if !seen[line] {
				seen[line] = true
				leaves = append(leaves, line)
			}
			return
		}
		for _, cause := range e.Causes {
			walk(cause)
		}
	}
	walk(violation)
	sort.Strings(leaves)
	return strings.Join(leaves, "; ")
}
