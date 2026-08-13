package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/observed_model_preservation/negative.yaml
var observedModelNegativeFixtureYAML []byte

const observedModelNegativeCaseCount = 3

var observedModelNegativeCaseNames = [...]string{
	"production_rewrite_preserves_enriched_evidence",
	"field_drop_withholds_capability_and_refuses_enriched_publish",
	"field_drop_keeps_legacy_publish_compatible",
}
var observedModelNegativeOperations = [...]string{"canonical_production_encoder", "drop_observed_model_at_production_rewrite", "legacy_payload_under_field_drop"}
var fieldDropErrorFragmentNames = [...]string{"evidence_field", "uploaded_evidence", "no_writes", "no_silent_strip", "negotiation_endpoint", "capability_name"}

//go:embed testdata/observed_model_preservation/publish_gate.yaml
var observedModelPublishGateFixtureYAML []byte

type observedModelPublishGateFixture struct {
	Cases []observedModelPublishGateCase `yaml:"cases"`
}

type observedModelPublishGateCase struct {
	Name          string `yaml:"name"`
	Content       string `yaml:"content"`
	WantErrorPart string `yaml:"wantErrorPart"`
}

const observedModelPublishGateCaseCount = 7

var observedModelPublishGateCaseNames = [...]string{"valid_assistant_evidence", "invalid_non_assistant_evidence", "malformed_enriched_input", "present_empty_observed_model", "present_null_observed_model", "legacy_provider_jsonl_trailing_blank_line", "legacy_provider_specific_jsonl_shape"}

type observedModelNegativeFixture struct {
	Cases []observedModelNegativeCase `yaml:"cases"`
}

type observedModelNegativeCase struct {
	Name               string                       `yaml:"name"`
	Operation          string                       `yaml:"operation"`
	WantProofFailure   bool                         `yaml:"wantProofFailure"`
	WantCapability     bool                         `yaml:"wantCapability"`
	WantPublishRefusal bool                         `yaml:"wantPublishRefusal"`
	WantErrorContains  []observedModelErrorFragment `yaml:"wantErrorContains"`
}

type observedModelErrorFragment struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

type droppingObservedModelEncoder struct{}

type countingRewriteEncoder struct {
	delegate   contentRewriteEncoder
	executions int
}

func (e *countingRewriteEncoder) Encode(version schema.PushContractVersion, payload *schema.SessionDetailPayload) ([]byte, error) {
	e.executions++
	return e.delegate.Encode(version, payload)
}

func (droppingObservedModelEncoder) Encode(version schema.PushContractVersion, payload *schema.SessionDetailPayload) ([]byte, error) {
	var document map[string]any
	raw, err := canonicalContentRewriteEncoder{}.Encode(version, payload)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	sessionDetail, _ := document["sessionDetail"].(map[string]any)
	turns, _ := sessionDetail["turns"].([]any)
	for _, rawTurn := range turns {
		if turn, ok := rawTurn.(map[string]any); ok {
			delete(turn, "observedModel")
		}
	}
	return json.Marshal(document)
}

type fixedPreservationEvaluator struct{ err error }

func (e fixedPreservationEvaluator) Evaluate() error { return e.err }

func TestObservedModelProductionPreservation(t *testing.T) {
	fixtures := loadObservedModelNegativeFixtures(t)
	fixtureCase := requireObservedModelNegativeCase(t, fixtures, "production_rewrite_preserves_enriched_evidence")
	err := executeObservedModelPreservationProof(canonicalContentRewriteEncoder{})
	if (err != nil) != fixtureCase.WantProofFailure {
		t.Fatalf("production preservation proof error=%v, want failure=%v", err, fixtureCase.WantProofFailure)
	}
	capabilities := advertisedContentCapabilities()
	if got := hasObservedModelCapability(capabilities); got != fixtureCase.WantCapability {
		t.Fatalf("production capability advertised=%v, want %v; capabilities=%+v", got, fixtureCase.WantCapability, capabilities)
	}
}

