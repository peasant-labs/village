package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// UploadAnnotations handles batch annotation push from the CLI.
// POST /api/v1/annotations (AuthRequired)
// Upserts each annotation by content_hash — existing entries get their updated_at bumped
// (skipped semantics); new entries are created. Per-item results are returned in the response.
func (h *Handler) UploadAnnotations(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	v := payloadValidator()
	if v == nil {
		writeError(w, http.StatusServiceUnavailable, "annotation validation unavailable")
		return
	}
	if err := v.ValidateAnnotation(raw); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "annotation request failed schema validation: "+err.Error())
		return
	}
	var req schema.AnnotationPushRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	resp := schema.AnnotationPushResponse{
		Results: make([]schema.AnnotationPushResult, 0, len(req.Annotations)),
	}

	// The full push is transaction-bound to the authenticated actor. Resolve
	// association targets before the batched upsert, so one unknown or foreign
	// target rolls back every item instead of leaving a partial annotation batch.
	if err := h.inTxAs(r.Context(), user.PgID(), func(q Querier) error {
		if err := resolveAssociationTargets(r.Context(), q, user.PgID(), req.Annotations); err != nil {
			return err
		}
		h.upsertAnnotationsBulk(r.Context(), q, user.PgID(), req.Annotations, &resp)

		// Retractions retain their owner-scoped, idempotent semantics and share
		// the same transaction as the validated push.
		for _, hash := range req.Retractions {
			err := q.DeleteAnnotationByContentHash(r.Context(), sqlc.DeleteAnnotationByContentHashParams{
				ContentHash: hash,
				OwnerID:     user.PgID(),
			})
			if err != nil {
				resp.Errors++
				resp.Results = append(resp.Results, schema.AnnotationPushResult{
					ContentHash: hash,
					Status:      schema.PushStatusError,
					Error:       "failed to retract annotation",
				})
			}
		}
		return nil
	}); err != nil {
		if errors.Is(err, ErrAssociationBinding) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "Failed to store annotation batch")
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// bulkAnnotationRecord is the per-annotation shape consumed by the
// BulkUpsertAnnotations query's jsonb_to_recordset. Pointer / json.RawMessage
// fields preserve SQL NULLs across the JSON boundary (a nil marshals to JSON
// null → a NULL column). owner_id is NOT here — it is applied as a single scalar
// to every row by the query, which is what makes the upsert owner-scoped.
type bulkAnnotationRecord struct {
	ContentHash    string          `json:"content_hash"`
	TargetKind     string          `json:"target_kind"`
	SessionID      *string         `json:"session_id"`
	EntrySessionID *string         `json:"entry_session_id"`
	EntryIndex     *int            `json:"entry_index"`
	EntryEndIndex  *int            `json:"entry_end_index"`
	AnnotationID   *string         `json:"annotation_id"`
	ProjectHash    *string         `json:"project_hash"`
	AssociationID  *string         `json:"target_association_id"`
	TypeID         string          `json:"type_id"`
	Value          string          `json:"value"`
	IsPrimary      bool            `json:"is_primary"`
	Confidence     *float64        `json:"confidence"`
	Reason         *string         `json:"reason"`
	AnnotatorName  *string         `json:"annotator_name"`
	Provenance     json.RawMessage `json:"provenance"`
}

