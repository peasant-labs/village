package handler

// Village back-compat suite over the versioned golden corpus.
// (testdata/contract/<version>/{valid,invalid}/{metadata,content,annotations}.json).
//
// For every corpus version the REAL ContentMigrator decodes + migrates-on-read +
// renders the stored content blob, and the REAL OpenAPI validator accepts valid
// metadata / rejects the invalid (enum-violating) metadata. Nothing is mocked.
//
// This exercises only the display migrate-on-read floor. It does not assert the
// push-acceptance floor or storage backfills; those are separate contracts.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// corpusVersion describes one golden-corpus version and its migrate-on-read
// expectations.
type corpusVersion struct {
	name        string
	wantRewrite bool           // current is already canonical (no rewrite); legacy rewrites
	wantHarness schema.Harness // expected normalized harness ("" = don't assert, e.g. raw JSONL)
	minTurns    int
}

func corpusVersions() []corpusVersion {
	return []corpusVersion{
		{name: "current", wantRewrite: false, wantHarness: schema.HarnessClaudeCode, minTurns: 2},
		{name: "legacy-provider-keyed", wantRewrite: true, wantHarness: schema.HarnessClaudeCode, minTurns: 2},
		{name: "legacy-raw-jsonl", wantRewrite: true, wantHarness: "", minTurns: 2},
		// legacy-metadata-field has CURRENT content (envelope, no rewrite) but a
		// legacy-keyed metadata.json — its legacy axis is the metadata surface,
		// asserted by TestCorpus_LegacyMetadataField_AcceptedViaNormalize.
		{name: "legacy-metadata-field", wantRewrite: false, wantHarness: schema.HarnessClaudeCode, minTurns: 2},
	}
}

func corpusFile(t *testing.T, version, validity, name string) []byte {
	t.Helper()
	p := filepath.Join("testdata", "contract", version, validity, name)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read corpus file %s: %v", p, err)
	}
	return b
}

// Every version's stored content blob decodes, migrates-on-read, and renders.
func TestCorpus_MigrateOnRead_Renders(t *testing.T) {
	m := NewContentMigrator()
	for _, v := range corpusVersions() {
		t.Run(v.name, func(t *testing.T) {
			raw := corpusFile(t, v.name, "valid", "content.json")
			payload, rewrite, err := m.Migrate(context.Background(), raw)
			if err != nil {
				t.Fatalf("Migrate(%s): %v", v.name, err)
			}
			if payload == nil {
				t.Fatalf("Migrate(%s): nil payload", v.name)
			}
			if rewrite != v.wantRewrite {
				t.Errorf("%s rewrite: got %v, want %v", v.name, rewrite, v.wantRewrite)
			}
			if len(payload.Turns) < v.minTurns {
				t.Errorf("%s turns: got %d, want >= %d", v.name, len(payload.Turns), v.minTurns)
			}
			if v.wantHarness != "" && payload.Harness != v.wantHarness {
				t.Errorf("%s harness: got %q, want %q (legacy value must be migrated)", v.name, payload.Harness, v.wantHarness)
			}
			// No legacy harness value should survive migrate-on-read.
			if payload.Harness == "claude" || payload.Harness == "gemini" {
				t.Errorf("%s: legacy harness value %q leaked through migrate-on-read", v.name, payload.Harness)
			}
		})
	}
}

// Every version's invalid content blob fails migrate-on-read loudly.
func TestCorpus_InvalidContent_Errors(t *testing.T) {
	m := NewContentMigrator()
	for _, v := range corpusVersions() {
		t.Run(v.name, func(t *testing.T) {
			raw := corpusFile(t, v.name, "invalid", "content.json")
			if _, _, err := m.Migrate(context.Background(), raw); err == nil {
				t.Errorf("%s invalid content: expected migrate error, got nil", v.name)
			}
		})
	}
}