func TestObservedModelFieldDropNegative(t *testing.T) {
	fixtures := loadObservedModelNegativeFixtures(t)
	fixtureCase := requireObservedModelNegativeCase(t, fixtures, "field_drop_withholds_capability_and_refuses_enriched_publish")
	encoder := &countingRewriteEncoder{delegate: droppingObservedModelEncoder{}}
	proofErr := executeObservedModelPreservationProof(encoder)
	if encoder.executions != observedModelPreservationCaseCount {
		t.Fatalf("executed baseline count=%d, want exactly %d", encoder.executions, observedModelPreservationCaseCount)
	}
	if (proofErr != nil) != fixtureCase.WantProofFailure {
		t.Fatalf("field-drop preservation proof error=%v, want failure=%v", proofErr, fixtureCase.WantProofFailure)
	}
	if proofErr == nil || !strings.Contains(proofErr.Error(), fixtureCase.WantErrorContains[0].Value) {
		t.Fatalf("field-drop error=%v, want actionable substring %q", proofErr, fixtureCase.WantErrorContains[0].Value)
	}
	outcomes, err := executeObservedModelPreservationProofOutcomes(droppingObservedModelEncoder{})
	if err != nil || len(outcomes) != 2 {
		t.Fatalf("named field-drop outcomes=%+v err=%v", outcomes, err)
	}
	if outcomes[0].Name != "enriched_repeated_change_and_omission" || outcomes[0].Err == nil || !strings.Contains(outcomes[0].Err.Error(), "re-emitted observedModel") {
		t.Fatalf("enriched field-loss outcome=%+v", outcomes[0])
	}
	if outcomes[1].Name != "legacy_without_observations" || outcomes[1].Err != nil {
		t.Fatalf("legacy field-drop outcome=%+v, want exact success", outcomes[1])
	}
	if got := advertisedContentCapabilitiesWithEvaluator(fixedPreservationEvaluator{err: proofErr}); hasObservedModelCapability(got) != fixtureCase.WantCapability {
		t.Fatalf("field-drop capability result=%+v, want advertised=%v", got, fixtureCase.WantCapability)
	}
	enriched := observedModelFixtureContent(t, "enriched_repeated_change_and_omission")
	refusal := requireSupportedContentCapabilityWithEvaluator(enriched, fixedPreservationEvaluator{err: proofErr})
	if (refusal != nil) != fixtureCase.WantPublishRefusal {
		t.Fatalf("field-drop enriched publish refusal=%v, want refusal=%v", refusal, fixtureCase.WantPublishRefusal)
	}
	for _, fragment := range fixtureCase.WantErrorContains[1:] {
		if !strings.Contains(refusal.Error(), fragment.Value) {
			t.Errorf("field-drop refusal lacks actionable fragment %q: %v", fragment.Value, refusal)
		}
	}
}

func TestObservedModelNegativeInventoryRejectsCountPreservingNameSubstitution(t *testing.T) {
	mutated := bytes.Replace(observedModelNegativeFixtureYAML, []byte("field_drop_withholds_capability_and_refuses_enriched_publish"), []byte("field_drop_substituted_name_same_count_xxxxxxxxxxxxxxxxxxxxx"), 1)
	if _, err := decodeObservedModelNegativeFixtures(mutated); err == nil || !strings.Contains(err.Error(), "unregistered") {
		t.Fatalf("count-preserving name substitution error=%v, want unregistered-name rejection", err)
	}
}

func TestObservedModelNegativeInventoryRejectsCountPreservingOperationSubstitution(t *testing.T) {
	mutated := bytes.Replace(observedModelNegativeFixtureYAML, []byte("legacy_payload_under_field_drop"), []byte("substitute_payload_field_drop"), 1)
	if _, err := decodeObservedModelNegativeFixtures(mutated); err == nil || !strings.Contains(err.Error(), "operation") {
		t.Fatalf("operation substitution error=%v", err)
	}
}

