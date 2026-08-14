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
	"sync"

	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

const observedModelPreservationCaseCount = 2

var observedModelPreservationCaseNames = [...]string{
	"enriched_repeated_change_and_omission",
	"legacy_without_observations",
}

//go:embed testdata/observed_model_preservation/cases.yaml
var observedModelPreservationFixtureYAML []byte

type observedModelPreservationFixture struct {
	Cases []observedModelPreservationCase `yaml:"cases"`
}

type observedModelPreservationCase struct {
	Name                 string    `yaml:"name"`
	ContractVersion      string    `yaml:"contractVersion"`
	SchemaVersion        string    `yaml:"schemaVersion"`
	SessionID            string    `yaml:"sessionId"`
	Harness              string    `yaml:"harness"`
	ObservedModels       []*string `yaml:"observedModels"`
	ExpectedObservedJSON []string  `yaml:"expectedObservedJSON"`
}

type contentRewriteEncoder interface {
	Encode(schema.PushContractVersion, *schema.SessionDetailPayload) ([]byte, error)
}

type observedModelPreservationEvaluator interface {
	Evaluate() error
}

var _ observedModelPreservationEvaluator = productionObservedModelPreservationEvaluator{}
var _ contentRewriteEncoder = canonicalContentRewriteEncoder{}

type productionObservedModelPreservationEvaluator struct{}

func (productionObservedModelPreservationEvaluator) Evaluate() error {
	return proveObservedModelPreservation()
}

type canonicalContentRewriteEncoder struct{}

func (canonicalContentRewriteEncoder) Encode(version schema.PushContractVersion, payload *schema.SessionDetailPayload) ([]byte, error) {
	encoded, err := json.Marshal(schema.TranscriptContent{
		ContractVersion: version,
		Kind:            schema.ContentKindSessionDetail,
		SessionDetail:   payload,
	})
	if err != nil {
		return nil, fmt.Errorf("canonical transcript rewrite encoding failed because the typed SessionDetailPayload could not be marshaled in handler.canonicalContentRewriteEncoder.Encode during migrate-on-read rewrite; the stored generation remains authoritative and no replacement can be advertised as preserving enriched evidence; repair the typed payload or schema pin, then retry: %w", err)
	}
	return encoded, nil
}

var productionContentRewriteEncoder contentRewriteEncoder = canonicalContentRewriteEncoder{}

var (
	observedModelPreservationOnce sync.Once
	observedModelPreservationErr  error
)

func proveObservedModelPreservation() error {
	observedModelPreservationOnce.Do(func() {
		observedModelPreservationErr = executeObservedModelPreservationProof(productionContentRewriteEncoder)
	})
	return observedModelPreservationErr
}

func requireSupportedContentCapabilityWithEvaluator(raw []byte, evaluator observedModelPreservationEvaluator) error {
	// Provider-native and legacy JSONL without enriched evidence retains the
	// historical byte-for-byte publish path; decoding it is a read concern.
	presence, presenceErr := inspectObservedModelMembers(raw)
	if presenceErr != nil {
		return presenceErr
	}
	if !presence {
		return nil
	}
	payload, _, err := NewContentMigrator().Migrate(context.Background(), raw)
	if err != nil {
		return fmt.Errorf("uploaded transcript content could not be decoded through handler.requireSupportedContentCapability before secret scan or storage; no transcript bytes or metadata were written; repair the transcript envelope and retry: %w", err)
	}
	if err := validateObservedModelEvidence(payload); err != nil {
		return err
	}
	required := schema.RequiredContentCapabilities(*payload)
	if !containsContentCapability(required, schema.ContentCapabilityObservedModelV1) {
		return nil
	}
	if err := evaluator.Evaluate(); err != nil {
		return fmt.Errorf("enriched transcript publish refused because the uploaded transcript_file carries observedModel evidence while Village's production preservation proof is failing in handler.requireSupportedContentCapability before secret scan or storage; no transcript bytes or metadata were written, and silently stripping the evidence would misattribute model output; deploy a Village build whose GET /api/v1/schema/version advertises %q after the preservation gate passes, then retry: %w", schema.ContentCapabilityObservedModelV1, err)
	}
	return nil
}

func containsContentCapability(capabilities []schema.ContentCapability, wanted schema.ContentCapability) bool {
	for _, capability := range capabilities {
		if capability == wanted {
			return true
		}
	}
	return false
}

func payloadCarriesObservedModels(payload *schema.SessionDetailPayload) bool {
	return payload != nil && containsContentCapability(schema.RequiredContentCapabilities(*payload), schema.ContentCapabilityObservedModelV1)
}

