package handler

// Handler-level migrate-on-read tests cover the migrator, immutable encrypted
// rewrite, and keyed concurrency guard in GetTranscriptContent.
//
// SUT = the real GetTranscriptContent handler (not mocked); only encrypted storage and DB
// dependencies are faked.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

// fakeBlobStore serves authenticated plaintext by descriptor and records
// immutable rewrites. It is thread-safe for the concurrency test.
type fakeBlobStore struct {
	mu        sync.Mutex
	blobs     map[string][]byte
	uploads   int
	lastBody  []byte
	downloads int
}

func newFakeBlobStore() *fakeBlobStore {
	return &fakeBlobStore{blobs: make(map[string][]byte)}
}

func (f *fakeBlobStore) put(key string, b []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blobs[key] = b
}

func (f *fakeBlobStore) Write(_ context.Context, _ uuid.UUID, b []byte) (storage.BlobDescriptor, storage.ContentIdentity, error) {
	key := "transcripts/10000000-0000-4000-8000-000000000001.bin"
	f.mu.Lock()
	f.blobs[key] = append([]byte(nil), b...)
	f.lastBody = append([]byte(nil), b...)
	f.uploads++
	f.mu.Unlock()
	descriptor, err := storage.NewBlobDescriptor(storage.ObjectKey(key), []byte("test-wrapped-key"), storage.EncryptionAES256GCMRandomNonceV1, 1)
	if err != nil {
		return storage.BlobDescriptor{}, storage.ContentIdentity{}, err
	}
	identity, err := storage.NewContentIdentity(schema.ComputeTranscriptHash(b), int64(len(b)))
	return descriptor, identity, err
}

func (f *fakeBlobStore) Read(_ context.Context, _ uuid.UUID, descriptor storage.BlobDescriptor, loaded storage.LoadedContentIdentity) ([]byte, storage.ContentIdentity, error) {
	f.mu.Lock()
	f.downloads++
	b := append([]byte(nil), f.blobs[string(descriptor.ObjectKey())]...)
	f.mu.Unlock()
	if known, ok := loaded.Known(); ok {
		return b, known, nil
	}
	identity, err := storage.NewContentIdentity(schema.ComputeTranscriptHash(b), int64(len(b)))
	return b, identity, err
}

func (*fakeBlobStore) Rewrap(context.Context, uuid.UUID, storage.BlobDescriptor) (storage.BlobDescriptor, error) {
	return storage.BlobDescriptor{}, errors.New("fake transcript blob rewrap not configured")
}
func (*fakeBlobStore) Delete(context.Context, storage.BlobDescriptor) error { return nil }

func (f *fakeBlobStore) uploadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.uploads
}

// publicTranscriptQuerier returns a mockQuerier whose GetTranscriptByID yields a
// public transcript backed by blobKey (so canViewTranscript passes for nil user).
func publicTranscriptQuerier(blobKey string) *mockQuerier {
	return &mockQuerier{
		getTranscriptByID: func(_ context.Context, id pgtype.UUID) (sqlc.Transcript, error) {
			return sqlc.Transcript{ID: id, Visibility: "public", BlobKey: blobKey, WrappedDataKey: []byte("test-wrapped-key"), EncryptionAlgorithm: "aes-256-gcm-random-nonce-v1", KeyVersion: 1}, nil
		},
	}
}

type generationTestBlobStore struct{ *fakeBlobStore }

func (s generationTestBlobStore) Write(ctx context.Context, _ uuid.UUID, content []byte) (storage.BlobDescriptor, storage.ContentIdentity, error) {
	descriptor, err := storage.NewBlobDescriptor(storage.ObjectKey("transcripts/20000000-0000-4000-8000-000000000002.bin"), []byte("test-wrapped-key"), storage.EncryptionAES256GCMRandomNonceV1, 1)
	if err != nil {
		return storage.BlobDescriptor{}, storage.ContentIdentity{}, err
	}
	identity, err := storage.NewContentIdentity(schema.ComputeTranscriptHash(content), int64(len(content)))
	if err == nil {
		s.mu.Lock()
		s.blobs[string(descriptor.ObjectKey())] = append([]byte(nil), content...)
		s.lastBody = append([]byte(nil), content...)
		s.uploads++
		s.mu.Unlock()
	}
	return descriptor, identity, err
}