func TestObservedModelNegativeInventoryRejectsCountPreservingFragmentSubstitution(t *testing.T) {
	mutated := bytes.Replace(observedModelNegativeFixtureYAML, []byte("capability_name"), []byte("substitute_name"), 1)
	if _, err := decodeObservedModelNegativeFixtures(mutated); err == nil || !strings.Contains(err.Error(), "fragment") {
		t.Fatalf("fragment substitution error=%v", err)
	}
}

func TestObservedModelFieldDropLegacyCompatibility(t *testing.T) {
	fixtures := loadObservedModelNegativeFixtures(t)
	fixtureCase := requireObservedModelNegativeCase(t, fixtures, "field_drop_keeps_legacy_publish_compatible")
	proofErr := executeObservedModelPreservationProof(droppingObservedModelEncoder{})
	legacy := observedModelFixtureContent(t, "legacy_without_observations")
	refusal := requireSupportedContentCapabilityWithEvaluator(legacy, fixedPreservationEvaluator{err: proofErr})
	if (refusal != nil) != fixtureCase.WantPublishRefusal {
		t.Fatalf("legacy publish refusal=%v, want refusal=%v", refusal, fixtureCase.WantPublishRefusal)
	}
}

func TestObservedModelPublishGateUsesTypedTurns(t *testing.T) {
	fixture := loadObservedModelPublishGateFixtures(t)
	for _, fixtureCase := range fixture.Cases {
		t.Run(fixtureCase.Name, func(t *testing.T) { assertMountedObservedModelPublishGate(t, fixtureCase) })
	}

	legacy := observedModelFixtureContent(t, "legacy_without_observations")
	if err := requireSupportedContentCapabilityWithEvaluator(legacy, fixedPreservationEvaluator{err: errors.New("proof unavailable")}); err != nil {
		t.Fatalf("legacy content rejected when preservation proof unavailable: %v", err)
	}
}

func assertMountedObservedModelPublishGate(t *testing.T, fixtureCase observedModelPublishGateCase) {
	t.Helper()
	var probes, creates, scans int
	q := &mockQuerier{
		getTranscriptIDByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
			probes++
			return pgtype.UUID{}, errors.New("not found")
		},
		createTranscript: func(context.Context, sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
			creates++
			return sqlc.Transcript{}, nil
		},
	}
	blobs := &mockTranscriptBlobStore{}
	h := newTestHandler(q, blobs)
	h.scanContent = func(content []byte) []string { scans++; return nil }
	response := mountedObservedModelPublish(t, h, []byte(fixtureCase.Content))
	if fixtureCase.WantErrorPart == "" {
		if response.Code != http.StatusCreated {
			t.Fatalf("mounted valid publish status=%d body=%s", response.Code, response.Body.String())
		}
		if scans != 1 || probes != 1 || creates != 1 || len(blobs.uploadedKeys) != 1 {
			t.Fatalf("valid effects scans=%d probes=%d creates=%d blobs=%d", scans, probes, creates, len(blobs.uploadedKeys))
		}
		if strings.HasPrefix(fixtureCase.Name, "legacy_provider_") {
			store := newFakeBlobStore()
			const key = "transcripts/10000000-0000-4000-8000-000000000001.bin"
			store.put(key, []byte(fixtureCase.Content))
			served := getContent(t, newTestHandler(publicTranscriptQuerier(key), store), mustFixtureUUID(t))
			if served.Code != http.StatusOK {
				t.Fatalf("mounted legacy read status=%d body=%s", served.Code, served.Body.String())
			}
			if strings.Contains(served.Body.String(), "observedModel") {
				t.Fatalf("legacy read fabricated observedModel: %s", served.Body.String())
			}
		}
		return
	}
	if response.Code != http.StatusConflict || !strings.Contains(decodeError(t, response.Body.Bytes()), fixtureCase.WantErrorPart) {
		t.Fatalf("mounted rejection status=%d body=%s want=%q", response.Code, response.Body.String(), fixtureCase.WantErrorPart)
	}
	if scans != 0 || probes != 0 || creates != 0 || len(blobs.uploadedKeys) != 0 || len(blobs.deletedKeys) != 0 {
		t.Fatalf("rejection caused effects scans=%d probes=%d creates=%d writes=%d deletes=%d", scans, probes, creates, len(blobs.uploadedKeys), len(blobs.deletedKeys))
	}

	corrected := observedModelFixtureContent(t, "enriched_repeated_change_and_omission")
	recovered := mountedObservedModelPublish(t, h, corrected)
	if recovered.Code != http.StatusCreated {
		t.Fatalf("corrected publish status=%d body=%s", recovered.Code, recovered.Body.String())
	}
	store := newFakeBlobStore()
	const key = "transcripts/10000000-0000-4000-8000-000000000001.bin"
	store.put(key, corrected)
	served := getContent(t, newTestHandler(publicTranscriptQuerier(key), store), mustFixtureUUID(t))
	if served.Code != http.StatusOK {
		t.Fatalf("corrected mounted read status=%d body=%s", served.Code, served.Body.String())
	}
	var payload schema.SessionDetailPayload
	if err := json.Unmarshal(served.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if err := requireObservedModelPreservationCase(t, "enriched_repeated_change_and_omission").assertObservedModels(&payload); err != nil {
		t.Fatal(err)
	}
}

