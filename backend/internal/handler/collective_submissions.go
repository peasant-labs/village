package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// OwnerCollectiveSubmission is one (transcript, collective) pair the caller has
// offered, carrying the latest recorded event of that pair.
//
// Status is the raw ledger status, not a rendered label: retracted (the owner
// withdrew) and revoked (the collective removed) stay distinct here, because
// the surface labels them by ACTOR and collapsing them would make the two
// indistinguishable to a reader who needs to know who acted.
type OwnerCollectiveSubmission struct {
	TranscriptID pgtype.UUID `json:"transcript_id"`
	GroupID      pgtype.UUID `json:"group_id"`
	Title        pgtype.Text `json:"title"`
	EventNum     int32       `json:"event_num"`
	Status       string      `json:"status"`
	RecordedAt   time.Time   `json:"recorded_at"`
}

// ListMyCollectiveSubmissions returns EVERY (transcript, collective) pair the
// caller has offered to one collective, including pairs that no longer have a
// current-state row because their last event was a withdrawal or a removal.
//
// That inclusion is why this endpoint exists. The surface it replaces read the
// derived current-state row, which is a fold that keeps only live states, so a
// contribution that was submitted, refused three times and then withdrawn was
// reported as three refusals on the profile and then, on opening, as "no
// submissions of yours are on record in this collective". Reading the attempt
// ledger keeps that pair listed and its history reachable.
//
// Owner-only BY ROUTE, not merely by predicate: there is deliberately no
// username parameter anywhere on this path, so no request through this route
// can name a subject other than the caller. A caller with no pair in this
// collective - which includes every caller who is not the contributor, and
// every signed-out caller - gets 404, not 403 and not an empty list: 404 is the
// same answer a collective that does not exist gives, so asking cannot be used
// to discover one. The nil-user branch is a defensive fallback for a handler
// invoked outside the AuthRequired middleware (as an integration test does);
// the mounted route is AuthRequired, so a genuinely anonymous caller is turned
// away before reaching here.
//
// GET /api/v1/users/me/collectives/{groupId}/submissions (AuthRequired)
func (h *Handler) ListMyCollectiveSubmissions(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusNotFound, notFoundSubmissionsMessage)
		return
	}

	groupID, err := uuid.Parse(chi.URLParam(r, "groupId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"Invalid collective ID %q in the URL path: %v. The {groupId} segment of "+
				"GET /api/v1/users/me/collectives/{groupId}/submissions must be a 36-character UUID "+
				"(e.g. 123e4567-e89b-12d3-a456-426614174000), matching the collective's own id field. This "+
				"request cannot be resolved until that segment is corrected.",
			chi.URLParam(r, "groupId"), err))
		return
	}

	rows, err := h.queries.ListOwnerCollectiveSubmissions(r.Context(), sqlc.ListOwnerCollectiveSubmissionsParams{
		GroupID: toPgUUID(groupID),
		OwnerID: user.PgID(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf(
			"Failed to load your submissions to collective %s: %v. This happened while querying the share-attempt "+
				"ledger in ListMyCollectiveSubmissions (collective_submissions.go), so it reflects a database or "+
				"connection problem rather than a bad request. Your submissions are unchanged and nothing was "+
				"written. Retry; if it keeps failing, check the backend's database connectivity and logs for the "+
				"underlying query error.",
			groupID, err))
		return
	}
	if len(rows) == 0 {
		// One answer for "no such collective" and "none of yours are here", so
		// that asking cannot be used to discover which collectives exist or who
		// contributed to them.
		writeError(w, http.StatusNotFound, notFoundSubmissionsMessage)
		return
	}

	out := make([]OwnerCollectiveSubmission, 0, len(rows))
	for _, row := range rows {
		out = append(out, OwnerCollectiveSubmission{
			TranscriptID: row.TranscriptID,
			GroupID:      row.GroupID,
			Title:        row.Title,
			EventNum:     row.EventNum,
			Status:       row.Status,
			RecordedAt:   row.RecordedAt.Time,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// notFoundSubmissionsMessage is the single answer given for every reason this
// listing is empty, so the wording itself cannot separate "no such collective"
// from "not yours".
const notFoundSubmissionsMessage = "No submissions of yours are recorded in that collective. Either the collective " +
	"does not exist, or you have never offered a transcript to it, or you are signed in as somebody else. This " +
	"route serves the signed-in caller's own submissions only and takes no other subject. Sign in as the " +
	"contributor and retry."
