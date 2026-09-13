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

// contractBody is a request body the served contract has accepted.
type contractBody struct {
	// Declared is the body re-encoded with only the fields the served contract
	// declares, so a decoder that matches keys without regard to case cannot
	// bind anything the validator did not see.
	Declared []byte
	// Undeclared lists the keys the body carried that the contract does not
	// declare, as JSON pointers in sorted depth-first order (array elements in
	// index order), for handlers that refuse them rather than ignore them.
	Undeclared []string
}

// readContractBody reads the JSON body under the size cap and validates it
// against the served contract's schema for op. On any refusal it has already
// written the response and returns false. Invalid JSON keeps the existing
// "Invalid request body" answer (json.Valid also refuses trailing bytes after
// the value, which the streaming decoder used to ignore); a contract
// violation answers 400 with the rendered violation; an operation the contract
// gives no body answers 500 because that is a wiring error, not a client
// error; a validator that cannot compile the served contract answers 503, the
// same fail-closed answer as a missing validator, so no internal text reaches
// a client.
func (h *Handler) readContractBody(w http.ResponseWriter, r *http.Request, op ContractOperation) (contractBody, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxContractBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("request body exceeds the %d byte limit", maxContractBodyBytes))
			return contractBody{}, false
		}
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return contractBody{}, false
	}
	if !json.Valid(raw) {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return contractBody{}, false
	}
	v := payloadValidator()
	if v == nil {
		writeError(w, http.StatusServiceUnavailable, "request validation unavailable")
		return contractBody{}, false
	}
	if err := v.ValidateBody(op.Method, op.Path, raw); err != nil {
		switch {
		case errors.Is(err, ErrContractBodyUndeclared):
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("contract wiring error: the served contract declares no request body for %s", op))
		case errors.Is(err, ErrSchemaInvalid):
			writeError(w, http.StatusBadRequest, "request body failed contract validation: "+strings.TrimPrefix(err.Error(), ErrSchemaInvalid.Error()+": "))
		default:
			writeError(w, http.StatusServiceUnavailable, "request validation unavailable")
		}
		return contractBody{}, false
	}
	body, err := declaredContractBody(op, raw)
	if err != nil {
		if errors.Is(err, ErrContractBodyUndeclared) {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("contract wiring error: the served contract declares no request body for %s", op))
		} else {
			writeError(w, http.StatusServiceUnavailable, "request validation unavailable")
		}
		return contractBody{}, false
	}
	return body, true
}

