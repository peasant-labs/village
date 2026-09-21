package handler

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
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

//go:embed testdata/session_origin_publish/loader_cases.yaml
var sessionOriginPublishLoaderCasesYAML []byte

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

var requiredPublishOriginRefusalNeedles = []string{
	"user, agent, unknown",
	"nothing was stored",
	"publish again",
}

func loadPublishOriginFixtures(t *testing.T) []publishOriginFixture {
	t.Helper()
	cases, err := readPublishOriginFixtures(sessionOriginPublishCasesYAML)
	if err != nil {
		t.Fatal(err)
	}
	return cases
}

func readPublishOriginFixtures(data []byte) ([]publishOriginFixture, error) {
	cases, err := decodeFixtureRows[publishOriginFixture](data)
	if err != nil {
		return nil, fmt.Errorf("testdata/session_origin_publish/cases.yaml: decode fixture during loader validation failed: %w; restore one valid YAML document using only known fields", err)
	}
	arms, names := map[string]bool{}, map[string]bool{}
	for _, c := range cases {
		if names[c.Name] {
			return nil, fmt.Errorf("testdata/session_origin_publish/cases.yaml row %q: loader validation found a duplicate name; fixture rows must have unique names; rename or remove the duplicate", c.Name)
		}
		names[c.Name], arms[c.Arm] = true, true
		if c.Undecodable && len(c.Turns) != 0 {
			return nil, fmt.Errorf("testdata/session_origin_publish/cases.yaml row %q: loader validation found turns on an undecodable upload; these inputs cannot coexist; remove the turns or clear undecodable", c.Name)
		}
		for _, run := range c.Turns {
			if run.Count < 1 || !schema.Role(run.Role).IsValid() {
				return nil, fmt.Errorf("testdata/session_origin_publish/cases.yaml row %q: loader validation found an unusable turn run %+v; handler coverage cannot execute it; use a valid role and positive count", c.Name, run)
			}
		}
		if c.refused() {
			if c.ExpectedStatus != http.StatusBadRequest {
				return nil, fmt.Errorf("testdata/session_origin_publish/cases.yaml row %q: loader validation found refusal status %d; this path only refuses with 400; restore expected_status: 400", c.Name, c.ExpectedStatus)
			}
			if c.ExpectedOrigin != "" {
				return nil, fmt.Errorf("testdata/session_origin_publish/cases.yaml row %q: loader validation found stored origin %q on a refused publish; refusals write nothing; remove expected_origin", c.Name, c.ExpectedOrigin)
			}
			for _, needle := range requiredPublishOriginRefusalNeedles {
				if !containsPublishOriginNeedle(c.ExpectedErrorContains, needle) {
					return nil, fmt.Errorf("testdata/session_origin_publish/cases.yaml row %q: loader validation is missing required refusal needle %q; the refusal message is not fully checked; restore it in expected_error_contains", c.Name, needle)
				}
			}
			if !containsPublishOriginNeedle(c.ExpectedErrorContains, c.DeclaredOrigin) {
				return nil, fmt.Errorf("testdata/session_origin_publish/cases.yaml row %q: loader validation is missing the declared input %q from refusal needles; the rejected value is not checked; add that exact input to expected_error_contains", c.Name, c.DeclaredOrigin)
			}
			continue
		}
		if len(c.ExpectedErrorContains) != 0 {
			return nil, fmt.Errorf("testdata/session_origin_publish/cases.yaml row %q: loader validation found error needles on an accepted publish; accepted rows have no refusal body; remove expected_error_contains", c.Name)
		}
		if _, err := sessionorigin.Parse(c.ExpectedOrigin); err != nil {
			return nil, fmt.Errorf("testdata/session_origin_publish/cases.yaml row %q: loader validation found an invalid expected origin: %w; use a supported stored origin", c.Name, err)
		}
		if c.Undecodable {
			if c.ClassifiedOrigin != "" {
				return nil, fmt.Errorf("testdata/session_origin_publish/cases.yaml row %q: loader validation found a classified answer for unreadable bytes; no classifier result exists; remove classified_origin", c.Name)
			}
			continue
		}
		if _, err := sessionorigin.Parse(c.ClassifiedOrigin); err != nil {
			return nil, fmt.Errorf("testdata/session_origin_publish/cases.yaml row %q: loader validation requires the server classifier answer: %w; set classified_origin to the supported result", c.Name, err)
		}
		switch c.DeclaredOrigin {
		case string(schema.SessionOriginUser), string(schema.SessionOriginAgent):
			// A declared-wins row is vacuous when the classifier agrees with the
			// declaration: it then passes for an implementation that ignores the
			// declaration entirely.
			if c.ClassifiedOrigin == c.DeclaredOrigin {
				return nil, fmt.Errorf("testdata/session_origin_publish/cases.yaml row %q: loader validation found declaration %q equal to classification %q, so declaration precedence is unobservable; use turns classified differently", c.Name, c.DeclaredOrigin, c.ClassifiedOrigin)
			}
			if c.ExpectedOrigin != c.DeclaredOrigin {
				return nil, fmt.Errorf("testdata/session_origin_publish/cases.yaml row %q: loader validation found expected origin %q for declaration %q; declaration must win; set expected_origin to %q", c.Name, c.ExpectedOrigin, c.DeclaredOrigin, c.DeclaredOrigin)
			}
		case string(schema.SessionOriginUnknown):
			// The same trap, one step further: a declared `unknown` on a payload
			// the classifier also calls `unknown` passes even for the forbidden
			// implementation that stores the declaration verbatim.
			if c.ClassifiedOrigin == string(schema.SessionOriginUnknown) {
				return nil, fmt.Errorf("testdata/session_origin_publish/cases.yaml row %q: loader validation found unknown declared and classified; classifier fallback is unobservable; use turns classified as user or agent", c.Name)
			}
			if c.ExpectedOrigin != c.ClassifiedOrigin {
				return nil, fmt.Errorf("testdata/session_origin_publish/cases.yaml row %q: loader validation found expected origin %q after unknown declaration; classification %q must win; restore that classified value", c.Name, c.ExpectedOrigin, c.ClassifiedOrigin)
			}
		case "":
			if c.ExpectedOrigin != c.ClassifiedOrigin {
				return nil, fmt.Errorf("testdata/session_origin_publish/cases.yaml row %q: loader validation found expected origin %q without a declaration; classification %q must be stored; restore that classified value", c.Name, c.ExpectedOrigin, c.ClassifiedOrigin)
			}
		default:
			return nil, fmt.Errorf("testdata/session_origin_publish/cases.yaml row %q: loader validation found out-of-menu declaration %q on an accepted publish; only a refused row may carry it; add the refusal expectation or use a menu value", c.Name, c.DeclaredOrigin)
		}
	}
	for _, arm := range requiredPublishOriginArms {
		if !arms[arm] {
			return nil, fmt.Errorf("testdata/session_origin_publish/cases.yaml: loader validation omits required arm %q; coverage would be lost; restore a row for that arm", arm)
		}
	}
	return cases, nil
}

