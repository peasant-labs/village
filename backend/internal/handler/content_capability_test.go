package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/peasant-labs/schema"
)

type fixedProvenancePreservationEvaluator struct{ err error }

func (e fixedProvenancePreservationEvaluator) Evaluate() error { return e.err }

// droppingProvenanceEncoder sits at the real typed rewrite boundary and deletes
// the durable session-relationship evidence: relationships, root identity,
// submission count, purpose, retained earlier history, and per-block/folded
// provenance. The production proof must notice, so the capability is withheld.
type droppingProvenanceEncoder struct{}

func (droppingProvenanceEncoder) Encode(version schema.PushContractVersion, payload *schema.SessionDetailPayload) ([]byte, error) {
	if payload == nil {
		return nil, errors.New("dropping provenance encoder requires a durable detail")
	}
	raw, err := json.Marshal(schema.TranscriptContent{ContractVersion: version, Kind: schema.ContentKindSessionDetail, SessionDetail: payload})
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	detail, _ := document["sessionDetail"].(map[string]any)
	if detail != nil {
		for _, key := range []string{"inputSubmissionCount", "rootSessionId", "purpose", "relationships", "earlierHistory"} {
			delete(detail, key)
		}
		if turns, ok := detail["turns"].([]any); ok {
			for _, rawTurn := range turns {
				turn, _ := rawTurn.(map[string]any)
				if turn == nil {
					continue
				}
				delete(turn, "provenance")
				if calls, ok := turn["toolCalls"].([]any); ok {
					for _, rawCall := range calls {
						call, _ := rawCall.(map[string]any)
						if call == nil {
							continue
						}
						delete(call, "callProvenance")
						delete(call, "resultProvenance")
					}
				}
			}
		}
	}
	return json.Marshal(document)
}

func hasProvenanceCapability(capabilities []schema.ContentCapability) bool {
	return containsContentCapability(capabilities, schema.ContentCapabilitySessionGraphProvenanceV1)
}

func TestProductionProvenancePreservationProofPasses(t *testing.T) {
	if err := executeProvenancePreservationProof(productionContentRewriteEncoder); err != nil {
		t.Fatalf("production provenance preservation proof failed: %v", err)
	}
}

func TestAdvertisedContentCapabilitiesIncludeProvenanceWhenProofsPass(t *testing.T) {
	capabilities := advertisedContentCapabilitiesWithEvaluators(fixedPreservationEvaluator{}, fixedProvenancePreservationEvaluator{})
	want := []schema.ContentCapability{
		schema.ContentCapabilityDetailedUsageV1,
		schema.ContentCapabilityNativeMetadataV1,
		schema.ContentCapabilityObservedModelV1,
		schema.ContentCapabilitySessionGraphProvenanceV1,
		schema.ContentCapabilityToolNamespaceV1,
	}
	if !slicesEqualCapabilities(capabilities, want) {
		t.Fatalf("advertised capabilities=%v, want the full inventory %v", capabilities, want)
	}
	if err := schema.ValidateContentCapabilityAdvertisements(capabilities); err != nil {
		t.Fatalf("passing proof advertised an invalid list: %v", err)
	}
}

func TestAdvertisedContentCapabilitiesWithholdOnlyProvenanceWhenItsProofFails(t *testing.T) {
	capabilities := advertisedContentCapabilitiesWithEvaluators(fixedPreservationEvaluator{}, fixedProvenancePreservationEvaluator{err: errors.New("provenance preservation unavailable")})
	if hasProvenanceCapability(capabilities) {
		t.Fatalf("failing provenance proof still advertised the provenance token: %v", capabilities)
	}
	want := []schema.ContentCapability{
		schema.ContentCapabilityDetailedUsageV1,
		schema.ContentCapabilityNativeMetadataV1,
		schema.ContentCapabilityObservedModelV1,
		schema.ContentCapabilityToolNamespaceV1,
	}
	if !slicesEqualCapabilities(capabilities, want) {
		t.Fatalf("advertised capabilities=%v, want the shared inventory %v", capabilities, want)
	}
	if err := schema.ValidateContentCapabilityAdvertisements(capabilities); err != nil {
		t.Fatalf("provenance-only withholding advertised an invalid list: %v", err)
	}
}

