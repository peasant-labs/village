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
	"slices"
	"strings"
	"testing"

	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/observed_model_preservation/pi_boundaries.yaml
var piBoundariesYAML []byte

//go:embed testdata/observed_model_preservation/pi_field_loss.yaml
var piFieldLossYAML []byte

type fieldLossEncoder struct{ member string }

var _ contentRewriteEncoder = fieldLossEncoder{}

func (e fieldLossEncoder) Encode(version schema.PushContractVersion, payload *schema.SessionDetailPayload) ([]byte, error) {
	raw, err := canonicalContentRewriteEncoder{}.Encode(version, payload)
	if err != nil {
		return nil, err
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		return nil, err
	}
	var remove func(any)
	remove = func(v any) {
		switch node := v.(type) {
		case map[string]any:
			delete(node, e.member)
			for _, child := range node {
				remove(child)
			}
		case []any:
			for _, child := range node {
				remove(child)
			}
		}
	}
	remove(value)
	return json.Marshal(value)
}

func TestPiFieldLossWithholdsDiscoveryAndPublish(t *testing.T) {
	var corpus struct {
		Cases []struct {
			Name   string `yaml:"name"`
			Member string `yaml:"member"`
		} `yaml:"cases"`
	}
	d := yaml.NewDecoder(bytes.NewReader(piFieldLossYAML))
	d.KnownFields(true)
	if err := d.Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		t.Fatal("expected one YAML document")
	}
	seen := map[string]bool{}
	for _, c := range corpus.Cases {
		if c.Name == "" || seen[c.Name] || c.Member == "" {
			t.Fatal("invalid field loss inventory")
		}
		seen[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			proofErr := provePiPreservation(fieldLossEncoder{member: c.Member})
			if proofErr == nil {
				t.Fatal("production-point field loss passed preservation proof")
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
				t.Fatal("lossy rewrite advertised capabilities")
			}
			raw := piContent(t)
			refusal := publishPiParts(t, h, GetUser(withTestUser(context.Background())), piMetadata(t, raw), raw)
			if refusal.Code != http.StatusConflict || !strings.Contains(refusal.Body.String(), "no transcript bytes or metadata were written") {
				t.Fatalf("lossy rewrite publish=%d %s", refusal.Code, refusal.Body.String())
			}
		})
	}
	for _, name := range []string{"native_metadata_loss_withholds_support", "detailed_usage_loss_withholds_support", "recorded_cost_loss_withholds_support", "tool_result_ref_loss_withholds_support"} {
		if !seen[name] {
			t.Fatalf("missing field loss case %q", name)
		}
	}
}

type piBoundaryCase struct {
	Name    string `yaml:"name"`
	Surface string `yaml:"surface"`
	Find    string `yaml:"find"`
	Replace string `yaml:"replace"`
	Repeat  int    `yaml:"repeat"`
	Status  int    `yaml:"status"`
	Error   string `yaml:"error"`
}

func loadPiBoundaries(t *testing.T) []piBoundaryCase {
	t.Helper()
	var corpus struct {
		Cases []piBoundaryCase `yaml:"cases"`
	}
	d := yaml.NewDecoder(bytes.NewReader(piBoundariesYAML))
	d.KnownFields(true)
	if err := d.Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		t.Fatal("expected one YAML document")
	}
	seen := map[string]bool{}
	for _, c := range corpus.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("empty or duplicate case %q", c.Name)
		}
		seen[c.Name] = true
		if c.Surface != "metadata" && c.Surface != "content" && c.Surface != "capability" {
			t.Fatalf("unknown surface %q", c.Surface)
		}
		if c.Status != http.StatusCreated && (c.Status < 400 || c.Status >= 500 || c.Error == "") {
			t.Fatalf("invalid expectation %q", c.Name)
		}
	}
	for _, name := range []string{"duplicate_metadata_key", "duplicate_content_key", "duplicate_opaque_key", "duplicate_owner", "wrong_usage_role", "wrong_metadata_target", "wrong_tool_result_ref", "token_overflow", "token_null", "cost_not_string", "opaque_integer_overflow", "opaque_underflow", "opaque_string_budget", "ordinary_text_outside_metadata_budget", "failed_capability_proof", "complete_tool_namespace_requires_release"} {
		if !seen[name] {
			t.Fatalf("required case %q missing", name)
		}
	}
	return corpus.Cases
}

