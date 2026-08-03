package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// --- Mapper: schemaToCommitParams ---

func TestSchemaToCommitRecords_MapsAllFields(t *testing.T) {
	commits := []schema.CommitInfo{
		{
			Hash:        "abc123def456",
			Message:     "feat: add SHA bridge",
			AuthorName:  "Alice Dev",
			AuthorEmail: "alice@example.com",
			CommitTime:  1708700060000,
			AuthorTime:  1708700050000,
		},
		{
			Hash:       "def789",
			Message:    "fix: patch",
			AuthorName: "Bob",
		},
	}

	recs := schemaToCommitRecords(commits)
	if len(recs) != 2 {
		t.Fatalf("len(recs) = %d, want 2", len(recs))
	}

	r0 := recs[0]
	if r0.CommitOrder != 0 {
		t.Errorf("CommitOrder = %d, want 0", r0.CommitOrder)
	}
	if r0.Sha != "abc123def456" {
		t.Errorf("Sha = %q, want abc123def456", r0.Sha)
	}
	if r0.Message == nil || *r0.Message != "feat: add SHA bridge" {
		t.Errorf("Message = %v, want 'feat: add SHA bridge'", r0.Message)
	}
	if r0.AuthorName == nil || *r0.AuthorName != "Alice Dev" {
		t.Errorf("AuthorName = %v, want 'Alice Dev'", r0.AuthorName)
	}
	if r0.AuthorEmail == nil || *r0.AuthorEmail != "alice@example.com" {
		t.Errorf("AuthorEmail = %v, want 'alice@example.com'", r0.AuthorEmail)
	}
	// Payload carries no additions/deletions — fields stay null.
	if r0.Additions != nil || r0.Deletions != nil {
		t.Errorf("Additions/Deletions should be nil: %v %v", r0.Additions, r0.Deletions)
	}
	if r0.AuthoredAt == nil {
		t.Errorf("AuthoredAt should be set for AuthorTime > 0")
	}
	if r0.CommittedAt == nil {
		t.Errorf("CommittedAt should be set for CommitTime > 0")
	}

	if recs[1].CommitOrder != 1 {
		t.Errorf("second commit CommitOrder = %d, want 1", recs[1].CommitOrder)
	}
	// Second commit has no email -> null.
	if recs[1].AuthorEmail != nil {
		t.Errorf("second AuthorEmail should be nil, got %v", recs[1].AuthorEmail)
	}
}

func TestSchemaToCommitRecords_SkipsEmptySHA(t *testing.T) {
	commits := []schema.CommitInfo{
		{Hash: "", Message: "no sha"},
		{Hash: "abc123", Message: "has sha"},
	}
	recs := schemaToCommitRecords(commits)
	if len(recs) != 1 {
		t.Fatalf("len(recs) = %d, want 1 (empty SHA skipped)", len(recs))
	}
	if recs[0].Sha != "abc123" {
		t.Errorf("Sha = %q, want abc123", recs[0].Sha)
	}
	// commit_order is the ORIGINAL payload index (1), not the post-skip index.
	if recs[0].CommitOrder != 1 {
		t.Errorf("CommitOrder = %d, want 1 (original index preserved)", recs[0].CommitOrder)
	}
}

func TestSchemaToCommitRecords_NilWhenNoCommits(t *testing.T) {
	if got := schemaToCommitRecords(nil); got != nil {
		t.Errorf("expected nil for no commits, got %+v", got)
	}
}

// --- Publish path: commits are persisted via delete-then-insert ---

func publishWithCommits(t *testing.T, mq *mockQuerier, commits []schema.CommitInfo) *httptest.ResponseRecorder {
	t.Helper()
	h := newTestHandler(mq, &mockTranscriptBlobStore{})

	metadata := schema.PublishRequest{
		Identity: schema.SessionIdentity{
			SessionID:     "550e8400-e29b-41d4-a716-446655440000",
			SchemaVersion: 4,
		},
		Model:       schema.ModelInfo{Harness: schema.HarnessCodex, Model: "gpt-4"},
		Timestamp:   schema.TimestampInfo{Start: 1700000000000, End: 1700000060000},
		Source:      schema.SourceInfo{FilePath: "/path/to/transcript.jsonl", Format: "jsonl"},
		Git:         schema.GitContext{Branch: strPtr("main"), Commits: commits},
		Project:     schema.ProjectContext{Hash: testProjectHash, Name: "test-project"},
		Stats:       schema.SessionStats{TurnCount: 1},
		Diagnostics: schema.DiagnosticsInfo{Warnings: []schema.DiagnosticEntry{}},
	}
	metadataJSON, _ := json.Marshal(metadata)

	body, boundary := multipartBody(t, map[string]string{"metadata": string(metadataJSON)}, `[{"role":"user","content":"hi"}]`)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(withTestUser(r.Context()))
	w := httptest.NewRecorder()
	h.PublishTranscript(w, r)
	return w
}

