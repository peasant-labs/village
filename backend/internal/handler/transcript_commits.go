package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// transcriptCommitResponse is the wire shape for a persisted transcript commit.
// Nullable columns (message, author name/email, timestamps) map to JSON null via
// pointers, mirroring how the annotations GET handler surfaces optional fields.
// Timestamps are emitted as Unix milliseconds, matching the payload's
// commitTime/authorTime convention.
type transcriptCommitResponse struct {
	Sha         string  `json:"sha"`
	Message     *string `json:"message"`
	AuthorName  *string `json:"authorName"`
	AuthorEmail *string `json:"authorEmail"`
	AuthoredAt  *int64  `json:"authoredAt"`
	CommittedAt *int64  `json:"committedAt"`
	Order       int32   `json:"order"`
}

// ListTranscriptCommits returns the git commits persisted for a transcript, in
// payload order.
//
// GET /api/v1/transcripts/{id}/commits (AuthOptional)
//
// Commits are written during publish from the payload's gitContext.commits[].
// This lets the frontend's commit-timeline overlay join transcript SHAs against
// a repo's cached commits. Visibility follows the transcript's own rules — if
// the caller cannot view the transcript, they get 404 (not 403, to avoid
// leaking existence), exactly like the annotations GET handler.
func (h *Handler) ListTranscriptCommits(w http.ResponseWriter, r *http.Request) {
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

	rows, err := h.queries.ListTranscriptCommits(r.Context(), transcript.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch commits")
		return
	}

	commits := make([]transcriptCommitResponse, 0, len(rows))
	for _, row := range rows {
		commits = append(commits, transcriptCommitRowToResponse(row))
	}

	writeJSON(w, http.StatusOK, map[string]any{"commits": commits})
}

// transcriptCommitRowToResponse maps a stored commit row to the wire shape,
// surfacing nullable pgtype columns as JSON null.
func transcriptCommitRowToResponse(row sqlc.TranscriptCommit) transcriptCommitResponse {
	return transcriptCommitResponse{
		Sha:         row.Sha,
		Message:     pgTextToStringPtr(row.Message),
		AuthorName:  pgTextToStringPtr(row.AuthorName),
		AuthorEmail: pgTextToStringPtr(row.AuthorEmail),
		AuthoredAt:  pgTimestamptzToMillisPtr(row.AuthoredAt),
		CommittedAt: pgTimestamptzToMillisPtr(row.CommittedAt),
		Order:       row.CommitOrder,
	}
}

// pgTimestamptzToMillisPtr renders a nullable timestamptz as a Unix-millis
// pointer (nil when the column is null), keeping the wire format aligned with
// the publish payload's millisecond timestamps.
func pgTimestamptzToMillisPtr(t pgtype.Timestamptz) *int64 {
	if !t.Valid {
		return nil
	}
	ms := t.Time.UnixMilli()
	return &ms
}
