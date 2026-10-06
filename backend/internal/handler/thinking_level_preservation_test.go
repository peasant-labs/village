package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/thinking_level_preservation/negative.yaml
var thinkingLevelNegativeFixtureYAML []byte

//go:embed testdata/thinking_level_preservation/publish_gate.yaml
var thinkingLevelPublishGateFixtureYAML []byte

//go:embed testdata/thinking_level_preservation/read_evidence.yaml
var thinkingLevelReadEvidenceFixtureYAML []byte

var thinkingLevelNegativeCaseNames = [...]string{
	"production_rewrite_preserves_level_evidence",
	"field_drop_withholds_capability_and_refuses_level_publish",
	"field_drop_keeps_legacy_publish_compatible",
}

// thinkingLevelNegativeBindings binds each registered negative to the one
// operation it must dispatch, so a swap of registered operations fails.
var thinkingLevelNegativeBindings = map[string]string{
	"production_rewrite_preserves_level_evidence":               "canonical_production_encoder",
	"field_drop_withholds_capability_and_refuses_level_publish": "drop_thinking_level_at_production_rewrite",
	"field_drop_keeps_legacy_publish_compatible":                "legacy_payload_under_field_drop",
}

var thinkingLevelFieldDropFragmentNames = [...]string{"evidence_field", "uploaded_evidence", "no_writes", "no_silent_strip", "negotiation_endpoint", "capability_name"}

var thinkingLevelPublishGateCaseNames = [...]string{
	"valid_assistant_levels",
	"legacy_without_levels_accepted",
	"user_turn_level_refused",
	"tool_turn_raw_refused",
	"non_canonical_turn_level_refused",
	"edge_whitespace_raw_refused",
	"invalid_session_level_refused",
	"valid_history_changes",
	"empty_history_entry_refused",
}

var thinkingLevelReadCaseNames = [...]string{
	"stored_user_turn_level_refused",
	"stored_non_canonical_level_refused",
	"stored_edge_whitespace_raw_refused",
	"stored_empty_history_entry_refused",
}

var thinkingLevelRewriteFailureCaseNames = [...]string{
	"level_bearing_blob_write_failure_denies",
	"legacy_blob_write_failure_serves",
}

type thinkingLevelNegativeFixture struct {
	Cases []thinkingLevelNegativeCase `yaml:"cases"`
}

type thinkingLevelNegativeCase struct {
	Name               string                       `yaml:"name"`
	Operation          string                       `yaml:"operation"`
	WantProofFailure   bool                         `yaml:"wantProofFailure"`
	WantCapability     bool                         `yaml:"wantCapability"`
	WantPublishRefusal bool                         `yaml:"wantPublishRefusal"`
	WantErrorContains  []observedModelErrorFragment `yaml:"wantErrorContains"`
}

type thinkingLevelPublishGateFixture struct {
	Cases []thinkingLevelPublishGateCase `yaml:"cases"`
}

type thinkingLevelPublishGateCase struct {
	Name           string                       `yaml:"name"`
	Content        string                       `yaml:"content"`
	WantErrorParts []observedModelErrorFragment `yaml:"wantErrorParts"`
}

type thinkingLevelReadEvidenceFixture struct {
	Reads           []thinkingLevelReadCase           `yaml:"reads"`
	RewriteFailures []thinkingLevelRewriteFailureCase `yaml:"rewriteFailures"`
}

type thinkingLevelReadCase struct {
	Name      string `yaml:"name"`
	Content   string `yaml:"content"`
	WantError string `yaml:"wantError"`
	Corrected string `yaml:"corrected"`
}

type thinkingLevelRewriteFailureCase struct {
	Name        string `yaml:"name"`
	ContentCase string `yaml:"contentCase"`
	Failure     string `yaml:"failure"`
	WantStatus  int    `yaml:"wantStatus"`
	WantLevels  bool   `yaml:"wantLevels"`
}

type fixedThinkingLevelPreservationEvaluator struct{ err error }

func (e fixedThinkingLevelPreservationEvaluator) Evaluate() error { return e.err }

// droppingThinkingLevelEncoder is the executed field-drop mutation: it runs the
// production canonical encoder, then strips every thinking-level member
// (session seed, raw spelling, and in-session history) from the session and its
// turns, exactly as a lossy rewrite would.
type droppingThinkingLevelEncoder struct{}

