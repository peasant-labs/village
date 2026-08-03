package handler

// OpenAPI serve + enforce tests. The village serves the Village API spec from
// schema.VillageAPISpecJSON() and enforces publish bodies through
// schema.ValidatePublishRequest — one contract-module byte-source, no vendored copy.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// TestServeOpenAPI_ReturnsSpec: GET the served spec returns a parseable OpenAPI
// document (object with an "openapi" or "info" key), not the 501 stub.
func TestServeOpenAPI_ReturnsSpec(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil)
	w := httptest.NewRecorder()

	h.ServeOpenAPI(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("ServeOpenAPI status: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var spec map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatalf("served spec is not valid JSON: %v", err)
	}
	if _, hasOpenAPI := spec["openapi"]; !hasOpenAPI {
		if _, hasInfo := spec["info"]; !hasInfo {
			t.Errorf("served spec missing both 'openapi' and 'info' keys: keys=%v", keysOf(spec))
		}
	}
}

func keysOf(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// TestPublishEnforce_RejectsSchemaInvalid verifies that metadata which passes
// the handler field checks but violates the schema module's embedded
// PublishRequest contract is rejected with 422 by the enforcement step.
func TestPublishEnforce_RejectsSchemaInvalid(t *testing.T) {
	mq := &mockQuerier{
		getTranscriptIDByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
			return pgtype.UUID{}, errFakeNotFound
		},
		createTranscript: func(context.Context, sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
			return sqlc.Transcript{}, nil
		},
	}
	h := newTestHandler(mq, &mockTranscriptBlobStore{})

	// Valid harness + model + sessionId pass handler field checks, but the
	// embedded PublishRequest contract must reject an out-of-menu source format.
	metadata := map[string]any{
		"identity":  map[string]any{"sessionId": "550e8400-e29b-41d4-a716-446655440000", "schemaVersion": 2},
		"model":     map[string]any{"harness": "claude-code", "model": "claude-opus-4-5"},
		"timestamp": map[string]any{"start": 1700000000000, "end": 1700000060000},
		"source":    map[string]any{"filePath": "/p/t.jsonl", "format": "xml"},
	}
	metaJSON, _ := json.Marshal(metadata)
	body, boundary := multipartBody(t, map[string]string{"metadata": string(metaJSON)}, `{"contractVersion":"0.1.0","kind":"session_detail","sessionDetail":{"id":"s","harness":"claude-code","turns":[]}}`)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(withTestUser(r.Context()))
	w := httptest.NewRecorder()

	h.PublishTranscript(w, r)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("schema-invalid publish: got %d, want 422 (body: %s)", w.Code, w.Body.String())
	}
}

func TestPublishTranscript_UnknownHarnessRejectsAsSchemaEnum422(t *testing.T) {
	if payloadValidator() == nil {
		t.Fatal("OpenAPI validator unavailable — harness enum 422 cannot be exercised")
	}

	mq := &mockQuerier{
		getTranscriptIDByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
			return pgtype.UUID{}, errFakeNotFound
		},
		createTranscript: func(context.Context, sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
			return sqlc.Transcript{}, nil
		},
	}
	h := newTestHandler(mq, &mockTranscriptBlobStore{})

	metadata := map[string]any{
		"identity":  map[string]any{"sessionId": "550e8400-e29b-41d4-a716-446655440000", "schemaVersion": 2},
		"model":     map[string]any{"harness": "totally-made-up", "model": "claude-opus-4-5"},
		"timestamp": map[string]any{"start": 1700000000000, "end": 1700000060000},
		"source":    map[string]any{"filePath": "/p/t.jsonl", "format": "jsonl"},
		"project":   map[string]any{"hash": testProjectHash, "name": "repo"},
	}
	metaJSON, _ := json.Marshal(metadata)
	body, boundary := multipartBody(t, map[string]string{"metadata": string(metaJSON)}, `{"contractVersion":"0.1.0","kind":"session_detail","sessionDetail":{"id":"s","harness":"claude-code","turns":[]}}`)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(withTestUser(r.Context()))
	w := httptest.NewRecorder()

	h.PublishTranscript(w, r)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown harness publish: got %d, want 422 (body: %s)", w.Code, w.Body.String())
	}
	respBody := w.Body.String()
	if !strings.Contains(respBody, "metadata failed schema validation") || !strings.Contains(respBody, "value must be one of") {
		t.Fatalf("unknown harness publish body = %s, want schema enum rejection body", respBody)
	}
	// Order-independent, self-updating drift tripwire: every harness the contract
	// enumerates must be named in the 422 body. Derive from schema.Harnesses() (the
	// FULL bestiary set the publish-request harness enum is generated from), NOT
	// schema.AllHarnesses (only the ingestion-supported subset) — the latter would
	// still pass if a non-subset harness were silently dropped from the enum.
	for _, hn := range schema.Harnesses() {
		if !strings.Contains(respBody, string(hn)) {
			t.Errorf("harness-422 body does not name enum value %q: %s", hn, respBody)
		}
	}
}

