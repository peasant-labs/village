package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

func multipartBody(t *testing.T, fields map[string]string, fileContent string) (*bytes.Buffer, string) {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	for key, value := range fields {
		part, err := writer.CreateFormField(key)
		if err != nil {
			t.Fatalf("failed to create form field: %v", err)
		}
		part.Write([]byte(value))
	}

	if fileContent != "" {
		part, err := writer.CreateFormFile("transcript_file", "transcript.jsonl")
		if err != nil {
			t.Fatalf("failed to create form file: %v", err)
		}
		part.Write([]byte(fileContent))
	}

	writer.Close()
	return body, writer.Boundary()
}

func withTestUser(ctx context.Context) context.Context {
	return context.WithValue(ctx, UserContextKey, &AuthUser{ID: uuid.New(), Username: "testuser"})
}

func TestPublishTranscript_MissingMetadata(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)

	body, boundary := multipartBody(t, nil, "")

	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(withTestUser(r.Context()))
	w := httptest.NewRecorder()

	h.PublishTranscript(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestPublishTranscript_InvalidMetadataJSON(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)

	body, boundary := multipartBody(t, map[string]string{
		"metadata": "{invalid json",
	}, "")

	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(withTestUser(r.Context()))
	w := httptest.NewRecorder()

	h.PublishTranscript(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestPublishTranscript_MissingSessionID(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)

	metadata := map[string]interface{}{
		"identity": map[string]interface{}{
			"schemaVersion": 2,
		},
		"model": map[string]interface{}{
			"modelHarness": "openai",
			"model":        "gpt-4",
		},
	}
	metadataJSON, _ := json.Marshal(metadata)

	body, boundary := multipartBody(t, map[string]string{
		"metadata": string(metadataJSON),
	}, "")

	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(withTestUser(r.Context()))
	w := httptest.NewRecorder()

	h.PublishTranscript(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d (body: %s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestPublishTranscript_MissingModelHarness(t *testing.T) {
	mq := &mockQuerier{
		getTranscriptIDByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
			return pgtype.UUID{}, errors.New("not found")
		},
		createTranscript: func(context.Context, sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
			return sqlc.Transcript{}, nil
		},
	}
	h := newTestHandler(mq, &mockTranscriptBlobStore{})

	metadata := map[string]any{
		"identity": map[string]any{
			"sessionId":     "550e8400-e29b-41d4-a716-446655440000",
			"schemaVersion": 2,
		},
		"model": map[string]any{
			"model": "gpt-4",
		},
		"timestamp": map[string]any{
			"start": 1700000000000,
			"end":   1700000060000,
		},
		"source": map[string]any{
			"filePath": "/path/to/transcript.jsonl",
			"format":   "jsonl",
		},
		"project": map[string]any{
			"hash": testProjectHash,
			"name": "test-project",
		},
		"stats":       map[string]any{"turnCount": 1, "durationMs": 60000},
		"diagnostics": map[string]any{"warnings": []any{}},
	}
	metadataJSON, _ := json.Marshal(metadata)

	body, boundary := multipartBody(t, map[string]string{
		"metadata": string(metadataJSON),
	}, `[{"role":"user","content":"hello"}]`)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(withTestUser(r.Context()))
	w := httptest.NewRecorder()

	h.PublishTranscript(w, r)

	// 1e8tk: model.harness is now required in the schema/OpenAPI contract, so an
	// omitted harness key is rejected as a documented schema-422 (it was accepted
	// in rc1 — the B1 hole, now closed). The vendored publish-request schema
	// (SchemaModelInfo.required=["harness","model"]) is the sole enforcement.
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status: got %d, want 422 (omitted harness rejected by required-harness schema) (body: %s)", w.Code, w.Body.String())
	}
}

// TestPublishTranscript_MissingModel asserts that an absent model.model is now a
// documented schema-422 (1e8tk), NOT the prior hand-written 400. The
// "model is required" 400 guard was removed; SchemaModelInfo.required=["harness",
// "model"] in the vendored schema is the sole enforcement, so an empty model key
// unifies to 422.
func TestPublishTranscript_MissingModel(t *testing.T) {
	mq := &mockQuerier{
		getTranscriptIDByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
			return pgtype.UUID{}, errors.New("not found")
		},
		createTranscript: func(context.Context, sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
			return sqlc.Transcript{}, nil
		},
	}
	h := newTestHandler(mq, &mockTranscriptBlobStore{})

	// Harness present + valid, but model KEY omitted → schema required-violation.
	metadata := map[string]any{
		"identity": map[string]any{
			"sessionId":     "550e8400-e29b-41d4-a716-446655440000",
			"schemaVersion": 2,
		},
		"model": map[string]any{
			"harness": "claude-code",
		},
		"timestamp":   map[string]any{"start": 1700000000000, "end": 1700000060000},
		"source":      map[string]any{"filePath": "/path/to/transcript.jsonl", "format": "jsonl"},
		"project":     map[string]any{"hash": testProjectHash, "name": "test-project"},
		"stats":       map[string]any{"turnCount": 1, "durationMs": 60000},
		"diagnostics": map[string]any{"warnings": []any{}},
	}
	metadataJSON, _ := json.Marshal(metadata)

	body, boundary := multipartBody(t, map[string]string{
		"metadata": string(metadataJSON),
	}, `[{"role":"user","content":"hello"}]`)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(withTestUser(r.Context()))
	w := httptest.NewRecorder()

	h.PublishTranscript(w, r)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status: got %d, want 422 (omitted model rejected by required-model schema, not the removed 400 guard) (body: %s)", w.Code, w.Body.String())
	}
}