func containsPublishOriginNeedle(needles []string, want string) bool {
	for _, needle := range needles {
		if needle == want {
			return true
		}
	}
	return false
}

type publishOriginLoaderFixture struct {
	Name                  string `yaml:"name"`
	RemoveNeedle          string `yaml:"remove_needle"`
	ClearNeedles          bool   `yaml:"clear_needles"`
	DeclaredOrigin        string `yaml:"declared_origin"`
	AppendNeedle          string `yaml:"append_needle"`
	ExpectedErrorContains string `yaml:"expected_error_contains"`
}

var requiredPublishOriginLoaderFixtures = []string{
	"unchanged_corpus_is_valid",
	"missing_accepted_menu_is_refused",
	"missing_storage_assurance_is_refused",
	"missing_retry_instruction_is_refused",
	"missing_all_needles_is_refused",
	"changed_declaration_requires_matching_echo",
	"extra_unregistered_needle_is_allowed",
}

func loadPublishOriginLoaderFixtures(t *testing.T) []publishOriginLoaderFixture {
	t.Helper()
	cases, err := decodeFixtureRows[publishOriginLoaderFixture](sessionOriginPublishLoaderCasesYAML)
	if err != nil {
		t.Fatalf("load publish-origin loader fixture: %v", err)
	}
	names := make(map[string]bool, len(cases))
	for _, c := range cases {
		if c.Name == "" || names[c.Name] {
			t.Fatalf("publish-origin loader fixture has empty or duplicate name %q", c.Name)
		}
		names[c.Name] = true
	}
	for _, name := range requiredPublishOriginLoaderFixtures {
		if !names[name] {
			t.Fatalf("publish-origin loader fixture omits required case %q", name)
		}
	}
	return cases
}