// TestPublishTranscript_BadLicenseRejectsAsSchemaEnum422 pins the ACTUAL 422 the
// peasant client sees for an off-menu publish license: the response body must carry
// the "metadata failed schema validation" prefix AND the verbatim SchemaLicense menu
// clause. This is the handler-level counterpart of the validator-level pin in
// openapi_license_guard_test.go — the bytes a cross-repo client couples on.
func TestPublishTranscript_BadLicenseRejectsAsSchemaEnum422(t *testing.T) {
	if payloadValidator() == nil {
		t.Fatal("OpenAPI validator unavailable — license enum 422 cannot be exercised")
	}

	mq := &mockQuerier{
		getTranscriptIDByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
			return pgtype.UUID{}, errFakeNotFound
		},
		createTranscript: func(context.Context, sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
			return sqlc.Transcript{}, nil
		},
	}
	h := newTestHandler(mq, &mockTranscriptBlobStore{})

	metadata := map[string]any{
		"identity":  map[string]any{"sessionId": "550e8400-e29b-41d4-a716-446655440000", "schemaVersion": 2},
		"model":     map[string]any{"harness": "claude-code", "model": "claude-opus-4-5"},
		"timestamp": map[string]any{"start": 1700000000000, "end": 1700000060000},
		"source":    map[string]any{"filePath": "/p/t.jsonl", "format": "jsonl"},
		"project":   map[string]any{"hash": testProjectHash, "name": "repo"},
		"license":   "MIT",
	}
	metaJSON, _ := json.Marshal(metadata)
	body, boundary := multipartBody(t, map[string]string{"metadata": string(metaJSON)}, `{"contractVersion":"0.1.0","kind":"session_detail","sessionDetail":{"id":"s","harness":"claude-code","turns":[]}}`)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(withTestUser(r.Context()))
	w := httptest.NewRecorder()

	h.PublishTranscript(w, r)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("off-menu license publish: got %d, want 422 (body: %s)", w.Code, w.Body.String())
	}
	// Decode the JSON error envelope: writeError JSON-encodes the message, so the
	// menu clause's quotes are escaped on the wire; the decoded field is the message
	// a client parses.
	var envelope struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("422 body is not a JSON error envelope: %v (body: %s)", err, w.Body.String())
	}
	if !strings.Contains(envelope.Error, "metadata failed schema validation") {
		t.Errorf("bad-license 422 error lost the schema-validation prefix: %s", envelope.Error)
	}
	if !strings.Contains(envelope.Error, wantLicenseMenu) {
		t.Errorf("bad-license 422 error lost the verbatim license menu clause %q: %s", wantLicenseMenu, envelope.Error)
	}
}

