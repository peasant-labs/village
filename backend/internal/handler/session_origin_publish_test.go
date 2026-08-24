package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/sessionorigin"
)

//go:embed testdata/session_origin_publish/cases.yaml
var sessionOriginPublishCasesYAML []byte

type publishOriginTurnRun struct {
	Role    string `yaml:"role"`
	Content string `yaml:"content"`
	Count   int    `yaml:"count"`
}

type publishOriginFixture struct {
	Name           string                 `yaml:"name"`
	Arm            string                 `yaml:"arm"`
	Turns          []publishOriginTurnRun `yaml:"turns"`
	ExpectedOrigin string                 `yaml:"expected_origin"`
	Undecodable    bool                   `yaml:"undecodable"`
}

const wantPublishOriginRows = 5

var requiredPublishOriginArms = []string{"user", "user-command-invocation", "agent", "unknown-system-only", "unknown-unreadable"}

func loadPublishOriginFixtures(t *testing.T) []publishOriginFixture {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(sessionOriginPublishCasesYAML))
	decoder.KnownFields(true)
	var cases []publishOriginFixture
	if err := decoder.Decode(&cases); err != nil {
		t.Fatalf("decode publish session-origin fixture: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("publish session-origin fixture must contain exactly one YAML document; got %v", trailing)
	}
	if len(cases) != wantPublishOriginRows {
		t.Fatalf("publish session-origin fixture has %d rows, want %d", len(cases), wantPublishOriginRows)
	}
	arms, names := map[string]bool{}, map[string]bool{}
	for _, c := range cases {
		if names[c.Name] {
			t.Fatalf("publish session-origin fixture repeats name %q", c.Name)
		}
		names[c.Name], arms[c.Arm] = true, true
		if _, err := sessionorigin.Parse(c.ExpectedOrigin); err != nil {
			t.Fatalf("row %q: %v", c.Name, err)
		}
		if c.Undecodable && len(c.Turns) != 0 {
			t.Fatalf("row %q uploads undecodable bytes and cannot also declare turns", c.Name)
		}
		for _, run := range c.Turns {
			if run.Count < 1 || !schema.Role(run.Role).IsValid() {
				t.Fatalf("row %q has an unusable turn run %+v", c.Name, run)
			}
		}
	}
	for _, arm := range requiredPublishOriginArms {
		if !arms[arm] {
			t.Fatalf("publish session-origin fixture omits required arm %q", arm)
		}
	}
	return cases
}

func (c publishOriginFixture) uploadBytes() string {
	if c.Undecodable {
		return "this upload is not a transcript envelope"
	}
	type turnJSON struct {
		Index   int    `json:"index"`
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	details := []turnJSON{}
	for _, run := range c.Turns {
		for range run.Count {
			details = append(details, turnJSON{Index: len(details), Role: run.Role, Content: run.Content})
		}
	}
	encoded, _ := json.Marshal(details)
	return fmt.Sprintf(`{"contractVersion":"0.1.1","kind":"session_detail","sessionDetail":{"id":"publish-origin-fixture","harness":"claude-code","turns":%s}}`, encoded)
}

func publishOriginMetadata(sessionID string) string {
	metadata := schema.PublishRequest{
		Identity:    schema.SessionIdentity{SessionID: schema.SessionID(sessionID), SchemaVersion: 2},
		Model:       schema.ModelInfo{Harness: schema.HarnessClaudeCode, Model: "claude"},
		Timestamp:   schema.TimestampInfo{Start: 1700000000000, End: 1700000060000},
		Source:      schema.SourceInfo{FilePath: "/p/t.jsonl", Format: "jsonl"},
		Project:     schema.ProjectContext{Hash: testProjectHash, Name: "test-project"},
		Diagnostics: schema.DiagnosticsInfo{Warnings: []schema.DiagnosticEntry{}},
	}
	encoded, _ := json.Marshal(metadata)
	return string(encoded)
}

// TestPublishTranscript_StoresClassifiedSessionOrigin proves the publish path
// classifies the uploaded content and stores the result, including the
// fail-safe value for content it cannot read.
func TestPublishTranscript_StoresClassifiedSessionOrigin(t *testing.T) {
	for _, fixture := range loadPublishOriginFixtures(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			var created sqlc.CreateTranscriptParams
			mq := &mockQuerier{
				getTranscriptIDByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
					return pgtype.UUID{}, errors.New("not found")
				},
				createTranscript: func(_ context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
					created = arg
					return sqlc.Transcript{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}}, nil
				},
			}
			h := newTestHandler(mq, &mockTranscriptBlobStore{})

			body, boundary := multipartBody(t, map[string]string{"metadata": publishOriginMetadata("550e8400-e29b-41d4-a716-446655440000")}, fixture.uploadBytes())
			r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
			r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
			r = r.WithContext(withTestUser(r.Context()))
			w := httptest.NewRecorder()

			h.PublishTranscript(w, r)

			if w.Code != http.StatusOK && w.Code != http.StatusCreated {
				t.Fatalf("publish status = %d, want an accepted publish (body: %s)", w.Code, w.Body.String())
			}
			if created.SessionOrigin != fixture.ExpectedOrigin {
				t.Fatalf("stored session_origin = %q, want %q (arm %q)", created.SessionOrigin, fixture.ExpectedOrigin, fixture.Arm)
			}
			if err := sessionorigin.Origin(created.SessionOrigin).Validate(); err != nil {
				t.Fatalf("publish stored a value the database would reject: %v", err)
			}
		})
	}
}