func mountedObservedModelPublish(t *testing.T, h *Handler, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	metadata := schema.PublishRequest{Identity: schema.SessionIdentity{SessionID: "550e8400-e29b-41d4-a716-446655440000", SchemaVersion: 2}, Model: schema.ModelInfo{Harness: schema.HarnessClaudeCode, Model: "model"}, Timestamp: schema.TimestampInfo{Start: 1, End: 2}, Source: schema.SourceInfo{FilePath: "/fixture", Format: "jsonl"}, Project: schema.ProjectContext{Hash: testProjectHash, Name: "fixture"}}
	meta, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	body, boundary := multipartBody(t, map[string]string{"metadata": string(meta)}, string(content))
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(withTestUser(r.Context()))
	w := httptest.NewRecorder()
	h.PublishTranscript(w, r)
	return w
}

func loadObservedModelPublishGateFixtures(t *testing.T) observedModelPublishGateFixture {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(observedModelPublishGateFixtureYAML))
	decoder.KnownFields(true)
	var fixture observedModelPublishGateFixture
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode observed-model publish fixtures: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("observed-model publish fixtures require exactly one YAML document: %v", err)
	}
	if len(fixture.Cases) != observedModelPublishGateCaseCount {
		t.Fatalf("observed-model publish fixture count=%d, want %d", len(fixture.Cases), observedModelPublishGateCaseCount)
	}
	required, seen := map[string]bool{}, map[string]bool{}
	for _, name := range observedModelPublishGateCaseNames {
		required[name] = true
	}
	for _, c := range fixture.Cases {
		if c.Name == "" || c.Name != strings.TrimSpace(c.Name) || !required[c.Name] || seen[c.Name] {
			t.Fatalf("unknown, duplicate, empty, or edge-padded publish fixture name %q", c.Name)
		}
		seen[c.Name] = true
	}
	for _, name := range observedModelPublishGateCaseNames {
		if !seen[name] {
			t.Fatalf("required publish fixture %q missing", name)
		}
	}
	return fixture
}

