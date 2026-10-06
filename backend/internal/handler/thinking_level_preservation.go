package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

// Thinking-level preservation is the guarantee behind the thinking_level_v1
// content capability: the session-level thinkingLevel seed, its bounded native
// thinkingLevelRaw spelling, and the same pair on every assistant turn survive
// publish, typed migration, canonical rewrite, and re-emission byte-exactly,
// and absent values stay absent (absence is distinct from the canonical "off").
//
// The production proof below runs that exact path over a reviewed corpus. A
// failing proof withholds only the thinking_level_v1 token (see
// advertisedContentCapabilitiesWithEvaluators) and refuses level-bearing
// publishes before any write (see requireSupportedContentForHarness), so the
// evidence cannot be silently lost while legacy publishes stay accepted.

const thinkingLevelPreservationCaseCount = 4

// thinkingLevelPreservationCaseNames is the independent required-name
// inventory that guards the corpus against deletion or silent renaming.
var thinkingLevelPreservationCaseNames = [...]string{
	"enriched_session_seed_and_per_turn_canonical_and_raw",
	"legacy_contract_with_levels",
	"legacy_without_levels_stays_absent",
	"enriched_session_history_only_changes",
}

// thinkingLevelAbsent is the fixture sentinel for an omitted JSON member.
const thinkingLevelAbsent = "absent"

//go:embed testdata/thinking_level_preservation/cases.yaml
var thinkingLevelPreservationFixtureYAML []byte

type thinkingLevelPreservationFixture struct {
	Cases []thinkingLevelPreservationCase `yaml:"cases"`
}

type thinkingLevelPreservationCase struct {
	Name             string                          `yaml:"name"`
	ContractVersion  string                          `yaml:"contractVersion"`
	SchemaVersion    string                          `yaml:"schemaVersion"`
	SessionID        string                          `yaml:"sessionId"`
	Harness          string                          `yaml:"harness"`
	ThinkingLevel    string                          `yaml:"thinkingLevel"`
	ThinkingLevelRaw string                          `yaml:"thinkingLevelRaw"`
	History          []thinkingLevelChangeSource     `yaml:"history"`
	Turns            []thinkingLevelPreservationTurn `yaml:"turns"`
	Expected         thinkingLevelPreservationExpect `yaml:"expected"`
}

type thinkingLevelChangeSource struct {
	Level string `yaml:"level"`
	Raw   string `yaml:"raw"`
}

type thinkingLevelPreservationTurn struct {
	Role             string `yaml:"role"`
	ThinkingLevel    string `yaml:"thinkingLevel"`
	ThinkingLevelRaw string `yaml:"thinkingLevelRaw"`
}

type thinkingLevelPreservationExpect struct {
	ThinkingLevel    string                        `yaml:"thinkingLevel"`
	ThinkingLevelRaw string                        `yaml:"thinkingLevelRaw"`
	History          []thinkingLevelExpectedChange `yaml:"history"`
	Turns            []thinkingLevelExpectedMember `yaml:"turns"`
}

type thinkingLevelExpectedChange struct {
	Level string `yaml:"level"`
	Raw   string `yaml:"raw"`
}

type thinkingLevelExpectedMember struct {
	ThinkingLevel    string `yaml:"thinkingLevel"`
	ThinkingLevelRaw string `yaml:"thinkingLevelRaw"`
}

type thinkingLevelPreservationEvaluator interface {
	Evaluate() error
}

var _ thinkingLevelPreservationEvaluator = productionThinkingLevelPreservationEvaluator{}

type productionThinkingLevelPreservationEvaluator struct{}

func (productionThinkingLevelPreservationEvaluator) Evaluate() error {
	_, _, thinkingLevelErr := EvaluatePreservationProofs()
	return thinkingLevelErr
}

