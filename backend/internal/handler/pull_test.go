package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"

	"github.com/peasant-labs/schema"
)

// ----------------------------------------------------------------------------
// Pull-surface test fixtures.
//
// These tests exercise the PRODUCTION code path: the real Handler methods
// (ListPullableTranscripts / GetPullTranscript / GetPullTranscriptContent /
// GetPullTranscriptAnnotations), the real canPullTranscript policy fn, and —
// for the 401 cases — the real AuthRequired middleware. Only the DB (Querier)
// and TranscriptBlobStore dependencies are mocked, never the subject.
// ----------------------------------------------------------------------------

// fixedTranscriptBlobStore returns deterministic authenticated plaintext for
// content and ETag tests.
type fixedTranscriptBlobStore struct {
	body        string
	downloadErr error
}

func (s *fixedTranscriptBlobStore) Write(context.Context, uuid.UUID, []byte) (storage.BlobDescriptor, storage.ContentIdentity, error) {
	return storage.BlobDescriptor{}, storage.ContentIdentity{}, errors.New("fixed transcript blob write not configured")
}
func (s *fixedTranscriptBlobStore) Read(_ context.Context, _ uuid.UUID, _ storage.BlobDescriptor, loaded storage.LoadedContentIdentity) ([]byte, storage.ContentIdentity, error) {
	if s.downloadErr != nil {
		return nil, storage.ContentIdentity{}, s.downloadErr
	}
	content := []byte(s.body)
	if known, ok := loaded.Known(); ok {
		return content, known, nil
	}
	identity, err := storage.NewContentIdentity(schema.ComputeTranscriptHash(content), int64(len(content)))
	return content, identity, err
}
func (*fixedTranscriptBlobStore) Rewrap(context.Context, uuid.UUID, storage.BlobDescriptor) (storage.BlobDescriptor, error) {
	return storage.BlobDescriptor{}, errors.New("fixed transcript blob rewrap not configured")
}
func (*fixedTranscriptBlobStore) Delete(context.Context, storage.BlobDescriptor) error { return nil }

// pullTestTranscript returns a minimal sqlc.Transcript owned by ownerID with the
// given visibility, suitable for the pull-surface tests.
func pullTestTranscript(id, ownerID uuid.UUID, visibility string) sqlc.Transcript {
	return sqlc.Transcript{
		ID:                  pgUUIDFrom(id),
		OwnerID:             pgUUIDFrom(ownerID),
		LocalID:             "local-" + id.String()[:8],
		Title:               pgText("Test Transcript"),
		Visibility:          visibility,
		ModelProvider:       string(schema.HarnessClaudeCode),
		BlobKey:             "transcripts/10000000-0000-4000-8000-000000000001.bin",
		SchemaVersion:       "0.1.0",
		BlobSizeBytes:       pgtype.Int8{Int64: 1, Valid: true},
		WrappedDataKey:      []byte("test-wrapped-key"),
		EncryptionAlgorithm: string(storage.EncryptionAES256GCMRandomNonceV1),
		KeyVersion:          1,
	}
}

// withUserID injects an authenticated AuthUser with a specific ID (the route
// handlers call GetUser; canPullTranscript compares against the owner ID).
func withUserID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, UserContextKey, &AuthUser{ID: id, Username: "puller"})
}

// ----------------------------------------------------------------------------
// 401 per route — through the REAL AuthRequired middleware (no credentials).
// ----------------------------------------------------------------------------