func TestObservedModelRealHandlerMigrateRewriteReemit(t *testing.T) {
	const key = "transcripts/10000000-0000-4000-8000-000000000001.bin"
	s3 := newFakeBlobStore()
	s3.put(key, observedModelFixtureContent(t, "enriched_repeated_change_and_omission"))
	h := newTestHandler(publicTranscriptQuerier(key), s3)

	response := getContent(t, h, mustFixtureUUID(t))
	if response.Code != http.StatusOK {
		t.Fatalf("real migrate/rewrite handler status=%d, want 200; body=%s", response.Code, response.Body.String())
	}
	fixtureCase := requireObservedModelPreservationCase(t, "enriched_repeated_change_and_omission")
	var served schema.SessionDetailPayload
	if err := json.Unmarshal(response.Body.Bytes(), &served); err != nil {
		t.Fatalf("decode real handler re-emission: %v", err)
	}
	if err := fixtureCase.assertObservedModels(&served); err != nil {
		t.Fatal(err)
	}
	if s3.uploadCount() != 1 {
		t.Fatalf("real migrate/rewrite handler uploads=%d, want exactly 1", s3.uploadCount())
	}
	s3.mu.Lock()
	rewritten := append([]byte(nil), s3.lastBody...)
	s3.mu.Unlock()
	reemitted, _, err := NewContentMigrator().Migrate(context.Background(), rewritten)
	if err != nil {
		t.Fatalf("migrate rewritten production bytes: %v", err)
	}
	if err := fixtureCase.assertObservedModels(reemitted); err != nil {
		t.Fatal(err)
	}
}

func TestObservedModelCapabilityMountedSchemaEndpoint(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/schema/version", nil)
	w := httptest.NewRecorder()
	h.GetSchemaVersion(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("schema endpoint status=%d, want 200; body=%s", w.Code, w.Body.String())
	}
	var response schema.SchemaVersionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode schema endpoint response: %v", err)
	}
	if !hasObservedModelCapability(response.ContentCapabilities) {
		t.Fatalf("mounted schema endpoint omitted observed-model capability: %+v", response.ContentCapabilities)
	}
	if response.PushContractVersion != currentContractVersion || response.MinPushContractVersion != minPushContractVersion {
		t.Fatalf("legacy push window changed while adding capability: [%q,%q], want [%q,%q]", response.MinPushContractVersion, response.PushContractVersion, minPushContractVersion, currentContractVersion)
	}
}