func (h *Handler) thinkingLevelProof() thinkingLevelPreservationEvaluator {
	if h.thinkingLevelEvaluator != nil {
		return h.thinkingLevelEvaluator
	}
	return productionThinkingLevelPreservationEvaluator{}
}

type thinkingLevelPreservationOutcome struct {
	Name string
	Err  error
}

// executeThinkingLevelPreservationProof runs the real typed migrator and the
// given canonical rewrite encoder over the corpus and returns the joined
// failures. It runs uncached; boot caches the production verdict.
func executeThinkingLevelPreservationProof(encoder contentRewriteEncoder) error {
	outcomes, err := executeThinkingLevelPreservationProofOutcomes(encoder)
	if err != nil {
		return err
	}
	failures := make([]error, 0, len(outcomes))
	for _, outcome := range outcomes {
		failures = append(failures, outcome.Err)
	}
	return errors.Join(failures...)
}

func executeThinkingLevelPreservationProofOutcomes(encoder contentRewriteEncoder) ([]thinkingLevelPreservationOutcome, error) {
	fixtures, err := loadThinkingLevelPreservationFixtures(thinkingLevelPreservationFixtureYAML)
	if err != nil {
		return nil, err
	}
	migrator := NewContentMigrator()
	outcomes := make([]thinkingLevelPreservationOutcome, 0, len(fixtures))
	for _, fixtureCase := range fixtures {
		outcomes = append(outcomes, thinkingLevelPreservationOutcome{Name: fixtureCase.Name, Err: fixtureCase.prove(migrator, encoder)})
	}
	return outcomes, nil
}

func (c thinkingLevelPreservationCase) prove(migrator ContentMigrator, encoder contentRewriteEncoder) error {
	raw, err := c.envelopeJSON()
	if err != nil {
		return err
	}
	payload, _, err := migrator.Migrate(context.Background(), raw)
	if err != nil {
		return fmt.Errorf("thinking-level preservation proof failed because fixture %q could not traverse the production typed migrator in handler.executeThinkingLevelPreservationProof during capability evaluation; thinking_level_v1 is withheld and level-bearing publishes must remain blocked; fix the migrator or fixture, then restart: %w", c.Name, err)
	}
	canonical, err := encoder.Encode(currentContractVersion, payload)
	if err != nil {
		return fmt.Errorf("thinking-level preservation proof failed because fixture %q could not round-trip the production canonical rewrite boundary in handler.executeThinkingLevelPreservationProof during capability evaluation; thinking_level_v1 is withheld and level-bearing publishes must remain blocked; fix the canonical encoder, then restart: %w", c.Name, err)
	}
	reemitted, _, err := migrator.Migrate(context.Background(), canonical)
	if err != nil {
		return fmt.Errorf("thinking-level preservation proof failed because fixture %q could not traverse the production typed rewrite/re-emit path in handler.executeThinkingLevelPreservationProof during capability evaluation; thinking_level_v1 is withheld and level-bearing publishes must remain blocked; fix the canonical encoder or migrator, then restart: %w", c.Name, err)
	}
	return c.assertThinkingLevels(reemitted)
}