// TestPublishTranscript_ValidatorNil_FailsClosed drives the FAIL-CLOSED branch
// behaviorally: the contract-module validator is the SOLE enforcement of the
// required model fields, so if payloadValidator() returns nil (the module validator
// is unavailable) the handler must REJECT with 503, never silently accept an
// unvalidated publish. The package-level payloadValidator seam is swapped to a
// nil-returning stub to exercise the branch without touching the module; it is
// restored after the test.
func TestPublishTranscript_ValidatorNil_FailsClosed(t *testing.T) {
	orig := payloadValidator
	payloadValidator = func() PayloadValidator { return nil }
	t.Cleanup(func() { payloadValidator = orig })

	h := newTestHandler(&mockQuerier{}, &mockTranscriptBlobStore{})

	// A body that WOULD validate if the validator were present — proving the 503 is
	// the fail-closed branch, not a content rejection.
	metadata := schema.PublishRequest{
		Identity:    schema.SessionIdentity{SessionID: "550e8400-e29b-41d4-a716-446655440000", SchemaVersion: 2},
		Model:       schema.ModelInfo{Harness: schema.HarnessCodex, Model: "gpt-4"},
		Timestamp:   schema.TimestampInfo{Start: 1700000000000, End: 1700000060000},
		Source:      schema.SourceInfo{FilePath: "/p/t.jsonl", Format: "jsonl"},
		Project:     schema.ProjectContext{Hash: testProjectHash, Name: "test-project"},
		Diagnostics: schema.DiagnosticsInfo{Warnings: []schema.DiagnosticEntry{}},
	}
	metadataJSON, _ := json.Marshal(metadata)

	body, boundary := multipartBody(t, map[string]string{
		"metadata": string(metadataJSON),
	}, `[{"role":"user","content":"hello"}]`)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(withTestUser(r.Context()))
	w := httptest.NewRecorder()

	h.PublishTranscript(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503 (nil validator must FAIL CLOSED, not accept) (body: %s)", w.Code, w.Body.String())
	}
}

func TestPublishTranscript_ValidMinimalPayload(t *testing.T) {
	mq := &mockQuerier{
		getTranscriptIDByOwnerAndLocalID: func(ctx context.Context, arg sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
			return pgtype.UUID{}, errors.New("not found")
		},
		createTranscript: func(ctx context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
			return sqlc.Transcript{}, nil
		},
	}
	mockS3 := &mockTranscriptBlobStore{}
	h := newTestHandler(mq, mockS3)

	metadata := schema.PublishRequest{
		Identity: schema.SessionIdentity{
			SessionID:     "550e8400-e29b-41d4-a716-446655440000",
			SchemaVersion: 2,
		},
		Model: schema.ModelInfo{
			Harness: schema.HarnessCodex,
			Model:   "gpt-4",
		},
		Timestamp: schema.TimestampInfo{
			Start: 1700000000000,
			End:   1700000060000,
		},
		Source: schema.SourceInfo{
			FilePath: "/path/to/transcript.jsonl",
			Format:   "jsonl",
		},
		Git: schema.GitContext{
			Branch: strPtr("main"),
		},
		Project: schema.ProjectContext{
			Hash: testProjectHash,
			Name: "test-project",
		},
		Stats: schema.SessionStats{
			TurnCount:     10,
			ToolCallCount: 25,
			DurationMs:    60000,
			TokensIn:      5000,
			TokensOut:     10000,
		},
		Diagnostics: schema.DiagnosticsInfo{
			Warnings: []schema.DiagnosticEntry{},
		},
	}
	metadataJSON, _ := json.Marshal(metadata)

	transcriptContent := `[{"role": "user", "content": "hello"}]`

	body, boundary := multipartBody(t, map[string]string{
		"metadata": string(metadataJSON),
	}, transcriptContent)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(withTestUser(r.Context()))
	w := httptest.NewRecorder()

	h.PublishTranscript(w, r)

	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Logf("Response body: %s", w.Body.String())
		t.Errorf("status: got %d, want %d or %d", w.Code, http.StatusOK, http.StatusCreated)
	}
}