func TestPull_Unauthenticated_401_AllRoutes(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)

	routes := []struct {
		name    string
		method  string
		target  string
		handler http.HandlerFunc
	}{
		{"list", http.MethodGet, "/api/v1/pull/transcripts", h.ListPullableTranscripts},
		{"meta", http.MethodGet, "/api/v1/pull/transcripts/" + uuid.NewString(), h.GetPullTranscript},
		{"content", http.MethodGet, "/api/v1/pull/transcripts/" + uuid.NewString() + "/content", h.GetPullTranscriptContent},
		{"annotations", http.MethodGet, "/api/v1/pull/transcripts/" + uuid.NewString() + "/annotations", h.GetPullTranscriptAnnotations},
	}

	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			// Wrap with the production AuthRequired middleware; no Authorization
			// header => authenticate returns nil => 401.
			guarded := h.AuthRequired(rt.handler)
			r := httptest.NewRequest(rt.method, rt.target, nil)
			w := httptest.NewRecorder()
			guarded.ServeHTTP(w, r)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status: got %d, want %d (body: %s)", w.Code, http.StatusUnauthorized, w.Body.String())
			}
			if got := decodeError(t, w.Body.Bytes()); got != "Authentication required" {
				t.Errorf("error: got %q, want %q", got, "Authentication required")
			}
		})
	}
}

// ----------------------------------------------------------------------------
// 404 variants — existence never leaks (404, never 403).
// ----------------------------------------------------------------------------

func TestPull_NotFound_Variants(t *testing.T) {
	puller := uuid.New()
	otherOwner := uuid.New()
	tid := uuid.New()

	cases := []struct {
		name string
		q    *mockQuerier
	}{
		{
			name: "transcript does not exist",
			q: &mockQuerier{
				getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
					return sqlc.Transcript{}, errors.New("no rows")
				},
			},
		},
		{
			name: "public but unshared (public is NOT pullable — divergence)",
			q: &mockQuerier{
				getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
					return pullTestTranscript(tid, otherOwner, "public"), nil
				},
			},
		},
		{
			name: "shared but requester not a member of any approved share group",
			q: &mockQuerier{
				getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
					return pullTestTranscript(tid, otherOwner, dbVisibilityShared), nil
				},
				listApprovedTranscriptShareGroups: func(_ context.Context, _ pgtype.UUID) ([]pgtype.UUID, error) {
					return []pgtype.UUID{pgUUIDFrom(uuid.New())}, nil
				},
				getGroupMember: func(_ context.Context, _ sqlc.GetGroupMemberParams) (sqlc.GroupMember, error) {
					return sqlc.GroupMember{}, errors.New("not a member")
				},
			},
		},
		{
			// The requester is a member of a group the transcript is shared with,
			// but the share is
			// PENDING. The approved-only filter (ListApprovedTranscriptShareGroups,
			// WHERE ts.status='approved') therefore returns NO group, so
			// canPullTranscript never reaches the membership check ⇒ 404. This is
			// the exact divergence-table row "group-shared, acceptance
			// pending/rejected => deny" — the one the approved-only filter exists
			// to enforce. A regression dropping status='approved' would re-admit
			// this pending share and this case would FAIL.
			name: "shared but the share to the requester's group is PENDING (approved-only ⇒ deny)",
			q: &mockQuerier{
				getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
					return pullTestTranscript(tid, otherOwner, dbVisibilityShared), nil
				},
				// Approved-only filter yields NO group (the only share is pending).
				listApprovedTranscriptShareGroups: func(_ context.Context, _ pgtype.UUID) ([]pgtype.UUID, error) {
					return nil, nil
				},
				// If a regression reached this (it must NOT), the requester WOULD be
				// a member — proving the deny comes from the status filter, not from
				// non-membership.
				getGroupMember: func(_ context.Context, arg sqlc.GetGroupMemberParams) (sqlc.GroupMember, error) {
					return sqlc.GroupMember{GroupID: arg.GroupID, UserID: arg.UserID}, nil
				},
			},
		},
		{
			name: "collective-owner preview is NOT pullable (divergence): private, not owned, not shared",
			q: &mockQuerier{
				getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
					return pullTestTranscript(tid, otherOwner, "private"), nil
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler(tc.q, nil)
			r := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts/"+tid.String(), nil)
			r = r.WithContext(withUserID(r.Context(), puller))
			r = withChiURLParam(r, "id", tid.String())
			w := httptest.NewRecorder()

			h.GetPullTranscript(w, r)

			if w.Code != http.StatusNotFound {
				t.Fatalf("status: got %d, want 404 (body: %s)", w.Code, w.Body.String())
			}
		})
	}
}