func loadThinkingLevelPreservationFixtures(data []byte) ([]thinkingLevelPreservationCase, error) {
	const where = "in handler.loadThinkingLevelPreservationFixtures before capability evaluation; thinking_level_v1 cannot be advertised"
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var fixture thinkingLevelPreservationFixture
	if err := decoder.Decode(&fixture); err != nil {
		return nil, fmt.Errorf("thinking-level preservation fixture load failed because strict YAML decoding rejected testdata/thinking_level_preservation/cases.yaml %s; correct the fixture fields and retry: %w", where, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("thinking-level preservation fixture load failed because a trailing YAML document could not be decoded %s; keep exactly one YAML document and retry: %w", where, err)
		}
		return nil, fmt.Errorf("thinking-level preservation fixture load failed because testdata/thinking_level_preservation/cases.yaml contains multiple YAML documents %s; keep exactly one YAML document and retry", where)
	}
	if len(fixture.Cases) != thinkingLevelPreservationCaseCount {
		return nil, fmt.Errorf("thinking-level preservation fixture inventory failed because testdata/thinking_level_preservation/cases.yaml contains %d cases, want exactly %d %s; restore the reviewed corpus and retry", len(fixture.Cases), thinkingLevelPreservationCaseCount, where)
	}
	required := make(map[string]struct{}, len(thinkingLevelPreservationCaseNames))
	for _, name := range thinkingLevelPreservationCaseNames {
		required[name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(fixture.Cases))
	for index, fixtureCase := range fixture.Cases {
		if fixtureCase.Name == "" || fixtureCase.Name != strings.TrimSpace(fixtureCase.Name) {
			return nil, fmt.Errorf("thinking-level preservation fixture inventory failed because case %d has an empty or edge-padded name %q %s; supply the exact registered name and retry", index, fixtureCase.Name, where)
		}
		if _, ok := required[fixtureCase.Name]; !ok {
			return nil, fmt.Errorf("thinking-level preservation fixture inventory failed because case %q is not in the independent required-name inventory %s; restore a registered case name and retry", fixtureCase.Name, where)
		}
		if _, duplicate := seen[fixtureCase.Name]; duplicate {
			return nil, fmt.Errorf("thinking-level preservation fixture inventory failed because case %q is duplicated %s; keep each registered case exactly once and retry", fixtureCase.Name, where)
		}
		seen[fixtureCase.Name] = struct{}{}
		if len(fixtureCase.Turns) != len(fixtureCase.Expected.Turns) {
			return nil, fmt.Errorf("thinking-level preservation fixture validation failed because case %q has %d source turns and %d expected turns %s; align the per-turn expectation lists and retry", fixtureCase.Name, len(fixtureCase.Turns), len(fixtureCase.Expected.Turns), where)
		}
	}
	for _, name := range thinkingLevelPreservationCaseNames {
		if _, ok := seen[name]; !ok {
			return nil, fmt.Errorf("thinking-level preservation fixture inventory failed because required case %q is absent %s; restore the reviewed case and retry", name, where)
		}
	}
	return fixture.Cases, nil
}

func (c thinkingLevelPreservationCase) envelopeJSON() ([]byte, error) {
	payload := &schema.SessionDetailPayload{
		SchemaVersion:        schema.PushContractVersion(c.SchemaVersion),
		ID:                   c.SessionID,
		Harness:              schema.Harness(c.Harness),
		ThinkingLevel:        schema.ThinkingLevel(c.ThinkingLevel),
		ThinkingLevelRaw:     schema.ThinkingLevelRaw(c.ThinkingLevelRaw),
		ThinkingLevelHistory: make([]schema.ThinkingLevelChange, 0, len(c.History)),
		Turns:                make([]schema.TurnDetail, 0, len(c.Turns)),
	}
	for _, source := range c.History {
		payload.ThinkingLevelHistory = append(payload.ThinkingLevelHistory, schema.ThinkingLevelChange{
			Level: schema.ThinkingLevel(source.Level),
			Raw:   schema.ThinkingLevelRaw(source.Raw),
		})
	}
	for index, source := range c.Turns {
		payload.Turns = append(payload.Turns, schema.TurnDetail{
			Index:            index,
			Role:             schema.Role(source.Role),
			Content:          fmt.Sprintf("%s turn %d", source.Role, index),
			ThinkingLevel:    schema.ThinkingLevel(source.ThinkingLevel),
			ThinkingLevelRaw: schema.ThinkingLevelRaw(source.ThinkingLevelRaw),
		})
	}
	if err := validateThinkingLevelEvidence(payload); err != nil {
		return nil, fmt.Errorf("thinking-level preservation fixture validation failed because case %q carries invalid source evidence in handler.thinkingLevelPreservationCase.envelopeJSON while constructing the real typed input; capability evaluation cannot proceed; correct the fixture value and retry: %w", c.Name, err)
	}
	encoded, err := json.Marshal(schema.TranscriptContent{
		ContractVersion: schema.PushContractVersion(c.ContractVersion),
		Kind:            schema.ContentKindSessionDetail,
		SessionDetail:   payload,
	})
	if err != nil {
		return nil, fmt.Errorf("thinking-level preservation fixture encoding failed because case %q could not become a typed TranscriptContent envelope in handler.thinkingLevelPreservationCase.envelopeJSON before production migration; capability evaluation cannot proceed; correct the fixture and retry: %w", c.Name, err)
	}
	return encoded, nil
}

// assertThinkingLevels compares the re-emitted session, history, and per-turn
// members to the exact expected JSON (or absence) and checks that the re-emitted
// payload still requires thinking_level_v1 exactly when evidence is expected.
func (c thinkingLevelPreservationCase) assertThinkingLevels(payload *schema.SessionDetailPayload) error {
	const consequence = "thinking_level_v1 is withheld and level-bearing publishes must remain blocked; restore the field on the production typed encoder and retry"
	if payload == nil || len(payload.Turns) != len(c.Expected.Turns) || len(payload.ThinkingLevelHistory) != len(c.Expected.History) {
		gotTurns, gotHistory := 0, 0
		if payload != nil {
			gotTurns, gotHistory = len(payload.Turns), len(payload.ThinkingLevelHistory)
		}
		return fmt.Errorf("thinking-level preservation proof failed because case %q re-emitted thinkingLevelHistory entries=%d and turns=%d, want %d and %d in handler.thinkingLevelPreservationCase.assertThinkingLevels after typed migrate/rewrite; %s", c.Name, gotHistory, gotTurns, len(c.Expected.History), len(c.Expected.Turns), consequence)
	}
	sessionJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("thinking-level preservation proof failed because case %q session could not be encoded for comparison in handler.thinkingLevelPreservationCase.assertThinkingLevels after typed migrate/rewrite; %s: %w", c.Name, consequence, err)
	}
	if err := compareThinkingLevelMembers(c.Name, "session", sessionJSON, c.Expected.ThinkingLevel, c.Expected.ThinkingLevelRaw); err != nil {
		return err
	}
	evidence := c.Expected.ThinkingLevel != thinkingLevelAbsent || c.Expected.ThinkingLevelRaw != thinkingLevelAbsent
	for index, want := range c.Expected.History {
		changeJSON, err := json.Marshal(payload.ThinkingLevelHistory[index])
		if err != nil {
			return fmt.Errorf("thinking-level preservation proof failed because case %q history entry %d could not be encoded for comparison in handler.thinkingLevelPreservationCase.assertThinkingLevels after typed migrate/rewrite; %s: %w", c.Name, index, consequence, err)
		}
		if err := compareThinkingLevelChangeMembers(c.Name, fmt.Sprintf("history entry %d", index), changeJSON, want.Level, want.Raw); err != nil {
			return err
		}
		evidence = evidence || want.Level != thinkingLevelAbsent || want.Raw != thinkingLevelAbsent
	}
	for index, want := range c.Expected.Turns {
		turnJSON, err := json.Marshal(payload.Turns[index])
		if err != nil {
			return fmt.Errorf("thinking-level preservation proof failed because case %q turn %d could not be encoded for comparison in handler.thinkingLevelPreservationCase.assertThinkingLevels after typed migrate/rewrite; %s: %w", c.Name, index, consequence, err)
		}
		if err := compareThinkingLevelMembers(c.Name, fmt.Sprintf("turn %d", index), turnJSON, want.ThinkingLevel, want.ThinkingLevelRaw); err != nil {
			return err
		}
		evidence = evidence || want.ThinkingLevel != thinkingLevelAbsent || want.ThinkingLevelRaw != thinkingLevelAbsent
	}
	if requires := containsContentCapability(schema.RequiredContentCapabilities(*payload), schema.ContentCapabilityThinkingLevelV1); requires != evidence {
		return fmt.Errorf("thinking-level preservation proof failed because case %q re-emitted a payload whose thinking_level_v1 requirement is %t, want %t in handler.thinkingLevelPreservationCase.assertThinkingLevels after typed migrate/rewrite; %s", c.Name, requires, evidence, consequence)
	}
	return nil
}