func TestObservedModelFieldDropMountedHandlers(t *testing.T) {
	proofErr := executeObservedModelPreservationProof(droppingObservedModelEncoder{})
	evaluator := fixedPreservationEvaluator{err: proofErr}
	var probes, creates, scans int
	q := &mockQuerier{
		getTranscriptIDByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
			probes++
			return pgtype.UUID{}, errors.New("not found")
		},
		createTranscript: func(context.Context, sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
			creates++
			return sqlc.Transcript{}, nil
		},
	}
	blobs := &mockTranscriptBlobStore{}
	h := newTestHandler(q, blobs)
	h.preservationEvaluator = evaluator
	h.scanContent = func([]byte) []string { scans++; return nil }
	schemaResponse := httptest.NewRecorder()
	h.GetSchemaVersion(schemaResponse, httptest.NewRequest(http.MethodGet, "/api/v1/schema/version", nil))
	var advertised schema.SchemaVersionResponse
	if err := json.Unmarshal(schemaResponse.Body.Bytes(), &advertised); err != nil {
		t.Fatal(err)
	}
	if len(advertised.ContentCapabilities) != 0 {
		t.Fatalf("capabilities=%+v, want exact empty inventory", advertised.ContentCapabilities)
	}

	metadata := schema.PublishRequest{Identity: schema.SessionIdentity{SessionID: "550e8400-e29b-41d4-a716-446655440000", SchemaVersion: 2}, Model: schema.ModelInfo{Harness: schema.HarnessClaudeCode, Model: "model"}, Timestamp: schema.TimestampInfo{Start: 1, End: 2}, Source: schema.SourceInfo{FilePath: "/fixture", Format: "jsonl"}, Project: schema.ProjectContext{Hash: testProjectHash, Name: "fixture"}}
	meta, _ := json.Marshal(metadata)
	publish := func(content []byte) *httptest.ResponseRecorder {
		body, boundary := multipartBody(t, map[string]string{"metadata": string(meta)}, string(content))
		r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
		r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
		r = r.WithContext(withTestUser(r.Context()))
		w := httptest.NewRecorder()
		h.PublishTranscript(w, r)
		return w
	}
	enrichedResponse := publish(observedModelFixtureContent(t, "enriched_repeated_change_and_omission"))
	if enrichedResponse.Code != http.StatusConflict {
		t.Fatalf("enriched status=%d body=%s", enrichedResponse.Code, enrichedResponse.Body.String())
	}
	for _, fragment := range requireObservedModelNegativeCase(t, loadObservedModelNegativeFixtures(t), "field_drop_withholds_capability_and_refuses_enriched_publish").WantErrorContains[1:] {
		if !strings.Contains(decodeError(t, enrichedResponse.Body.Bytes()), fragment.Value) {
			t.Fatalf("conflict body lacks %q", fragment.Value)
		}
	}
	if len(blobs.uploadedKeys) != 0 {
		t.Fatalf("enriched denial wrote %d blobs", len(blobs.uploadedKeys))
	}
	if scans != 0 || probes != 0 || creates != 0 || len(blobs.deletedKeys) != 0 {
		t.Fatalf("field-drop denial effects scans=%d probes=%d creates=%d writes=%d deletes=%d", scans, probes, creates, len(blobs.uploadedKeys), len(blobs.deletedKeys))
	}

	legacyResponse := publish(observedModelFixtureContent(t, "legacy_without_observations"))
	if legacyResponse.Code != http.StatusCreated {
		t.Fatalf("legacy status=%d body=%s", legacyResponse.Code, legacyResponse.Body.String())
	}
	if len(blobs.uploadedKeys) != 1 {
		t.Fatalf("legacy blob writes=%d, want 1", len(blobs.uploadedKeys))
	}
	legacy := observedModelFixtureContent(t, "legacy_without_observations")
	store := newFakeBlobStore()
	const key = "transcripts/10000000-0000-4000-8000-000000000001.bin"
	store.put(key, legacy)
	served := getContent(t, newTestHandler(publicTranscriptQuerier(key), store), mustFixtureUUID(t))
	if served.Code != http.StatusOK {
		t.Fatalf("persisted legacy read status=%d body=%s", served.Code, served.Body.String())
	}
	var payload schema.SessionDetailPayload
	if err := json.Unmarshal(served.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if err := requireObservedModelPreservationCase(t, "legacy_without_observations").assertObservedModels(&payload); err != nil {
		t.Fatal(err)
	}
}

func hasObservedModelCapability(capabilities []schema.ContentCapability) bool {
	for _, capability := range capabilities {
		if capability == schema.ContentCapabilityObservedModelV1 {
			return true
		}
	}
	return false
}