func TestPublishOriginFixtureLoader(t *testing.T) {
	for _, testCase := range loadPublishOriginLoaderFixtures(t) {
		t.Run(testCase.Name, func(t *testing.T) {
			cases, err := decodeFixtureRows[publishOriginFixture](sessionOriginPublishCasesYAML)
			if err != nil {
				t.Fatalf("decode fresh canonical corpus: %v", err)
			}
			var refusal *publishOriginFixture
			for i := range cases {
				if cases[i].Name == "declared_out_of_menu_value_is_refused_and_stores_nothing" {
					refusal = &cases[i]
					break
				}
			}
			if refusal == nil {
				t.Fatal("canonical corpus is missing the named refusal row")
			}
			if testCase.RemoveNeedle != "" {
				if !containsPublishOriginNeedle(refusal.ExpectedErrorContains, testCase.RemoveNeedle) {
					t.Fatalf("mutation target %q is absent from the fresh refusal row", testCase.RemoveNeedle)
				}
				filtered := refusal.ExpectedErrorContains[:0]
				for _, needle := range refusal.ExpectedErrorContains {
					if needle != testCase.RemoveNeedle {
						filtered = append(filtered, needle)
					}
				}
				refusal.ExpectedErrorContains = filtered
			}
			if testCase.ClearNeedles {
				refusal.ExpectedErrorContains = nil
			}
			if testCase.DeclaredOrigin != "" {
				refusal.DeclaredOrigin = testCase.DeclaredOrigin
			}
			if testCase.AppendNeedle != "" {
				refusal.ExpectedErrorContains = append(refusal.ExpectedErrorContains, testCase.AppendNeedle)
			}
			data, err := yaml.Marshal(cases)
			if err != nil {
				t.Fatalf("marshal mutated corpus: %v", err)
			}
			_, err = readPublishOriginFixtures(data)
			if testCase.ExpectedErrorContains == "" {
				if err != nil {
					t.Fatalf("loader unexpectedly rejected corpus: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.ExpectedErrorContains) {
				t.Fatalf("loader error = %v, want fragment %q", err, testCase.ExpectedErrorContains)
			}
		})
	}
}

// refused reports whether the row expects the publish to be turned away.
func (c publishOriginFixture) refused() bool { return c.ExpectedStatus != 0 }

func (c publishOriginFixture) uploadBytes() string {
	if c.Undecodable {
		return "this upload is not a transcript envelope"
	}
	type turnJSON struct {
		Index     int    `json:"index"`
		Role      string `json:"role"`
		Content   string `json:"content"`
		Timestamp string `json:"timestamp"`
		Depth     int    `json:"depth"`
	}
	details := []turnJSON{}
	for _, run := range c.Turns {
		for range run.Count {
			// Turn timestamps stay inside the session start/end window below;
			// the published wire shape requires every turn to carry one.
			details = append(details, turnJSON{Index: len(details), Role: run.Role, Content: run.Content, Timestamp: fmt.Sprintf("2023-11-14T22:13:%02dZ", 20+len(details)), Depth: 0})
		}
	}
	encoded, _ := json.Marshal(details)
	declaration := ""
	if c.DeclaredOrigin != "" {
		encodedDeclaration, _ := json.Marshal(c.DeclaredOrigin)
		declaration = fmt.Sprintf(`,"sessionOrigin":%s`, encodedDeclaration)
	}
	return fmt.Sprintf(`{"contractVersion":"0.1.1","kind":"session_detail","sessionDetail":{"id":"publish-origin-fixture","harness":"claude-code","startTime":"2023-11-14T22:13:20Z","endTime":"2023-11-14T22:14:20Z","durationMins":1,"tokensIn":100,"tokensOut":50,"totalTokens":150,"toolCallCount":0,"turnCount":%d,"turns":%s%s}}`, len(details), encoded, declaration)
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