// FB/A unified-schema-c7lco: a publish body whose entries[].harness carry a
// PRE-RENAME value ("claude" — from session_entries.provider rows the V33 rename
// never canonicalized) but whose model.harness is already canonical must be
// ACCEPTED. The vendored schema enum-keys entries[].harness and would 422 the
// pre-rename value, so normalizeMetadataHarnessKey canonicalizes entries[].harness
// (symmetric with model.harness) BEFORE schema validation. The schema itself is
// not loosened — an UNKNOWN garbage harness still 422s (asserted by the second
// case).
func TestPublishTranscript_EntryHarnessPreRename_Normalized(t *testing.T) {
	if payloadValidator() == nil {
		t.Skip("OpenAPI validator unavailable — entry-harness 422 cannot be exercised")
	}

	baseMetadata := func() schema.PublishRequest {
		return schema.PublishRequest{
			Identity: schema.SessionIdentity{
				SessionID:     "550e8400-e29b-41d4-a716-446655440000",
				SchemaVersion: 2,
			},
			Model: schema.ModelInfo{
				// CANONICAL model.harness — only entries[].harness is pre-rename.
				Harness: schema.HarnessClaudeCode,
				Model:   "claude-3-5-sonnet",
			},
			Timestamp:   schema.TimestampInfo{Start: 1700000000000, End: 1700000060000},
			Source:      schema.SourceInfo{FilePath: "/path/to/transcript.jsonl", Format: "jsonl"},
			Git:         schema.GitContext{Branch: strPtr("main")},
			Project:     schema.ProjectContext{Hash: testProjectHash, Name: "test-project"},
			Stats:       schema.SessionStats{TurnCount: 1, ToolCallCount: 0, DurationMs: 60000},
			Diagnostics: schema.DiagnosticsInfo{Warnings: []schema.DiagnosticEntry{}},
		}
	}

	cases := []struct {
		name         string
		entryHarness schema.Harness
		wantAccepted bool
	}{
		// Pre-rename entry harness → normalized to claude-code → accepted.
		{"pre-rename entry harness 'claude' accepted via normalize", schema.Harness("claude"), true},
		// Unknown garbage harness → not in the enum, not normalizable → 422.
		{"unknown garbage entry harness still rejected", schema.Harness("not-a-harness"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mq := &mockQuerier{
				getTranscriptIDByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
					return pgtype.UUID{}, errors.New("not found")
				},
				createTranscript: func(context.Context, sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
					return sqlc.Transcript{}, nil
				},
			}
			h := newTestHandler(mq, &mockTranscriptBlobStore{})

			md := baseMetadata()
			md.Entries = []schema.SessionEntry{{
				SessionID:  md.Identity.SessionID,
				EntryIndex: 0,
				Harness:    tc.entryHarness,
				EntryType:  schema.EntryTypeText,
				Role:       schema.RoleUser,
				Depth:      0,
			}}
			metadataJSON, err := json.Marshal(md)
			if err != nil {
				t.Fatalf("marshal metadata: %v", err)
			}

			body, boundary := multipartBody(t, map[string]string{"metadata": string(metadataJSON)}, `[{"role":"user","content":"hello"}]`)
			r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
			r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
			r = r.WithContext(withTestUser(r.Context()))
			w := httptest.NewRecorder()

			h.PublishTranscript(w, r)

			accepted := w.Code == http.StatusOK || w.Code == http.StatusCreated
			if accepted != tc.wantAccepted {
				t.Errorf("entry harness %q: got status %d (accepted=%v), want accepted=%v (body: %s)",
					tc.entryHarness, w.Code, accepted, tc.wantAccepted, w.Body.String())
			}
			if !tc.wantAccepted && w.Code != http.StatusUnprocessableEntity {
				t.Errorf("entry harness %q: rejection status = %d, want 422", tc.entryHarness, w.Code)
			}
		})
	}
}

