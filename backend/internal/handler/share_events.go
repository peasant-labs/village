package handler

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// ShareEventHistoryEntry keeps the handler's existing Go name while the shared
// contract owns the wire shape, actor capacities and attempt validation.
type ShareEventHistoryEntry = schema.VillageShareEvent

// ListShareEventHistory returns the full event history for one (transcript,
// collective) pair, oldest event first, so the owner can see why a
// submission was rejected and how many times it was tried.
//
// Owner-only BY ROUTE, not merely by predicate: there is deliberately no
// username parameter anywhere on this path, so no request through this route
// can ever name a viewer other than the caller. A caller who is not the
// transcript owner gets 404, not 403 - a 403 would confirm the transcript
// exists, which is exactly the disclosure the pull routes already refuse
// (see lookupPullable in pull.go). The nil-user branch is a defensive
// fallback for a handler invoked outside the AuthRequired middleware (as an
// integration test does); the mounted route is AuthRequired, so a genuinely
// anonymous caller is turned away by the middleware before reaching here.
//
// GET /api/v1/users/me/collectives/{groupId}/transcripts/{transcriptId}/events (AuthRequired)
func (h *Handler) ListShareEventHistory(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}

	groupID, err := uuid.Parse(chi.URLParam(r, "groupId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"Invalid collective ID %q in the URL path: %v. The {groupId} segment of "+
				"GET /api/v1/users/me/collectives/{groupId}/transcripts/{transcriptId}/events must be a "+
				"36-character UUID (e.g. 123e4567-e89b-12d3-a456-426614174000), matching the collective's own "+
				"id field. This request cannot be resolved until that segment is corrected.",
			chi.URLParam(r, "groupId"), err))
		return
	}
	transcriptID, err := uuid.Parse(chi.URLParam(r, "transcriptId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"Invalid transcript ID %q in the URL path: %v. The {transcriptId} segment of "+
				"GET /api/v1/users/me/collectives/{groupId}/transcripts/{transcriptId}/events must be a "+
				"36-character UUID (e.g. 123e4567-e89b-12d3-a456-426614174000), matching the transcript's own "+
				"id field. This request cannot be resolved until that segment is corrected.",
			chi.URLParam(r, "transcriptId"), err))
		return
	}

	transcript, err := h.queries.GetTranscriptByID(r.Context(), toPgUUID(transcriptID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "Transcript not found")
		} else {
			writeError(w, http.StatusInternalServerError, "Share-event history could not be loaded because its transcript lookup failed. No history was returned; retry the request. If the failure persists, ask an operator to check database connectivity.")
		}
		return
	}
	if transcript.OwnerID != user.PgID() {
		// 404, NOT 403: revealing "forbidden" would confirm this transcript
		// exists to a caller who does not own it. Matches lookupPullable's
		// discipline in pull.go.
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}

	rows, err := h.queries.ListShareAttempts(r.Context(), sqlc.ListShareAttemptsParams{
		TranscriptID: toPgUUID(transcriptID),
		GroupID:      toPgUUID(groupID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf(
			"Failed to load the share-event history for transcript %s in collective %s: %v. This happened "+
				"while querying transcript_share_attempts in ListShareEventHistory (share_events.go), after "+
				"ownership had already been confirmed, so it reflects a database or connection problem rather "+
				"than a bad request. Retry the request; if it keeps failing, check the backend's database "+
				"connectivity and logs for the underlying query error.",
			transcriptID, groupID, err))
		return
	}

	events := make([]ShareEventHistoryEntry, 0, len(rows))
	for _, row := range rows {
		actor, err := schema.VillageContributionAttemptActor(schema.VillageShareStatus(row.Status))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "The stored contribution attempt has an unsupported status. No history was returned; ask an operator to inspect the attempt ledger before retrying.")
			return
		}
		entry := ShareEventHistoryEntry{
			EventNum:       row.EventNum,
			Status:         schema.VillageShareStatus(row.Status),
			RecordedAt:     row.RecordedAt.Time.UTC(),
			DecidedByActor: actor,
		}
		if row.DecidedAt.Valid {
			decidedAt := row.DecidedAt.Time.UTC()
			entry.DecidedAt = &decidedAt
		}
		if err := entry.Validate(); err != nil {
			writeError(w, http.StatusInternalServerError, "The stored contribution attempt does not satisfy the history contract. No history was returned; ask an operator to inspect the attempt ledger before retrying.")
			return
		}
		events = append(events, entry)
	}
	writeJSON(w, http.StatusOK, events)
}