func TestPublishTranscript_MalformedMetadataStillRejects400(t *testing.T) {
	tests := []struct {
		name     string
		metadata string
	}{
		{name: "invalid JSON syntax", metadata: "{invalid json"},
		{name: "valid JSON non-object", metadata: `[]`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler(&mockQuerier{}, nil)
			body, boundary := multipartBody(t, map[string]string{"metadata": tc.metadata}, "")

			r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
			r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
			r = r.WithContext(withTestUser(r.Context()))
			w := httptest.NewRecorder()

			h.PublishTranscript(w, r)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("malformed metadata: got %d, want 400 (body: %s)", w.Code, w.Body.String())
			}
		})
	}
}

// wellFormedPublishMetadata is a schema-valid PublishRequest "metadata" body (the
// shape the village enforces). Used as the accept baseline; the reject cases below
// mutate one field at a time.
const wellFormedPublishMetadata = `{
  "identity": {"sessionId": "550e8400-e29b-41d4-a716-446655440000", "schemaVersion": 2},
  "model": {"harness": "claude-code", "model": "claude-opus-4-5"},
  "timestamp": {"start": 1700000000000, "end": 1700000060000},
  "source": {"filePath": "/p/t.jsonl", "format": "jsonl"},
  "project": {"hash": "` + testProjectHash + `", "name": "repo"}
}`

// TestValidatePublish_SchemaVerdicts drives the schema module's embedded
// PublishRequest validator, which is the handler's 422 enforcement path. A
// well-formed body is accepted; missing required model fields, wrong types,
// out-of-menu harnesses, source formats, and licenses are rejected by the
// published contract.
func TestValidatePublish_SchemaVerdicts(t *testing.T) {
	v := payloadValidator()
	if v == nil {
		t.Skip("OpenAPI validator unavailable (embedded contract failed to compile)")
	}

	cases := []struct {
		name       string
		body       string
		wantAccept bool
	}{
		{"well-formed", wellFormedPublishMetadata, true},
		// enum violation: source.format ∉ {jsonl,json}.
		{"bad-source-format", `{"source": {"filePath": "/p/t", "format": "xml"}}`, false},
		// out-of-enum harness: the derived schema is harness-keyed (BestiaryHarness
		// enum), so an unknown harness is now a 422 — a tightening over the retired
		// flat `type:string` legacy schema.
		{"unknown-harness", `{"model": {"harness": "totally-made-up", "model": "x"}}`, false},
		// omitted harness is now REJECTED: SchemaModelInfo.required=["harness","model"]
		// makes an omitted harness key a 422 missing-required violation. This
		// compatibility hole is closed. Mirrors TestPublishTranscript_MissingModelHarness.
		{"omitted-harness-schema-rejects", `{"model": {"model": "x"}}`, false},
		// omitted model KEY within model is rejected: SchemaModelInfo.required=["harness","model"]
		// makes an absent model field a 422 (this was the old hand-written "model is required"
		// 400 guard, now unified into the schema layer). Mirrors TestPublishTranscript_MissingModel.
		{"omitted-model-key-schema-rejects", `{"model": {"harness": "claude-code"}}`, false},
		// omitted model OBJECT entirely is rejected: SchemaPublishRequest.required=["model"]
		// makes an absent model object a top-level 422 missing-required violation.
		{"omitted-model-object-schema-rejects", `{"identity": {"sessionId": "550e8400-e29b-41d4-a716-446655440000", "schemaVersion": 2}}`, false},
		// wrong type: timestamp.start must be an integer, not a string.
		{"wrong-type-start", `{"timestamp": {"start": "soon", "end": 1700000060000}}`, false},
		// wrong type: entries must be an array, not an object.
		{"wrong-type-entries", `{"entries": {"not": "an array"}}`, false},
		// pattern violation on the constrained ProjectHash newtype.
		{"bad-project-hash", `{"project": {"hash": "tooshort", "name": "x"}}`, false},
		// License is optional but constrained by the Schema license menu: a
		// published value is accepted and an out-of-menu value is rejected.
		// Mirrors peasant's publish verdict corpus (valid-license / bad-license).
		{"valid-license", `{"model": {"harness": "claude-code", "model": "x"}, "license": "CC-BY-4.0"}`, true},
		{"bad-license", `{"model": {"harness": "claude-code", "model": "x"}, "license": "MIT"}`, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := v.ValidatePublish([]byte(tc.body))
			if tc.wantAccept && err != nil {
				t.Errorf("well-formed body rejected by embedded contract: %v\nbody: %s", err, tc.body)
			}
			if !tc.wantAccept && err == nil {
				t.Errorf("malformed body accepted by embedded contract (expected 422-class rejection)\nbody: %s", tc.body)
			}
			if !tc.wantAccept && err != nil && !errors.Is(err, ErrSchemaInvalid) {
				t.Errorf("rejection did not wrap ErrSchemaInvalid (the 422 sentinel): %v", err)
			}
		})
	}
}