func TestAdvertisedContentCapabilitiesWithholdAllWhenBaseProofFails(t *testing.T) {
	capabilities := advertisedContentCapabilitiesWithEvaluators(fixedPreservationEvaluator{err: errors.New("base preservation unavailable")}, fixedProvenancePreservationEvaluator{})
	if len(capabilities) != 0 {
		t.Fatalf("failing base proof advertised %v, want the exact empty inventory", capabilities)
	}
	if err := schema.ValidateContentCapabilityAdvertisements(capabilities); err != nil {
		t.Fatalf("empty advertisement is invalid: %v", err)
	}
}

func TestAdvertisedContentCapabilitiesWithholdOnlyProvenanceWhenEvidenceDropped(t *testing.T) {
	proofErr := executeProvenancePreservationProof(droppingProvenanceEncoder{})
	if proofErr == nil {
		t.Fatal("lossy provenance rewrite passed the production preservation proof")
	}
	if !strings.Contains(proofErr.Error(), "provenance preservation proof failed") {
		t.Fatalf("proof error is not actionable: %v", proofErr)
	}
	capabilities := advertisedContentCapabilitiesWithEvaluators(fixedPreservationEvaluator{}, fixedProvenancePreservationEvaluator{err: proofErr})
	if hasProvenanceCapability(capabilities) {
		t.Fatalf("lossy provenance rewrite still advertised the provenance token: %v", capabilities)
	}
	if err := schema.ValidateContentCapabilityAdvertisements(capabilities); err != nil {
		t.Fatalf("lossy provenance advertisement is invalid: %v", err)
	}
}

func TestProvenancePreservationFixtureInventoryGuards(t *testing.T) {
	if _, err := loadProvenancePreservationFixtures(provenancePreservationFixtureYAML); err != nil {
		t.Fatalf("pristine provenance fixture rejected: %v", err)
	}
	renamed := bytes.Replace(provenancePreservationFixtureYAML, []byte("measured_zero_submission_count"), []byte("substituted_count_case"), 1)
	if _, err := loadProvenancePreservationFixtures(renamed); err == nil {
		t.Fatal("count-preserving fixture rename was accepted")
	}
	duplicated := append([]byte(nil), provenancePreservationFixtureYAML...)
	duplicated = bytes.Replace(duplicated, []byte("full_provenance_evidence"), []byte("measured_zero_submission_count"), 1)
	if _, err := loadProvenancePreservationFixtures(duplicated); err == nil {
		t.Fatal("duplicated fixture name was accepted")
	}
}

func TestProvenancePreservationFixtureRejectsNonProvenanceCorpus(t *testing.T) {
	stripped := bytes.Replace(provenancePreservationFixtureYAML, []byte("capabilities: [session_graph_provenance_v1]"), []byte("capabilities: []"), 1)
	if _, err := loadProvenancePreservationFixtures(stripped); err == nil {
		t.Fatal("fixture without declared provenance capability was accepted")
	}
}

func TestSchemaEndpointAdvertisesProvenanceCapability(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)
	w := httptest.NewRecorder()
	h.GetSchemaVersion(w, httptest.NewRequest(http.MethodGet, "/api/v1/schema/version", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("schema endpoint status=%d body=%s", w.Code, w.Body.String())
	}
	var response schema.SchemaVersionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode schema endpoint response: %v", err)
	}
	if !hasProvenanceCapability(response.ContentCapabilities) {
		t.Fatalf("mounted schema endpoint omitted the provenance capability: %v", response.ContentCapabilities)
	}
	if err := schema.ValidateContentCapabilityAdvertisements(response.ContentCapabilities); err != nil {
		t.Fatalf("mounted schema endpoint advertised an invalid list: %v", err)
	}
}