// TestPull_InvalidID_400 verifies a malformed UUID yields 400, not a panic.
func TestPull_InvalidID_400(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts/not-a-uuid", nil)
	r = r.WithContext(withUserID(r.Context(), uuid.New()))
	r = withChiURLParam(r, "id", "not-a-uuid")
	w := httptest.NewRecorder()

	h.GetPullTranscript(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
}

// ----------------------------------------------------------------------------
// Owner can pull own transcript; metadata + 'shared'->group mapping.
// ----------------------------------------------------------------------------

func TestPull_GetMeta_Owner_SharedMapsToGroup(t *testing.T) {
	owner := uuid.New()
	tid := uuid.New()

	// Populate every field PullTranscriptInfo carries so the wire mapping is
	// fully asserted: Harness, ProjectName, timestamps
	// (ms), ContractVersion, AnnotationCount — not just Visibility/Owner/Hash.
	publishedAt := time.UnixMilli(1_700_000_000_000)
	updatedAt := time.UnixMilli(1_700_000_500_000)
	tr := pullTestTranscript(tid, owner, dbVisibilityShared)
	tr.ModelProvider = string(schema.HarnessClaudeCode)
	tr.ProjectName = pgText("acme/widget")
	tr.SchemaVersion = "0.1.0"
	tr.ContentHash = pgText("deadbeef")
	tr.PublishedAt = pgtype.Timestamptz{Time: publishedAt, Valid: true}
	tr.UpdatedAt = pgtype.Timestamptz{Time: updatedAt, Valid: true}

	q := &mockQuerier{
		getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
			return tr, nil
		},
		getUserByID: func(_ context.Context, _ pgtype.UUID) (sqlc.User, error) {
			return sqlc.User{ID: pgUUIDFrom(owner), GithubUsername: "owner-handle"}, nil
		},
		// Non-zero so the count query's wiring is actually verified (a 0 default
		// would not prove CountTranscriptAnnotations is consulted).
		countTranscriptAnnotations: func(_ context.Context, _ pgtype.UUID) (int64, error) {
			return 7, nil
		},
	}
	h := newTestHandler(q, nil)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts/"+tid.String(), nil)
	r = r.WithContext(withUserID(r.Context(), owner))
	r = withChiURLParam(r, "id", tid.String())
	w := httptest.NewRecorder()

	h.GetPullTranscript(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var info schema.PullTranscriptInfo
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// DB 'shared' must map to the wire VisibilityGroup — never the raw
	// out-of-enum DB string.
	if info.Visibility != schema.VisibilityGroup {
		t.Errorf("visibility: got %q, want %q (DB 'shared' must map to group)", info.Visibility, schema.VisibilityGroup)
	}
	if info.OwnerUsername != "owner-handle" {
		t.Errorf("ownerUsername: got %q, want %q", info.OwnerUsername, "owner-handle")
	}
	if info.ContentHash != "deadbeef" {
		t.Errorf("contentHash: got %q, want %q", info.ContentHash, "deadbeef")
	}
	// Remaining field-mapping assertions.
	if info.Harness != schema.HarnessClaudeCode {
		t.Errorf("harness: got %q, want %q", info.Harness, schema.HarnessClaudeCode)
	}
	if info.ProjectName != "acme/widget" {
		t.Errorf("projectName: got %q, want %q", info.ProjectName, "acme/widget")
	}
	if info.ContractVersion != schema.ContractVersion("0.1.0") {
		t.Errorf("contractVersion: got %q, want %q", info.ContractVersion, "0.1.0")
	}
	if info.AnnotationCount != 7 {
		t.Errorf("annotationCount: got %d, want 7 (CountTranscriptAnnotations wiring unverified)", info.AnnotationCount)
	}
	if info.PublishedAt != publishedAt.UnixMilli() {
		t.Errorf("publishedAt: got %d, want %d (UnixMilli)", info.PublishedAt, publishedAt.UnixMilli())
	}
	if info.UpdatedAt != updatedAt.UnixMilli() {
		t.Errorf("updatedAt: got %d, want %d (UnixMilli)", info.UpdatedAt, updatedAt.UnixMilli())
	}
	if info.LocalID != tr.LocalID {
		t.Errorf("localId: got %q, want %q", info.LocalID, tr.LocalID)
	}
}

// TestPull_GetMeta_SharedMember_CanPull asserts the positive shared branch of
// canPullTranscript: a non-owner who IS a member of a group with an approved
// share can pull (mirrors canViewTranscript's shared branch, minus public/
// preview).
func TestPull_GetMeta_SharedMember_CanPull(t *testing.T) {
	owner := uuid.New()
	puller := uuid.New() // NOT the owner
	tid := uuid.New()
	shareGroup := uuid.New()

	q := &mockQuerier{
		getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
			return pullTestTranscript(tid, owner, dbVisibilityShared), nil
		},
		listApprovedTranscriptShareGroups: func(_ context.Context, _ pgtype.UUID) ([]pgtype.UUID, error) {
			return []pgtype.UUID{pgUUIDFrom(shareGroup)}, nil
		},
		getGroupMember: func(_ context.Context, arg sqlc.GetGroupMemberParams) (sqlc.GroupMember, error) {
			if arg.GroupID == pgUUIDFrom(shareGroup) && arg.UserID == pgUUIDFrom(puller) {
				return sqlc.GroupMember{GroupID: arg.GroupID, UserID: arg.UserID}, nil
			}
			return sqlc.GroupMember{}, errors.New("not a member")
		},
		getUserByID: func(_ context.Context, _ pgtype.UUID) (sqlc.User, error) {
			return sqlc.User{GithubUsername: "owner-handle"}, nil
		},
	}
	h := newTestHandler(q, nil)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts/"+tid.String(), nil)
	r = r.WithContext(withUserID(r.Context(), puller))
	r = withChiURLParam(r, "id", tid.String())
	w := httptest.NewRecorder()

	h.GetPullTranscript(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (shared member must pull) (body: %s)", w.Code, w.Body.String())
	}
}

