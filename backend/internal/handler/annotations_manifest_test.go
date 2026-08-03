package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// ----------------------------------------------------------------------------
// GET /annotations/manifest server-authoritative currency checks.
// ----------------------------------------------------------------------------

// getManifest drives GetAnnotationManifest with an authed user in context.
func getManifest(t *testing.T, h *Handler) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/annotations/manifest", nil)
	r = r.WithContext(withTestUser(r.Context()))
	w := httptest.NewRecorder()
	h.GetAnnotationManifest(w, r)
	return w
}

func TestGetAnnotationManifest_OwnerScopedHashesAndDigest(t *testing.T) {
	// Returned in arbitrary order from the DB; the handler must normalize.
	stored := []string{"hashB", "hashA", "hashC"}

	var gotOwner pgtype.UUID
	q := &mockQuerier{
		listAnnotationContentHashesByOwner: func(_ context.Context, ownerID pgtype.UUID) ([]string, error) {
			gotOwner = ownerID
			return stored, nil
		},
	}
	h := newTestHandler(q, nil)

	w := getManifest(t, h)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}

	// Owner-scoped: the query must be called with the authed user's PgID, never
	// a foreign or zero id.
	if !gotOwner.Valid {
		t.Error("manifest query must be scoped to a valid owner id")
	}

	var got schema.AnnotationManifestResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Hashes are normalized (sorted + deduped) and the digest matches the set.
	want := schema.NewAnnotationManifestResponse(stored)
	if len(got.Hashes) != len(want.Hashes) {
		t.Fatalf("hashes len: got %d, want %d", len(got.Hashes), len(want.Hashes))
	}
	for i := range want.Hashes {
		if got.Hashes[i] != want.Hashes[i] {
			t.Errorf("hashes[%d]: got %q, want %q", i, got.Hashes[i], want.Hashes[i])
		}
	}
	if got.Digest != want.Digest {
		t.Errorf("digest: got %q, want %q", got.Digest, want.Digest)
	}
	if got.Digest != got.ComputeDigest() {
		t.Error("digest must be consistent with the returned hash set")
	}
}

func TestGetAnnotationManifest_EmptyOwnerYieldsStableDigest(t *testing.T) {
	q := &mockQuerier{
		listAnnotationContentHashesByOwner: func(_ context.Context, _ pgtype.UUID) ([]string, error) {
			return []string{}, nil
		},
	}
	h := newTestHandler(q, nil)

	w := getManifest(t, h)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	var got schema.AnnotationManifestResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Hashes) != 0 {
		t.Errorf("empty manifest hashes: got %d, want 0", len(got.Hashes))
	}
	// The empty set must hash to the well-defined empty digest so two empty
	// manifests compare equal (no-op short-circuit correctness).
	if got.Digest != schema.ComputeManifestDigest(nil) {
		t.Errorf("empty digest: got %q, want %q", got.Digest, schema.ComputeManifestDigest(nil))
	}
}

func TestGetAnnotationManifest_DBErrorIs500(t *testing.T) {
	q := &mockQuerier{
		listAnnotationContentHashesByOwner: func(_ context.Context, _ pgtype.UUID) ([]string, error) {
			return nil, errors.New("db: connection reset")
		},
	}
	h := newTestHandler(q, nil)

	w := getManifest(t, h)
	// A fetch failure must surface as 500 — never as an empty manifest, which
	// would make the client skip everything instead of failing safe.
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// ----------------------------------------------------------------------------
// POST /annotations — retraction processing.
// ----------------------------------------------------------------------------

// postAnnotationPush drives UploadAnnotations with an authed user in context.
func postAnnotationPush(t *testing.T, h *Handler, req schema.AnnotationPushRequest) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/annotations", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(withTestUser(r.Context()))
	w := httptest.NewRecorder()
	h.UploadAnnotations(w, r)
	return w
}

func TestUploadAnnotations_RetractionDropsOwnerScoped(t *testing.T) {
	var deleted []sqlc.DeleteAnnotationByContentHashParams
	q := &mockQuerier{
		deleteAnnotationByContentHash: func(_ context.Context, arg sqlc.DeleteAnnotationByContentHashParams) error {
			deleted = append(deleted, arg)
			return nil
		},
	}
	h := newTestHandler(q, nil)

	w := postAnnotationPush(t, h, schema.AnnotationPushRequest{
		Annotations: []schema.AnnotationPushItem{},
		Retractions: []string{"retire-1", "retire-2"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}

	if len(deleted) != 2 {
		t.Fatalf("delete calls: got %d, want 2", len(deleted))
	}
	if deleted[0].ContentHash != "retire-1" || deleted[1].ContentHash != "retire-2" {
		t.Errorf("retracted hashes: got %q,%q want retire-1,retire-2",
			deleted[0].ContentHash, deleted[1].ContentHash)
	}
	// Owner-scoping: every delete must carry the authed owner's id, so a foreign
	// machine's annotation can never be retracted by another owner.
	for i, d := range deleted {
		if !d.OwnerID.Valid {
			t.Errorf("delete[%d] must be owner-scoped (valid owner id)", i)
		}
	}
}

func TestUploadAnnotations_RetractionIsIdempotent(t *testing.T) {
	// A delete affecting zero rows (already-absent hash) returns nil and must not
	// produce an error in the response.
	q := &mockQuerier{
		deleteAnnotationByContentHash: func(_ context.Context, _ sqlc.DeleteAnnotationByContentHashParams) error {
			return nil
		},
	}
	h := newTestHandler(q, nil)

	w := postAnnotationPush(t, h, schema.AnnotationPushRequest{
		Annotations: []schema.AnnotationPushItem{},
		Retractions: []string{"never-existed"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	var resp schema.AnnotationPushResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Errors != 0 {
		t.Errorf("idempotent retraction must not report errors: got %d", resp.Errors)
	}
}

func TestUploadAnnotations_RetractionDBErrorReported(t *testing.T) {
	q := &mockQuerier{
		deleteAnnotationByContentHash: func(_ context.Context, _ sqlc.DeleteAnnotationByContentHashParams) error {
			return errors.New("db: deadlock")
		},
	}
	h := newTestHandler(q, nil)

	w := postAnnotationPush(t, h, schema.AnnotationPushRequest{
		Annotations: []schema.AnnotationPushItem{},
		Retractions: []string{"boom"},
	})
	// The push as a whole still returns 200 (additive, best-effort), but the
	// failed retraction is surfaced via the existing Errors/Results fields.
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
	var resp schema.AnnotationPushResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Errors != 1 {
		t.Errorf("retraction DB error must be reported: got Errors=%d, want 1", resp.Errors)
	}
}

// A push with no retractions must not touch the delete path at all (backwards
// compatible: the additive field is absent on legacy clients). The mock panics
// if DeleteAnnotationByContentHash is called without a stub, so reaching 200
// proves the path was skipped.
func TestUploadAnnotations_NoRetractionsSkipsDeletePath(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)

	w := postAnnotationPush(t, h, schema.AnnotationPushRequest{
		Annotations: []schema.AnnotationPushItem{},
		Retractions: nil,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
}
