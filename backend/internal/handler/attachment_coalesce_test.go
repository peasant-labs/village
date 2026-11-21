package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

type coalesceQueries struct {
	*mockQuerier
	mu                         sync.Mutex
	calls                      map[string]int
	active                     map[string]int
	maxActive                  map[string]int
	started                    chan string
	release                    chan struct{}
	refreshed                  []pgtype.UUID
	refreshBeforeSweepFinished bool
}

func (q *coalesceQueries) ListAuthorAttachmentsForRepo(ctx context.Context, arg sqlc.ListAuthorAttachmentsForRepoParams) ([]sqlc.PullRequestAttachment, error) {
	q.mu.Lock()
	q.calls[arg.RepoName]++
	q.active[arg.RepoName]++
	if q.active[arg.RepoName] > q.maxActive[arg.RepoName] {
		q.maxActive[arg.RepoName] = q.active[arg.RepoName]
	}
	q.mu.Unlock()
	q.started <- arg.RepoName
	select {
	case <-q.release:
	case <-ctx.Done():
	}
	q.mu.Lock()
	q.active[arg.RepoName]--
	q.mu.Unlock()
	return nil, nil
}

func (q *coalesceQueries) ListAttachmentsBindingTranscript(_ context.Context, id pgtype.UUID) ([]sqlc.PullRequestAttachment, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.calls["one"] < 2 || q.active["one"] != 0 {
		q.refreshBeforeSweepFinished = true
	}
	q.refreshed = append(q.refreshed, id)
	return nil, nil
}

func newCoalesceTestHandler() (*Handler, *coalesceQueries) {
	q := &coalesceQueries{
		mockQuerier: &mockQuerier{
			getTranscriptIDByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
				return pgtype.UUID{}, errors.New("not found")
			},
			createTranscript: func(_ context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
				return sqlc.Transcript{ID: arg.ID, OwnerID: arg.OwnerID, GitRemote: arg.GitRemote}, nil
			},
		},
		calls: make(map[string]int), active: make(map[string]int), maxActive: make(map[string]int),
		started: make(chan string, 32), release: make(chan struct{}),
	}
	return newTestHandler(q, &mockTranscriptBlobStore{}), q
}

func publishForCoalesce(t *testing.T, h *Handler, owner uuid.UUID, repo string) {
	t.Helper()
	publishSessionForCoalesce(t, h, owner, repo, uuid.NewString(), http.StatusCreated)
}

func publishSessionForCoalesce(t *testing.T, h *Handler, owner uuid.UUID, repo, session string, status int) {
	t.Helper()
	metadata := schema.PublishRequest{
		Identity:    schema.SessionIdentity{SessionID: schema.SessionID(session), SchemaVersion: 2},
		Model:       schema.ModelInfo{Harness: schema.HarnessCodex, Model: "gpt-4"},
		Timestamp:   schema.TimestampInfo{Start: 1700000000000, End: 1700000060000},
		Source:      schema.SourceInfo{FilePath: "/fixture.jsonl", Format: "jsonl"},
		Git:         schema.GitContext{Remote: strPtr("git@github.com:acme/" + repo + ".git")},
		Project:     schema.ProjectContext{Hash: testProjectHash, Name: "test-project"},
		Diagnostics: schema.DiagnosticsInfo{Warnings: []schema.DiagnosticEntry{}},
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	body, boundary := multipartBody(t, map[string]string{"metadata": string(raw)}, `[{"role":"user","content":"hello"}]`)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &AuthUser{ID: owner, Username: "test"}))
	w := httptest.NewRecorder()
	h.PublishTranscript(w, r)
	if w.Code != status {
		t.Fatalf("publish: %d %s", w.Code, w.Body.String())
	}
}