func validateObservedModelEvidence(payload *schema.SessionDetailPayload) error {
	if payload == nil {
		return fmt.Errorf("observed-model evidence validation failed because the typed session payload is nil in handler.validateObservedModelEvidence after migrate-on-read and before serving or rewrite; no response or replacement was produced; repair or republish the transcript with a sessionDetail payload, then retry")
	}
	for index, turn := range payload.Turns {
		if err := schema.ValidateObservedModelEvidence(turn.Role, turn.ObservedModel); err != nil {
			return fmt.Errorf("observed-model evidence validation failed because session %q turn %d violates the released Schema policy in handler.validateObservedModelEvidence after upload decoding and before secret scan or storage; no transcript bytes or metadata were written; correct the producer attribution and observedModel value, then retry: %w", payload.ID, index, err)
		}
	}
	return nil
}

func validateObservedModelValues(payload *schema.SessionDetailPayload) error {
	if payload == nil {
		return fmt.Errorf("observed-model value validation failed because the typed session payload is nil in handler.validateObservedModelValues after migrate-on-read and before serving; no response or replacement was produced; repair or republish the transcript, then retry")
	}
	for index, turn := range payload.Turns {
		if turn.ObservedModel == "" {
			continue
		}
		if _, err := schema.NewObservedModelID(turn.ObservedModel.String()); err != nil {
			return fmt.Errorf("observed-model value validation failed because session %q turn %d carries an invalid observedModel in handler.validateObservedModelValues after migrate-on-read and before serving; no response or canonical rewrite was produced; correct the stored producer evidence and retry: %w", payload.ID, index, err)
		}
	}
	return nil
}

func inspectObservedModelMembers(raw []byte) (bool, error) {
	documents := [][]byte{bytes.TrimSpace(raw)}
	if !json.Valid(documents[0]) {
		documents = bytes.Split(raw, []byte("\n"))
	}
	present := false
	for _, document := range documents {
		document = bytes.TrimSpace(document)
		if len(document) == 0 {
			continue
		}
		if !json.Valid(document) {
			decoder := json.NewDecoder(bytes.NewReader(document))
			for {
				token, err := decoder.Token()
				if name, ok := token.(string); ok && name == "observedModel" {
					return true, fmt.Errorf("uploaded transcript content could not be decoded through handler.inspectObservedModelMembers before secret scan or storage; an observedModel member began but its JSON value or containing document is malformed; no transcript bytes or metadata were written; repair the enriched transcript envelope and retry: %w", err)
				}
				if err != nil {
					break
				}
			}
			continue
		}
		var root map[string]json.RawMessage
		if json.Unmarshal(document, &root) != nil {
			continue
		}
		detail := root
		if nested, ok := root["sessionDetail"]; ok {
			if json.Unmarshal(nested, &detail) != nil {
				continue
			}
		}
		var turns []json.RawMessage
		if json.Unmarshal(detail["turns"], &turns) != nil {
			continue
		}
		for index, rawTurn := range turns {
			var turn map[string]json.RawMessage
			if err := json.Unmarshal(rawTurn, &turn); err != nil {
				continue
			}
			value, memberPresent := turn["observedModel"]
			if !memberPresent {
				continue
			}
			present = true
			var observed *string
			if err := json.Unmarshal(value, &observed); err != nil || observed == nil || *observed == "" {
				return true, fmt.Errorf("observed-model evidence validation failed because turn %d contains a present observedModel that is null, empty, or not a string in handler.inspectObservedModelMembers during publish decoding before secret scan or storage; no transcript bytes or metadata were written, so malformed enriched evidence cannot bypass capability negotiation; omit the member for legacy content or provide a non-empty Schema-valid model identifier, then retry", index)
			}
		}
	}
	return present, nil
}

func executeObservedModelPreservationProof(encoder contentRewriteEncoder) error {
	outcomes, err := executeObservedModelPreservationProofOutcomes(encoder)
	if err != nil {
		return err
	}
	var failures []error
	for _, outcome := range outcomes {
		failures = append(failures, outcome.Err)
	}
	return errors.Join(failures...)
}

type observedModelPreservationOutcome struct {
	Name string
	Err  error
}