func TestPublishTranscript_PersistsCommits(t *testing.T) {
	tid := toPgUUID(uuid.New())
	var deleteCount int
	var batched []sqlc.InsertTranscriptCommitsParams

	mq := &mockQuerier{
		getTranscriptIDByOwnerAndLocalID: func(ctx context.Context, arg sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
			return pgtype.UUID{}, errors.New("not found")
		},
		createTranscript: func(ctx context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
			return sqlc.Transcript{ID: tid}, nil
		},
		deleteTranscriptCommits: func(ctx context.Context, transcriptID pgtype.UUID) error {
			if transcriptID != tid {
				t.Errorf("delete called with wrong transcript id")
			}
			deleteCount++
			return nil
		},
		insertTranscriptCommits: func(ctx context.Context, arg sqlc.InsertTranscriptCommitsParams) error {
			batched = append(batched, arg)
			return nil
		},
	}

	commits := []schema.CommitInfo{
		{Hash: "sha1", Message: "first", AuthorName: "A", CommitTime: 1, AuthorTime: 1},
		{Hash: "sha2", Message: "second", AuthorName: "B", CommitTime: 2, AuthorTime: 2},
	}

	w := publishWithCommits(t, mq, commits)
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 200/201 (body: %s)", w.Code, w.Body.String())
	}

	// Idempotency contract: delete is always called before the batched insert.
	if deleteCount != 1 {
		t.Errorf("deleteTranscriptCommits called %d times, want 1", deleteCount)
	}
	// All commits go out in ONE batched insert (C5), not N per-row inserts.
	if len(batched) != 1 {
		t.Fatalf("InsertTranscriptCommits called %d times, want 1 (batched)", len(batched))
	}
	if batched[0].TranscriptID != tid {
		t.Errorf("batched insert scoped to wrong transcript")
	}
	shas := shasInPayload(t, batched[0].Commits)
	if len(shas) != 2 || shas[0] != "sha1" || shas[1] != "sha2" {
		t.Errorf("unexpected SHAs persisted: %v, want [sha1 sha2]", shas)
	}
}

func TestPublishTranscript_RepublishDoesNotDuplicateCommits(t *testing.T) {
	tid := toPgUUID(uuid.New())

	// Simulate a persistent store keyed on (transcript_id, sha) so we can assert
	// that re-publishing the same commits does not grow the row set.
	store := map[string]bool{}

	mq := &mockQuerier{
		// First publish creates; subsequent publishes are updates.
		getTranscriptIDByOwnerAndLocalID: func() func(ctx context.Context, arg sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
			created := false
			return func(ctx context.Context, arg sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
				if !created {
					created = true
					return pgtype.UUID{}, errors.New("not found")
				}
				return tid, nil
			}
		}(),
		createTranscript: func(ctx context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
			return sqlc.Transcript{ID: tid}, nil
		},
		updateTranscriptByOwnerAndLocalID: func(ctx context.Context, arg sqlc.UpdateTranscriptByOwnerAndLocalIDParams) (sqlc.Transcript, error) {
			return sqlc.Transcript{ID: tid}, nil
		},
		getTranscriptByID: func(context.Context, pgtype.UUID) (sqlc.Transcript, error) {
			return sqlc.Transcript{ID: tid, BlobKey: "transcripts/20000000-0000-4000-8000-000000000002.bin", WrappedDataKey: []byte("old-wrapped-key"), EncryptionAlgorithm: "aes-256-gcm-random-nonce-v1", KeyVersion: 1}, nil
		},
		deleteTranscriptCommits: func(ctx context.Context, transcriptID pgtype.UUID) error {
			// Mirror the SQL DELETE: clears existing rows for this transcript.
			store = map[string]bool{}
			return nil
		},
		insertTranscriptCommits: func(ctx context.Context, arg sqlc.InsertTranscriptCommitsParams) error {
			// Mirror the batched INSERT...ON CONFLICT (transcript_id, sha): upsert
			// every SHA in the JSONB payload by key.
			var recs []commitJSONRecord
			if err := json.Unmarshal(arg.Commits, &recs); err != nil {
				t.Fatalf("decode batch payload: %v", err)
			}
			for _, rec := range recs {
				store[rec.Sha] = true
			}
			return nil
		},
	}

	commits := []schema.CommitInfo{
		{Hash: "sha1", Message: "first", CommitTime: 1, AuthorTime: 1},
		{Hash: "sha2", Message: "second", CommitTime: 2, AuthorTime: 2},
	}

	w1 := publishWithCommits(t, mq, commits)
	if w1.Code != http.StatusOK && w1.Code != http.StatusCreated {
		t.Fatalf("first publish status = %d (body: %s)", w1.Code, w1.Body.String())
	}
	if len(store) != 2 {
		t.Fatalf("after first publish: %d commits, want 2", len(store))
	}

	// Re-publish the identical payload.
	w2 := publishWithCommits(t, mq, commits)
	if w2.Code != http.StatusOK && w2.Code != http.StatusCreated {
		t.Fatalf("second publish status = %d (body: %s)", w2.Code, w2.Body.String())
	}
	if len(store) != 2 {
		t.Errorf("after re-publish: %d commits, want 2 (no duplication)", len(store))
	}
}