func (droppingThinkingLevelEncoder) Encode(version schema.PushContractVersion, payload *schema.SessionDetailPayload) ([]byte, error) {
	raw, err := canonicalContentRewriteEncoder{}.Encode(version, payload)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	detail, _ := document["sessionDetail"].(map[string]any)
	delete(detail, "thinkingLevel")
	delete(detail, "thinkingLevelRaw")
	delete(detail, "thinkingLevelHistory")
	turns, _ := detail["turns"].([]any)
	for _, rawTurn := range turns {
		if turn, ok := rawTurn.(map[string]any); ok {
			delete(turn, "thinkingLevel")
			delete(turn, "thinkingLevelRaw")
		}
	}
	return json.Marshal(document)
}

type thinkingLevelOperation struct {
	encoder contentRewriteEncoder
	oracle  func([]thinkingLevelPreservationOutcome) error
}

var thinkingLevelOperationRegistry = map[string]thinkingLevelOperation{
	"canonical_production_encoder":              {canonicalContentRewriteEncoder{}, thinkingLevelCanonicalOracle},
	"drop_thinking_level_at_production_rewrite": {droppingThinkingLevelEncoder{}, thinkingLevelFieldDropOracle},
	"legacy_payload_under_field_drop":           {droppingThinkingLevelEncoder{}, thinkingLevelFieldDropOracle},
}

func thinkingLevelCanonicalOracle(outcomes []thinkingLevelPreservationOutcome) error {
	for _, outcome := range outcomes {
		if outcome.Err != nil {
			return outcome.Err
		}
	}
	return nil
}

// The field drop must fail exactly the three level-bearing cases and leave the
// level-free legacy case passing.
func thinkingLevelFieldDropOracle(outcomes []thinkingLevelPreservationOutcome) error {
	failed := []string{}
	for _, outcome := range outcomes {
		if outcome.Err != nil {
			if !strings.Contains(outcome.Err.Error(), "re-emitted thinkingLevel") {
				return fmt.Errorf("field-drop outcome %q failed for the wrong reason: %v", outcome.Name, outcome.Err)
			}
			failed = append(failed, outcome.Name)
		}
	}
	want := []string{"enriched_session_seed_and_per_turn_canonical_and_raw", "legacy_contract_with_levels", "enriched_session_history_only_changes"}
	if !slices.Equal(failed, want) {
		return fmt.Errorf("field-drop failed cases=%v, want %v", failed, want)
	}
	return nil
}

func hasThinkingLevelCapability(capabilities []schema.ContentCapability) bool {
	return containsContentCapability(capabilities, schema.ContentCapabilityThinkingLevelV1)
}

