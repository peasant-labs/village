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
	"strings"
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
	Name string `yaml:"name"`
	Arm  string `yaml:"arm"`
	// DeclaredOrigin is what the producer put on the wire. Empty means an older
	// producer that sends no declaration at all.
	DeclaredOrigin string `yaml:"declared_origin"`
	// ClassifiedOrigin is what this server's own classifier answers for the same
	// turns. The test recomputes it from the published bytes, so a row cannot
	// claim a divergence it does not have.
	ClassifiedOrigin      string                 `yaml:"classified_origin"`
	Turns                 []publishOriginTurnRun `yaml:"turns"`
	ExpectedOrigin        string                 `yaml:"expected_origin"`
	ExpectedStatus        int                    `yaml:"expected_status"`
	ExpectedErrorContains []string               `yaml:"expected_error_contains"`
	Undecodable           bool                   `yaml:"undecodable"`
}

// requiredPublishOriginArms is the deletion guard: every named arm must be
// present. There is no row count, so adding a row is one edit to the corpus.
var requiredPublishOriginArms = []string{
	"user",
	"user-command-invocation",
	"agent",
	"unknown-system-only",
	"unknown-unreadable",
	"declared-agent-wins",
	"declared-user-wins",
	"declared-unknown-defers",
	"declared-out-of-menu",
}

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
	arms, names := map[string]bool{}, map[string]bool{}
	for _, c := range cases {
		if names[c.Name] {
			t.Fatalf("publish session-origin fixture repeats name %q", c.Name)
		}
		names[c.Name], arms[c.Arm] = true, true
		if c.Undecodable && len(c.Turns) != 0 {
			t.Fatalf("row %q uploads undecodable bytes and cannot also declare turns", c.Name)
		}
		for _, run := range c.Turns {
			if run.Count < 1 || !schema.Role(run.Role).IsValid() {
				t.Fatalf("row %q has an unusable turn run %+v", c.Name, run)
			}
		}
		if c.refused() {
			if c.ExpectedStatus != http.StatusBadRequest {
				t.Fatalf("row %q expects status %d; the only refusal this path has is 400", c.Name, c.ExpectedStatus)
			}
			if c.ExpectedOrigin != "" {
				t.Fatalf("row %q is refused and therefore stores nothing, so it cannot expect origin %q", c.Name, c.ExpectedOrigin)
			}
			if len(c.ExpectedErrorContains) == 0 {
				t.Fatalf("row %q refuses the publish but authors no error needle; a refusal with an unchecked message proves nothing", c.Name)
			}
			continue
		}
		if len(c.ExpectedErrorContains) != 0 {
			t.Fatalf("row %q is accepted and cannot also expect an error message", c.Name)
		}
		if _, err := sessionorigin.Parse(c.ExpectedOrigin); err != nil {
			t.Fatalf("row %q: %v", c.Name, err)
		}
		if c.Undecodable {
			if c.ClassifiedOrigin != "" {
				t.Fatalf("row %q uploads bytes no classifier can read, so it cannot state a classified answer", c.Name)
			}
			continue
		}
		if _, err := sessionorigin.Parse(c.ClassifiedOrigin); err != nil {
			t.Fatalf("row %q must state what this server's own classifier answers: %v", c.Name, err)
		}
		switch c.DeclaredOrigin {
		case string(schema.SessionOriginUser), string(schema.SessionOriginAgent):
			// A declared-wins row is vacuous when the classifier agrees with the
			// declaration: it then passes for an implementation that ignores the
			// declaration entirely.
			if c.ClassifiedOrigin == c.DeclaredOrigin {
				t.Fatalf("row %q declares %q on a payload this server would classify %q too, so honouring the declaration is unobservable; give it a payload the classifier answers differently", c.Name, c.DeclaredOrigin, c.ClassifiedOrigin)
			}
			if c.ExpectedOrigin != c.DeclaredOrigin {
				t.Fatalf("row %q declares %q, so the stored value must be %q, not %q", c.Name, c.DeclaredOrigin, c.DeclaredOrigin, c.ExpectedOrigin)
			}
		case string(schema.SessionOriginUnknown):
			// The same trap, one step further: a declared `unknown` on a payload
			// the classifier also calls `unknown` passes even for the forbidden
			// implementation that stores the declaration verbatim.
			if c.ClassifiedOrigin == string(schema.SessionOriginUnknown) {
				t.Fatalf("row %q declares unknown on a payload this server also classifies unknown, so storing the declaration verbatim would pass; give it a payload the classifier answers user or agent", c.Name)
			}
			if c.ExpectedOrigin != c.ClassifiedOrigin {
				t.Fatalf("row %q declares unknown, which returns the decision to this server, so the stored value must be the classified %q, not %q", c.Name, c.ClassifiedOrigin, c.ExpectedOrigin)
			}
		case "":
			if c.ExpectedOrigin != c.ClassifiedOrigin {
				t.Fatalf("row %q carries no declaration, so the stored value must be exactly the classified %q, not %q", c.Name, c.ClassifiedOrigin, c.ExpectedOrigin)
			}
		default:
			t.Fatalf("row %q declares %q, which is neither a menu value nor an accepted publish; only the refused row may carry an out-of-menu declaration", c.Name, c.DeclaredOrigin)
		}
	}
	for _, arm := range requiredPublishOriginArms {
		if !arms[arm] {
			t.Fatalf("publish session-origin fixture omits required arm %q", arm)
		}
	}
	return cases
}

