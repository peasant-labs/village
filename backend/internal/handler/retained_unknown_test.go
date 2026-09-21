package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

func TestRetainedUnknownPreservation(t *testing.T) {
	if err := proveRetainedUnknownPreservation(canonicalContentRewriteEncoder{}); err != nil {
		t.Fatal(err)
	}
	if err := proveRetainedUnknownPreservation(fieldLossEncoder{member: "retainedUnknown"}); err == nil {
		t.Fatal("evidence loss passed production rewrite proof")
	}
	if err := proveRetainedUnknownPreservation(fieldLossEncoder{member: "diagnostics"}); err == nil {
		t.Fatal("partial signal loss passed production rewrite proof")
	}
	if !containsContentCapability(advertisedContentCapabilities(), schema.ContentCapabilityRetainedUnknownV1) {
		t.Fatal("retained evidence capability not advertised")
	}
}

func TestRetainedUnknownLossRefusesPublicationAndAdvertisement(t *testing.T) {
	proofErr := proveRetainedUnknownPreservation(fieldLossEncoder{member: "retainedUnknown"})
	if proofErr == nil {
		t.Fatal("lossy rewrite passed preservation proof")
	}
	h := newTestHandler(&mockQuerier{}, newFakeBlobStore())
	h.preservationEvaluator = fixedPreservationEvaluator{err: proofErr}
	w := httptest.NewRecorder()
	h.GetSchemaVersion(w, httptest.NewRequest(http.MethodGet, "/api/v1/schema/version", nil))
	var version schema.SchemaVersionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &version); err != nil {
		t.Fatal(err)
	}
	if len(version.ContentCapabilities) != 0 {
		t.Fatal("lossy rewrite advertised preservation")
	}
	cases, err := loadRetainedUnknownFixtures(retainedUnknownYAML)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(cases[0].Content)
	refused := publishPiParts(t, h, GetUser(withTestUser(context.Background())), retainedUnknownMetadata(t, raw), raw)
	if refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "retained_unknown_v1") || !strings.Contains(refused.Body.String(), "no transcript bytes or metadata were written") {
		t.Fatalf("lossy rewrite publish=%d %s", refused.Code, refused.Body.String())
	}
}

func TestRetainedUnknownInvalidStoredEvidenceRefusesMigration(t *testing.T) {
	for _, c := range loadRetainedUnknownBoundaries(t) {
		if c.Status != http.StatusConflict {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			raw, _ := c.parts(t)
			if _, rewrite, err := NewContentMigrator().Migrate(context.Background(), raw); err == nil || rewrite {
				t.Fatalf("invalid stored evidence was served or rewritten: rewrite=%t err=%v", rewrite, err)
			}
			// Even without a canonical harness, null/empty evidence markers must
			// not route through the permissive historic decoder.
			raw = bytes.ReplaceAll(raw, []byte(`"harness":"claude-code",`), nil)
			if _, _, err := NewContentMigrator().Migrate(context.Background(), raw); err == nil || errors.Is(err, ErrEmptyBlob) {
				t.Fatalf("invalid retained evidence retried as legacy content: %v", err)
			}
		})
	}
}

func TestRetainedUnknownBareDetailPreservesEvidence(t *testing.T) {
	cases, err := loadRetainedUnknownFixtures(retainedUnknownYAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal([]byte(c.Content), &envelope); err != nil {
				t.Fatal(err)
			}
			original, err := schema.DecodeTranscriptContentRaw([]byte(c.Content))
			if err != nil {
				t.Fatal(err)
			}
			migrated, rewrite, err := NewContentMigrator().Migrate(context.Background(), envelope["sessionDetail"])
			if err != nil || !rewrite {
				t.Fatalf("bare detail migration: rewrite=%t err=%v", rewrite, err)
			}
			encoded, err := encodeCanonicalTranscript(migrated)
			if err != nil {
				t.Fatal(err)
			}
			got, err := schema.DecodeTranscriptContentRaw(encoded)
			if err != nil {
				t.Fatal(err)
			}
			original.SessionDetail.SchemaVersion = got.SessionDetail.SchemaVersion
			if !equalPublicPayloads(original.SessionDetail, got.SessionDetail) {
				t.Fatal("bare-detail normalization lost retained source evidence")
			}
		})
	}
}

//go:embed testdata/retained_unknown_boundaries.yaml
var retainedUnknownBoundariesYAML []byte

type retainedUnknownBoundary struct {
	Name          string  `yaml:"name"`
	Payload       *string `yaml:"payload"`
	Find          string  `yaml:"find"`
	Replace       string  `yaml:"replace"`
	MetadataFalse bool    `yaml:"metadata_false"`
	Padding       int     `yaml:"padding"`
	Depth         int     `yaml:"depth"`
	Status        int     `yaml:"status"`
	Error         string  `yaml:"error"`
}

