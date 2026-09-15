package handler

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

// Provenance preservation is the guarantee behind the sealed
// session_graph_provenance_v1 wire token: durable session-relationship evidence
// (relationships and their public anchors, the root session identity, the
// submission count including a measured zero, every nonempty purpose including
// an unknown one, retained earlier history, and per-block plus folded tool
// provenance) survives publish, typed migration, canonical rewrite, and
// re-emission unchanged. It is durable evidence, not a rendered graph, and the
// wire literal is frozen.
//
// The production proof below runs that exact path over a canonical corpus. A
// failing proof withholds the token (see advertisedContentCapabilities) and
// refuses provenance-bearing publishes before any write (see
// requireSupportedContentForHarness), so evidence cannot be silently lost.

// provenancePreservationCaseNames is the independent required-name inventory
// that guards the corpus against accidental deletion or silent renaming.
var provenancePreservationCaseNames = [...]string{
	"full_provenance_evidence",
	"measured_zero_submission_count",
	"safe_maximum_count_with_unknown_purpose",
}

//go:embed testdata/provenance_preservation/cases.yaml
var provenancePreservationFixtureYAML []byte

type provenancePreservationFixture struct {
	Cases []provenancePreservationCase `yaml:"cases"`
}

type provenancePreservationCase struct {
	Name         string                     `yaml:"name"`
	Content      string                     `yaml:"content"`
	Capabilities []schema.ContentCapability `yaml:"capabilities"`
}

type provenancePreservationEvaluator interface {
	Evaluate() error
}

var _ provenancePreservationEvaluator = productionProvenancePreservationEvaluator{}

type productionProvenancePreservationEvaluator struct{}

func (productionProvenancePreservationEvaluator) Evaluate() error {
	_, provenanceErr := EvaluatePreservationProofs()
	return provenanceErr
}

// executeProvenancePreservationProof runs the real typed migrator and the same
// canonical rewrite boundary used by migrate-on-read over the corpus, then
// compares the re-emitted durable payload. Any change to relationship evidence,
// its anchors, the root identity, count presence, purpose, retained history, or
// per-block provenance fails the proof, so the provenance capability is withheld
// and provenance-bearing publishes must stay blocked. It runs uncached.
func executeProvenancePreservationProof(encoder contentRewriteEncoder) error {
	cases, err := loadProvenancePreservationFixtures(provenancePreservationFixtureYAML)
	if err != nil {
		return err
	}
	migrator := NewContentMigrator()
	for _, fixtureCase := range cases {
		original, err := schema.DecodeTranscriptContentRaw([]byte(fixtureCase.Content))
		if err != nil {
			return fmt.Errorf("provenance preservation proof failed because fixture %q could not be decoded as canonical durable content in handler.executeProvenancePreservationProof during capability evaluation; the provenance capability is withheld and provenance-bearing publishes must remain blocked; restore the canonical envelope and retry: %w", fixtureCase.Name, err)
		}
		if original.SessionDetail == nil {
			return fmt.Errorf("provenance preservation proof failed because fixture %q decoded without a durable session detail in handler.executeProvenancePreservationProof during capability evaluation; the provenance capability is withheld; restore the durable detail and retry", fixtureCase.Name)
		}
		if derived := schema.RequiredContentCapabilities(*original.SessionDetail); !slices.Equal(derived, fixtureCase.Capabilities) {
			return fmt.Errorf("provenance preservation proof failed because fixture %q derives capabilities %v, want the declared %v in handler.executeProvenancePreservationProof during capability evaluation; the corpus no longer exercises the provenance capability and the token is withheld; restore the declared relationship evidence and retry", fixtureCase.Name, derived, fixtureCase.Capabilities)
		}
		payload, _, err := migrator.Migrate(context.Background(), []byte(fixtureCase.Content))
		if err != nil {
			return fmt.Errorf("provenance preservation proof failed because fixture %q could not traverse the production typed migrator in handler.executeProvenancePreservationProof during capability evaluation; the provenance capability is withheld and provenance-bearing publishes must remain blocked; fix the migrator or the fixture, then restart: %w", fixtureCase.Name, err)
		}
		rewritten, err := encoder.Encode(currentContractVersion, payload)
		if err != nil {
			return fmt.Errorf("provenance preservation proof failed because fixture %q could not round-trip the production canonical rewrite boundary in handler.executeProvenancePreservationProof during capability evaluation; the provenance capability is withheld and provenance-bearing publishes must remain blocked; fix the canonical encoder, then restart: %w", fixtureCase.Name, err)
		}
		reemitted, _, err := migrator.Migrate(context.Background(), rewritten)
		if err != nil {
			return fmt.Errorf("provenance preservation proof failed because fixture %q could not traverse the production typed rewrite/re-emit path in handler.executeProvenancePreservationProof during capability evaluation; the provenance capability is withheld and provenance-bearing publishes must remain blocked; fix the canonical encoder or migrator, then restart: %w", fixtureCase.Name, err)
		}
		original.SessionDetail.SchemaVersion = reemitted.SchemaVersion
		if !equalPublicPayloads(original.SessionDetail, reemitted) {
			return fmt.Errorf("provenance preservation proof failed because fixture %q changed durable relationship evidence, anchors, root identity, count presence, purpose, retained history, or per-block provenance across the typed migrate/rewrite path in handler.executeProvenancePreservationProof during capability evaluation; the provenance capability is withheld and provenance-bearing publishes must remain blocked; restore lossless production encoding, then retry", fixtureCase.Name)
		}
		if !containsContentCapability(schema.RequiredContentCapabilities(*reemitted), schema.ContentCapabilitySessionGraphProvenanceV1) {
			return fmt.Errorf("provenance preservation proof failed because fixture %q no longer requires the sealed provenance capability after the typed rewrite/re-emit path in handler.executeProvenancePreservationProof during capability evaluation; the provenance capability is withheld and provenance-bearing publishes must remain blocked; restore the relationship evidence on the canonical encoder, then retry", fixtureCase.Name)
		}
	}
	return nil
}

