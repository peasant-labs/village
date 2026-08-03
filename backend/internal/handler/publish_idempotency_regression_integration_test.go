//go:build integration

package handler

// Characterization / regression lock on the EXISTING publish idempotency
// behavior. Publish identity is keyed on the SOURCE (owner + the client's local
// session id, carried as identity.sessionId and stored as transcripts.local_id),
// enforced by UNIQUE(owner_id, local_id) plus the GetTranscriptIDByOwnerAndLocalID
// probe in PublishTranscript. The transcript CONTENT is never consulted for
// identity, and content_hash is a plain value-only column (no UNIQUE, no
// ON CONFLICT). These tests drive the REAL PublishTranscript handler over
// httptest against a real Postgres and assert observable outcomes (HTTP status,
// persisted row counts + ids + blob keys), so a future change that made publish
// content-keyed would break them.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// publishResult is the observable outcome of one publish call: the HTTP status
// and the transcript id + blob key echoed in the response body.
type publishResult struct {
	status  int
	id      string
	blobKey string
}

// doPublish drives the REAL PublishTranscript handler over httptest for one
// (owner, sessionID => local_id, content) and returns the observed status and
// the returned transcript id + blob key. It builds a schema-valid PublishRequest
// so the request reaches the create/update path (not a validation reject).
func doPublish(t *testing.T, ctx context.Context, h *Handler, owner pgtype.UUID, username, sessionID, content string) publishResult {
	t.Helper()

	meta := schema.PublishRequest{
		Identity:    schema.SessionIdentity{SessionID: schema.SessionID(sessionID), SchemaVersion: 2},
		Model:       schema.ModelInfo{Harness: schema.HarnessClaudeCode, Model: "m"},
		Timestamp:   schema.TimestampInfo{Start: 1700000000000, End: 1700000060000},
		Source:      schema.SourceInfo{FilePath: "/p/transcript.jsonl", Format: "jsonl"},
		Git:         schema.GitContext{Branch: strPtr("main")},
		Project:     schema.ProjectContext{Hash: testProjectHash, Name: "idem-project"},
		Stats:       schema.SessionStats{TurnCount: 1, ToolCallCount: 1, DurationMs: 1000, TokensIn: 1, TokensOut: 1},
		Diagnostics: schema.DiagnosticsInfo{Warnings: []schema.DiagnosticEntry{}},
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	body, boundary := multipartBody(t, map[string]string{"metadata": string(metaJSON)}, content)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(context.WithValue(ctx, UserContextKey, &AuthUser{ID: uuid.UUID(owner.Bytes), Username: username}))
	w := httptest.NewRecorder()

	h.PublishTranscript(w, r)

	res := publishResult{status: w.Code}
	if w.Code == http.StatusOK || w.Code == http.StatusCreated {
		var resp struct {
			Transcript struct {
				ID      string `json:"id"`
				BlobKey string `json:"blob_key"`
			} `json:"transcript"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode publish response (status %d): %v; body=%s", w.Code, err, w.Body.String())
		}
		res.id = resp.Transcript.ID
		res.blobKey = resp.Transcript.BlobKey
	}
	return res
}

// ownerRowCount returns how many transcript rows the owner has (the observable
// dedupe outcome: a re-push must NOT add a row; a fork must).
func ownerRowCount(t *testing.T, ctx context.Context, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, owner pgtype.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcripts WHERE owner_id = $1`, owner).Scan(&n); err != nil {
		t.Fatalf("count rows for owner: %v", err)
	}
	return n
}

// transcriptRow reads the persisted (id, blob_key, content_hash) for one
// (owner, local_id) row, or found=false when absent.
func transcriptRow(t *testing.T, ctx context.Context, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, owner pgtype.UUID, localID string) (id, blobKey string, contentHash pgtype.Text, found bool) {
	t.Helper()
	var pid pgtype.UUID
	err := pool.QueryRow(ctx,
		`SELECT id, blob_key, content_hash FROM transcripts WHERE owner_id = $1 AND local_id = $2`,
		owner, localID).Scan(&pid, &blobKey, &contentHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", pgtype.Text{}, false
	}
	if err != nil {
		t.Fatalf("select row (owner, %s): %v", localID, err)
	}
	return uuid.UUID(pid.Bytes).String(), blobKey, contentHash, true
}

// TestPublishSourceKeyedIdempotency is the paired source-identity control:
// same local_id re-push reuses the row (200, one row, same id); a different
// local_id with byte-identical content is a distinct fork (201, two rows).
func TestPublishSourceKeyedIdempotency(t *testing.T) {
	pool := govTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	h := &Handler{pool: pool, queries: sqlc.New(pool), blobs: &mockTranscriptBlobStore{}}

	t.Run("same_local_id_repush_reuses_row", func(t *testing.T) {
		owner := pullInsertUser(t, ctx, pool, 934001, "idem-repush-owner")
		defer cleanupOwners(t, ctx, pool, owner)

		sessionID := uuid.NewString()
		content := `[{"role":"user","content":"hello"}]`

		first := doPublish(t, ctx, h, owner, "idem-repush-owner", sessionID, content)
		if first.status != http.StatusCreated {
			t.Fatalf("first publish: got %d, want 201", first.status)
		}

		second := doPublish(t, ctx, h, owner, "idem-repush-owner", sessionID, content)
		if second.status != http.StatusOK {
			t.Errorf("re-push of the same local_id: got %d, want 200 (reuse, not create)", second.status)
		}

		if n := ownerRowCount(t, ctx, pool, owner); n != 1 {
			t.Errorf("row count after re-push: got %d, want 1 (a retry must reuse the row, not insert)", n)
		}

		dbID, _, _, found := transcriptRow(t, ctx, pool, owner, sessionID)
		if !found {
			t.Fatalf("row (owner, %s) missing after re-push", sessionID)
		}
		if first.id == "" || second.id != first.id {
			t.Errorf("returned transcript id must be stable across a re-push: first=%q second=%q", first.id, second.id)
		}
		if dbID != first.id {
			t.Errorf("persisted id %q != returned id %q", dbID, first.id)
		}
	})

	t.Run("fork_distinct_local_id_identical_content_is_new_row", func(t *testing.T) {
		owner := pullInsertUser(t, ctx, pool, 934002, "idem-fork-owner")
		defer cleanupOwners(t, ctx, pool, owner)

		content := `[{"role":"user","content":"identical fork content"}]`
		sidA := uuid.NewString()
		sidB := uuid.NewString()

		a := doPublish(t, ctx, h, owner, "idem-fork-owner", sidA, content)
		if a.status != http.StatusCreated {
			t.Fatalf("fork A publish: got %d, want 201", a.status)
		}
		// Different local_id, byte-identical content: content is never identity.
		b := doPublish(t, ctx, h, owner, "idem-fork-owner", sidB, content)
		if b.status != http.StatusCreated {
			t.Errorf("fork B (different local_id, identical content): got %d, want 201 (content bytes never dedupe a publish)", b.status)
		}

		if n := ownerRowCount(t, ctx, pool, owner); n != 2 {
			t.Errorf("row count for two forks: got %d, want 2 (identical content under distinct local_ids is two rows)", n)
		}
		if a.id == "" || b.id == "" || a.id == b.id {
			t.Errorf("fork transcript ids must be distinct: A=%q B=%q", a.id, b.id)
		}
	})
}

// TestPublishForksSurvive_NoContentKeyedIdempotency is the load-bearing negative
// control. Two forks (same owner, distinct local_ids, byte-identical content)
// keep INDEPENDENT rows and independent per-uuid blob keys, and BOTH persist the
// SAME non-null content_hash. content_hash is a plain value-only column with no
// UNIQUE and no ON CONFLICT, so two rows may legitimately share one hash. If a
// UNIQUE / ON-CONFLICT on content_hash were introduced (content-keyed
// idempotency), the second fork's best-effort hash write would collide and drop
// to NULL, turning the shared-hash assertions RED, which is exactly the
// regression this test exists to catch.
func TestPublishForksSurvive_NoContentKeyedIdempotency(t *testing.T) {
	pool := govTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	h := &Handler{pool: pool, queries: sqlc.New(pool), blobs: &mockTranscriptBlobStore{}}

	owner := pullInsertUser(t, ctx, pool, 934003, "idem-forks-owner")
	defer cleanupOwners(t, ctx, pool, owner)

	content := `[{"role":"user","content":"byte-identical across forks"}]`
	sidA := uuid.NewString()
	sidB := uuid.NewString()

	a := doPublish(t, ctx, h, owner, "idem-forks-owner", sidA, content)
	b := doPublish(t, ctx, h, owner, "idem-forks-owner", sidB, content)
	if a.status != http.StatusCreated || b.status != http.StatusCreated {
		t.Fatalf("fork publishes: A=%d B=%d, want 201 and 201", a.status, b.status)
	}

	// Forks survive: two independent rows.
	if n := ownerRowCount(t, ctx, pool, owner); n != 2 {
		t.Fatalf("forks did not survive: got %d rows, want 2", n)
	}

	idA, blobA, hashA, foundA := transcriptRow(t, ctx, pool, owner, sidA)
	idB, blobB, hashB, foundB := transcriptRow(t, ctx, pool, owner, sidB)
	if !foundA || !foundB {
		t.Fatalf("fork rows missing: A found=%v, B found=%v", foundA, foundB)
	}
	if idA == idB {
		t.Errorf("fork ids collapsed to one: %q == %q", idA, idB)
	}

	// Independent per-uuid blob keys: the blob key is derived from the transcript
	// uuid, so distinct fork rows never share a blob.
	if blobA == blobB {
		t.Errorf("fork blob keys collapsed: %q == %q", blobA, blobB)
	}

	// Contradiction gate: both forks persist the SAME non-null content_hash. A
	// UNIQUE / ON-CONFLICT on content_hash would drop the second write to NULL and
	// fail these assertions.
	want := schema.ComputeTranscriptHash([]byte(content))
	if !hashA.Valid || hashA.String != want {
		t.Errorf("fork A content_hash: got valid=%v %q, want %q", hashA.Valid, hashA.String, want)
	}
	if !hashB.Valid || hashB.String != want {
		t.Errorf("fork B content_hash: got valid=%v %q, want %q (two rows legitimately share one content_hash: value-only, not unique)", hashB.Valid, hashB.String, want)
	}
	if hashA.String != hashB.String {
		t.Errorf("forks must share one identical content_hash: A=%q B=%q", hashA.String, hashB.String)
	}
}