// Publish installs the server-computed plaintext hash atomically with the
// encrypted descriptor and final application-generated transcript id.
func TestPublishTranscript_RecordsContentHash(t *testing.T) {
	createdID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	var createdParams sqlc.CreateTranscriptParams

	mq := &mockQuerier{
		getTranscriptIDByOwnerAndLocalID: func(ctx context.Context, arg sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
			return pgtype.UUID{}, errors.New("not found")
		},
		createTranscript: func(ctx context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
			createdParams = arg
			return sqlc.Transcript{ID: createdID}, nil
		},
	}
	h := newTestHandler(mq, &mockTranscriptBlobStore{})

	metadata := schema.PublishRequest{
		Identity:    schema.SessionIdentity{SessionID: "550e8400-e29b-41d4-a716-446655440000", SchemaVersion: 2},
		Model:       schema.ModelInfo{Harness: schema.HarnessCodex, Model: "gpt-4"},
		Timestamp:   schema.TimestampInfo{Start: 1700000000000, End: 1700000060000},
		Source:      schema.SourceInfo{FilePath: "/p/t.jsonl", Format: "jsonl"},
		Project:     schema.ProjectContext{Hash: testProjectHash, Name: "test-project"},
		Diagnostics: schema.DiagnosticsInfo{Warnings: []schema.DiagnosticEntry{}},
	}
	metadataJSON, _ := json.Marshal(metadata)

	transcriptContent := `[{"role": "user", "content": "hello"}]`
	body, boundary := multipartBody(t, map[string]string{"metadata": string(metadataJSON)}, transcriptContent)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(withTestUser(r.Context()))
	w := httptest.NewRecorder()

	h.PublishTranscript(w, r)

	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("status: got %d, body: %s", w.Code, w.Body.String())
	}
	if !createdParams.ID.Valid {
		t.Fatal("create transcript did not carry the final application-generated id")
	}
	want := schema.ComputeTranscriptHash([]byte(transcriptContent))
	if !createdParams.ContentHash.Valid || createdParams.ContentHash.String != want {
		t.Fatalf("content_hash = %q (valid=%v), want %q", createdParams.ContentHash.String, createdParams.ContentHash.Valid, want)
	}
}

func TestPublishTranscript_DefaultSchemaVersion(t *testing.T) {
	mq := &mockQuerier{
		getTranscriptIDByOwnerAndLocalID: func(ctx context.Context, arg sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
			return pgtype.UUID{}, errors.New("not found")
		},
		createTranscript: func(ctx context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
			return sqlc.Transcript{}, nil
		},
	}
	mockS3 := &mockTranscriptBlobStore{}
	h := newTestHandler(mq, mockS3)

	metadata := schema.PublishRequest{
		Identity: schema.SessionIdentity{
			SessionID:     "550e8400-e29b-41d4-a716-446655440000",
			SchemaVersion: 0,
		},
		Model: schema.ModelInfo{
			Harness: schema.HarnessCodex,
			Model:   "gpt-4",
		},
		Timestamp: schema.TimestampInfo{
			Start: 1700000000000,
			End:   1700000060000,
		},
		Source: schema.SourceInfo{
			FilePath: "/path/to/transcript.jsonl",
			Format:   "jsonl",
		},
		Project: schema.ProjectContext{Hash: testProjectHash, Name: "test-project"},
		Stats: schema.SessionStats{
			TurnCount: 10,
		},
		Diagnostics: schema.DiagnosticsInfo{
			Warnings: []schema.DiagnosticEntry{},
		},
	}
	metadataJSON, _ := json.Marshal(metadata)

	transcriptContent := `[{"role": "user", "content": "hello"}]`

	body, boundary := multipartBody(t, map[string]string{
		"metadata": string(metadataJSON),
	}, transcriptContent)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(withTestUser(r.Context()))
	w := httptest.NewRecorder()

	h.PublishTranscript(w, r)

	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Logf("Response body: %s", w.Body.String())
	}
}