func thinkingLevelFixtureContent(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := requireThinkingLevelPreservationCase(t, name).envelopeJSON()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func requireThinkingLevelPreservationCase(t *testing.T, name string) thinkingLevelPreservationCase {
	t.Helper()
	cases, err := loadThinkingLevelPreservationFixtures(thinkingLevelPreservationFixtureYAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("missing thinking-level preservation case %q", name)
	return thinkingLevelPreservationCase{}
}

func decodeStrictSingleDocument(data []byte, target any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("fixture must contain exactly one YAML document: %v", err)
	}
	return nil
}

func requireExactNames(kind string, got []string, want []string) error {
	seen := map[string]bool{}
	for _, name := range got {
		if name == "" || name != strings.TrimSpace(name) || !slices.Contains(want, name) || seen[name] {
			return fmt.Errorf("%s fixture name %q is unregistered, duplicate, empty, or edge-padded", kind, name)
		}
		seen[name] = true
	}
	for _, name := range want {
		if !seen[name] {
			return fmt.Errorf("%s fixture is missing required case %q", kind, name)
		}
	}
	return nil
}

func decodeThinkingLevelNegativeFixtures(data []byte) ([]thinkingLevelNegativeCase, error) {
	var fixture thinkingLevelNegativeFixture
	if err := decodeStrictSingleDocument(data, &fixture); err != nil {
		return nil, err
	}
	names := []string{}
	for _, c := range fixture.Cases {
		names = append(names, c.Name)
		if _, ok := thinkingLevelOperationRegistry[c.Operation]; !ok {
			return nil, fmt.Errorf("negative %q dispatches unregistered operation %q", c.Name, c.Operation)
		}
		if c.WantPublishRefusal {
			fragments := []string{}
			for _, fragment := range c.WantErrorContains {
				if fragment.Value == "" {
					return nil, fmt.Errorf("negative %q fragment %q is empty", c.Name, fragment.Name)
				}
				fragments = append(fragments, fragment.Name)
			}
			if !slices.Equal(fragments, thinkingLevelFieldDropFragmentNames[:]) {
				return nil, fmt.Errorf("negative %q fragment inventory=%v, want %v", c.Name, fragments, thinkingLevelFieldDropFragmentNames)
			}
		}
	}
	if err := requireExactNames("negative", names, thinkingLevelNegativeCaseNames[:]); err != nil {
		return nil, err
	}
	return fixture.Cases, nil
}

func loadThinkingLevelNegative(t *testing.T, name string) thinkingLevelNegativeCase {
	t.Helper()
	cases, err := decodeThinkingLevelNegativeFixtures(thinkingLevelNegativeFixtureYAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("missing negative %q", name)
	return thinkingLevelNegativeCase{}
}

func decodeThinkingLevelPublishGateFixtures(data []byte) ([]thinkingLevelPublishGateCase, error) {
	var fixture thinkingLevelPublishGateFixture
	if err := decodeStrictSingleDocument(data, &fixture); err != nil {
		return nil, err
	}
	names := []string{}
	for _, c := range fixture.Cases {
		names = append(names, c.Name)
	}
	return fixture.Cases, requireExactNames("publish gate", names, thinkingLevelPublishGateCaseNames[:])
}

func decodeThinkingLevelReadEvidenceFixtures(data []byte) (thinkingLevelReadEvidenceFixture, error) {
	var fixture thinkingLevelReadEvidenceFixture
	if err := decodeStrictSingleDocument(data, &fixture); err != nil {
		return fixture, err
	}
	reads, rewrites := []string{}, []string{}
	for _, c := range fixture.Reads {
		reads = append(reads, c.Name)
	}
	for _, c := range fixture.RewriteFailures {
		rewrites = append(rewrites, c.Name)
	}
	if err := requireExactNames("read evidence", reads, thinkingLevelReadCaseNames[:]); err != nil {
		return fixture, err
	}
	return fixture, requireExactNames("rewrite failure", rewrites, thinkingLevelRewriteFailureCaseNames[:])
}

func TestThinkingLevelProductionPreservation(t *testing.T) {
	negative := loadThinkingLevelNegative(t, "production_rewrite_preserves_level_evidence")
	outcomes, err := executeThinkingLevelPreservationProofOutcomes(canonicalContentRewriteEncoder{})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != thinkingLevelPreservationCaseCount {
		t.Fatalf("executed %d cases, want every registered case", len(outcomes))
	}
	if err := thinkingLevelCanonicalOracle(outcomes); (err != nil) != negative.WantProofFailure {
		t.Fatalf("production preservation error=%v, want failure=%v", err, negative.WantProofFailure)
	}
	if got := hasThinkingLevelCapability(advertisedContentCapabilities()); got != negative.WantCapability {
		t.Fatalf("production thinking_level_v1 advertised=%v, want %v", got, negative.WantCapability)
	}
}

func TestThinkingLevelFieldDropNegative(t *testing.T) {
	negative := loadThinkingLevelNegative(t, "field_drop_withholds_capability_and_refuses_level_publish")
	encoder := &countingRewriteEncoder{delegate: droppingThinkingLevelEncoder{}}
	proofErr := executeThinkingLevelPreservationProof(encoder)
	if encoder.executions != thinkingLevelPreservationCaseCount {
		t.Fatalf("field-drop executions=%d, want exactly %d", encoder.executions, thinkingLevelPreservationCaseCount)
	}
	if (proofErr != nil) != negative.WantProofFailure || !strings.Contains(proofErr.Error(), negative.WantErrorContains[0].Value) {
		t.Fatalf("field-drop proof error=%v, want failure naming %q", proofErr, negative.WantErrorContains[0].Value)
	}
	evaluator := fixedThinkingLevelPreservationEvaluator{err: proofErr}
	capabilities := advertisedContentCapabilitiesWithEvaluators(fixedPreservationEvaluator{}, fixedProvenancePreservationEvaluator{}, evaluator)
	if hasThinkingLevelCapability(capabilities) != negative.WantCapability {
		t.Fatalf("field-drop advertisement=%v, want thinking_level_v1=%v", capabilities, negative.WantCapability)
	}
	want := slices.DeleteFunc(slices.Clone(schema.AllContentCapabilities), func(c schema.ContentCapability) bool { return c == schema.ContentCapabilityThinkingLevelV1 })
	if !slices.Equal(capabilities, want) {
		t.Fatalf("field-drop advertisement=%v, want every other token %v", capabilities, want)
	}
	refusal := requireSupportedContentForHarness(thinkingLevelFixtureContent(t, "enriched_session_seed_and_per_turn_canonical_and_raw"), "codex", fixedPreservationEvaluator{}, fixedProvenancePreservationEvaluator{}, evaluator)
	if (refusal != nil) != negative.WantPublishRefusal {
		t.Fatalf("field-drop publish refusal=%v, want refusal=%v", refusal, negative.WantPublishRefusal)
	}
	for _, fragment := range negative.WantErrorContains[1:] {
		if !strings.Contains(refusal.Error(), fragment.Value) {
			t.Errorf("refusal lacks %s fragment %q: %v", fragment.Name, fragment.Value, refusal)
		}
	}
}

func TestThinkingLevelFieldDropLegacyCompatibility(t *testing.T) {
	negative := loadThinkingLevelNegative(t, "field_drop_keeps_legacy_publish_compatible")
	proofErr := executeThinkingLevelPreservationProof(droppingThinkingLevelEncoder{})
	if (proofErr != nil) != negative.WantProofFailure {
		t.Fatalf("field-drop proof error=%v, want failure=%v", proofErr, negative.WantProofFailure)
	}
	legacy := thinkingLevelFixtureContent(t, "legacy_without_levels_stays_absent")
	refusal := requireSupportedContentForHarness(legacy, "claude-code", fixedPreservationEvaluator{}, fixedProvenancePreservationEvaluator{}, fixedThinkingLevelPreservationEvaluator{err: proofErr})
	if (refusal != nil) != negative.WantPublishRefusal {
		t.Fatalf("legacy publish under field drop refusal=%v, want refusal=%v", refusal, negative.WantPublishRefusal)
	}
}

// A failing base proof withholds the whole advertisement, so it must refuse
// level-bearing content too even while the thinking-level proof passes.
func TestThinkingLevelPublishRefusedWhenBaseProofFails(t *testing.T) {
	refusal := requireSupportedContentForHarness(thinkingLevelFixtureContent(t, "enriched_session_seed_and_per_turn_canonical_and_raw"), "codex", fixedPreservationEvaluator{err: errors.New("base preservation unavailable")}, fixedProvenancePreservationEvaluator{}, fixedThinkingLevelPreservationEvaluator{})
	if refusal == nil || !strings.Contains(refusal.Error(), "GET /api/v1/schema/version") {
		t.Fatalf("base failure refusal=%v, want an actionable refusal", refusal)
	}
}

// The in-session history is presence-bearing evidence: content that carries
// thinkingLevelHistory must take the strict public decode rather than the
// legacy projection path that could drop it.
func TestThinkingLevelHistoryIsPublicEvidence(t *testing.T) {
	legacy := []byte(`{"id":"history-only","provider":"claude","thinkingLevelHistory":[{"level":"medium"}]}`)
	if _, err := validateContentBoundary(legacy, "", contentPublication); err == nil {
		t.Fatal("history-only legacy-shaped content took the legacy projection path instead of the strict public decode")
	}
}

// A session with only a history (no seed and no per-turn level) still requires
// thinking_level_v1, so it is refused before any write while the proof fails and
// accepted when the proof passes.
func TestThinkingLevelHistoryOnlyPublishGate(t *testing.T) {
	raw := thinkingLevelFixtureContent(t, "enriched_session_history_only_changes")
	refusal := requireSupportedContentForHarness(raw, "codex", fixedPreservationEvaluator{}, fixedProvenancePreservationEvaluator{}, fixedThinkingLevelPreservationEvaluator{err: errors.New("thinking-level preservation unavailable")})
	if refusal == nil || !strings.Contains(refusal.Error(), "thinking_level_v1") {
		t.Fatalf("history-only publish under a failing proof refusal=%v, want the thinking-level gate", refusal)
	}
	if err := requireSupportedContentForHarness(raw, "codex", fixedPreservationEvaluator{}, fixedProvenancePreservationEvaluator{}, fixedThinkingLevelPreservationEvaluator{}); err != nil {
		t.Fatalf("history-only publish under a passing proof refusal=%v, want acceptance", err)
	}
}

func TestThinkingLevelRegisteredOperationsDispatch(t *testing.T) {
	cases, err := decodeThinkingLevelNegativeFixtures(thinkingLevelNegativeFixtureYAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if thinkingLevelNegativeBindings[c.Name] != c.Operation {
			t.Fatalf("negative %q dispatches %q, want %q", c.Name, c.Operation, thinkingLevelNegativeBindings[c.Name])
		}
		entry := thinkingLevelOperationRegistry[c.Operation]
		outcomes, err := executeThinkingLevelPreservationProofOutcomes(entry.encoder)
		if err != nil {
			t.Fatal(err)
		}
		if err := entry.oracle(outcomes); err != nil {
			t.Fatalf("operation %q: %v", c.Operation, err)
		}
	}
	// The oracles discriminate: the canonical encoder cannot satisfy the
	// field-drop oracle, and the field drop cannot satisfy the canonical one.
	canonical, _ := executeThinkingLevelPreservationProofOutcomes(canonicalContentRewriteEncoder{})
	if thinkingLevelFieldDropOracle(canonical) == nil {
		t.Fatal("canonical encoder satisfied the field-drop oracle")
	}
	dropped, _ := executeThinkingLevelPreservationProofOutcomes(droppingThinkingLevelEncoder{})
	if thinkingLevelCanonicalOracle(dropped) == nil {
		t.Fatal("field-drop encoder satisfied the canonical oracle")
	}
}

func TestThinkingLevelFixtureInventoryMutationGuards(t *testing.T) {
	mutations := []struct {
		name   string
		decode func([]byte) error
		data   []byte
		old    string
		new    string
	}{
		{"cases_name_substitution", func(d []byte) error { _, err := loadThinkingLevelPreservationFixtures(d); return err }, thinkingLevelPreservationFixtureYAML, "legacy_contract_with_levels", "legacy_contract_with_level_substitute"},
		{"cases_duplicate", func(d []byte) error { _, err := loadThinkingLevelPreservationFixtures(d); return err }, thinkingLevelPreservationFixtureYAML, "name: legacy_contract_with_levels", "name: legacy_without_levels_stays_absent"},
		{"cases_unknown_field", func(d []byte) error { _, err := loadThinkingLevelPreservationFixtures(d); return err }, thinkingLevelPreservationFixtureYAML, "    harness: codex", "    harness: codex\n    unexpected: true"},
		{"cases_second_document", func(d []byte) error { _, err := loadThinkingLevelPreservationFixtures(d); return err }, thinkingLevelPreservationFixtureYAML, "cases:", "cases: []\n---\ncases:"},
		{"negative_name_substitution", func(d []byte) error { _, err := decodeThinkingLevelNegativeFixtures(d); return err }, thinkingLevelNegativeFixtureYAML, "field_drop_keeps_legacy_publish_compatible", "field_drop_keeps_legacy_publish_substitute"},
		{"negative_operation_substitution", func(d []byte) error { _, err := decodeThinkingLevelNegativeFixtures(d); return err }, thinkingLevelNegativeFixtureYAML, "operation: legacy_payload_under_field_drop", "operation: substitute_payload_under_field_drop"},
		{"negative_fragment_substitution", func(d []byte) error { _, err := decodeThinkingLevelNegativeFixtures(d); return err }, thinkingLevelNegativeFixtureYAML, "name: capability_name", "name: substitute_name"},
		{"publish_gate_name_substitution", func(d []byte) error { _, err := decodeThinkingLevelPublishGateFixtures(d); return err }, thinkingLevelPublishGateFixtureYAML, "tool_turn_raw_refused", "tool_turn_raw_substitute"},
		{"read_name_substitution", func(d []byte) error { _, err := decodeThinkingLevelReadEvidenceFixtures(d); return err }, thinkingLevelReadEvidenceFixtureYAML, "stored_edge_whitespace_raw_refused", "stored_edge_whitespace_substitute"},
		{"rewrite_name_substitution", func(d []byte) error { _, err := decodeThinkingLevelReadEvidenceFixtures(d); return err }, thinkingLevelReadEvidenceFixtureYAML, "legacy_blob_write_failure_serves", "legacy_blob_write_failure_substitute"},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			if err := mutation.decode(mutation.data); err != nil {
				t.Fatalf("unmutated fixture rejected: %v", err)
			}
			mutated := bytes.Replace(mutation.data, []byte(mutation.old), []byte(mutation.new), 1)
			if bytes.Equal(mutated, mutation.data) {
				t.Fatalf("mutation %q did not apply", mutation.old)
			}
			if err := mutation.decode(mutated); err == nil {
				t.Fatal("mutated fixture was accepted")
			}
		})
	}
}

