package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/retained_unknown.yaml
var retainedUnknownYAML []byte

var retainedUnknownCaseNames = [...]string{
	"claude_unknown_records_and_blocks",
	"codex_unknown_records_and_blocks",
	"cursor_unknown_records_and_blocks",
	"strike_unknown_records_and_blocks",
	"opencode_unknown_records_and_blocks",
	"pi_large_unknown_records_and_blocks",
}

type retainedUnknownCase struct {
	Name    string         `yaml:"name"`
	Harness schema.Harness `yaml:"harness"`
	Padding int            `yaml:"padding"`
	Content string         `yaml:"-"`
}

func loadRetainedUnknownFixtures(raw []byte) ([]retainedUnknownCase, error) {
	var corpus struct {
		Content string                `yaml:"content"`
		Cases   []retainedUnknownCase `yaml:"cases"`
	}
	d := yaml.NewDecoder(bytes.NewReader(raw))
	d.KnownFields(true)
	if err := d.Decode(&corpus); err != nil {
		return nil, err
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("retained evidence fixture must contain exactly one YAML document")
	}
	seen := map[string]bool{}
	harnesses := map[schema.Harness]bool{}
	for i := range corpus.Cases {
		c := &corpus.Cases[i]
		if !slices.Contains(retainedUnknownCaseNames[:], c.Name) || seen[c.Name] || c.Padding < 1 || c.Padding > 8<<20 || c.Harness == "" {
			return nil, fmt.Errorf("invalid retained evidence fixture %q; restore the named corpus and bounded payload size", c.Name)
		}
		seen[c.Name] = true
		if harnesses[c.Harness] {
			return nil, fmt.Errorf("duplicate retained evidence fixture harness %q", c.Harness)
		}
		harnesses[c.Harness] = true
		c.Content = strings.ReplaceAll(strings.ReplaceAll(corpus.Content, "HARNESS", string(c.Harness)), "PADDING", strings.Repeat("x", c.Padding))
	}
	for _, name := range retainedUnknownCaseNames {
		if !seen[name] {
			return nil, fmt.Errorf("retained evidence fixture %q is missing; restore the preservation corpus", name)
		}
	}
	for _, harness := range []schema.Harness{schema.HarnessClaudeCode, schema.HarnessCodex, schema.HarnessCursor, schema.HarnessStrike, schema.HarnessOpenCode, schema.HarnessPi} {
		if !harnesses[harness] {
			return nil, fmt.Errorf("retained evidence corpus omits harness %q", harness)
		}
		delete(harnesses, harness)
	}
	if len(harnesses) != 0 {
		return nil, fmt.Errorf("retained evidence corpus contains unexpected harnesses")
	}
	return corpus.Cases, nil
}

// Run the same typed migration and canonical rewrite used by content reads.
// Comparing the entire payload also pins lexical JSON text, ordering and known
// siblings. Failure withholds the base advertisement and refuses enriched writes.
func proveRetainedUnknownPreservation(encoder contentRewriteEncoder) error {
	cases, err := loadRetainedUnknownFixtures(retainedUnknownYAML)
	if err != nil {
		return err
	}
	for _, c := range cases {
		original, err := schema.DecodeTranscriptContentRaw([]byte(c.Content))
		if err != nil {
			return err
		}
		required := []schema.ContentCapability{schema.ContentCapabilityRetainedUnknownV1}
		if !slices.Equal(schema.RequiredContentCapabilities(*original.SessionDetail), required) {
			return fmt.Errorf("retained evidence fixture %q has unexpected capability membership", c.Name)
		}
		migrated, _, err := NewContentMigrator().Migrate(context.Background(), []byte(c.Content))
		if err != nil {
			return err
		}
		encoded, err := encoder.Encode(currentContractVersion, migrated)
		if err != nil {
			return err
		}
		got, _, err := NewContentMigrator().Migrate(context.Background(), encoded)
		if err != nil {
			return err
		}
		original.SessionDetail.SchemaVersion = got.SchemaVersion
		if !equalPublicPayloads(original.SessionDetail, got) {
			return fmt.Errorf("retained unknown preservation failed for %q in canonical migration/rewrite; capabilities are withheld because evidence or partial signaling changed; restore lossless encoding and restart", c.Name)
		}
	}
	return nil
}

// Payload is already validated JSON TEXT, not an object to normalize. Decode
// only for scanning: escaped Unicode and nested JSON strings must not hide a
// secret. UseNumber accepts arbitrary lexical numbers without float conversion.
// The original payload is never replaced by this inspection representation.
func (h *Handler) scanRetainedUnknown(detail *schema.SessionDetailPayload) ([]string, error) {
	if detail == nil {
		return nil, nil
	}
	var issues []string
	// Limits bound inspection, not retention. Exhaustion is a refusal before
	// storage, never permission to accept an incompletely scanned payload.
	remainingBytes, remainingNodes := 64<<20, 1<<20
	inspectionError := func() error {
		return fmt.Errorf("Redaction check failed. Retained payload inspection could not complete safely in handler.scanRetainedUnknown before storage; no content was written; simplify embedded JSON or redact it at the source and retry")
	}
	var visit func(any, int) error
	var inspect func(string, int) error
	inspect = func(text string, layers int) error {
		remainingBytes -= len(text)
		if remainingBytes < 0 {
			return inspectionError()
		}
		if !json.Valid([]byte(text)) {
			if layers == 0 {
				return inspectionError()
			}
			return nil
		}
		if layers > 16 {
			return inspectionError()
		}
		if err := schema.ScanRawJSONDocument([]byte(text), schema.RawJSONPathPolicy{MaxDocumentBytes: 8 << 20, MaxDocumentDepth: 64}); err != nil {
			return inspectionError()
		}
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return inspectionError()
		}
		return visit(value, layers)
	}
	visit = func(value any, layers int) error {
		remainingNodes--
		if remainingNodes < 0 {
			return inspectionError()
		}
		switch v := value.(type) {
		case string:
			issues = append(issues, h.scanTranscriptContent([]byte(v))...)
			return inspect(v, layers+1)
		case []any:
			for _, child := range v {
				if err := visit(child, layers); err != nil {
					return err
				}
			}
		case map[string]any:
			for key, child := range v {
				if err := visit(key, layers); err != nil {
					return err
				}
				// Key/value patterns such as api_key must not be hidden by JSON quotes.
				if text, ok := child.(string); ok {
					issues = append(issues, h.scanTranscriptContent([]byte(key+"="+text))...)
				}
				if err := visit(child, layers); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, record := range detail.RetainedUnknown {
		issues = append(issues, h.scanTranscriptContent([]byte(record.Payload))...)
		if err := inspect(record.Payload, 0); err != nil {
			return nil, err
		}
	}
	return issues, nil
}
