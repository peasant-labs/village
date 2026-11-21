package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

type blockingAttachmentQueries struct {
	*mockQuerier
	started chan struct{}
	release chan struct{}
	done    chan error
}

func (q *blockingAttachmentQueries) ListAuthorAttachmentsForRepo(ctx context.Context, _ sqlc.ListAuthorAttachmentsForRepoParams) ([]sqlc.PullRequestAttachment, error) {
	close(q.started)
	select {
	case <-q.release:
		q.done <- ctx.Err()
	case <-ctx.Done():
		q.done <- ctx.Err()
	}
	return nil, nil
}

func TestPublishAnswersBeforeAttachmentWork(t *testing.T) {
	q := &blockingAttachmentQueries{
		mockQuerier: &mockQuerier{
			getTranscriptIDByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
				return pgtype.UUID{}, errors.New("not found")
			},
			createTranscript: func(_ context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
				return sqlc.Transcript{ID: arg.ID, OwnerID: arg.OwnerID, GitRemote: arg.GitRemote}, nil
			},
		},
		started: make(chan struct{}), release: make(chan struct{}), done: make(chan error, 1),
	}
	h := newTestHandler(q, &mockTranscriptBlobStore{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(q.release) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.PublishTranscript(w, r.WithContext(withTestUser(r.Context())))
	}))
	defer srv.Close()
	defer func() {
		release()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.DrainPublishAttachments(ctx); err != nil {
			t.Error(err)
		}
	}()
	metadata := schema.PublishRequest{
		Identity:    schema.SessionIdentity{SessionID: "550e8400-e29b-41d4-a716-446655440000", SchemaVersion: 2},
		Model:       schema.ModelInfo{Harness: schema.HarnessCodex, Model: "gpt-4"},
		Timestamp:   schema.TimestampInfo{Start: 1700000000000, End: 1700000060000},
		Source:      schema.SourceInfo{FilePath: "/fixture.jsonl", Format: "jsonl"},
		Git:         schema.GitContext{Remote: strPtr("git@github.com:acme/repo.git")},
		Project:     schema.ProjectContext{Hash: testProjectHash, Name: "test-project"},
		Diagnostics: schema.DiagnosticsInfo{Warnings: []schema.DiagnosticEntry{}},
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	body, boundary := multipartBody(t, map[string]string{"metadata": string(raw)}, `[{"role":"user","content":"hello"}]`)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(srv.URL, "multipart/form-data; boundary="+boundary, body)
	if err != nil {
		t.Fatalf("publish response while attachment work blocked: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("publish response = %d %s, read error %v", resp.StatusCode, data, err)
	}
	var receipt struct {
		Transcript json.RawMessage `json:"transcript"`
	}
	if err := json.Unmarshal(data, &receipt); err != nil || len(receipt.Transcript) == 0 {
		t.Fatalf("publish response missing transcript: %s (decode error %v)", data, err)
	}
	select {
	case <-q.started:
	case <-time.After(5 * time.Second):
		t.Fatal("attachment work did not start")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.DrainPublishAttachments(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("draining blocked work with a cancelled context = %v", err)
	}
	release()
	select {
	case err := <-q.done:
		if err != nil {
			t.Fatalf("attachment context cancelled after response: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("attachment work did not finish")
	}
}