func executeObservedModelPreservationProofOutcomes(encoder contentRewriteEncoder) ([]observedModelPreservationOutcome, error) {
	fixtures, err := loadObservedModelPreservationFixtures(observedModelPreservationFixtureYAML)
	if err != nil {
		return nil, err
	}
	migrator := NewContentMigrator()
	outcomes := make([]observedModelPreservationOutcome, 0, len(fixtures))
	for _, fixtureCase := range fixtures {
		var outcomeErr error
		raw, err := fixtureCase.envelopeJSON()
		if err != nil {
			outcomes = append(outcomes, observedModelPreservationOutcome{Name: fixtureCase.Name, Err: err})
			continue
		}
		payload, _, err := migrator.Migrate(context.Background(), raw)
		if err != nil {
			outcomeErr = fmt.Errorf("observed-model preservation proof failed because fixture %q could not traverse the production typed migrator in handler.executeObservedModelPreservationProof during capability evaluation; enriched capability is withheld and enriched publishes must remain blocked; fix the migrator or fixture, then restart: %w", fixtureCase.Name, err)
			outcomes = append(outcomes, observedModelPreservationOutcome{Name: fixtureCase.Name, Err: outcomeErr})
			continue
		}
		canonical, err := encoder.Encode(currentContractVersion, payload)
		if err != nil {
			outcomes = append(outcomes, observedModelPreservationOutcome{Name: fixtureCase.Name, Err: fmt.Errorf("fixture %q rewrite failed: %w", fixtureCase.Name, err)})
			continue
		}
		reemitted, _, err := migrator.Migrate(context.Background(), canonical)
		if err != nil {
			outcomes = append(outcomes, observedModelPreservationOutcome{Name: fixtureCase.Name, Err: fmt.Errorf("observed-model preservation proof failed because fixture %q could not traverse the production typed rewrite/re-emit path in handler.executeObservedModelPreservationProof during capability evaluation; enriched capability is withheld and enriched publishes must remain blocked; fix the canonical encoder or migrator, then restart: %w", fixtureCase.Name, err)})
			continue
		}
		outcomeErr = fixtureCase.assertObservedModels(reemitted)
		outcomes = append(outcomes, observedModelPreservationOutcome{Name: fixtureCase.Name, Err: outcomeErr})
	}
	return outcomes, nil
}