func getContent(t *testing.T, h *Handler, id uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+id.String()+"/content", nil)
	r = withChiURLParam(r, "id", id.String())
	w := httptest.NewRecorder()
	h.GetTranscriptContent(w, r)
	return w
}

// TestGetContent_LegacyBlob_MigratesAndRewrites: a stored legacy provider-keyed
// blob is normalized to the current harness shape on read, and the stored blob
// is rewritten so a second read is a no-op.
func TestGetContent_LegacyBlob_MigratesAndRewrites(t *testing.T) {
	const key = "transcripts/10000000-0000-4000-8000-000000000001.bin"
	id := uuid.New()
	s3 := newFakeBlobStore()
	s3.put(key, legacyBarePayloadJSON("claude"))
	h := newTestHandler(publicTranscriptQuerier(key), s3)

	// First read: migrates + rewrites.
	w := getContent(t, h, id)
	if w.Code != http.StatusOK {
		t.Fatalf("first read status: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var got schema.SessionDetailPayload
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("response not a SessionDetailPayload: %v (body: %s)", err, w.Body.String())
	}
	if got.Harness != schema.HarnessClaudeCode {
		t.Errorf("served harness: got %q, want %q", got.Harness, schema.HarnessClaudeCode)
	}
	if s3.uploadCount() != 1 {
		t.Errorf("expected exactly 1 rewrite upload on first read, got %d", s3.uploadCount())
	}

	// Second read: stored blob is now canonical -> no further rewrite.
	_ = getContent(t, h, id)
	if s3.uploadCount() != 1 {
		t.Errorf("second read must be a no-op (no extra upload), got %d total uploads", s3.uploadCount())
	}
}

// TestGetContent_RewriteOnRead_UpdatesContentHash verifies that when
// migrate-on-read rewrites the stored blob to canonical bytes, the recorded
// content_hash MUST be recomputed over the freshly-stored canonical bytes and
// persisted — otherwise the pull surface advertises a stale ETag/ContentHash for
// bytes it no longer serves, permanently defeating the conditional-GET 304 fast
// path for migrated rows. Asserts the SetTranscriptContentHash the handler issues
// equals ComputeTranscriptHash(uploaded canonical bytes).
func TestGetContent_RewriteOnRead_UpdatesContentHash(t *testing.T) {
	const key = "transcripts/10000000-0000-4000-8000-000000000001.bin"
	id := uuid.New()
	s3 := newFakeBlobStore()
	s3.put(key, legacyBarePayloadJSON("claude"))

	q := publicTranscriptQuerier(key)
	var gotCAS sqlc.CompareAndSwapTranscriptBlobParams
	var casCalls int
	q.compareAndSwapTranscriptBlob = func(_ context.Context, arg sqlc.CompareAndSwapTranscriptBlobParams) (sqlc.Transcript, error) {
		casCalls++
		gotCAS = arg
		return sqlc.Transcript{ID: arg.ID}, nil
	}
	h := newTestHandler(q, s3)

	w := getContent(t, h, id)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if s3.uploadCount() != 1 {
		t.Fatalf("expected exactly 1 rewrite upload, got %d", s3.uploadCount())
	}
	if casCalls != 1 {
		t.Fatalf("rewrite-on-read must install descriptor and identity exactly once, got %d CAS calls", casCalls)
	}
	if uuid.UUID(gotCAS.ID.Bytes) != id {
		t.Errorf("content identity written for wrong transcript: got %s, want %s", uuid.UUID(gotCAS.ID.Bytes), id)
	}
	// The persisted hash MUST equal the hash of the EXACT canonical bytes the
	// rewrite uploaded (not the pre-rewrite legacy bytes).
	s3.mu.Lock()
	uploaded := append([]byte(nil), s3.lastBody...)
	s3.mu.Unlock()
	wantHash := schema.ComputeTranscriptHash(uploaded)
	if gotCAS.ContentHash.String != wantHash {
		t.Errorf("persisted content_hash = %q, want hash of served canonical bytes %q", gotCAS.ContentHash.String, wantHash)
	}
	if gotCAS.ContentHash.String == "" {
		t.Error("persisted content_hash is empty — stale-hash invariant not fixed")
	}
}

// TestGetContent_CurrentEnvelope_UnwrapsNoRewrite: a fresh-push TranscriptContent
// envelope is unwrapped to a bare SessionDetailPayload for the frontend, with no
// rewrite.
func TestGetContent_CurrentEnvelope_UnwrapsNoRewrite(t *testing.T) {
	const key = "transcripts/10000000-0000-4000-8000-000000000001.bin"
	id := uuid.New()
	s3 := newFakeBlobStore()
	s3.put(key, currentEnvelopeJSON(t, "claude-code"))
	h := newTestHandler(publicTranscriptQuerier(key), s3)

	w := getContent(t, h, id)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var got schema.SessionDetailPayload
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("served body must be a bare SessionDetailPayload, not an envelope: %v", err)
	}
	if got.Harness != schema.HarnessClaudeCode || got.ID != "sess-current" {
		t.Errorf("unwrapped payload mismatch: harness=%q id=%q", got.Harness, got.ID)
	}
	// Body must NOT still be an envelope (no contractVersion key passed through).
	if bytes.Contains(w.Body.Bytes(), []byte("contractVersion")) {
		t.Error("served body still contains the envelope (contractVersion) — must be unwrapped")
	}
	if s3.uploadCount() != 0 {
		t.Errorf("current envelope must not rewrite, got %d uploads", s3.uploadCount())
	}
}