func TestPublishTranscript_PersistFailureFailsAtomicPublish(t *testing.T) {
	tid := toPgUUID(uuid.New())
	mq := &mockQuerier{
		getTranscriptIDByOwnerAndLocalID: func(ctx context.Context, arg sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
			return pgtype.UUID{}, errors.New("not found")
		},
		createTranscript: func(ctx context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
			return sqlc.Transcript{ID: tid}, nil
		},
		deleteTranscriptCommits: func(ctx context.Context, transcriptID pgtype.UUID) error {
			return errors.New("db down")
		},
	}

	w := publishWithCommits(t, mq, []schema.CommitInfo{{Hash: "sha1", Message: "x"}})
	// Commit evidence is part of the publication transaction. A failed commit
	// replacement must reject the publication rather than acknowledge partial state.
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 when atomic commit persistence fails (body: %s)", w.Code, w.Body.String())
	}
}

// --- Read: persistCommits + ListTranscriptCommits round trip via the helper ---

func TestPersistCommits_DeletesThenInserts(t *testing.T) {
	tid := toPgUUID(uuid.New())
	var calls []string
	mq := &mockQuerier{
		deleteTranscriptCommits: func(ctx context.Context, transcriptID pgtype.UUID) error {
			calls = append(calls, "delete")
			return nil
		},
		insertTranscriptCommits: func(ctx context.Context, arg sqlc.InsertTranscriptCommitsParams) error {
			calls = append(calls, "insert-batch")
			return nil
		},
	}

	err := persistCommits(context.Background(), mq, tid, []schema.CommitInfo{
		{Hash: "sha1"}, {Hash: "sha2"},
	})
	if err != nil {
		t.Fatalf("persistCommits error: %v", err)
	}

	// DELETE (prune) precedes the single batched INSERT.
	want := []string{"delete", "insert-batch"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("call[%d] = %q, want %q", i, calls[i], want[i])
		}
	}
}

// Ensure the read query method is wired through the Querier and returns rows.
func TestListTranscriptCommits_ReturnsRows(t *testing.T) {
	tid := toPgUUID(uuid.New())
	mq := &mockQuerier{
		listTranscriptCommits: func(ctx context.Context, transcriptID pgtype.UUID) ([]sqlc.TranscriptCommit, error) {
			return []sqlc.TranscriptCommit{
				{Sha: "sha1", CommitOrder: 0},
				{Sha: "sha2", CommitOrder: 1},
			}, nil
		},
	}
	rows, err := mq.ListTranscriptCommits(context.Background(), tid)
	if err != nil {
		t.Fatalf("ListTranscriptCommits error: %v", err)
	}
	if len(rows) != 2 || rows[0].Sha != "sha1" || rows[1].Sha != "sha2" {
		t.Errorf("unexpected rows: %+v", rows)
	}
}

// ----------------------------------------------------------------------------
// GET /transcripts/{id}/commits
// ----------------------------------------------------------------------------

// commitResp mirrors the handler's wire shape for decoding in assertions.
type commitResp struct {
	Sha         string  `json:"sha"`
	Message     *string `json:"message"`
	AuthorName  *string `json:"authorName"`
	AuthorEmail *string `json:"authorEmail"`
	AuthoredAt  *int64  `json:"authoredAt"`
	CommittedAt *int64  `json:"committedAt"`
	Order       int32   `json:"order"`
}