// decodeContractBody is readContractBody followed by a decode of the declared
// fields into dst.
func (h *Handler) decodeContractBody(w http.ResponseWriter, r *http.Request, op ContractOperation, dst any) bool {
	body, ok := h.readContractBody(w, r, op)
	if !ok {
		return false
	}
	if err := json.Unmarshal(body.Declared, dst); err != nil {
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
			// Compile the schema at its own location in the document, so a
			// bare $ref and an inline schema (and any keyword beside a $ref)
			// are all honoured exactly as the served document states them.
			location := "#/paths/" + jsonPointerEscape(path) + "/" + method + "/requestBody/content/application~1json/schema"
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

// compiledContractBody returns the compiled request-body schema for an
// operation, loading the table on first use.
func compiledContractBody(method, path string) (*jsonschema.Schema, error) {
	contractBodiesOnce.Do(func() { contractBodies = loadContractBodySchemas() })
	if contractBodies.err != nil {
		return nil, contractBodies.err
	}
	compiled, ok := contractBodies.byOperation[contractOperationKey(method, path)]
	if !ok {
		return nil, fmt.Errorf("%w: %s %s", ErrContractBodyUndeclared, strings.ToUpper(method), path)
	}
	return compiled, nil
}

// parseContractJSON parses one JSON value the way the validator and the
// declared-field walk both see it: numbers stay json.Number so an int64
// survives the round trip exactly, and bytes after the value are refused.
func parseContractJSON(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("bytes follow the JSON value")
	}
	return value, nil
}

// ValidateBody implements PayloadValidator over the served document's
// request-body schemas. After the schema accepts the body, a key that differs
// from a declared field only in case is refused too: Go's JSON decoder would
// bind it to the declared field, so the decoded object could differ from the
// validated one.
func (moduleValidator) ValidateBody(method, path string, raw []byte) error {
	compiled, err := compiledContractBody(method, path)
	if err != nil {
		return err
	}
	value, err := parseContractJSON(raw)
	if err != nil {
		return fmt.Errorf("%w: the body is not valid JSON", ErrSchemaInvalid)
	}
	if err := compiled.Validate(value); err != nil {
		return fmt.Errorf("%w: %s", ErrSchemaInvalid, renderContractViolation(err))
	}
	if alias := findCaseVariantKey(value, compiled, ""); alias != "" {
		return fmt.Errorf("%w: %s", ErrSchemaInvalid, alias)
	}
	return nil
}

// declaredContractBody re-encodes an already validated body with only the
// fields the served contract declares, and lists the keys it dropped.
func declaredContractBody(op ContractOperation, raw []byte) (contractBody, error) {
	compiled, err := compiledContractBody(op.Method, op.Path)
	if err != nil {
		return contractBody{}, err
	}
	value, err := parseContractJSON(raw)
	if err != nil {
		return contractBody{}, err
	}
	var undeclared []string
	filtered := filterDeclared(value, compiled, "", &undeclared)
	declared, err := json.Marshal(filtered)
	if err != nil {
		return contractBody{}, err
	}
	return contractBody{Declared: declared, Undeclared: undeclared}, nil
}

// schemaProperties resolves the object properties a compiled schema declares,
// following references and composition keywords, so the declared-field walk
// sees the same fields the validator enforced.
func schemaProperties(s *jsonschema.Schema) map[string]*jsonschema.Schema {
	props := map[string]*jsonschema.Schema{}
	var collect func(s *jsonschema.Schema, depth int)
	collect = func(s *jsonschema.Schema, depth int) {
		if s == nil || depth > 8 {
			return
		}
		for name, ps := range s.Properties {
			if _, seen := props[name]; !seen {
				props[name] = ps
			}
		}
		collect(s.Ref, depth+1)
		for _, group := range [][]*jsonschema.Schema{s.AllOf, s.AnyOf, s.OneOf} {
			for _, sub := range group {
				collect(sub, depth+1)
			}
		}
	}
	collect(s, 0)
	return props
}

// schemaItems resolves the schema an array's elements are validated against,
// or nil when the compiled schema declares none.
func schemaItems(s *jsonschema.Schema) *jsonschema.Schema {
	var find func(s *jsonschema.Schema, depth int) *jsonschema.Schema
	find = func(s *jsonschema.Schema, depth int) *jsonschema.Schema {
		if s == nil || depth > 8 {
			return nil
		}
		if s.Items2020 != nil {
			return s.Items2020
		}
		if item, ok := s.Items.(*jsonschema.Schema); ok {
			return item
		}
		if item := find(s.Ref, depth+1); item != nil {
			return item
		}
		for _, group := range [][]*jsonschema.Schema{s.AllOf, s.AnyOf, s.OneOf} {
			for _, sub := range group {
				if item := find(sub, depth+1); item != nil {
					return item
				}
			}
		}
		return nil
	}
	return find(s, 0)
}

func sortedObjectKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for k := range object {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// findCaseVariantKey reports the first key that differs from a declared field
// only in case, as the sentence the client should read, or "" when there is
// none. Nested objects and array elements are walked the same way.
func findCaseVariantKey(value any, s *jsonschema.Schema, at string) string {
	switch v := value.(type) {
	case map[string]any:
		props := schemaProperties(s)
		declared := make([]string, 0, len(props))
		for name := range props {
			declared = append(declared, name)
		}
		sort.Strings(declared)
		for _, k := range sortedObjectKeys(v) {
			if _, ok := props[k]; ok {
				continue
			}
			for _, name := range declared {
				if strings.EqualFold(k, name) {
					where := ""
					if at != "" {
						where = at + ": "
					}
					return fmt.Sprintf("%skey %q differs only in case from the declared field %q; use the declared spelling", where, k, name)
				}
			}
		}
		for _, k := range sortedObjectKeys(v) {
			if ps, ok := props[k]; ok {
				if msg := findCaseVariantKey(v[k], ps, at+"/"+jsonPointerEscape(k)); msg != "" {
					return msg
				}
			}
		}
	case []any:
		item := schemaItems(s)
		if item == nil {
			return ""
		}
		for i, elem := range v {
			if msg := findCaseVariantKey(elem, item, fmt.Sprintf("%s/%d", at, i)); msg != "" {
				return msg
			}
		}
	}
	return ""
}

// filterDeclared returns value with every object key the schema does not
// declare removed, recording each removed key as a JSON pointer. An object the
// schema declares no properties for is kept whole: there is nothing to bind
// and nothing to strip.
func filterDeclared(value any, s *jsonschema.Schema, at string, undeclared *[]string) any {
	switch v := value.(type) {
	case map[string]any:
		props := schemaProperties(s)
		if len(props) == 0 {
			return v
		}
		out := make(map[string]any, len(v))
		for _, k := range sortedObjectKeys(v) {
			ps, ok := props[k]
			if !ok {
				*undeclared = append(*undeclared, at+"/"+jsonPointerEscape(k))
				continue
			}
			out[k] = filterDeclared(v[k], ps, at+"/"+jsonPointerEscape(k), undeclared)
		}
		return out
	case []any:
		item := schemaItems(s)
		if item == nil {
			return v
		}
		for i := range v {
			v[i] = filterDeclared(v[i], item, fmt.Sprintf("%s/%d", at, i), undeclared)
		}
		return v
	}
	return value
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
