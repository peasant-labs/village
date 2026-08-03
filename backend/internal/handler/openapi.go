package handler

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/peasant-labs/schema"
)

// OpenAPI serve + enforce. The village imports the contract MODULE
// (github.com/peasant-labs/schema) rather than vendoring spec bytes: it SERVES the
// Village API spec at GET /api/v1/openapi.json from schema.VillageAPISpecJSON(), and
// ENFORCES the publish "metadata" body through schema.ValidatePublishRequest. Both read
// the SAME in-module bytes (the version-aware accessor + the schema it compiles), so the
// served document and the enforced schema are one byte-source and cannot drift, and both
// follow the go.mod pin automatically — there is no vendored copy to re-sync.
//
// The publish request schema is harness-keyed (a BestiaryHarness enum), so it rejects
// pre-rename values ("claude"/"gemini") at both model.harness and entries[].harness — by
// design. The handler reconciles legacy clients via normalizeMetadataHarnessKey
// (migrator.go), which canonicalizes both surfaces BEFORE schema validation; the schema
// is not loosened. See transcripts_test.go (TestPublishTranscript_EntryHarnessPreRename_Normalized)
// for the entry-harness case.

// PayloadValidator validates a decoded request body against the contract schema.
// A non-nil error means the body is invalid and the handler must reject it (422).
type PayloadValidator interface {
	ValidatePublish(raw []byte) error
	ValidateAnnotation(raw []byte) error
}

// ErrSchemaInvalid wraps a schema-validation failure for the enforce path. The publish
// handler prefixes "metadata failed schema validation: " onto err.Error(); re-wrapping the
// module's validation error here keeps the 422 body byte-identical to the pre-swap path
// (peasant's cross-repo verdict tests couple on that wording).
var ErrSchemaInvalid = errors.New("request body failed OpenAPI schema validation")

// moduleValidator delegates publish validation to the contract module's single
// byte-source JSON-Schema and preserves the 422 body via the ErrSchemaInvalid wrap.
type moduleValidator struct{}

var _ PayloadValidator = moduleValidator{}

// ValidatePublish validates a publish "metadata" PublishRequest JSON body against the
// contract module's single byte-source schema (types + enums, e.g. source.format ∈
// {jsonl,json}, the harness enum, and the SchemaLicense menu).
func (moduleValidator) ValidatePublish(raw []byte) error {
	if err := schema.ValidatePublishRequest(raw); err != nil {
		return fmt.Errorf("%w: %v", ErrSchemaInvalid, err)
	}
	return nil
}

// ValidateAnnotation enforces the published annotation-push operation contract.
// The module validates generated JSON Schema and its typed exclusive-target rules
// from the same tagged contract that Village serves at /openapi.json.
func (moduleValidator) ValidateAnnotation(raw []byte) error {
	if err := schema.ValidateAnnotationPushRequest(raw); err != nil {
		return fmt.Errorf("%w: %v", ErrSchemaInvalid, err)
	}
	return nil
}

// payloadValidator returns the process validator. It stays a package-level var so a test
// can swap a nil-returning stub to drive the publish handler's fail-closed 503 branch;
// the production value is never nil (the module owns compilation). A test that swaps it
// MUST run sequentially (not t.Parallel) and restore via t.Cleanup.
var payloadValidator = func() PayloadValidator { return moduleValidator{} }

// ServeOpenAPI serves the contract module's Village API OpenAPI spec as JSON.
func (h *Handler) ServeOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(schema.VillageAPISpecJSON())
}
