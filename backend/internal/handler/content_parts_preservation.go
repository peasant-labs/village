package handler

import (
	"context"
	_ "embed"
	"fmt"
	"sync"

	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/content_parts_preservation.yaml
var contentPartsPreservationYAML []byte

var contentPartsProofOnce sync.Once
var contentPartsProofErr error

type contentPartsEvaluator interface{ Evaluate() error }
type productionContentPartsEvaluator struct{}

func (productionContentPartsEvaluator) Evaluate() error {
	contentPartsProofOnce.Do(func() { contentPartsProofErr = proveContentParts(productionContentRewriteEncoder) })
	return contentPartsProofErr
}

func proveContentParts(encoder contentRewriteEncoder) error {
	var fixtures struct {
		Cases []struct {
			Name    string `yaml:"name"`
			Content string `yaml:"content"`
		} `yaml:"cases"`
	}
	if err := yaml.Unmarshal(contentPartsPreservationYAML, &fixtures); err != nil {
		return fmt.Errorf("decode content-parts preservation fixture: %w", err)
	}
	found := false
	for _, c := range fixtures.Cases {
		if c.Name == "root_and_earlier_ordered_parts" {
			found = true
		}
		original, err := schema.DecodeTranscriptContentRaw([]byte(c.Content))
		if err != nil {
			return fmt.Errorf("decode content-parts fixture %q: %w", c.Name, err)
		}
		if original.SessionDetail == nil || !containsContentCapability(schema.RequiredContentCapabilities(*original.SessionDetail), schema.ContentCapabilityContentPartsV1) {
			return fmt.Errorf("content-parts fixture %q lacks required evidence", c.Name)
		}
		boundary, err := validateContentBoundary([]byte(c.Content), "", contentPublication)
		if err != nil || boundary.canonical == nil {
			return fmt.Errorf("content-parts fixture %q failed the production publication boundary: %v", c.Name, err)
		}
		migrator := NewContentMigrator()
		payload, _, err := migrator.Migrate(context.Background(), []byte(c.Content))
		if err != nil {
			return fmt.Errorf("migrate content-parts fixture %q: %w", c.Name, err)
		}
		rewritten, err := encoder.Encode(currentContractVersion, payload)
		if err != nil {
			return fmt.Errorf("rewrite content-parts fixture %q: %w", c.Name, err)
		}
		result, _, err := migrator.Migrate(context.Background(), rewritten)
		if err != nil {
			return fmt.Errorf("remigrate content-parts fixture %q: %w", c.Name, err)
		}
		boundary, err = validateContentBoundary(rewritten, "", contentStoredRead)
		if err != nil || boundary.canonical == nil {
			return fmt.Errorf("content-parts fixture %q failed the production stored-read boundary: %v", c.Name, err)
		}
		original.SessionDetail.SchemaVersion = result.SchemaVersion
		if !equalPublicPayloads(original.SessionDetail, result) || !containsContentCapability(schema.RequiredContentCapabilities(*result), schema.ContentCapabilityContentPartsV1) {
			return fmt.Errorf("content-parts fixture %q lost evidence during typed migration or rewrite", c.Name)
		}
	}
	if !found {
		return fmt.Errorf("required content-parts preservation fixture is missing")
	}
	return nil
}