// TestServeOpenAPI_ServesModuleSpec pins that GET /api/v1/openapi.json returns
// exactly schema.VillageAPISpecJSON() (the contract module is the served SSOT — no
// vendored copy can drift), and that the served document declares the SchemaLicense
// menu with the module's AllLicenses values.
func TestServeOpenAPI_ServesModuleSpec(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil)
	w := httptest.NewRecorder()

	h.ServeOpenAPI(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("ServeOpenAPI status: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), schema.VillageAPISpecJSON()) {
		t.Fatal("served spec != schema.VillageAPISpecJSON() — the served document must be the module's bytes verbatim, with no vendored copy to drift")
	}

	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Enum []string `json:"enum"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("served spec is not valid JSON: %v", err)
	}
	got := doc.Components.Schemas["SchemaLicense"].Enum
	if len(got) == 0 {
		t.Fatal("served spec has no components.schemas.SchemaLicense enum — the license menu is missing from the served contract")
	}
	want := make(map[string]bool, len(schema.AllLicenses))
	for _, l := range schema.AllLicenses {
		want[string(l)] = true
	}
	if len(got) != len(want) {
		t.Fatalf("served SchemaLicense enum %v does not match schema.AllLicenses %v", got, schema.AllLicenses)
	}
	for _, v := range got {
		if !want[v] {
			t.Errorf("served SchemaLicense enum names %q, absent from schema.AllLicenses %v", v, schema.AllLicenses)
		}
	}
}

// wantVillageAPIVersion is the contract version the village serves + enforces. It is
// the consumer-side pin on the module's schema.VillageAPIVersion.
const wantVillageAPIVersion = "0.12.0"

// TestPinnedContractVersion_MatchesExpected asserts the pinned schema module reports
// the contract version the village expects. The schema repo's go-apidiff gate
// deliberately EXEMPTS the exported VillageAPIVersion const (an intentional version
// MARKER — pin target + golden trigger, not an API-surface change), so a re-pin that
// moves the contract version slips past the upstream gate. Detection therefore lives
// at the consumer — the village owns it because it serves VillageAPISpecJSON() and
// enforces ValidatePublishRequest, both VillageAPIVersion-keyed.
func TestPinnedContractVersion_MatchesExpected(t *testing.T) {
	if schema.VillageAPIVersion != wantVillageAPIVersion {
		t.Fatalf("pinned schema module reports VillageAPIVersion %q, want %q — a re-pin moved the "+
			"contract version under the village. The module's go-apidiff gate EXEMPTS the "+
			"VillageAPIVersion stamp, so this consumer-side test owns the drift detection. If "+
			"intended: bump wantVillageAPIVersion, inspect the served/enforced Village API spec "+
			"(menu / required arrays / manifest shape may have moved), and update the version-bump "+
			"docs. If NOT intended: pin the correct module tag.",
			schema.VillageAPIVersion, wantVillageAPIVersion)
	}
}

var errFakeNotFound = errors.New("not found")

// testProjectHash is a schema-valid 64-hex ProjectHash (the real contract: a
// SHA-256 hex digest). Publish tests that reach OpenAPI enforcement must use it
// rather than a short placeholder, which the embedded contract rejects.
const testProjectHash = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"

// guard: keep pgtype import used even if helpers change.
var _ = pgtype.UUID{}