// TestGetContent_N2_ConcurrentReads_RaceGuard: concurrent first-reads of the same
// legacy blob must not race (run under -race) and must not produce a storm of
// divergent rewrites.
func TestGetContent_N2_ConcurrentReads_RaceGuard(t *testing.T) {
	const key = "transcripts/10000000-0000-4000-8000-000000000001.bin"
	id := uuid.New()
	s3 := newFakeBlobStore()
	s3.put(key, legacyBarePayloadJSON("gemini"))
	q := publicTranscriptQuerier(key)
	var installed atomic.Pointer[sqlc.Transcript]
	q.compareAndSwapTranscriptBlob = func(_ context.Context, arg sqlc.CompareAndSwapTranscriptBlobParams) (sqlc.Transcript, error) {
		row := sqlc.Transcript{ID: arg.ID, Visibility: "public", BlobKey: arg.BlobKey, WrappedDataKey: arg.WrappedDataKey, EncryptionAlgorithm: arg.EncryptionAlgorithm, KeyVersion: arg.KeyVersion, ContentHash: arg.ContentHash, BlobSizeBytes: arg.PlaintextSize}
		installed.Store(&row)
		return row, nil
	}
	originalGet := q.getTranscriptByID
	q.getTranscriptByID = func(ctx context.Context, id pgtype.UUID) (sqlc.Transcript, error) {
		if row := installed.Load(); row != nil {
			return *row, nil
		}
		return originalGet(ctx, id)
	}
	h := newTestHandler(q, s3)
	h.blobs = generationTestBlobStore{s3}

	const n = 8
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			w := getContent(t, h, id)
			if w.Code != http.StatusOK {
				t.Errorf("concurrent read status: got %d", w.Code)
			}
		}()
	}
	wg.Wait()
	// The keyedMutex must serialize the rewrite so it happens at most once.
	if c := s3.uploadCount(); c > 1 {
		t.Errorf("N2 guard: expected at most 1 rewrite under concurrency, got %d", c)
	}
}