func compareThinkingLevelMembers(name, scope string, document []byte, wantLevel, wantRaw string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(document, &object); err != nil {
		return fmt.Errorf("thinking-level preservation proof failed because case %q %s re-emission was not a JSON object in handler.compareThinkingLevelMembers after typed migrate/rewrite; thinking_level_v1 is withheld; repair the canonical encoder and retry: %w", name, scope, err)
	}
	for _, member := range []struct{ key, want string }{{"thinkingLevel", wantLevel}, {"thinkingLevelRaw", wantRaw}} {
		got, present := object[member.key]
		if member.want == thinkingLevelAbsent {
			if present {
				return fmt.Errorf("thinking-level preservation proof failed because case %q %s fabricated %s=%s in handler.compareThinkingLevelMembers after typed migrate/rewrite; absence must stay distinct from any level, so thinking_level_v1 is withheld; omit unobserved evidence and retry", name, scope, member.key, got)
			}
			continue
		}
		if !present || string(got) != member.want {
			return fmt.Errorf("thinking-level preservation proof failed because case %q %s re-emitted %s=%s (present=%t), want exact JSON %s in handler.compareThinkingLevelMembers after typed migrate/rewrite; thinking_level_v1 is withheld and level-bearing publishes must remain blocked; restore the field on the production typed encoder and retry", name, scope, member.key, got, present, member.want)
		}
	}
	return nil
}