func loadRetainedUnknownBoundaries(t *testing.T) []retainedUnknownBoundary {
	t.Helper()
	var corpus struct {
		Cases []retainedUnknownBoundary `yaml:"cases"`
	}
	d := yaml.NewDecoder(bytes.NewReader(retainedUnknownBoundariesYAML))
	d.KnownFields(true)
	if err := d.Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		t.Fatal("expected exactly one boundary fixture document")
	}
	seen := map[string]bool{}
	for _, c := range corpus.Cases {
		if c.Name == "" || seen[c.Name] || c.Error == "" || c.Status < 400 {
			t.Fatalf("invalid boundary fixture %q", c.Name)
		}
		seen[c.Name] = true
	}
	for _, name := range strings.Fields("case_alias_after_canonical_evidence case_alias_before_canonical_evidence envelope_case_alias malformed_payload duplicate_payload_member invalid_payload_unicode null_evidence null_diagnostics false_partial missing_partial reversed_positions invalid_pointer metadata_partial_mismatch escaped_unicode_secret escaped_nested_secret escaped_json_key_value_secret oversized_outer_document excessive_payload_depth") {
		if !seen[name] {
			t.Fatalf("required boundary fixture %q absent", name)
		}
	}
	return corpus.Cases
}

func (c retainedUnknownBoundary) parts(t *testing.T) (content, metadata []byte) {
	t.Helper()
	cases, err := loadRetainedUnknownFixtures(retainedUnknownYAML)
	if err != nil {
		t.Fatal(err)
	}
	content = []byte(cases[0].Content)
	if c.Payload != nil || c.Padding > 0 || c.Depth > 0 {
		var envelope schema.TranscriptContent
		if err := json.Unmarshal(content, &envelope); err != nil {
			t.Fatal(err)
		}
		if c.Payload != nil {
			envelope.SessionDetail.RetainedUnknown[0].Payload = *c.Payload
		}
		if c.Padding > 0 {
			envelope.SessionDetail.RetainedUnknown[0].Payload = `"` + strings.Repeat("x", c.Padding) + `"`
		}
		if c.Depth > 0 {
			envelope.SessionDetail.RetainedUnknown[0].Payload = strings.Repeat("[", c.Depth) + "0" + strings.Repeat("]", c.Depth)
		}
		content, err = json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
	}
	metadata = retainedUnknownMetadata(t, content)
	if c.Find != "" {
		if !bytes.Contains(content, []byte(c.Find)) {
			t.Fatalf("fixture %q mutation target absent", c.Name)
		}
		content = bytes.Replace(content, []byte(c.Find), []byte(c.Replace), 1)
	}
	var req schema.AuthoritativePublishRequest
	if err := json.Unmarshal(metadata, &req); err != nil {
		t.Fatal(err)
	}
	req.ContentHash = schema.ComputeTranscriptContentHash(content)
	if c.MetadataFalse {
		partial := false
		req.Diagnostics.Partial = &partial
	}
	metadata, err = json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return content, metadata
}

func retainedUnknownMetadata(t *testing.T, content []byte) []byte {
	t.Helper()
	var envelope schema.TranscriptContent
	if err := json.Unmarshal(content, &envelope); err != nil {
		t.Fatal(err)
	}
	var req schema.AuthoritativePublishRequest
	if err := json.Unmarshal(piMetadata(t, content), &req); err != nil {
		t.Fatal(err)
	}
	req.Identity.SessionID = schema.SessionID(envelope.SessionDetail.ID)
	req.Model.Harness = envelope.SessionDetail.Harness
	partial := true
	req.Diagnostics.Partial = &partial
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRetainedUnknownFixtureInventory(t *testing.T) {
	if _, err := loadRetainedUnknownFixtures(bytes.ReplaceAll(retainedUnknownYAML, []byte("padding:"), []byte("typo:"))); err == nil {
		t.Fatal("unknown fixture field accepted")
	}
	if _, err := loadRetainedUnknownFixtures(append(append([]byte{}, retainedUnknownYAML...), []byte("\n---\n{}\n")...)); err == nil {
		t.Fatal("trailing fixture document accepted")
	}
	for _, name := range retainedUnknownCaseNames {
		if _, err := loadRetainedUnknownFixtures(bytes.ReplaceAll(retainedUnknownYAML, []byte(name), []byte("removed_case"))); err == nil {
			t.Fatalf("missing %s accepted", name)
		}
	}
}

func TestRetainedUnknownPublicationBoundaries(t *testing.T) {
	for _, c := range loadRetainedUnknownBoundaries(t) {
		t.Run(c.Name, func(t *testing.T) {
			raw, meta := c.parts(t)
			h := newTestHandler(&mockQuerier{}, newFakeBlobStore())
			w := publishPiParts(t, h, GetUser(withTestUser(context.Background())), meta, raw)
			if w.Code != c.Status || !strings.Contains(w.Body.String(), c.Error) {
				t.Fatalf("got %d %s; want %d containing %q", w.Code, w.Body.String(), c.Status, c.Error)
			}
			if c.Status == http.StatusUnprocessableEntity && strings.Contains(c.Error, "Redaction check failed") && strings.Contains(w.Body.String(), "schema validation") {
				t.Fatal("secret scan confused with contract refusal")
			}
		})
	}
}