func TestThinkingLevelMountedAdvertisementAndPublishRefusal(t *testing.T) {
	proofErr := executeThinkingLevelPreservationProof(droppingThinkingLevelEncoder{})
	negative := loadThinkingLevelNegative(t, "field_drop_withholds_capability_and_refuses_level_publish")
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
	h.thinkingLevelEvaluator = fixedThinkingLevelPreservationEvaluator{err: proofErr}
	h.scanContent = func([]byte) []string { scans++; return nil }

	schemaResponse := httptest.NewRecorder()
	h.GetSchemaVersion(schemaResponse, httptest.NewRequest(http.MethodGet, "/api/v1/schema/version", nil))
	var advertised schema.SchemaVersionResponse
	if err := json.Unmarshal(schemaResponse.Body.Bytes(), &advertised); err != nil {
		t.Fatal(err)
	}
	if hasThinkingLevelCapability(advertised.ContentCapabilities) || !hasObservedModelCapability(advertised.ContentCapabilities) {
		t.Fatalf("mounted advertisement=%v, want every token except thinking_level_v1", advertised.ContentCapabilities)
	}

	refused := mountedObservedModelPublish(t, h, thinkingLevelFixtureContent(t, "enriched_session_seed_and_per_turn_canonical_and_raw"))
	if refused.Code != http.StatusConflict {
		t.Fatalf("level-bearing publish status=%d body=%s, want 409", refused.Code, refused.Body.String())
	}
	body := decodeError(t, refused.Body.Bytes())
	for _, fragment := range negative.WantErrorContains[1:] {
		if !strings.Contains(body, fragment.Value) {
			t.Errorf("mounted refusal lacks %s fragment %q: %s", fragment.Name, fragment.Value, body)
		}
	}
	if scans != 0 || probes != 0 || creates != 0 || len(blobs.uploadedKeys) != 0 {
		t.Fatalf("refusal caused effects scans=%d probes=%d creates=%d writes=%d", scans, probes, creates, len(blobs.uploadedKeys))
	}
	legacy := mountedObservedModelPublish(t, h, thinkingLevelFixtureContent(t, "legacy_without_levels_stays_absent"))
	if legacy.Code != http.StatusCreated || len(blobs.uploadedKeys) != 1 {
		t.Fatalf("legacy publish under field drop status=%d writes=%d body=%s", legacy.Code, len(blobs.uploadedKeys), legacy.Body.String())
	}
}

