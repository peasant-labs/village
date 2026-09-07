package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"

	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/observed_model_preservation/pi.yaml
var piPreservationYAML []byte

type piPreservationCase struct {
	Name         string                     `yaml:"name"`
	Content      string                     `yaml:"content"`
	Capabilities []schema.ContentCapability `yaml:"capabilities"`
}

func loadPiPreservationFixtures() ([]piPreservationCase, error) {
	var corpus struct {
		Cases []piPreservationCase `yaml:"cases"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(piPreservationYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&corpus); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("Pi preservation corpus requires exactly one YAML document; restore the fixture before capability evaluation")
	}
	seen := map[string]bool{}
	for _, c := range corpus.Cases {
		if c.Name == "" || seen[c.Name] || c.Content == "" {
			return nil, fmt.Errorf("Pi preservation corpus has an empty or duplicate case; restore unique named evidence before capability evaluation")
		}
		seen[c.Name] = true
	}
	if !seen["pi_all_metadata_and_usage_owners"] {
		return nil, fmt.Errorf("Pi preservation corpus is missing pi_all_metadata_and_usage_owners; restore the required evidence before capability evaluation")
	}
	return corpus.Cases, nil
}

// provePiPreservation uses the same migrator and injectable canonical encoder as
// the served read/rewrite path. Comparison includes every field, not just totals.
func provePiPreservation(encoder contentRewriteEncoder) error {
	cases, err := loadPiPreservationFixtures()
	if err != nil {
		return err
	}
	for _, c := range cases {
		original, err := schema.DecodeTranscriptContentRaw([]byte(c.Content))
		if err != nil {
			return err
		}
		if !slices.Equal(schema.RequiredContentCapabilities(*original.SessionDetail), c.Capabilities) {
			return fmt.Errorf("Pi preservation case %q lacks its declared capability evidence; restore the fixture before advertising support", c.Name)
		}
		payload, _, err := NewContentMigrator().Migrate(context.Background(), []byte(c.Content))
		if err != nil {
			return err
		}
		raw, err := encoder.Encode(currentContractVersion, payload)
		if err != nil {
			return err
		}
		reemitted, _, err := NewContentMigrator().Migrate(context.Background(), raw)
		if err != nil {
			return err
		}
		original.SessionDetail.SchemaVersion = reemitted.SchemaVersion
		if !equalPublicPayloads(original.SessionDetail, reemitted) {
			return fmt.Errorf("Pi transcript preservation failed in canonical migrate/rewrite evaluation because public evidence changed; capabilities are withheld and no enriched publication is safe; restore lossless usage, refs, cost strings and metadata encoding, then retry")
		}
	}
	return nil
}

func equalPublicPayloads(a, b *schema.SessionDetailPayload) bool {
	// UseNumber avoids floating conversion of opaque metadata's safe integers.
	decode := func(v *schema.SessionDetailPayload) any {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		var result any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if d.Decode(&result) != nil {
			return nil
		}
		return result
	}
	return reflect.DeepEqual(decode(a), decode(b))
}