func waitSweep(t *testing.T, q *coalesceQueries, want string) {
	t.Helper()
	select {
	case got := <-q.started:
		if got != want {
			t.Fatalf("sweep repository = %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sweep did not start")
	}
}

func TestPublishAttachmentSweepsCoalesce(t *testing.T) {
	h, q := newCoalesceTestHandler()
	var once sync.Once
	release := func() { once.Do(func() { close(q.release) }) }
	defer func() { release(); _ = h.DrainPublishAttachments(context.Background()) }()
	owner := uuid.New()
	publishForCoalesce(t, h, owner, "one")
	waitSweep(t, q, "one")
	for i := 0; i < 8; i++ {
		publishForCoalesce(t, h, owner, "ONE")
	}
	q.mu.Lock()
	if q.calls["one"] != 1 || q.maxActive["one"] != 1 {
		t.Errorf("before release calls=%d max=%d", q.calls["one"], q.maxActive["one"])
	}
	q.mu.Unlock()
	release()
	if err := h.DrainPublishAttachments(context.Background()); err != nil {
		t.Fatal(err)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.calls["one"] != 2 || q.maxActive["one"] != 1 {
		t.Fatalf("calls=%d max=%d, want 2 and 1", q.calls["one"], q.maxActive["one"])
	}
}

func TestPublishAttachmentSweepsDifferentRepositoriesRunConcurrently(t *testing.T) {
	h, q := newCoalesceTestHandler()
	defer func() { close(q.release); _ = h.DrainPublishAttachments(context.Background()) }()
	owner := uuid.New()
	publishForCoalesce(t, h, owner, "one")
	waitSweep(t, q, "one")
	publishForCoalesce(t, h, owner, "two")
	waitSweep(t, q, "two")
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.active["one"] != 1 || q.active["two"] != 1 {
		t.Fatal("different repository sweeps did not overlap")
	}
}

func TestPublishAttachmentQueuedNarrowings(t *testing.T) {
	h, q := newCoalesceTestHandler()
	var once sync.Once
	release := func() { once.Do(func() { close(q.release) }) }
	defer func() { release(); _ = h.DrainPublishAttachments(context.Background()) }()
	owner := toPgUUID(uuid.New())
	publishForCoalesce(t, h, uuid.UUID(owner.Bytes), "one")
	waitSweep(t, q, "one")
	publishForCoalesce(t, h, uuid.UUID(owner.Bytes), "one")
	blobs := h.blobs.(*mockTranscriptBlobStore)
	first, second := toPgUUID(uuid.New()), toPgUUID(uuid.New())
	firstSession, secondSession := uuid.NewString(), uuid.NewString()
	rows := make(map[pgtype.UUID]sqlc.Transcript)
	for _, id := range []pgtype.UUID{first, second} {
		d, _, err := blobs.Write(context.Background(), uuid.UUID(id.Bytes), []byte("existing"))
		if err != nil {
			t.Fatal(err)
		}
		rows[id] = sqlc.Transcript{
			ID: id, OwnerID: owner, Visibility: "public",
			GitRemote: pgtype.Text{String: "git@github.com:acme/one.git", Valid: true},
			BlobKey:   string(d.ObjectKey()), WrappedDataKey: d.WrappedDEK(),
			EncryptionAlgorithm: string(d.Algorithm()), KeyVersion: int32(d.KeyVersion()),
		}
	}
	q.getTranscriptIDByOwnerAndLocalID = func(_ context.Context, arg sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
		if arg.LocalID == firstSession {
			return first, nil
		}
		if arg.LocalID == secondSession {
			return second, nil
		}
		return pgtype.UUID{}, errors.New("not found")
	}
	q.getTranscriptByID = func(_ context.Context, id pgtype.UUID) (sqlc.Transcript, error) { return rows[id], nil }
	q.getTranscriptGovernanceForUpdate = func(context.Context, pgtype.UUID) (sqlc.GetTranscriptGovernanceForUpdateRow, error) {
		return sqlc.GetTranscriptGovernanceForUpdateRow{Visibility: "public"}, nil
	}
	q.updateTranscriptMetadata = func(context.Context, sqlc.UpdateTranscriptMetadataParams) (sqlc.Transcript, error) {
		return sqlc.Transcript{}, nil
	}
	blobs.uploadErr = errors.New("storage unavailable")
	publishSessionForCoalesce(t, h, uuid.UUID(owner.Bytes), "one", firstSession, http.StatusInternalServerError)
	publishSessionForCoalesce(t, h, uuid.UUID(owner.Bytes), "one", secondSession, http.StatusInternalServerError)
	publishSessionForCoalesce(t, h, uuid.UUID(owner.Bytes), "one", firstSession, http.StatusInternalServerError)
	q.mu.Lock()
	if len(q.refreshed) != 0 {
		t.Error("refreshed before sweep finished")
	}
	q.mu.Unlock()
	release()
	if err := h.DrainPublishAttachments(context.Background()); err != nil {
		t.Fatal(err)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.calls["one"] != 2 || len(q.refreshed) != 2 || q.refreshBeforeSweepFinished {
		t.Fatalf("sweeps=%d refreshed=%v before completion=%t", q.calls["one"], q.refreshed, q.refreshBeforeSweepFinished)
	}
	seen := map[pgtype.UUID]bool{}
	for _, id := range q.refreshed {
		seen[id] = true
	}
	if !seen[first] || !seen[second] {
		t.Fatalf("refreshed wrong transcripts: %v", q.refreshed)
	}
}

func TestPublishAttachmentNarrowingWithoutRepository(t *testing.T) {
	h, q := newCoalesceTestHandler()
	id := toPgUUID(uuid.New())
	h.startPublishAttachmentWork(context.Background(), toPgUUID(uuid.New()), "", false, id, true)
	if err := h.DrainPublishAttachments(context.Background()); err != nil {
		t.Fatal(err)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.calls) != 0 || len(q.refreshed) != 1 || q.refreshed[0] != id {
		t.Fatalf("sweeps=%v refreshed=%v", q.calls, q.refreshed)
	}
}