func TestHandlerListTranscriptCommits_MapsRows(t *testing.T) {
	transcript := publicTranscript()

	commitRow := sqlc.TranscriptCommit{
		TranscriptID: transcript.ID,
		CommitOrder:  0,
		Sha:          "abc123def456",
		Message:      pgText("feat: add SHA bridge"),
		AuthorName:   pgText("Alice Dev"),
		AuthorEmail:  pgText("alice@example.com"),
		AuthoredAt:   nowTs(),
		CommittedAt:  nowTs(),
	}
	// Second commit exercises the nullable → JSON null path: no message,
	// author, or timestamps.
	nullRow := sqlc.TranscriptCommit{
		TranscriptID: transcript.ID,
		CommitOrder:  1,
		Sha:          "def789",
	}

	var gotTranscriptID pgtype.UUID
	q := &mockQuerier{
		getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
			return transcript, nil
		},
		listTranscriptCommits: func(_ context.Context, transcriptID pgtype.UUID) ([]sqlc.TranscriptCommit, error) {
			gotTranscriptID = transcriptID
			return []sqlc.TranscriptCommit{commitRow, nullRow}, nil
		},
	}
	h := newTestHandler(q, nil)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+idString(transcript)+"/commits", nil)
	r = withChiURLParam(r, "id", idString(transcript))
	w := httptest.NewRecorder()

	h.ListTranscriptCommits(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}
	// The query must be keyed on the transcript's own id, not the URL UUID.
	if gotTranscriptID != transcript.ID {
		t.Errorf("query transcript id: got %v, want %v", gotTranscriptID, transcript.ID)
	}

	var resp struct {
		Commits []commitResp `json:"commits"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Commits) != 2 {
		t.Fatalf("commit count: got %d, want 2", len(resp.Commits))
	}

	got := resp.Commits[0]
	if got.Sha != "abc123def456" {
		t.Errorf("sha: got %q, want abc123def456", got.Sha)
	}
	if got.Order != 0 {
		t.Errorf("order: got %d, want 0", got.Order)
	}
	if got.Message == nil || *got.Message != "feat: add SHA bridge" {
		t.Errorf("message: got %v, want 'feat: add SHA bridge'", got.Message)
	}
	if got.AuthorName == nil || *got.AuthorName != "Alice Dev" {
		t.Errorf("authorName: got %v, want 'Alice Dev'", got.AuthorName)
	}
	if got.AuthorEmail == nil || *got.AuthorEmail != "alice@example.com" {
		t.Errorf("authorEmail: got %v, want 'alice@example.com'", got.AuthorEmail)
	}
	if got.AuthoredAt == nil || *got.AuthoredAt != 1700000000000 {
		t.Errorf("authoredAt: got %v, want 1700000000000", got.AuthoredAt)
	}
	if got.CommittedAt == nil || *got.CommittedAt != 1700000000000 {
		t.Errorf("committedAt: got %v, want 1700000000000", got.CommittedAt)
	}

	// Nullable columns must surface as JSON null (nil pointers), not zero values.
	null := resp.Commits[1]
	if null.Sha != "def789" || null.Order != 1 {
		t.Errorf("second commit: got sha=%q order=%d", null.Sha, null.Order)
	}
	if null.Message != nil || null.AuthorName != nil || null.AuthorEmail != nil {
		t.Errorf("null text fields should decode as nil: %+v", null)
	}
	if null.AuthoredAt != nil || null.CommittedAt != nil {
		t.Errorf("null timestamps should decode as nil: authoredAt=%v committedAt=%v", null.AuthoredAt, null.CommittedAt)
	}
}

func TestHandlerListTranscriptCommits_EmptyWhenNone(t *testing.T) {
	transcript := publicTranscript()
	q := &mockQuerier{
		getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
			return transcript, nil
		},
		listTranscriptCommits: func(_ context.Context, _ pgtype.UUID) ([]sqlc.TranscriptCommit, error) {
			return []sqlc.TranscriptCommit{}, nil
		},
	}
	h := newTestHandler(q, nil)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+idString(transcript)+"/commits", nil)
	r = withChiURLParam(r, "id", idString(transcript))
	w := httptest.NewRecorder()

	h.ListTranscriptCommits(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
	// Must serialize as [] (not null) so the frontend can iterate safely.
	if !bytes.Contains(w.Body.Bytes(), []byte(`"commits":[]`)) {
		t.Errorf("expected empty array, got %s", w.Body.String())
	}
}

func TestHandlerListTranscriptCommits_PrivateHidden(t *testing.T) {
	transcript := privateTranscript()
	q := &mockQuerier{
		getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
			return transcript, nil
		},
	}
	h := newTestHandler(q, nil)

	// Anonymous caller (AuthOptional → no user in context): must get 404, not
	// 403, to avoid leaking the transcript's existence.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+idString(transcript)+"/commits", nil)
	r = withChiURLParam(r, "id", idString(transcript))
	w := httptest.NewRecorder()

	h.ListTranscriptCommits(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d (visibility leak)", w.Code, http.StatusNotFound)
	}
}

func TestHandlerListTranscriptCommits_BadID(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/not-a-uuid/commits", nil)
	r = withChiURLParam(r, "id", "not-a-uuid")
	w := httptest.NewRecorder()

	h.ListTranscriptCommits(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}
