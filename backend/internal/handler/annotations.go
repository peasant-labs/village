package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// ListTranscriptAnnotations returns every annotation stored for a transcript.
//
// GET /api/v1/transcripts/{id}/annotations (AuthOptional)
//
// Pushed annotations are linked by their owner-scoped peasant session ids or an
// owner-scoped association ledger row. Village-created manual labels retain their
// entry wire target but persist the exact transcript UUID as their local locator.
// Visibility follows the same rules as the transcript itself — if the caller
// cannot view the transcript, they get 404 (not 403, to avoid leaking existence).
//
// The response is a flat array of schema.AnnotationSummary objects (both
// session-level, entry-level, and association), matching the shared viewer's
// expected shape.
func (h *Handler) ListTranscriptAnnotations(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid transcript ID")
		return
	}

	transcript, err := h.queries.GetTranscriptByID(r.Context(), toPgUUID(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}

	user := GetUser(r.Context())
	if !h.canViewTranscript(r.Context(), user, transcript) {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}

	rows, err := h.queries.ListAnnotationsByTranscriptID(r.Context(), transcript.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch annotations")
		return
	}

	annotations := make([]schema.AnnotationSummary, 0, len(rows))
	for _, row := range rows {
		annotations = append(annotations, annotationRowToSummary(row))
	}

	writeJSON(w, http.StatusOK, map[string]any{"annotations": annotations})
}

// createManualAnnotationRequest is the JSON body for POST .../annotations.
// It targets a single entry (turn / tool call) within the transcript's session.
type createManualAnnotationRequest struct {
	TypeID     string  `json:"typeId"`
	Value      string  `json:"value"`
	EntryIndex *int    `json:"entryIndex"`
	EndIndex   *int    `json:"endIndex,omitempty"`
	IsPrimary  bool    `json:"isPrimary,omitempty"`
	Reason     *string `json:"reason,omitempty"`
}