// ----------------------------------------------------------------------------
// Listing — own + approved group-shared (public/preview excluded by the query).
// ----------------------------------------------------------------------------

func TestPull_List_OwnerScoped(t *testing.T) {
	owner := uuid.New()
	t1, t2 := uuid.New(), uuid.New()

	q := &mockQuerier{
		listPullableTranscripts: func(_ context.Context, arg sqlc.ListPullableTranscriptsParams) ([]sqlc.ListPullableTranscriptsRow, error) {
			if arg.UserID != pgUUIDFrom(owner) {
				t.Errorf("list scoped to wrong user")
			}
			return []sqlc.ListPullableTranscriptsRow{
				{ID: pgUUIDFrom(t1), OwnerID: pgUUIDFrom(owner), OwnerUsername: "owner-handle", Visibility: "private"},
				{ID: pgUUIDFrom(t2), OwnerID: pgUUIDFrom(owner), OwnerUsername: "owner-handle", Visibility: "private"},
			}, nil
		},
		countPullableTranscripts: func(_ context.Context, _ pgtype.UUID) (int64, error) {
			return 2, nil
		},
	}
	h := newTestHandler(q, nil)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts?page=1&limit=10", nil)
	r = r.WithContext(withUserID(r.Context(), owner))
	w := httptest.NewRecorder()

	h.ListPullableTranscripts(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var resp schema.PullListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Transcripts) != 2 {
		t.Errorf("transcripts: got %d, want 2", len(resp.Transcripts))
	}
	if resp.Total != 2 || resp.Page != 1 || resp.Limit != 10 {
		t.Errorf("pagination: got total=%d page=%d limit=%d, want 2/1/10", resp.Total, resp.Page, resp.Limit)
	}
}