// compareThinkingLevelChangeMembers is the history-entry counterpart of
// compareThinkingLevelMembers: it compares the re-emitted level/raw members of
// one thinkingLevelHistory entry to the exact expected JSON (or absence).
func compareThinkingLevelChangeMembers(name, scope string, document []byte, wantLevel, wantRaw string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(document, &object); err != nil {
		return fmt.Errorf("thinking-level preservation proof failed because case %q %s re-emission was not a JSON object in handler.compareThinkingLevelChangeMembers after typed migrate/rewrite; thinking_level_v1 is withheld; repair the canonical encoder and retry: %w", name, scope, err)
	}
	for _, member := range []struct{ key, want string }{{"level", wantLevel}, {"raw", wantRaw}} {
		got, present := object[member.key]
		if member.want == thinkingLevelAbsent {
			if present {
				return fmt.Errorf("thinking-level preservation proof failed because case %q %s fabricated thinkingLevelHistory %s=%s in handler.compareThinkingLevelChangeMembers after typed migrate/rewrite; absence must stay distinct from any level, so thinking_level_v1 is withheld; omit unobserved evidence and retry", name, scope, member.key, got)
			}
			continue
		}
		if !present || string(got) != member.want {
			return fmt.Errorf("thinking-level preservation proof failed because case %q %s re-emitted thinkingLevelHistory %s=%s (present=%t), want exact JSON %s in handler.compareThinkingLevelChangeMembers after typed migrate/rewrite; thinking_level_v1 is withheld and level-bearing publishes must remain blocked; restore the field on the production typed encoder and retry", name, scope, member.key, got, present, member.want)
		}
	}
	return nil
}

