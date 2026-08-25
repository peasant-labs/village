package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// shareEventActor is the closed set of actor classes rendered on a
// share-event history entry. It NEVER carries a user id: exposing a
// moderator's identity to the submitter they reviewed is a disclosure this
// plan has not authorised, and the owner-facing history only needs to say
// WHO ACTED IN WHAT CAPACITY, not who they are.
type shareEventActor string

const (
	// shareEventActorNone is rendered for a still-open (pending) event: it has
	// not been decided yet, so there is no actor to name.
	shareEventActorNone       shareEventActor = ""
	shareEventActorOwner      shareEventActor = "owner"
	shareEventActorCollective shareEventActor = "collective"
	shareEventActorModerator  shareEventActor = "moderator"
)

// classifyShareEventActor derives the actor class from the event's STATUS,
// never from the decided_by user id column. The write paths in shares.sql
// establish this mapping: UnshareTranscript records a retraction without
// setting decided_by (the owner is acting on their own submission);
// RemoveGroupTranscript records a revocation without setting decided_by (the
// collective is acting); UpdateShareStatus records an approval or rejection
// WITH decided_by set to the reviewing moderator's id. A pending event has
// not been decided yet, so it carries no actor.
func classifyShareEventActor(status string) shareEventActor {
	switch status {
	case "approved", "rejected":
		return shareEventActorModerator
	case "retracted":
		return shareEventActorOwner
	case "revoked":
		return shareEventActorCollective
	default:
		return shareEventActorNone
	}
}

// ShareEventHistoryEntry is one row of an owner's share-event history: a
// numbered event with its outcome and, once decided, who acted in what
// capacity. See classifyShareEventActor for why DecidedByActor is a closed
// class rather than a user id.
type ShareEventHistoryEntry struct {
	EventNum       int32           `json:"event_num"`
	Status         string          `json:"status"`
	RecordedAt     time.Time       `json:"recorded_at"`
	DecidedAt      *time.Time      `json:"decided_at"`
	DecidedByActor shareEventActor `json:"decided_by_actor"`
}

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
		writeError(w, http.StatusNotFound, "Transcript not found")
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
		entry := ShareEventHistoryEntry{
			EventNum:       row.EventNum,
			Status:         row.Status,
			RecordedAt:     row.RecordedAt.Time,
			DecidedByActor: classifyShareEventActor(row.Status),
		}
		if row.DecidedAt.Valid {
			decidedAt := row.DecidedAt.Time
			entry.DecidedAt = &decidedAt
		}
		events = append(events, entry)
	}
	writeJSON(w, http.StatusOK, events)
}