func TestThinkingLevelMountedPublishGate(t *testing.T) {
	cases, err := decodeThinkingLevelPublishGateFixtures(thinkingLevelPublishGateFixtureYAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixtureCase := range cases {
		t.Run(fixtureCase.Name, func(t *testing.T) {
			var probes, creates, scans int
			var persisted sqlc.Transcript
			q := &mockQuerier{
				getTranscriptIDByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
					probes++
					return pgtype.UUID{}, errors.New("not found")
				},
				createTranscript: func(_ context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
					creates++
					persisted = sqlc.Transcript{ID: arg.ID, Visibility: "public", BlobKey: arg.BlobKey, WrappedDataKey: append([]byte(nil), arg.WrappedDataKey...), EncryptionAlgorithm: arg.EncryptionAlgorithm, KeyVersion: arg.KeyVersion}
					return persisted, nil
				},
				getTranscriptByID: func(context.Context, pgtype.UUID) (sqlc.Transcript, error) { return persisted, nil },
			}
			blobs := &descriptorCheckingStore{delegate: newFakeBlobStore()}
			h := newTestHandler(q, blobs)
			h.scanContent = func([]byte) []string { scans++; return nil }
			response := mountedObservedModelPublish(t, h, []byte(fixtureCase.Content))
			if len(fixtureCase.WantErrorParts) == 0 {
				if response.Code != http.StatusCreated || scans != 1 || creates != 1 || blobs.uploadCount() != 1 {
					t.Fatalf("accepted publish status=%d scans=%d creates=%d writes=%d body=%s", response.Code, scans, creates, blobs.uploadCount(), response.Body.String())
				}
				served := getContent(t, h, uuidFromPg(persisted.ID))
				if served.Code != http.StatusOK {
					t.Fatalf("accepted publish read status=%d body=%s", served.Code, served.Body.String())
				}
				var original schema.TranscriptContent
				if err := json.Unmarshal([]byte(fixtureCase.Content), &original); err != nil {
					t.Fatal(err)
				}
				payload := decodeContentResponseEnvelope(t, served.Body.Bytes())
				if payload.ThinkingLevel != original.SessionDetail.ThinkingLevel || payload.ThinkingLevelRaw != original.SessionDetail.ThinkingLevelRaw {
					t.Fatalf("served session level=%q/%q, want %q/%q", payload.ThinkingLevel, payload.ThinkingLevelRaw, original.SessionDetail.ThinkingLevel, original.SessionDetail.ThinkingLevelRaw)
				}
				if len(payload.ThinkingLevelHistory) != len(original.SessionDetail.ThinkingLevelHistory) {
					t.Fatalf("served history entries=%d, want %d", len(payload.ThinkingLevelHistory), len(original.SessionDetail.ThinkingLevelHistory))
				}
				for index, change := range original.SessionDetail.ThinkingLevelHistory {
					if payload.ThinkingLevelHistory[index] != change {
						t.Fatalf("served history entry %d=%+v, want %+v", index, payload.ThinkingLevelHistory[index], change)
					}
				}
				for index, turn := range original.SessionDetail.Turns {
					if payload.Turns[index].ThinkingLevel != turn.ThinkingLevel || payload.Turns[index].ThinkingLevelRaw != turn.ThinkingLevelRaw {
						t.Fatalf("served turn %d level=%q/%q, want %q/%q", index, payload.Turns[index].ThinkingLevel, payload.Turns[index].ThinkingLevelRaw, turn.ThinkingLevel, turn.ThinkingLevelRaw)
					}
				}
				return
			}
			if response.Code != http.StatusConflict {
				t.Fatalf("refused publish status=%d body=%s, want 409", response.Code, response.Body.String())
			}
			body := decodeError(t, response.Body.Bytes())
			for _, part := range fixtureCase.WantErrorParts {
				if !strings.Contains(body, part.Value) {
					t.Errorf("refusal lacks %s component %q: %s", part.Name, part.Value, body)
				}
			}
			if scans != 0 || probes != 0 || creates != 0 || blobs.uploadCount() != 0 {
				t.Fatalf("refusal caused effects scans=%d probes=%d creates=%d writes=%d", scans, probes, creates, blobs.uploadCount())
			}
		})
	}
}