func observedModelFixtureContent(t *testing.T, name string) []byte {
	t.Helper()
	fixtureCase := requireObservedModelPreservationCase(t, name)
	raw, err := fixtureCase.envelopeJSON()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func requireObservedModelPreservationCase(t *testing.T, name string) observedModelPreservationCase {
	t.Helper()
	cases, err := loadObservedModelPreservationFixtures(observedModelPreservationFixtureYAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixtureCase := range cases {
		if fixtureCase.Name == name {
			return fixtureCase
		}
	}
	t.Fatalf("required preservation fixture %q missing", name)
	return observedModelPreservationCase{}
}

func mustFixtureUUID(t *testing.T) uuid.UUID {
	t.Helper()
	return uuid.MustParse("10000000-0000-4000-8000-000000000001")
}

func loadObservedModelNegativeFixtures(t *testing.T) []observedModelNegativeCase {
	t.Helper()
	cases, err := decodeObservedModelNegativeFixtures(observedModelNegativeFixtureYAML)
	if err != nil {
		t.Fatal(err)
	}
	return cases
}

func decodeObservedModelNegativeFixtures(data []byte) ([]observedModelNegativeCase, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var fixture observedModelNegativeFixture
	if err := decoder.Decode(&fixture); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("observed-model negative fixtures must contain exactly one document")
	}
	if len(fixture.Cases) != observedModelNegativeCaseCount {
		return nil, fmt.Errorf("observed-model negative fixture count=%d, want exactly %d", len(fixture.Cases), observedModelNegativeCaseCount)
	}
	required := make(map[string]struct{}, len(observedModelNegativeCaseNames))
	for _, name := range observedModelNegativeCaseNames {
		required[name] = struct{}{}
	}
	seenNames := make(map[string]struct{}, len(fixture.Cases))
	seenOperations := make(map[string]struct{}, len(fixture.Cases))
	requiredOperations := map[string]struct{}{}
	for _, operation := range observedModelNegativeOperations {
		requiredOperations[operation] = struct{}{}
	}
	for _, fixtureCase := range fixture.Cases {
		if _, ok := required[fixtureCase.Name]; !ok {
			return nil, fmt.Errorf("unregistered observed-model negative fixture name %q", fixtureCase.Name)
		}
		if _, duplicate := seenNames[fixtureCase.Name]; duplicate {
			return nil, fmt.Errorf("duplicate observed-model negative fixture name %q", fixtureCase.Name)
		}
		seenNames[fixtureCase.Name] = struct{}{}
		if fixtureCase.Operation == "" || fixtureCase.Operation != strings.TrimSpace(fixtureCase.Operation) {
			return nil, fmt.Errorf("observed-model negative fixture %q has empty operation", fixtureCase.Name)
		}
		if _, ok := requiredOperations[fixtureCase.Operation]; !ok {
			return nil, fmt.Errorf("unregistered observed-model negative fixture operation %q", fixtureCase.Operation)
		}
		if _, duplicate := seenOperations[fixtureCase.Operation]; duplicate {
			return nil, fmt.Errorf("duplicate observed-model negative fixture operation %q", fixtureCase.Operation)
		}
		seenOperations[fixtureCase.Operation] = struct{}{}
		seenFragments := make(map[string]struct{}, len(fixtureCase.WantErrorContains))
		requiredFragments := map[string]struct{}{}
		if fixtureCase.Name == "field_drop_withholds_capability_and_refuses_enriched_publish" {
			for _, name := range fieldDropErrorFragmentNames {
				requiredFragments[name] = struct{}{}
			}
			if len(fixtureCase.WantErrorContains) != len(fieldDropErrorFragmentNames) {
				return nil, fmt.Errorf("observed-model negative fixture %q error fragment count=%d want=%d", fixtureCase.Name, len(fixtureCase.WantErrorContains), len(fieldDropErrorFragmentNames))
			}
		}
		for _, fragment := range fixtureCase.WantErrorContains {
			if fragment.Name == "" || fragment.Name != strings.TrimSpace(fragment.Name) || fragment.Value == "" {
				return nil, fmt.Errorf("observed-model negative fixture %q has empty error fragment", fixtureCase.Name)
			}
			if _, duplicate := seenFragments[fragment.Name]; duplicate {
				return nil, fmt.Errorf("observed-model negative fixture %q duplicates error fragment %q", fixtureCase.Name, fragment.Name)
			}
			seenFragments[fragment.Name] = struct{}{}
			if len(requiredFragments) > 0 {
				if _, ok := requiredFragments[fragment.Name]; !ok {
					return nil, fmt.Errorf("unregistered error fragment %q", fragment.Name)
				}
			}
		}
		for name := range requiredFragments {
			if _, ok := seenFragments[name]; !ok {
				return nil, fmt.Errorf("required error fragment %q missing", name)
			}
		}
	}
	for _, name := range observedModelNegativeCaseNames {
		if _, ok := seenNames[name]; !ok {
			return nil, fmt.Errorf("required observed-model negative fixture %q missing", name)
		}
	}
	return fixture.Cases, nil
}

func requireObservedModelNegativeCase(t *testing.T, cases []observedModelNegativeCase, name string) observedModelNegativeCase {
	t.Helper()
	for _, fixtureCase := range cases {
		if fixtureCase.Name == name {
			return fixtureCase
		}
	}
	t.Fatalf("required observed-model negative fixture %q missing", name)
	return observedModelNegativeCase{}
}