func TestPublishTranscript_MissingTranscriptFile(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)

	metadata := schema.PublishRequest{
		Identity: schema.SessionIdentity{
			SessionID:     "550e8400-e29b-41d4-a716-446655440000",
			SchemaVersion: 2,
		},
		Model: schema.ModelInfo{
			Harness: schema.HarnessCodex,
			Model:   "gpt-4",
		},
		Timestamp: schema.TimestampInfo{
			Start: 1700000000000,
			End:   1700000060000,
		},
		Source: schema.SourceInfo{
			FilePath: "/path/to/transcript.jsonl",
			Format:   "jsonl",
		},
		Project: schema.ProjectContext{Hash: testProjectHash, Name: "test-project"},
		Stats: schema.SessionStats{
			TurnCount: 10,
		},
		Diagnostics: schema.DiagnosticsInfo{},
	}
	metadataJSON, _ := json.Marshal(metadata)

	body, boundary := multipartBody(t, map[string]string{
		"metadata": string(metadataJSON),
	}, "")

	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(withTestUser(r.Context()))
	w := httptest.NewRecorder()

	h.PublishTranscript(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d (body: %s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestPublishTranscript_FullPayloadWithAllFields(t *testing.T) {
	mq := &mockQuerier{
		getTranscriptIDByOwnerAndLocalID: func(ctx context.Context, arg sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
			return pgtype.UUID{}, errors.New("not found")
		},
		createTranscript: func(ctx context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
			return sqlc.Transcript{}, nil
		},
	}
	mockS3 := &mockTranscriptBlobStore{}
	h := newTestHandler(mq, mockS3)

	yes := true
	quality := schema.QualityMetrics{
		TitleGenerated:      strPtr("Test Session"),
		Outcome:             outcomePtr("resolved"),
		FilesTouched:        intPtr(10),
		LinesChanged:        intPtr(100),
		SignalDensity:       float64Ptr(0.75),
		SpecQualityScore:    float64Ptr(0.9),
		M2TokenOutcomeRatio: float64Ptr(0.85),
		M3UniqueToolCount:   intPtr(15),
	}

	metadata := schema.PublishRequest{
		Identity: schema.SessionIdentity{
			SessionID:       "550e8400-e29b-41d4-a716-446655440000",
			ParentSessionID: sessionIDPtr("660e8400-e29b-41d4-a716-446655440001"),
			SchemaVersion:   2,
		},
		Model: schema.ModelInfo{
			Harness:        schema.HarnessCodex,
			Model:          "gpt-4",
			HarnessVersion: "1.0",
		},
		Timestamp: schema.TimestampInfo{
			Start: 1700000000000,
			End:   1700000060000,
		},
		Source: schema.SourceInfo{
			FilePath: "/path/to/transcript.jsonl",
			Format:   "jsonl",
		},
		Git: schema.GitContext{
			Branch:   strPtr("main"),
			Remote:   strPtr("origin"),
			Worktree: strPtr("/path/to/worktree"),
		},
		Project: schema.ProjectContext{
			Hash:     testProjectHash,
			FilePath: "/path/to/project",
			Name:     "test-project",
		},
		Stats: schema.SessionStats{
			TurnCount:     10,
			ToolCallCount: 25,
			SubagentCount: 2,
			DurationMs:    60000,
			TokensIn:      5000,
			TokensOut:     10000,
		},
		Quality:   &quality,
		Subagents: []schema.SubagentRef{},
		Diagnostics: schema.DiagnosticsInfo{
			Partial: &yes,
		},
	}
	metadataJSON, _ := json.Marshal(metadata)

	transcriptContent := `[{"role": "user", "content": "hello"}]`

	body, boundary := multipartBody(t, map[string]string{
		"metadata": string(metadataJSON),
	}, transcriptContent)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(withTestUser(r.Context()))
	w := httptest.NewRecorder()

	h.PublishTranscript(w, r)

	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Logf("Response body: %s", w.Body.String())
		t.Errorf("status: got %d, want %d or %d", w.Code, http.StatusOK, http.StatusCreated)
	}
}

func sessionIDPtr(s string) *schema.SessionID {
	id := schema.SessionID(s)
	return &id
}

func outcomePtr(s string) *schema.SessionOutcome {
	o := schema.SessionOutcome(s)
	return &o
}