// TestPull_List_Pagination_OffsetAndCap asserts the
// offset arithmetic (page=2 ⇒ offset=limit) and the page-size cap (limit>200 ⇒
// 200) by capturing the params the handler passes to the batched list query.
func TestPull_List_Pagination_OffsetAndCap(t *testing.T) {
	owner := uuid.New()

	cases := []struct {
		name       string
		query      string
		wantLimit  int32
		wantOffset int32
	}{
		{"page 2 limit 10 => offset 10", "page=2&limit=10", 10, 10},
		{"page 3 limit 25 => offset 50", "page=3&limit=25", 25, 50},
		{"limit over cap clamped to 200", "page=1&limit=500", 200, 0},
		{"page 2 over-cap limit => offset uses clamped 200", "page=2&limit=500", 200, 200},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotLimit, gotOffset int32
			q := &mockQuerier{
				listPullableTranscripts: func(_ context.Context, arg sqlc.ListPullableTranscriptsParams) ([]sqlc.ListPullableTranscriptsRow, error) {
					gotLimit, gotOffset = arg.Limit, arg.Offset
					return nil, nil
				},
				countPullableTranscripts: func(_ context.Context, _ pgtype.UUID) (int64, error) {
					return 0, nil
				},
			}
			h := newTestHandler(q, nil)

			r := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts?"+tc.query, nil)
			r = r.WithContext(withUserID(r.Context(), owner))
			w := httptest.NewRecorder()

			h.ListPullableTranscripts(w, r)

			if w.Code != http.StatusOK {
				t.Fatalf("status: got %d, want 200 (body: %s)", w.Code, w.Body.String())
			}
			if gotLimit != tc.wantLimit {
				t.Errorf("Limit: got %d, want %d", gotLimit, tc.wantLimit)
			}
			if gotOffset != tc.wantOffset {
				t.Errorf("Offset: got %d, want %d", gotOffset, tc.wantOffset)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// Content — ETag from content_hash; If-None-Match => 304; NULL hash => no ETag.
// ----------------------------------------------------------------------------

func TestPull_Content_ETag_And_304(t *testing.T) {
	owner := uuid.New()
	tid := uuid.New()
	const hash = "abc123abc123abc123abc123abc123abc123abc123abc123abc123abc123abcd"
	const blob = `{"contractVersion":"0.1.0","kind":"turns"}`

	newHandler := func() *Handler {
		q := &mockQuerier{
			getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
				row := pullTestTranscript(tid, owner, "private")
				row.ContentHash = pgText(hash)
				return row, nil
			},
		}
		return newTestHandler(q, &fixedTranscriptBlobStore{body: blob})
	}

	t.Run("200 sets ETag and streams body", func(t *testing.T) {
		h := newHandler()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts/"+tid.String()+"/content", nil)
		r = r.WithContext(withUserID(r.Context(), owner))
		r = withChiURLParam(r, "id", tid.String())
		w := httptest.NewRecorder()

		h.GetPullTranscriptContent(w, r)

		if w.Code != http.StatusOK {
			t.Fatalf("status: got %d, want 200 (body: %s)", w.Code, w.Body.String())
		}
		wantETag := `"` + hash + `"`
		if got := w.Header().Get("ETag"); got != wantETag {
			t.Errorf("ETag: got %q, want %q", got, wantETag)
		}
		if w.Body.String() != blob {
			t.Errorf("body: got %q, want %q", w.Body.String(), blob)
		}
	})

	// The server is tolerant on If-None-Match. It must return 304
	// for the quoted ETag (verbatim echo), the RAW served-blob hash (the
	// documented client key), AND a weak (W/) validator prefix — all designate the
	// same served-blob hash.
	matchForms := []struct {
		name        string
		ifNoneMatch string
	}{
		{"quoted ETag (verbatim echo)", `"` + hash + `"`},
		{"raw served-blob hash (documented key)", hash},
		{"weak validator prefix", `W/"` + hash + `"`},
	}
	for _, mf := range matchForms {
		t.Run("If-None-Match "+mf.name+" => 304 no body", func(t *testing.T) {
			h := newHandler()
			r := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts/"+tid.String()+"/content", nil)
			r.Header.Set("If-None-Match", mf.ifNoneMatch)
			r = r.WithContext(withUserID(r.Context(), owner))
			r = withChiURLParam(r, "id", tid.String())
			w := httptest.NewRecorder()

			h.GetPullTranscriptContent(w, r)

			if w.Code != http.StatusNotModified {
				t.Fatalf("status: got %d, want 304 (body: %s)", w.Code, w.Body.String())
			}
			if w.Body.Len() != 0 {
				t.Errorf("304 must have no body, got %q", w.Body.String())
			}
		})
	}

	t.Run("If-None-Match for a DIFFERENT hash => 200 full body", func(t *testing.T) {
		h := newHandler()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts/"+tid.String()+"/content", nil)
		r.Header.Set("If-None-Match", `"some-other-hash"`)
		r = r.WithContext(withUserID(r.Context(), owner))
		r = withChiURLParam(r, "id", tid.String())
		w := httptest.NewRecorder()

		h.GetPullTranscriptContent(w, r)

		if w.Code != http.StatusOK {
			t.Fatalf("status: got %d, want 200 (non-matching INM must NOT 304) (body: %s)", w.Code, w.Body.String())
		}
		if w.Body.String() != blob {
			t.Errorf("body: got %q, want %q", w.Body.String(), blob)
		}
	})
}

func TestPull_Content_NullHash_NoETag_FullBody(t *testing.T) {
	owner := uuid.New()
	tid := uuid.New()
	const blob = `legacy raw jsonl`

	q := &mockQuerier{
		getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
			row := pullTestTranscript(tid, owner, "private")
			row.BlobSizeBytes = pgtype.Int8{}
			return row, nil
		},
	}
	h := newTestHandler(q, &fixedTranscriptBlobStore{body: blob})

	r := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts/"+tid.String()+"/content", nil)
	// Even with a stale If-None-Match, a NULL hash must NOT 304.
	r.Header.Set("If-None-Match", `"whatever"`)
	r = r.WithContext(withUserID(r.Context(), owner))
	r = withChiURLParam(r, "id", tid.String())
	w := httptest.NewRecorder()

	h.GetPullTranscriptContent(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (NULL hash => unconditional GET) (body: %s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("ETag"); got != "" {
		t.Errorf("ETag must be absent for NULL hash, got %q", got)
	}
	if w.Body.String() != blob {
		t.Errorf("body: got %q, want %q", w.Body.String(), blob)
	}
}

// ----------------------------------------------------------------------------
// Annotations — bare array, author identity via users join.
// ----------------------------------------------------------------------------

func TestPull_Annotations_BareArray_AuthorFields(t *testing.T) {
	owner := uuid.New()
	tid := uuid.New()
	authorA := uuid.New()
	authorB := uuid.New()

	q := &mockQuerier{
		getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
			return pullTestTranscript(tid, owner, "private"), nil
		},
		listAnnotationsByTranscriptID: func(_ context.Context, _ pgtype.UUID) ([]sqlc.Annotation, error) {
			return []sqlc.Annotation{
				{ID: pgUUIDFrom(uuid.New()), OwnerID: pgUUIDFrom(authorA), TargetKind: string(schema.TargetSession), TypeID: "quality.session_outcome", Value: "success"},
				{ID: pgUUIDFrom(uuid.New()), OwnerID: pgUUIDFrom(authorB), TargetKind: string(schema.TargetSession), TypeID: "quality.session_outcome", Value: "fail"},
				{ID: pgUUIDFrom(uuid.New()), OwnerID: pgUUIDFrom(authorA), TargetKind: string(schema.TargetSession), TypeID: "quality.user_frustration", Value: "high"},
			}, nil
		},
		getUserByID: func(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
			switch uuid.UUID(id.Bytes) {
			case authorA:
				return sqlc.User{ID: id, GithubUsername: "author-a"}, nil
			case authorB:
				return sqlc.User{ID: id, GithubUsername: "author-b"}, nil
			default:
				return sqlc.User{}, errors.New("unknown user")
			}
		},
	}
	h := newTestHandler(q, nil)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts/"+tid.String()+"/annotations", nil)
	r = r.WithContext(withUserID(r.Context(), owner))
	r = withChiURLParam(r, "id", tid.String())
	w := httptest.NewRecorder()

	h.GetPullTranscriptAnnotations(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	// The generated API contract requires a bare JSON array, not an
	// {"annotations":[...]} object wrapper.
	trimmed := strings.TrimSpace(w.Body.String())
	if !strings.HasPrefix(trimmed, "[") {
		t.Fatalf("response must be a bare JSON array, got: %s", trimmed)
	}

	var anns []schema.PullAnnotation
	if err := json.Unmarshal(w.Body.Bytes(), &anns); err != nil {
		t.Fatalf("decode bare array: %v", err)
	}
	if len(anns) != 3 {
		t.Fatalf("annotations: got %d, want 3", len(anns))
	}
	// Author identity resolved via the users join (per owner_id).
	wantAuthor := map[string]string{
		authorA.String(): "author-a",
		authorB.String(): "author-b",
	}
	for i, a := range anns {
		if a.AuthorUsername != wantAuthor[a.AuthorUserID] {
			t.Errorf("ann[%d]: authorUserId=%s got username %q, want %q", i, a.AuthorUserID, a.AuthorUsername, wantAuthor[a.AuthorUserID])
		}
		if a.AuthorUserID == "" {
			t.Errorf("ann[%d]: authorUserId empty", i)
		}
	}
}

// TestPull_Annotations_Empty_SerializesBareArray guards the generated contract
// against the classic nil-slice⇒null regression: a transcript
// with ZERO annotations must serialize as the bare array "[]", NOT "null". The
// existing bare-array test always has items, so its HasPrefix("[") check would
// not catch a refactor to `var annotations []schema.PullAnnotation` (which emits
// null on an empty slice).
func TestPull_Annotations_Empty_SerializesBareArray(t *testing.T) {
	owner := uuid.New()
	tid := uuid.New()

	q := &mockQuerier{
		getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
			return pullTestTranscript(tid, owner, "private"), nil
		},
		listAnnotationsByTranscriptID: func(_ context.Context, _ pgtype.UUID) ([]sqlc.Annotation, error) {
			return nil, nil // zero annotations
		},
	}
	h := newTestHandler(q, nil)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts/"+tid.String()+"/annotations", nil)
	r = r.WithContext(withUserID(r.Context(), owner))
	r = withChiURLParam(r, "id", tid.String())
	w := httptest.NewRecorder()

	h.GetPullTranscriptAnnotations(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Errorf("empty annotations must serialize as the bare array %q, got %q (nil-slice⇒null regression)", "[]", got)
	}
}

// ----------------------------------------------------------------------------
// Schema-version window advertisement — pull window present.
// ----------------------------------------------------------------------------

func TestPull_SchemaVersion_AdvertisesPullWindow(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/schema/version", nil)
	w := httptest.NewRecorder()

	h.GetSchemaVersion(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	var resp schema.SchemaVersionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.PullContractVersion != "0.1.0" {
		t.Errorf("pullContractVersion: got %q, want %q", resp.PullContractVersion, "0.1.0")
	}
	if resp.MinPullContractVersion != "0.1.0" {
		t.Errorf("minPullContractVersion: got %q, want %q", resp.MinPullContractVersion, "0.1.0")
	}
}
