package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/peasant-labs/schema"
)

// The boot evaluation caches all three verdicts once per process. On this
// build every proof passes, so the cached verdicts advertise the full closed
// inventory and the production publish gate accepts evidence it can preserve.
func TestBootCachedVerdictsAdvertiseFullInventory(t *testing.T) {
	baseErr, provenanceErr, thinkingLevelErr := EvaluatePreservationProofs()
	if baseErr != nil {
		t.Fatalf("boot-cached base verdict failed: %v", baseErr)
	}
	if provenanceErr != nil {
		t.Fatalf("boot-cached provenance verdict failed: %v", provenanceErr)
	}
	if thinkingLevelErr != nil {
		t.Fatalf("boot-cached thinking-level verdict failed: %v", thinkingLevelErr)
	}
	if err := (productionThinkingLevelPreservationEvaluator{}).Evaluate(); err != nil {
		t.Fatalf("production thinking-level evaluator disagrees with boot cache: %v", err)
	}
	baseEvaluator := productionObservedModelPreservationEvaluator{}
	if err := baseEvaluator.Evaluate(); err != nil {
		t.Fatalf("production base evaluator disagrees with boot cache: %v", err)
	}
	provenanceEvaluator := productionProvenancePreservationEvaluator{}
	if err := provenanceEvaluator.Evaluate(); err != nil {
		t.Fatalf("production provenance evaluator disagrees with boot cache: %v", err)
	}
	capabilities := advertisedContentCapabilities()
	want := []schema.ContentCapability{
		schema.ContentCapabilityDetailedUsageV1,
		schema.ContentCapabilityNativeMetadataV1,
		schema.ContentCapabilityObservedModelV1,
		schema.ContentCapabilityRetainedUnknownV1,
		schema.ContentCapabilitySessionGraphProvenanceV1,
		schema.ContentCapabilityThinkingLevelV1,
		schema.ContentCapabilityToolNamespaceV1,
	}
	if !slicesEqualCapabilities(capabilities, want) {
		t.Fatalf("boot-cached advertisement=%v, want the full inventory %v", capabilities, want)
	}
	if err := schema.ValidateContentCapabilityAdvertisements(capabilities); err != nil {
		t.Fatalf("boot-cached advertisement is invalid: %v", err)
	}
}

// The advertisement and publish gate read the boot-cached verdicts through the
// production evaluators: the mounted schema endpoint advertises the full
// inventory and enriched plus provenance-bearing publishes are accepted.
func TestBootCachedVerdictsDriveAdvertisementAndPublishGate(t *testing.T) {
	baseErr, provenanceErr, thinkingLevelErr := EvaluatePreservationProofs()
	if baseErr != nil || provenanceErr != nil || thinkingLevelErr != nil {
		t.Fatalf("boot cache verdicts base=%v provenance=%v thinking-level=%v, want all passing", baseErr, provenanceErr, thinkingLevelErr)
	}
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
	if !hasObservedModelCapability(response.ContentCapabilities) || !hasProvenanceCapability(response.ContentCapabilities) || !hasThinkingLevelCapability(response.ContentCapabilities) {
		t.Fatalf("boot-cached mounted advertisement omitted evidence tokens: %v", response.ContentCapabilities)
	}
	if err := schema.ValidateContentCapabilityAdvertisements(response.ContentCapabilities); err != nil {
		t.Fatalf("boot-cached mounted advertisement is invalid: %v", err)
	}
	enriched := observedModelFixtureContent(t, "enriched_repeated_change_and_omission")
	if err := requireSupportedContentForHarness(enriched, "claude-code", productionObservedModelPreservationEvaluator{}, productionProvenancePreservationEvaluator{}, productionThinkingLevelPreservationEvaluator{}); err != nil {
		t.Fatalf("boot-cached publish gate refused preservable enriched content: %v", err)
	}
	provenance := firstProvenanceFixtureContent(t)
	if err := requireSupportedContentForHarness([]byte(provenance), "codex", productionObservedModelPreservationEvaluator{}, productionProvenancePreservationEvaluator{}, productionThinkingLevelPreservationEvaluator{}); err != nil {
		t.Fatalf("boot-cached publish gate refused preservable provenance content: %v", err)
	}
	if err := requireSupportedContentForHarness(thinkingLevelFixtureContent(t, "enriched_session_seed_and_per_turn_canonical_and_raw"), "codex", productionObservedModelPreservationEvaluator{}, productionProvenancePreservationEvaluator{}, productionThinkingLevelPreservationEvaluator{}); err != nil {
		t.Fatalf("boot-cached publish gate refused preservable level-bearing content: %v", err)
	}
}