// CreateTranscriptAnnotation creates a per-turn (entry-level) annotation for
// manual labeling on the village.
//
// POST /api/v1/transcripts/{id}/annotations (AuthRequired)
//
// The annotation is village-only: it is persisted to the same annotations table
// the CLI push writes to, but is never propagated anywhere. annotator_kind is
// fixed to 'human'. The caller must be able to view the transcript.
func (h *Handler) CreateTranscriptAnnotation(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid transcript ID")
		return
	}

	transcript, err := h.queries.GetTranscriptByID(r.Context(), toPgUUID(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}

	if !h.canViewTranscript(r.Context(), user, transcript) {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}

	var req createManualAnnotationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.TypeID == "" {
		writeError(w, http.StatusBadRequest, "typeId is required")
		return
	}
	if req.Value == "" {
		writeError(w, http.StatusBadRequest, "value is required")
		return
	}
	if req.EntryIndex == nil || *req.EntryIndex < 0 {
		writeError(w, http.StatusBadRequest, "entryIndex is required and must be non-negative")
		return
	}

	// Default the half-open end to a single-entry span [entryIndex, entryIndex+1).
	endIndex := *req.EntryIndex + 1
	if req.EndIndex != nil {
		endIndex = *req.EndIndex
	}
	if endIndex <= *req.EntryIndex {
		writeError(w, http.StatusBadRequest, "endIndex must be greater than entryIndex")
		return
	}

	sessionID := transcript.LocalID

	// Village-only manual labels use a versioned local hash that includes the
	// exact transcript UUID. The published schema hash remains authoritative for
	// pushed annotations and intentionally is not changed here.
	contentHash := computeManualAnnotationHash(manualAnnotationHashInput{
		TargetTranscriptID: id,
		TypeID:             req.TypeID,
		AnnotatorName:      user.Username,
		Value:              req.Value,
		EntrySessionID:     sessionID,
		EntryIndex:         *req.EntryIndex,
		EntryEndIndex:      endIndex,
		IsPrimary:          req.IsPrimary,
		Reason:             req.Reason,
	})

	row, err := h.queries.CreateManualAnnotation(r.Context(), sqlc.CreateManualAnnotationParams{
		ContentHash:        contentHash,
		OwnerID:            user.PgID(),
		EntrySessionID:     toPgText(sessionID),
		EntryIndex:         toPgInt4FromInt(req.EntryIndex),
		EntryEndIndex:      toPgInt4FromInt(&endIndex),
		TargetTranscriptID: toPgUUID(id),
		TypeID:             req.TypeID,
		Value:              req.Value,
		IsPrimary:          req.IsPrimary,
		Reason:             toPgTextPtr(req.Reason),
		AnnotatorName:      toPgText(user.Username),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create annotation")
		return
	}

	writeJSON(w, http.StatusCreated, annotationRowToSummary(row))
}

// annotationRowToSummary maps a stored annotation row to the shared wire shape.
//
// AnnotatorKind precedence: the stored annotator_kind column (set for village
// manual labels) wins; otherwise it is inferred from provenance.method for
// pushed annotations; otherwise it defaults to human.
func annotationRowToSummary(row sqlc.Annotation) schema.AnnotationSummary {
	s := schema.AnnotationSummary{
		ID:            uuid.UUID(row.ID.Bytes).String(),
		TargetKind:    schema.TargetKind(row.TargetKind),
		IsPrimary:     row.IsPrimary,
		AnnotatorName: textOrEmpty(row.AnnotatorName),
		TypeID:        row.TypeID,
		TypeName:      row.TypeID, // display-name lookup is a frontend concern
		Value:         row.Value,
		CreatedAt:     row.CreatedAt.Time.UnixMilli(),
	}

	if row.ContentHash != "" {
		ch := row.ContentHash
		s.ContentHash = &ch
	}
	if row.SessionID.Valid {
		v := row.SessionID.String
		s.TargetSessionID = &v
	} else if row.EntrySessionID.Valid {
		v := row.EntrySessionID.String
		s.TargetSessionID = &v
	}
	if row.EntryIndex.Valid {
		v := int(row.EntryIndex.Int32)
		s.TargetEntryIndex = &v
	}
	if row.EntryEndIndex.Valid {
		v := int(row.EntryEndIndex.Int32)
		s.TargetEntryEndIndex = &v
	}
	if row.AnnotationID.Valid {
		v := row.AnnotationID.String
		s.TargetAnnotID = &v
	}
	if row.ProjectHash.Valid {
		v := schema.ProjectHash(row.ProjectHash.String)
		s.TargetProjectHash = &v
	}
	if row.TargetAssociationID.Valid {
		v := schema.AssociationID(row.TargetAssociationID.String)
		s.TargetAssociationID = &v
	}
	if row.Confidence.Valid {
		v := row.Confidence.Float64
		s.Confidence = &v
	}
	if row.Reason.Valid {
		v := row.Reason.String
		s.Reason = &v
	}

	var prov *schema.Provenance
	if len(row.Provenance) > 0 {
		var p schema.Provenance
		if err := json.Unmarshal(row.Provenance, &p); err == nil {
			prov = &p
			s.Provenance = prov
		}
	}

	s.AnnotatorKind = resolveAnnotatorKind(row.AnnotatorKind, prov)
	return s
}

// resolveAnnotatorKind derives an AnnotatorKind from the stored column (if set)
// or the annotation's provenance method (for pushed rows), defaulting to human.
func resolveAnnotatorKind(stored pgtype.Text, prov *schema.Provenance) schema.AnnotatorKind {
	if stored.Valid {
		if k := schema.AnnotatorKind(stored.String); k.IsValid() {
			return k
		}
	}
	if prov != nil {
		switch prov.Method {
		case "llm_judge":
			return schema.AnnotatorAgent
		case "heuristic", "regex":
			return schema.AnnotatorRule
		case "manual":
			return schema.AnnotatorHuman
		}
	}
	return schema.AnnotatorHuman
}

func textOrEmpty(t pgtype.Text) string {
	if t.Valid {
		return t.String
	}
	return ""
}
