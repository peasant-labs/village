package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

const testLocalID = "550e8400-e29b-41d4-a716-446655440000"

// withChiURLParam injects a chi route param so handlers calling
// chi.URLParam(r, key) resolve it without going through the full router.
func withChiURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// idString returns the string form of a transcript's UUID for URL building.
func idString(t sqlc.Transcript) string {
	return uuid.UUID(t.ID.Bytes).String()
}

// publicTranscript returns a public transcript whose local_id is the peasant
// session id used to link annotations.
func publicTranscript() sqlc.Transcript {
	return sqlc.Transcript{
		ID:         pgUUIDFrom(uuid.New()),
		OwnerID:    pgUUIDFrom(uuid.New()),
		LocalID:    testLocalID,
		Visibility: "public",
	}
}

// privateTranscript returns a private transcript owned by a random user.
func privateTranscript() sqlc.Transcript {
	return sqlc.Transcript{
		ID:         pgUUIDFrom(uuid.New()),
		OwnerID:    pgUUIDFrom(uuid.New()),
		LocalID:    testLocalID,
		Visibility: "private",
	}
}

func pgInt4(v int32) pgtype.Int4 {
	return pgtype.Int4{Int32: v, Valid: true}
}

func nowTs() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.UnixMilli(1700000000000), Valid: true}
}

// ----------------------------------------------------------------------------
// GET /transcripts/{id}/annotations
// ----------------------------------------------------------------------------