// The uncached proof entry points drive negatives without poisoning the boot
// cache: a lossy rewrite fails its proof, yet the cached verdicts and the
// production advertisement stay passing.
func TestUncachedProofEntryPointsDoNotPoisonBootCache(t *testing.T) {
	observedErr := executeObservedModelPreservationProof(droppingObservedModelEncoder{})
	if observedErr == nil {
		t.Fatal("lossy observed-model rewrite passed the uncached proof")
	}
	provenanceErr := executeProvenancePreservationProof(droppingProvenanceEncoder{})
	if provenanceErr == nil {
		t.Fatal("lossy provenance rewrite passed the uncached proof")
	}
	if executeThinkingLevelPreservationProof(droppingThinkingLevelEncoder{}) == nil {
		t.Fatal("lossy thinking-level rewrite passed the uncached proof")
	}
	baseCached, provenanceCached, thinkingLevelCached := EvaluatePreservationProofs()
	if thinkingLevelCached != nil {
		t.Fatalf("uncached thinking-level negative poisoned the boot thinking-level verdict: %v", thinkingLevelCached)
	}
	if baseCached != nil {
		t.Fatalf("uncached observed-model negative poisoned the boot base verdict: %v", baseCached)
	}
	if provenanceCached != nil {
		t.Fatalf("uncached provenance negative poisoned the boot provenance verdict: %v", provenanceCached)
	}
	capabilities := advertisedContentCapabilities()
	if !hasObservedModelCapability(capabilities) || !hasProvenanceCapability(capabilities) || !hasThinkingLevelCapability(capabilities) {
		t.Fatalf("uncached negatives withheld the production advertisement: %v", capabilities)
	}
}

// After boot evaluation, the injected-evaluator seams still withhold exactly:
// a failing base evaluator empties the advertisement and a failing provenance
// evaluator withholds only the sealed token.
func TestInjectedEvaluatorSeamsStillWithholdAfterBoot(t *testing.T) {
	_, _, _ = EvaluatePreservationProofs()
	withheld := advertisedContentCapabilitiesWithEvaluators(fixedPreservationEvaluator{err: errors.New("base preservation unavailable")}, fixedProvenancePreservationEvaluator{}, fixedThinkingLevelPreservationEvaluator{})
	if len(withheld) != 0 {
		t.Fatalf("injected base failure advertised %v, want the exact empty inventory", withheld)
	}
	if err := schema.ValidateContentCapabilityAdvertisements(withheld); err != nil {
		t.Fatalf("empty advertisement is invalid: %v", err)
	}
	shared := advertisedContentCapabilitiesWithEvaluators(fixedPreservationEvaluator{}, fixedProvenancePreservationEvaluator{err: errors.New("provenance preservation unavailable")}, fixedThinkingLevelPreservationEvaluator{})
	if hasProvenanceCapability(shared) {
		t.Fatalf("injected provenance failure still advertised the token: %v", shared)
	}
	if !hasObservedModelCapability(shared) {
		t.Fatalf("injected provenance failure dropped the shared tokens: %v", shared)
	}
	if err := schema.ValidateContentCapabilityAdvertisements(shared); err != nil {
		t.Fatalf("provenance-only withholding advertised an invalid list: %v", err)
	}
	levelless := advertisedContentCapabilitiesWithEvaluators(fixedPreservationEvaluator{}, fixedProvenancePreservationEvaluator{}, fixedThinkingLevelPreservationEvaluator{err: errors.New("thinking-level preservation unavailable")})
	if hasThinkingLevelCapability(levelless) || !hasObservedModelCapability(levelless) || !hasProvenanceCapability(levelless) {
		t.Fatalf("injected thinking-level failure advertised %v, want every token except thinking_level_v1", levelless)
	}
	proofErr := executeObservedModelPreservationProof(droppingObservedModelEncoder{})
	refusal := requireSupportedContentForHarness(observedModelFixtureContent(t, "enriched_repeated_change_and_omission"), "claude-code", fixedPreservationEvaluator{err: proofErr}, fixedProvenancePreservationEvaluator{}, fixedThinkingLevelPreservationEvaluator{})
	if refusal == nil || !strings.Contains(refusal.Error(), "no transcript bytes or metadata were written") {
		t.Fatalf("injected base refusal is not fail-closed: %v", refusal)
	}
}