// validateThinkingLevelEvidence is the publish-time producer rule: the
// session-level pair must be valid, and per-turn levels may appear only on
// assistant turns with a valid canonical level and bounded raw spelling.
func validateThinkingLevelEvidence(payload *schema.SessionDetailPayload) error {
	if payload == nil {
		return fmt.Errorf("thinking-level evidence validation failed because the typed session payload is nil in handler.validateThinkingLevelEvidence after upload decoding and before secret scan or storage; no transcript bytes or metadata were written; republish the transcript with a sessionDetail payload, then retry")
	}
	if err := validateThinkingLevelPair(payload.ThinkingLevel, payload.ThinkingLevelRaw); err != nil {
		return fmt.Errorf("thinking-level evidence validation failed because session %q carries an invalid session-level thinkingLevel/thinkingLevelRaw in handler.validateThinkingLevelEvidence after upload decoding and before secret scan or storage; no transcript bytes or metadata were written; correct the producer's session seed and retry: %w", payload.ID, err)
	}
	for index, change := range payload.ThinkingLevelHistory {
		if err := change.Validate(); err != nil {
			return fmt.Errorf("thinking-level evidence validation failed because session %q thinkingLevelHistory entry %d violates the released Schema history policy in handler.validateThinkingLevelEvidence after upload decoding and before secret scan or storage; no transcript bytes or metadata were written; correct the producer's history entry and retry: %w", payload.ID, index, err)
		}
	}
	for index, turn := range payload.Turns {
		if err := schema.ValidateThinkingLevelEvidence(turn.Role, turn.ThinkingLevel, turn.ThinkingLevelRaw); err != nil {
			return fmt.Errorf("thinking-level evidence validation failed because session %q turn %d violates the released Schema policy in handler.validateThinkingLevelEvidence after upload decoding and before secret scan or storage; no transcript bytes or metadata were written; correct the producer attribution and thinkingLevel/thinkingLevelRaw values, then retry: %w", payload.ID, index, err)
		}
	}
	return nil
}

// validateThinkingLevelValues is the read-path check after migrate-on-read:
// stored values must still be canonical and bounded before serving or rewrite.
func validateThinkingLevelValues(payload *schema.SessionDetailPayload) error {
	if payload == nil {
		return fmt.Errorf("thinking-level value validation failed because the typed session payload is nil in handler.validateThinkingLevelValues after migrate-on-read and before serving; no response or replacement was produced; repair or republish the transcript, then retry")
	}
	if err := validateThinkingLevelPair(payload.ThinkingLevel, payload.ThinkingLevelRaw); err != nil {
		return fmt.Errorf("thinking-level value validation failed because session %q carries an invalid stored session-level thinkingLevel/thinkingLevelRaw in handler.validateThinkingLevelValues after migrate-on-read and before serving; no response or canonical rewrite was produced; correct the stored producer evidence and republish: %w", payload.ID, err)
	}
	for index, change := range payload.ThinkingLevelHistory {
		if err := change.Validate(); err != nil {
			return fmt.Errorf("thinking-level value validation failed because session %q thinkingLevelHistory entry %d carries invalid stored evidence in handler.validateThinkingLevelValues after migrate-on-read and before serving; no response or canonical rewrite was produced; correct the stored producer evidence and republish: %w", payload.ID, index, err)
		}
	}
	for index, turn := range payload.Turns {
		if err := validateThinkingLevelPair(turn.ThinkingLevel, turn.ThinkingLevelRaw); err != nil {
			return fmt.Errorf("thinking-level value validation failed because session %q turn %d carries an invalid stored thinkingLevel/thinkingLevelRaw in handler.validateThinkingLevelValues after migrate-on-read and before serving; no response or canonical rewrite was produced; correct the stored producer evidence and republish: %w", payload.ID, index, err)
		}
	}
	return nil
}

func validateThinkingLevelPair(level schema.ThinkingLevel, raw schema.ThinkingLevelRaw) error {
	if !level.IsValid() {
		return fmt.Errorf("thinkingLevel %q is outside the canonical set %v", level, schema.AllThinkingLevels)
	}
	if raw != "" {
		if err := raw.Validate(); err != nil {
			return err
		}
	}
	return nil
}