func TestListTranscriptAnnotations_MapsSessionAndEntry(t *testing.T) {
	transcript := publicTranscript()

	sessionRow := sqlc.Annotation{
		ID:            pgUUIDFrom(uuid.New()),
		ContentHash:   "hash-session",
		TargetKind:    string(schema.TargetSession),
		SessionID:     pgText(testLocalID),
		TypeID:        "quality.outcome",
		Value:         "success",
		IsPrimary:     true,
		AnnotatorName: pgText("system"),
		Provenance:    []byte(`{"method":"llm_judge"}`),
		CreatedAt:     nowTs(),
		UpdatedAt:     nowTs(),
	}
	entryRow := sqlc.Annotation{
		ID:             pgUUIDFrom(uuid.New()),
		ContentHash:    "hash-entry",
		TargetKind:     string(schema.TargetEntry),
		EntrySessionID: pgText(testLocalID),
		EntryIndex:     pgInt4(3),
		EntryEndIndex:  pgInt4(4),
		TypeID:         "turn.helpful",
		Value:          "yes",
		AnnotatorName:  pgText("alice"),
		AnnotatorKind:  pgText(string(schema.AnnotatorHuman)),
		CreatedAt:      nowTs(),
		UpdatedAt:      nowTs(),
	}

	var gotTranscriptID pgtype.UUID
	q := &mockQuerier{
		getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
			return transcript, nil
		},
		listAnnotationsByTranscriptID: func(_ context.Context, transcriptID pgtype.UUID) ([]sqlc.Annotation, error) {
			gotTranscriptID = transcriptID
			return []sqlc.Annotation{sessionRow, entryRow}, nil
		},
	}
	h := newTestHandler(q, nil)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+idString(transcript)+"/annotations", nil)
	r = withChiURLParam(r, "id", idString(transcript))
	w := httptest.NewRecorder()

	h.ListTranscriptAnnotations(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}
	if gotTranscriptID != transcript.ID {
		t.Errorf("query transcript id: got %v, want %v", gotTranscriptID, transcript.ID)
	}

	var resp struct {
		Annotations []schema.AnnotationSummary `json:"annotations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Annotations) != 2 {
		t.Fatalf("annotation count: got %d, want 2", len(resp.Annotations))
	}

	got := resp.Annotations[0]
	if got.TargetKind != schema.TargetSession {
		t.Errorf("session targetKind: got %q", got.TargetKind)
	}
	if got.TargetSessionID == nil || *got.TargetSessionID != testLocalID {
		t.Errorf("session targetSessionId: got %v", got.TargetSessionID)
	}
	// llm_judge provenance must resolve to agent kind.
	if got.AnnotatorKind != schema.AnnotatorAgent {
		t.Errorf("session annotatorKind: got %q, want agent", got.AnnotatorKind)
	}
	if got.TypeID != "quality.outcome" || got.Value != "success" {
		t.Errorf("session typeId/value: got %q/%q", got.TypeID, got.Value)
	}

	entry := resp.Annotations[1]
	if entry.TargetKind != schema.TargetEntry {
		t.Errorf("entry targetKind: got %q", entry.TargetKind)
	}
	if entry.TargetEntryIndex == nil || *entry.TargetEntryIndex != 3 {
		t.Errorf("entry index: got %v, want 3", entry.TargetEntryIndex)
	}
	if entry.TargetEntryEndIndex == nil || *entry.TargetEntryEndIndex != 4 {
		t.Errorf("entry end index: got %v, want 4", entry.TargetEntryEndIndex)
	}
	// stored annotator_kind=human must win.
	if entry.AnnotatorKind != schema.AnnotatorHuman {
		t.Errorf("entry annotatorKind: got %q, want human", entry.AnnotatorKind)
	}
}

func TestListTranscriptAnnotations_EmptyWhenNone(t *testing.T) {
	transcript := publicTranscript()
	q := &mockQuerier{
		getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
			return transcript, nil
		},
		listAnnotationsByTranscriptID: func(_ context.Context, _ pgtype.UUID) ([]sqlc.Annotation, error) {
			return []sqlc.Annotation{}, nil
		},
	}
	h := newTestHandler(q, nil)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+idString(transcript)+"/annotations", nil)
	r = withChiURLParam(r, "id", idString(transcript))
	w := httptest.NewRecorder()

	h.ListTranscriptAnnotations(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
	// Must serialize as [] (not null) so the frontend can iterate safely.
	if !bytes.Contains(w.Body.Bytes(), []byte(`"annotations":[]`)) {
		t.Errorf("expected empty array, got %s", w.Body.String())
	}
}

func TestListTranscriptAnnotations_PrivateHidden(t *testing.T) {
	transcript := privateTranscript()
	q := &mockQuerier{
		getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
			return transcript, nil
		},
	}
	h := newTestHandler(q, nil)

	// Anonymous caller (AuthOptional → no user in context).
	r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+idString(transcript)+"/annotations", nil)
	r = withChiURLParam(r, "id", idString(transcript))
	w := httptest.NewRecorder()

	h.ListTranscriptAnnotations(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d (visibility leak)", w.Code, http.StatusNotFound)
	}
}

func TestListTranscriptAnnotations_BadID(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/not-a-uuid/annotations", nil)
	r = withChiURLParam(r, "id", "not-a-uuid")
	w := httptest.NewRecorder()

	h.ListTranscriptAnnotations(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// ----------------------------------------------------------------------------
// POST /transcripts/{id}/annotations
// ----------------------------------------------------------------------------

func postAnnotation(t *testing.T, h *Handler, transcriptID string, body any, authed bool) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/"+transcriptID+"/annotations", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r = withChiURLParam(r, "id", transcriptID)
	if authed {
		r = r.WithContext(withTestUser(r.Context()))
	}
	w := httptest.NewRecorder()
	h.CreateTranscriptAnnotation(w, r)
	return w
}

func TestCreateTranscriptAnnotation_Success(t *testing.T) {
	transcript := publicTranscript()

	var captured sqlc.CreateManualAnnotationParams
	q := &mockQuerier{
		getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
			return transcript, nil
		},
		createManualAnnotation: func(_ context.Context, arg sqlc.CreateManualAnnotationParams) (sqlc.Annotation, error) {
			captured = arg
			return sqlc.Annotation{
				ID:             pgUUIDFrom(uuid.New()),
				ContentHash:    arg.ContentHash,
				TargetKind:     string(schema.TargetEntry),
				EntrySessionID: arg.EntrySessionID,
				EntryIndex:     arg.EntryIndex,
				EntryEndIndex:  arg.EntryEndIndex,
				TypeID:         arg.TypeID,
				Value:          arg.Value,
				IsPrimary:      arg.IsPrimary,
				AnnotatorName:  arg.AnnotatorName,
				AnnotatorKind:  pgText(string(schema.AnnotatorHuman)),
				CreatedAt:      nowTs(),
				UpdatedAt:      nowTs(),
			}, nil
		},
	}
	h := newTestHandler(q, nil)

	idx := 5
	w := postAnnotation(t, h, idString(transcript), createManualAnnotationRequest{
		TypeID:     "turn.helpful",
		Value:      "yes",
		EntryIndex: &idx,
	}, true)

	if w.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want %d (body: %s)", w.Code, http.StatusCreated, w.Body.String())
	}

	// The entry session id remains the transcript's wire-local target, while the
	// exact UUID is retained for manual-label lookup across shared transcripts.
	if captured.EntrySessionID.String != testLocalID {
		t.Errorf("entry session id: got %q, want %q", captured.EntrySessionID.String, testLocalID)
	}
	if captured.TargetTranscriptID != transcript.ID {
		t.Errorf("manual target transcript id: got %v, want %v", captured.TargetTranscriptID, transcript.ID)
	}
	if captured.EntryIndex.Int32 != 5 {
		t.Errorf("entry index: got %d, want 5", captured.EntryIndex.Int32)
	}
	// EndIndex defaults to a single-entry half-open span [5, 6).
	if captured.EntryEndIndex.Int32 != 6 {
		t.Errorf("entry end index: got %d, want 6", captured.EntryEndIndex.Int32)
	}
	if captured.ContentHash == "" {
		t.Error("content hash must be set for dedup")
	}

	var got schema.AnnotationSummary
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.TargetKind != schema.TargetEntry {
		t.Errorf("targetKind: got %q, want entry", got.TargetKind)
	}
	if got.AnnotatorKind != schema.AnnotatorHuman {
		t.Errorf("annotatorKind: got %q, want human", got.AnnotatorKind)
	}
	if got.AnnotatorName != "testuser" {
		t.Errorf("annotatorName: got %q, want testuser", got.AnnotatorName)
	}
}

func TestCreateTranscriptAnnotation_Validation(t *testing.T) {
	transcript := publicTranscript()
	q := &mockQuerier{
		getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
			return transcript, nil
		},
	}
	h := newTestHandler(q, nil)

	idx := 2
	neg := -1
	bad := 2

	cases := []struct {
		name string
		body createManualAnnotationRequest
	}{
		{"missing typeId", createManualAnnotationRequest{Value: "yes", EntryIndex: &idx}},
		{"missing value", createManualAnnotationRequest{TypeID: "turn.helpful", EntryIndex: &idx}},
		{"missing entryIndex", createManualAnnotationRequest{TypeID: "turn.helpful", Value: "yes"}},
		{"negative entryIndex", createManualAnnotationRequest{TypeID: "turn.helpful", Value: "yes", EntryIndex: &neg}},
		{"endIndex not after start", createManualAnnotationRequest{TypeID: "turn.helpful", Value: "yes", EntryIndex: &idx, EndIndex: &bad}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postAnnotation(t, h, idString(transcript), tc.body, true)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status: got %d, want %d (body: %s)", w.Code, http.StatusBadRequest, w.Body.String())
			}
		})
	}
}

func TestCreateTranscriptAnnotation_PrivateHidden(t *testing.T) {
	transcript := privateTranscript()
	// canViewTranscript for a non-owner private transcript skips the shares
	// branch (visibility != "shared") and falls through to the collective-owner
	// query, which the mock returns empty for — so the view is denied.
	q := &mockQuerier{
		getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
			return transcript, nil
		},
	}
	h := newTestHandler(q, nil)

	idx := 1
	w := postAnnotation(t, h, idString(transcript), createManualAnnotationRequest{
		TypeID:     "turn.helpful",
		Value:      "yes",
		EntryIndex: &idx,
	}, true) // authed, but not the owner

	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d (visibility leak)", w.Code, http.StatusNotFound)
	}
}

func TestCreateTranscriptAnnotation_TranscriptNotFound(t *testing.T) {
	q := &mockQuerier{
		getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
			return sqlc.Transcript{}, errors.New("no rows")
		},
	}
	h := newTestHandler(q, nil)

	idx := 1
	w := postAnnotation(t, h, uuid.New().String(), createManualAnnotationRequest{
		TypeID:     "turn.helpful",
		Value:      "yes",
		EntryIndex: &idx,
	}, true)

	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusNotFound)
	}
}