// TestRepublishTranscript_ReclassifiesChangedContent proves a re-publish that
// replaces the content also replaces the stored classification, so a session
// that gains a real user prompt stops being grouped as agent work.
func TestRepublishTranscript_ReclassifiesChangedContent(t *testing.T) {
	fixtures := loadPublishOriginFixtures(t)
	var agentCase, userCase publishOriginFixture
	for _, fixture := range fixtures {
		switch fixture.Arm {
		case "agent":
			agentCase = fixture
		case "user":
			userCase = fixture
		}
	}
	if agentCase.Name == "" || userCase.Name == "" {
		t.Fatal("fixture set no longer carries both an agent and a user row")
	}

	existingID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	var updated sqlc.UpdateTranscriptByOwnerAndLocalIDParams
	mq := &mockQuerier{
		getTranscriptIDByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
			return existingID, nil
		},
		getTranscriptByID: func(context.Context, pgtype.UUID) (sqlc.Transcript, error) {
			return sqlc.Transcript{
				ID: existingID, Visibility: dbVisibilityPrivate,
				BlobKey: "transcripts/20000000-0000-4000-8000-000000000002.bin", WrappedDataKey: []byte("wrapped"),
				EncryptionAlgorithm: "aes-256-gcm-random-nonce-v1", KeyVersion: 1,
				SessionOrigin: sessionorigin.Agent.String(),
			}, nil
		},
		updateTranscriptByOwnerAndLocalID: func(_ context.Context, arg sqlc.UpdateTranscriptByOwnerAndLocalIDParams) (sqlc.Transcript, error) {
			updated = arg
			return sqlc.Transcript{ID: existingID}, nil
		},
	}
	h := newTestHandler(mq, &mockTranscriptBlobStore{})

	body, boundary := multipartBody(t, map[string]string{"metadata": publishOriginMetadata("550e8400-e29b-41d4-a716-446655440000")}, userCase.uploadBytes())
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(withTestUser(r.Context()))
	w := httptest.NewRecorder()

	h.PublishTranscript(w, r)

	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("republish status = %d, want an accepted publish (body: %s)", w.Code, w.Body.String())
	}
	if updated.SessionOrigin != userCase.ExpectedOrigin {
		t.Fatalf("republished session_origin = %q, want %q; replacing the content must replace the classification", updated.SessionOrigin, userCase.ExpectedOrigin)
	}
	if updated.SessionOrigin == agentCase.ExpectedOrigin {
		t.Fatalf("republish kept the previous %q classification after the content changed", agentCase.ExpectedOrigin)
	}
}