// upsertAnnotationsBulk persists every pushed annotation in ONE batched upsert
// and appends a per-item Result to resp (preserving the order, length, and
// content_hash of the input, plus the created/updated/error classification the
// client depends on). It is the bulk replacement for the prior per-row loop.
//
// Parity notes:
//   - a provenance that fails to marshal yields a per-item error (as before) and
//     is excluded from the batch;
//   - records are de-duplicated by content_hash (last-wins) before the single
//     ON CONFLICT statement, which cannot affect the same row twice — a duplicate
//     content_hash within one request is not a realistic input (content_hash is
//     the client's own dedup key), but is handled safely;
//   - a batch-level DB failure marks every batched item as an error (the client
//     re-pushes, fail-safe) rather than silently dropping outcomes.
func (h *Handler) upsertAnnotationsBulk(ctx context.Context, q Querier, ownerID pgtype.UUID, items []schema.AnnotationPushItem, resp *schema.AnnotationPushResponse) {
	if len(items) == 0 {
		return
	}

	provFailed := make(map[int]bool)
	records := make([]bulkAnnotationRecord, 0, len(items))
	for i, item := range items {
		var prov json.RawMessage
		if item.Provenance != nil {
			b, err := json.Marshal(item.Provenance)
			if err != nil {
				provFailed[i] = true
				continue
			}
			prov = b
		}
		records = append(records, recordFromPushItem(item, prov))
	}

	createdByHash := make(map[string]bool, len(records))
	batchErr := false
	if len(records) > 0 {
		payload, err := json.Marshal(dedupeAnnotationRecords(records))
		if err != nil {
			batchErr = true
		} else if rows, err := q.BulkUpsertAnnotations(ctx, sqlc.BulkUpsertAnnotationsParams{
			OwnerID: ownerID,
			Items:   payload,
		}); err != nil {
			batchErr = true
		} else {
			for _, row := range rows {
				createdByHash[row.ContentHash] = row.Created
			}
		}
	}

	// appendErr records a per-item error Result (and bumps the error count) — used
	// at every error site below so the error shape stays in one place.
	appendErr := func(hash, msg string) {
		resp.Errors++
		resp.Results = append(resp.Results, schema.AnnotationPushResult{
			ContentHash: hash,
			Status:      schema.PushStatusError,
			Error:       msg,
		})
	}

	for i, item := range items {
		switch {
		case provFailed[i]:
			appendErr(item.ContentHash, "invalid provenance JSON")
		case batchErr:
			appendErr(item.ContentHash, "failed to store annotation")
		default:
			created, ok := createdByHash[item.ContentHash]
			if !ok {
				// Every non-failed item is in the batch RETURNING; a miss means the
				// row neither inserted nor updated — treat as an error, not silent.
				appendErr(item.ContentHash, "failed to store annotation")
				continue
			}
			if created {
				resp.Created++
				resp.Results = append(resp.Results, schema.AnnotationPushResult{
					ContentHash: item.ContentHash,
					Status:      schema.PushStatusCreated,
				})
			} else {
				resp.Updated++
				resp.Results = append(resp.Results, schema.AnnotationPushResult{
					ContentHash: item.ContentHash,
					Status:      schema.PushStatusUpdated,
				})
			}
		}
	}
}

// recordFromPushItem maps a push item to the batched-upsert record, applying the
// same null rules the per-row loop used (empty strings → NULL; an absent
// EntryTarget → NULL entry columns).
func recordFromPushItem(item schema.AnnotationPushItem, prov json.RawMessage) bulkAnnotationRecord {
	rec := bulkAnnotationRecord{
		ContentHash:   item.ContentHash,
		TargetKind:    string(item.TargetKind),
		TypeID:        item.TypeID,
		Value:         item.Value,
		IsPrimary:     item.IsPrimary,
		SessionID:     nonEmptyPtr(item.SessionID),
		AnnotationID:  nonEmptyPtr(item.AnnotationID),
		ProjectHash:   nonEmptyPtr((*string)(item.ProjectHash)),
		AssociationID: associationIDPtr(item.TargetAssociationID),
		Confidence:    item.Confidence,
		Reason:        nonEmptyPtr(item.Reason),
		AnnotatorName: emptyStringToNil(item.AnnotatorName),
		Provenance:    prov,
	}
	if item.EntryTarget != nil {
		rec.EntrySessionID = emptyStringToNil(item.EntryTarget.SessionID)
		ei := item.EntryTarget.EntryIndex
		ee := item.EntryTarget.EndIndex
		rec.EntryIndex = &ei
		rec.EntryEndIndex = &ee
	}
	return rec
}

func associationIDPtr(id *schema.AssociationID) *string {
	if id == nil {
		return nil
	}
	value := id.String()
	return &value
}

// dedupeAnnotationRecords collapses duplicate content_hashes to a single record,
// keeping the LAST occurrence (matching ON CONFLICT DO UPDATE last-wins), so the
// single-statement upsert never tries to affect the same row twice.
func dedupeAnnotationRecords(recs []bulkAnnotationRecord) []bulkAnnotationRecord {
	if len(recs) < 2 {
		return recs
	}
	lastIdx := make(map[string]int, len(recs))
	for i, r := range recs {
		lastIdx[r.ContentHash] = i
	}
	out := make([]bulkAnnotationRecord, 0, len(recs))
	for i, r := range recs {
		if lastIdx[r.ContentHash] == i {
			out = append(out, r)
		}
	}
	return out
}

// nonEmptyPtr returns p unless it is nil or points at "" (→ nil = SQL NULL),
// mirroring toPgText's empty-string-is-null behavior.
func nonEmptyPtr(p *string) *string {
	if p == nil || *p == "" {
		return nil
	}
	return p
}

// emptyStringToNil maps "" → nil (SQL NULL), else a pointer to s.
func emptyStringToNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