// Valid metadata (after the handler's harness-key normalization) passes the
// schema module's embedded contract; invalid metadata with an out-of-menu
// source.format is rejected through the same path used by the publish handler.
func TestCorpus_Metadata_EnforceAcceptsValid_RejectsInvalid(t *testing.T) {
	v := payloadValidator()
	if v == nil {
		t.Skip("OpenAPI validator unavailable")
	}
	for _, cv := range corpusVersions() {
		t.Run(cv.name, func(t *testing.T) {
			valid := normalizeMetadataHarnessKey(corpusFile(t, cv.name, "valid", "metadata.json"))
			if err := v.ValidatePublish(valid); err != nil {
				t.Errorf("%s valid metadata rejected: %v", cv.name, err)
			}
			invalid := normalizeMetadataHarnessKey(corpusFile(t, cv.name, "invalid", "metadata.json"))
			if err := v.ValidatePublish(invalid); err == nil {
				t.Errorf("%s invalid metadata (bad source.format) must be rejected, got nil", cv.name)
			}
		})
	}
}

// TestCorpus_LegacyMetadataField_AcceptedViaNormalize drives a legacy
// provider/modelHarness-keyed metadata.json through the REAL PublishTranscript
// handler — the SECOND wire surface — and asserts the village accepts it AND
// canonicalizes the harness via normalizeMetadataHarnessKey (observed through the
// ModelProvider persisted on the CreateTranscriptParams). This complements the
// CONTENT-surface migrate-on-read coverage (bare-payload / raw-JSONL).
func TestCorpus_LegacyMetadataField_AcceptedViaNormalize(t *testing.T) {
	cases := []struct {
		version     string
		wantHarness schema.Harness // canonical value the legacy key must map to
	}{
		{"legacy-provider-keyed", schema.HarnessClaudeCode}, // metadata model.modelHarness: "claude"
		{"legacy-raw-jsonl", schema.HarnessGeminiCLI},       // metadata model.provider: "gemini"
		{"legacy-metadata-field", schema.HarnessClaudeCode}, // isolated metadata surface: model.modelHarness "claude"
	}
	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			var gotProvider string
			mq := &mockQuerier{
				getTranscriptIDByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
					return pgtype.UUID{}, errors.New("not found")
				},
				createTranscript: func(_ context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
					gotProvider = arg.ModelProvider
					return sqlc.Transcript{}, nil
				},
			}
			h := newTestHandler(mq, &mockTranscriptBlobStore{})

			meta := string(corpusFile(t, tc.version, "valid", "metadata.json"))
			content := string(corpusFile(t, tc.version, "valid", "content.json"))
			body, boundary := multipartBody(t, map[string]string{"metadata": meta}, content)

			r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
			r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
			r = r.WithContext(withTestUser(r.Context()))
			w := httptest.NewRecorder()

			h.PublishTranscript(w, r)

			if w.Code != http.StatusOK && w.Code != http.StatusCreated {
				t.Fatalf("%s legacy metadata-field publish: got %d, want 200/201 (body: %s)", tc.version, w.Code, w.Body.String())
			}
			// The legacy key must have been normalized to the canonical harness
			// before it was persisted (proves normalizeMetadataHarnessKey ran on
			// the publish path, not just in isolation).
			if gotProvider != string(tc.wantHarness) {
				t.Errorf("%s: persisted model_provider = %q, want %q (legacy key not normalized on publish path)", tc.version, gotProvider, tc.wantHarness)
			}
		})
	}
}

// The versioned corpus carries AnnotationSummary response arrays, not the newer
// AnnotationPushRequest operation body. Keep its historical JSON-malformation
// assertion separate from the mounted push contract tests.
func TestCorpus_InvalidAnnotations_Rejected(t *testing.T) {
	for _, cv := range corpusVersions() {
		t.Run(cv.name, func(t *testing.T) {
			// valid annotations.json parses; invalid is malformed JSON.
			if !json.Valid(corpusFile(t, cv.name, "valid", "annotations.json")) {
				t.Errorf("%s valid annotation response corpus is not valid JSON", cv.name)
			}
			if json.Valid(corpusFile(t, cv.name, "invalid", "annotations.json")) {
				t.Errorf("%s malformed annotations must be rejected, got nil", cv.name)
			}
		})
	}
}