func TestThinkingLevelRealHandlerMigrateRewriteReemit(t *testing.T) {
	const key = "transcripts/10000000-0000-4000-8000-000000000001.bin"
	fixtureCase := requireThinkingLevelPreservationCase(t, "legacy_contract_with_levels")
	s3 := newFakeBlobStore()
	s3.put(key, thinkingLevelFixtureContent(t, fixtureCase.Name))
	h := newTestHandler(publicTranscriptQuerier(key), s3)
	response := getContent(t, h, mustFixtureUUID(t))
	if response.Code != http.StatusOK {
		t.Fatalf("migrate/rewrite read status=%d body=%s", response.Code, response.Body.String())
	}
	if err := fixtureCase.assertThinkingLevels(decodeContentResponseEnvelope(t, response.Body.Bytes())); err != nil {
		t.Fatal(err)
	}
	if s3.uploadCount() != 1 {
		t.Fatalf("migrate/rewrite uploads=%d, want exactly 1 canonical rewrite", s3.uploadCount())
	}
	s3.mu.Lock()
	rewritten := append([]byte(nil), s3.lastBody...)
	s3.mu.Unlock()
	reemitted, _, err := NewContentMigrator().Migrate(context.Background(), rewritten)
	if err != nil {
		t.Fatalf("migrate rewritten production bytes: %v", err)
	}
	if err := fixtureCase.assertThinkingLevels(reemitted); err != nil {
		t.Fatal(err)
	}
}