func loadProvenancePreservationFixtures(data []byte) ([]provenancePreservationCase, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var fixture provenancePreservationFixture
	if err := decoder.Decode(&fixture); err != nil {
		return nil, fmt.Errorf("provenance preservation fixture load failed because strict YAML decoding rejected testdata/provenance_preservation/cases.yaml in handler.loadProvenancePreservationFixtures before capability evaluation; the provenance capability cannot be advertised; correct the fixture fields and retry: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("provenance preservation fixture load failed because a trailing YAML document could not be decoded in handler.loadProvenancePreservationFixtures before capability evaluation; the provenance capability cannot be advertised; keep exactly one YAML document and retry: %w", err)
		}
		return nil, fmt.Errorf("provenance preservation fixture load failed because testdata/provenance_preservation/cases.yaml contains multiple YAML documents in handler.loadProvenancePreservationFixtures before capability evaluation; the provenance capability cannot be advertised; keep exactly one YAML document and retry")
	}
	required := make(map[string]struct{}, len(provenancePreservationCaseNames))
	for _, name := range provenancePreservationCaseNames {
		required[name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(fixture.Cases))
	for index, fixtureCase := range fixture.Cases {
		if fixtureCase.Name == "" || fixtureCase.Name != strings.TrimSpace(fixtureCase.Name) {
			return nil, fmt.Errorf("provenance preservation fixture inventory failed because case %d has an empty or edge-padded name %q in handler.loadProvenancePreservationFixtures before capability evaluation; the provenance capability cannot be advertised; supply the exact registered name and retry", index, fixtureCase.Name)
		}
		if _, ok := required[fixtureCase.Name]; !ok {
			return nil, fmt.Errorf("provenance preservation fixture inventory failed because case %q is not in the independent required-name inventory in handler.loadProvenancePreservationFixtures before capability evaluation; the provenance capability cannot be advertised; restore a registered case name and retry", fixtureCase.Name)
		}
		if _, duplicate := seen[fixtureCase.Name]; duplicate {
			return nil, fmt.Errorf("provenance preservation fixture inventory failed because case %q is duplicated in handler.loadProvenancePreservationFixtures before capability evaluation; the provenance capability cannot be advertised; keep each registered case exactly once and retry", fixtureCase.Name)
		}
		if strings.TrimSpace(fixtureCase.Content) == "" {
			return nil, fmt.Errorf("provenance preservation fixture inventory failed because case %q has empty content in handler.loadProvenancePreservationFixtures before capability evaluation; the provenance capability cannot be advertised; restore the canonical envelope and retry", fixtureCase.Name)
		}
		if !containsContentCapability(fixtureCase.Capabilities, schema.ContentCapabilitySessionGraphProvenanceV1) {
			return nil, fmt.Errorf("provenance preservation fixture inventory failed because case %q does not declare the sealed provenance capability in its expected inventory in handler.loadProvenancePreservationFixtures before capability evaluation; a corpus without relationship evidence cannot vouch for provenance and the capability is withheld; restore graph-bearing evidence and its declared capabilities, then retry", fixtureCase.Name)
		}
		seen[fixtureCase.Name] = struct{}{}
	}
	for _, name := range provenancePreservationCaseNames {
		if _, ok := seen[name]; !ok {
			return nil, fmt.Errorf("provenance preservation fixture inventory failed because required case %q is absent in handler.loadProvenancePreservationFixtures before capability evaluation; the provenance capability cannot be advertised; restore the reviewed case and retry", name)
		}
	}
	return fixture.Cases, nil
}