func loadObservedModelPreservationFixtures(data []byte) ([]observedModelPreservationCase, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var fixture observedModelPreservationFixture
	if err := decoder.Decode(&fixture); err != nil {
		return nil, fmt.Errorf("observed-model preservation fixture load failed because strict YAML decoding rejected testdata/observed_model_preservation/cases.yaml in handler.loadObservedModelPreservationFixtures before capability evaluation; enriched capability cannot be advertised; correct the fixture fields and retry: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("observed-model preservation fixture load failed because a trailing YAML document could not be decoded in handler.loadObservedModelPreservationFixtures before capability evaluation; enriched capability cannot be advertised; keep exactly one YAML document and retry: %w", err)
		}
		return nil, fmt.Errorf("observed-model preservation fixture load failed because testdata/observed_model_preservation/cases.yaml contains multiple YAML documents in handler.loadObservedModelPreservationFixtures before capability evaluation; enriched capability cannot be advertised; keep exactly one YAML document and retry")
	}
	if len(fixture.Cases) != observedModelPreservationCaseCount {
		return nil, fmt.Errorf("observed-model preservation fixture inventory failed because testdata/observed_model_preservation/cases.yaml contains %d cases, want exactly %d in handler.loadObservedModelPreservationFixtures before capability evaluation; enriched capability cannot be advertised; restore the reviewed corpus and retry", len(fixture.Cases), observedModelPreservationCaseCount)
	}
	required := make(map[string]struct{}, len(observedModelPreservationCaseNames))
	for _, name := range observedModelPreservationCaseNames {
		required[name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(fixture.Cases))
	for index, fixtureCase := range fixture.Cases {
		if fixtureCase.Name == "" || fixtureCase.Name != strings.TrimSpace(fixtureCase.Name) {
			return nil, fmt.Errorf("observed-model preservation fixture inventory failed because case %d has an empty or edge-padded name %q in handler.loadObservedModelPreservationFixtures before capability evaluation; enriched capability cannot be advertised; supply the exact registered name and retry", index, fixtureCase.Name)
		}
		if _, ok := required[fixtureCase.Name]; !ok {
			return nil, fmt.Errorf("observed-model preservation fixture inventory failed because case %q is not in the independent required-name inventory in handler.loadObservedModelPreservationFixtures before capability evaluation; enriched capability cannot be advertised; restore a registered case name and retry", fixtureCase.Name)
		}
		if _, duplicate := seen[fixtureCase.Name]; duplicate {
			return nil, fmt.Errorf("observed-model preservation fixture inventory failed because case %q is duplicated in handler.loadObservedModelPreservationFixtures before capability evaluation; enriched capability cannot be advertised; keep each registered case exactly once and retry", fixtureCase.Name)
		}
		seen[fixtureCase.Name] = struct{}{}
		if len(fixtureCase.ObservedModels) != len(fixtureCase.ExpectedObservedJSON) {
			return nil, fmt.Errorf("observed-model preservation fixture validation failed because case %q has %d source turns and %d expected JSON observations in handler.loadObservedModelPreservationFixtures before capability evaluation; enriched capability cannot be advertised; align the per-turn expectation lists and retry", fixtureCase.Name, len(fixtureCase.ObservedModels), len(fixtureCase.ExpectedObservedJSON))
		}
	}
	for _, name := range observedModelPreservationCaseNames {
		if _, ok := seen[name]; !ok {
			return nil, fmt.Errorf("observed-model preservation fixture inventory failed because required case %q is absent in handler.loadObservedModelPreservationFixtures before capability evaluation; enriched capability cannot be advertised; restore the reviewed case and retry", name)
		}
	}
	return fixture.Cases, nil
}

func (c observedModelPreservationCase) envelopeJSON() ([]byte, error) {
	payload := &schema.SessionDetailPayload{
		SchemaVersion: schema.PushContractVersion(c.SchemaVersion),
		ID:            c.SessionID,
		Harness:       schema.Harness(c.Harness),
		Turns:         make([]schema.TurnDetail, 0, len(c.ObservedModels)),
	}
	for index, observed := range c.ObservedModels {
		turn := schema.TurnDetail{Index: index, Role: schema.RoleAssistant, Content: fmt.Sprintf("assistant turn %d", index)}
		if observed != nil {
			validated, err := schema.NewObservedModelID(*observed)
			if err != nil {
				return nil, fmt.Errorf("observed-model preservation fixture validation failed because case %q turn %d has invalid source evidence in handler.observedModelPreservationCase.envelopeJSON while constructing the real typed input; capability evaluation cannot proceed; correct the fixture value and retry: %w", c.Name, index, err)
			}
			turn.ObservedModel = validated
		}
		payload.Turns = append(payload.Turns, turn)
	}
	encoded, err := json.Marshal(schema.TranscriptContent{
		ContractVersion: schema.PushContractVersion(c.ContractVersion),
		Kind:            schema.ContentKindSessionDetail,
		SessionDetail:   payload,
	})
	if err != nil {
		return nil, fmt.Errorf("observed-model preservation fixture encoding failed because case %q could not become a typed TranscriptContent envelope in handler.observedModelPreservationCase.envelopeJSON before production migration; capability evaluation cannot proceed; correct the fixture and retry: %w", c.Name, err)
	}
	return encoded, nil
}

func (c observedModelPreservationCase) assertObservedModels(payload *schema.SessionDetailPayload) error {
	if payload == nil || len(payload.Turns) != len(c.ExpectedObservedJSON) {
		got := 0
		if payload != nil {
			got = len(payload.Turns)
		}
		return fmt.Errorf("observed-model preservation proof failed because case %q re-emitted %d turns, want %d in handler.observedModelPreservationCase.assertObservedModels after typed migrate/rewrite; enriched capability is withheld and enriched publishes must remain blocked; restore lossless production encoding and retry", c.Name, got, len(c.ExpectedObservedJSON))
	}
	for index, wantJSON := range c.ExpectedObservedJSON {
		turnJSON, err := json.Marshal(payload.Turns[index])
		if err != nil {
			return fmt.Errorf("observed-model preservation proof failed because case %q turn %d could not be encoded for semantic comparison in handler.observedModelPreservationCase.assertObservedModels after typed migrate/rewrite; enriched capability is withheld; repair the schema value and retry: %w", c.Name, index, err)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(turnJSON, &object); err != nil {
			return fmt.Errorf("observed-model preservation proof failed because case %q turn %d re-emission was not a JSON object in handler.observedModelPreservationCase.assertObservedModels after typed migrate/rewrite; enriched capability is withheld; repair the canonical encoder and retry: %w", c.Name, index, err)
		}
		got, present := object["observedModel"]
		if wantJSON == "absent" {
			if present {
				return fmt.Errorf("observed-model preservation proof failed because case %q turn %d fabricated observedModel=%s in handler.observedModelPreservationCase.assertObservedModels after typed migrate/rewrite; enriched capability is withheld and legacy behavior cannot be trusted; omit unobserved evidence and retry", c.Name, index, got)
			}
			continue
		}
		if !present || string(got) != wantJSON {
			return fmt.Errorf("observed-model preservation proof failed because case %q turn %d re-emitted observedModel=%s (present=%t), want exact JSON %s in handler.observedModelPreservationCase.assertObservedModels after typed migrate/rewrite; enriched capability is withheld and enriched publishes must remain blocked; restore the field on the production typed encoder and retry", c.Name, index, got, present, wantJSON)
		}
	}
	return nil
}