func TestSchemaEndpointWithholdsOnlyProvenanceWhenItsProofFails(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)
	h.provenanceEvaluator = fixedProvenancePreservationEvaluator{err: errors.New("provenance preservation unavailable")}
	w := httptest.NewRecorder()
	h.GetSchemaVersion(w, httptest.NewRequest(http.MethodGet, "/api/v1/schema/version", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("schema endpoint status=%d body=%s", w.Code, w.Body.String())
	}
	var response schema.SchemaVersionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode schema endpoint response: %v", err)
	}
	if hasProvenanceCapability(response.ContentCapabilities) {
		t.Fatalf("failing provenance proof still advertised the token: %v", response.ContentCapabilities)
	}
	if !hasObservedModelCapability(response.ContentCapabilities) {
		t.Fatalf("provenance-only withholding dropped the shared tokens: %v", response.ContentCapabilities)
	}
	if err := schema.ValidateContentCapabilityAdvertisements(response.ContentCapabilities); err != nil {
		t.Fatalf("mounted schema endpoint advertised an invalid list: %v", err)
	}
}

func TestProvenancePublishGateRefusesWhenItsProofFails(t *testing.T) {
	content := firstProvenanceFixtureContent(t)
	refusal := requireSupportedContentForHarness([]byte(content), "codex", fixedPreservationEvaluator{}, fixedProvenancePreservationEvaluator{err: errors.New("provenance preservation unavailable")})
	if refusal == nil {
		t.Fatal("provenance-bearing content was accepted while its proof failed")
	}
	for _, fragment := range []string{"session_graph_provenance_v1", "no transcript bytes or metadata were written", "GET /api/v1/schema/version"} {
		if !strings.Contains(refusal.Error(), fragment) {
			t.Fatalf("provenance refusal lacks %q: %v", fragment, refusal)
		}
	}
}

func TestProvenancePublishGateIsIndependentOfSharedProofForProvenanceOnlyEvidence(t *testing.T) {
	content := firstProvenanceFixtureContent(t)
	if err := requireSupportedContentForHarness([]byte(content), "codex", fixedPreservationEvaluator{err: errors.New("shared preservation unavailable")}, fixedProvenancePreservationEvaluator{}); err != nil {
		t.Fatalf("provenance-only content was refused by the shared gate: %v", err)
	}
}

func TestProvenancePublishGateMountedRefusalWritesNothing(t *testing.T) {
	content := firstProvenanceFixtureContent(t)
	blobs := &mockTranscriptBlobStore{}
	h := newTestHandler(&mockQuerier{}, blobs)
	h.provenanceEvaluator = fixedProvenancePreservationEvaluator{err: errors.New("provenance preservation unavailable")}
	response := publishGraphContent(t, h, []byte(content))
	if response.Code != http.StatusConflict {
		t.Fatalf("mounted provenance publish status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "provenance preservation proof is failing") {
		t.Fatalf("mounted provenance refusal is not actionable: %s", response.Body.String())
	}
	if len(blobs.uploadedKeys) != 0 {
		t.Fatalf("refused provenance publish wrote %d blobs", len(blobs.uploadedKeys))
	}
}

func firstProvenanceFixtureContent(t *testing.T) string {
	t.Helper()
	cases, err := loadProvenancePreservationFixtures(provenancePreservationFixtureYAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixtureCase := range cases {
		if fixtureCase.Name == "full_provenance_evidence" {
			return fixtureCase.Content
		}
	}
	t.Fatal("full_provenance_evidence fixture missing")
	return ""
}

func publishGraphContent(t *testing.T, h *Handler, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	metadata := schema.PublishRequest{
		Identity:  schema.SessionIdentity{SessionID: "550e8400-e29b-41d4-a716-446655440000", SchemaVersion: 2},
		Model:     schema.ModelInfo{Harness: schema.HarnessCodex, Model: "model"},
		Timestamp: schema.TimestampInfo{Start: 1, End: 2},
		Source:    schema.SourceInfo{FilePath: "/fixture", Format: "jsonl"},
		Project:   schema.ProjectContext{Hash: testProjectHash, Name: "fixture"},
	}
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

func slicesEqualCapabilities(a, b []schema.ContentCapability) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