func piContent(t *testing.T) []byte {
	t.Helper()
	cases, err := loadPiPreservationFixtures()
	if err != nil {
		t.Fatal(err)
	}
	return []byte(cases[0].Content)
}

func piMetadata(t *testing.T, content []byte) []byte {
	t.Helper()
	req := schema.AuthoritativePublishRequest{
		Identity:    schema.AuthoritativeSessionIdentity{SessionID: "550e8400-e29b-41d4-a716-446655440163", SchemaVersion: 10},
		Model:       schema.AuthoritativeModelInfo{Harness: schema.HarnessPi, Model: "fixture-model"},
		Timestamp:   schema.AuthoritativeTimestampInfo{Start: 1700000000000, End: 1700000001000},
		Source:      schema.AuthoritativeSourceInfo{FilePath: "/fixture/session.jsonl", Format: schema.SourceFormatJSONL},
		Project:     schema.AuthoritativeProjectContext{Hash: testProjectHash, Name: "fixture"},
		ContentHash: schema.ComputeTranscriptContentHash(content), VisibilityIntent: schema.VisibilityIntentPrivate,
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schema.DecodeAuthoritativePublishMetadataRaw(raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func (c piBoundaryCase) parts(t *testing.T) ([]byte, []byte) {
	t.Helper()
	content := piContent(t)
	replacement := c.Replace
	if c.Repeat > 0 {
		replacement = `"` + strings.Repeat("x", c.Repeat) + `"`
	}
	mutate := func(raw []byte) []byte {
		if !bytes.Contains(raw, []byte(c.Find)) || c.Find == "" {
			t.Fatalf("fixture %q missing mutation target", c.Name)
		}
		return bytes.Replace(raw, []byte(c.Find), []byte(replacement), 1)
	}
	if c.Surface == "content" {
		content = mutate(content)
	}
	meta := piMetadata(t, content)
	if c.Surface == "metadata" {
		meta = mutate(meta)
	}
	return meta, content
}

func publishPiParts(t *testing.T, h *Handler, user *AuthUser, metadata, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	body, boundary := multipartBody(t, map[string]string{"metadata": string(metadata)}, string(content))
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, user))
	w := httptest.NewRecorder()
	h.PublishTranscript(w, r)
	return w
}

func TestPiPublicPreservationAndCapabilities(t *testing.T) {
	if err := provePiPreservation(canonicalContentRewriteEncoder{}); err != nil {
		t.Fatal(err)
	}
	cases, err := loadPiPreservationFixtures()
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	newTestHandler(&mockQuerier{}, nil).GetSchemaVersion(w, httptest.NewRequest(http.MethodGet, "/api/v1/schema/version", nil))
	var version schema.SchemaVersionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &version); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(version.ContentCapabilities, cases[0].Capabilities) {
		t.Fatalf("capabilities=%v want=%v", version.ContentCapabilities, cases[0].Capabilities)
	}
}

func TestPiPublishRawBoundaries(t *testing.T) {
	for _, c := range loadPiBoundaries(t) {
		if c.Status == http.StatusCreated {
			continue
		} // Accepted persistence is exercised by the encrypted integration gate.
		t.Run(c.Name, func(t *testing.T) {
			meta, content := c.parts(t)
			blobs := newFakeBlobStore()
			h := newTestHandler(&mockQuerier{}, blobs)
			h.scanContent = func([]byte) []string { t.Fatal("invalid payload reached secret scan"); return nil }
			if c.Surface == "capability" {
				h.preservationEvaluator = fixedPreservationEvaluator{err: errors.New("preservation unavailable")}
			}
			w := publishPiParts(t, h, GetUser(withTestUser(context.Background())), meta, content)
			if w.Code != c.Status || !strings.Contains(w.Body.String(), c.Error) {
				t.Fatalf("status=%d want=%d error=%q body=%s", w.Code, c.Status, c.Error, w.Body.String())
			}
			if blobs.uploadCount() != 0 {
				t.Fatal("invalid publication wrote object")
			}
		})
	}
}