// refused reports whether the row expects the publish to be turned away.
func (c publishOriginFixture) refused() bool { return c.ExpectedStatus != 0 }

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
	declaration := ""
	if c.DeclaredOrigin != "" {
		encodedDeclaration, _ := json.Marshal(c.DeclaredOrigin)
		declaration = fmt.Sprintf(`,"sessionOrigin":%s`, encodedDeclaration)
	}
	return fmt.Sprintf(`{"contractVersion":"0.1.1","kind":"session_detail","sessionDetail":{"id":"publish-origin-fixture","harness":"claude-code","turns":%s%s}}`, encoded, declaration)
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

// TestPublishTranscript_StoresResolvedSessionOrigin proves the publish path
// resolves who drove the session -- the producer's declaration when it made
// one, this server's own classifier when it did not -- stores that value, and
// refuses an out-of-menu declaration without writing anything.
//
// Every row that states a classified answer has it recomputed here from the
// exact bytes it publishes, so a declared row cannot claim a divergence from
// the classifier that it does not actually have. That check is what keeps the
// declared rows from passing for an implementation that ignores the
// declaration, and the declared-unknown row from passing for one that stores
// the declaration verbatim.
func TestPublishTranscript_StoresResolvedSessionOrigin(t *testing.T) {
	for _, fixture := range loadPublishOriginFixtures(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			if fixture.ClassifiedOrigin != "" {
				payload, _, err := defaultContentMigrator.Migrate(t.Context(), []byte(fixture.uploadBytes()))
				if err != nil {
					t.Fatalf("row %q does not decode, so its stated classified answer cannot be checked: %v", fixture.Name, err)
				}
				if got := sessionorigin.Classify(payload); got.String() != fixture.ClassifiedOrigin {
					t.Fatalf("this server classifies the published payload %q, but the row says %q; the row's divergence from the classifier is what makes it non-vacuous, so it must state the real answer", got, fixture.ClassifiedOrigin)
				}
			}

			var created sqlc.CreateTranscriptParams
			createCalls := 0
			mq := &mockQuerier{
				getTranscriptIDByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
					return pgtype.UUID{}, errors.New("not found")
				},
				createTranscript: func(_ context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
					created, createCalls = arg, createCalls+1
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

			if fixture.refused() {
				if w.Code != fixture.ExpectedStatus {
					t.Fatalf("publish status = %d, want %d for an out-of-menu declaration (body: %s)", w.Code, fixture.ExpectedStatus, w.Body.String())
				}
				if createCalls != 0 {
					t.Fatalf("a refused publish wrote a transcript row carrying origin %q; the refusal must store nothing", created.SessionOrigin)
				}
				for _, needle := range fixture.ExpectedErrorContains {
					if !strings.Contains(w.Body.String(), needle) {
						t.Fatalf("refusal message does not say %q, so a publisher cannot act on it; got: %s", needle, w.Body.String())
					}
				}
				return
			}

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