func TestThinkingLevelMountedReadEvidence(t *testing.T) {
	fixture, err := decodeThinkingLevelReadEvidenceFixtures(thinkingLevelReadEvidenceFixtureYAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range fixture.Reads {
		t.Run(tc.Name, func(t *testing.T) {
			const key = "transcripts/10000000-0000-4000-8000-000000000001.bin"
			store := newFakeBlobStore()
			store.put(key, []byte(tc.Content))
			response := getContent(t, newTestHandler(publicTranscriptQuerier(key), store), mustFixtureUUID(t))
			if response.Code != http.StatusInternalServerError || !strings.Contains(decodeError(t, response.Body.Bytes()), tc.WantError) {
				t.Fatalf("stored invalid read status=%d body=%s, want 500 naming %q", response.Code, response.Body.String(), tc.WantError)
			}
			if store.uploadCount() != 0 {
				t.Fatalf("refused read rewrote %d generations", store.uploadCount())
			}
			store.put(key, []byte(tc.Corrected))
			recovered := getContent(t, newTestHandler(publicTranscriptQuerier(key), store), mustFixtureUUID(t))
			if recovered.Code != http.StatusOK {
				t.Fatalf("corrected read status=%d body=%s", recovered.Code, recovered.Body.String())
			}
			var corrected schema.SessionDetailPayload
			if err := json.Unmarshal([]byte(tc.Corrected), &corrected); err != nil {
				t.Fatal(err)
			}
			served := decodeContentResponseEnvelope(t, recovered.Body.Bytes())
			if served.ThinkingLevel != corrected.ThinkingLevel || served.ThinkingLevelRaw != corrected.ThinkingLevelRaw || served.Turns[0].ThinkingLevel != corrected.Turns[0].ThinkingLevel {
				t.Fatalf("corrected read served %+v, want the stored values", served)
			}
		})
	}
}

// validateThinkingLevelValues is the read-path guard after migrate-on-read; it
// must refuse stored values that bypassed decode-time schema validation.
func TestValidateThinkingLevelValuesRefusesInvalidStoredValues(t *testing.T) {
	fixture, err := decodeThinkingLevelReadEvidenceFixtures(thinkingLevelReadEvidenceFixtureYAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range fixture.Reads {
		var payload schema.SessionDetailPayload
		if err := json.Unmarshal([]byte(tc.Content), &payload); err != nil {
			t.Fatal(err)
		}
		err := validateThinkingLevelValues(&payload)
		if tc.WantError == "is not assistant" {
			// Values-only validation leaves the role rule to the strict decode.
			if err != nil {
				t.Fatalf("%s: values-only validation refused a valid value: %v", tc.Name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "after migrate-on-read and before serving") {
			t.Fatalf("%s: validateThinkingLevelValues error=%v, want an actionable read-path refusal", tc.Name, err)
		}
	}
	if err := validateThinkingLevelValues(nil); err == nil {
		t.Fatal("nil payload accepted")
	}
}

func TestThinkingLevelMountedRewriteFailurePolicy(t *testing.T) {
	fixture, err := decodeThinkingLevelReadEvidenceFixtures(thinkingLevelReadEvidenceFixtureYAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range fixture.RewriteFailures {
		t.Run(tc.Name, func(t *testing.T) {
			const key = "transcripts/10000000-0000-4000-8000-000000000001.bin"
			var envelope schema.TranscriptContent
			if err := json.Unmarshal(thinkingLevelFixtureContent(t, tc.ContentCase), &envelope); err != nil || envelope.SessionDetail == nil {
				t.Fatalf("decode fixture envelope: %v", err)
			}
			bare, err := json.Marshal(envelope.SessionDetail)
			if err != nil {
				t.Fatal(err)
			}
			store := newFakeBlobStore()
			store.put(key, bare)
			q := publicTranscriptQuerier(key)
			h := newTestHandler(q, failingRewriteStore{fakeBlobStore: store, failWrite: tc.Failure == "blob_write"})
			var logs bytes.Buffer
			old := log.Writer()
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(old) })
			response := getContent(t, h, mustFixtureUUID(t))
			if response.Code != tc.WantStatus {
				t.Fatalf("status=%d body=%s want=%d", response.Code, response.Body.String(), tc.WantStatus)
			}
			if response.Code == http.StatusOK {
				payload := decodeContentResponseEnvelope(t, response.Body.Bytes())
				if got := containsContentCapability(schema.RequiredContentCapabilities(*payload), schema.ContentCapabilityThinkingLevelV1); got != tc.WantLevels {
					t.Fatalf("served level evidence=%v, want %v", got, tc.WantLevels)
				}
			}
			if _, ok := store.blobs[key]; !ok {
				t.Fatal("old generation was not retained")
			}
		})
	}
}
