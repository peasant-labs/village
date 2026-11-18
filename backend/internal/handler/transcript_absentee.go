package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/village/backend/internal/contentstate"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

const retractedTranscriptCopy = "This transcript was retracted by its publisher. Its content has been deleted and cannot be shown."
const unavailableTranscriptCopy = "This transcript's stored content has been lost and cannot be loaded. Village operators recorded the loss; there is no copy to restore."

func refuseUnavailableTranscript(w http.ResponseWriter, row sqlc.Transcript) bool {
	if row.ContentState != contentstate.Unavailable.String() {
		return false
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeError(w, http.StatusGone, unavailableTranscriptCopy)
	return true
}

func writeRetractedTranscript(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
	writeError(w, http.StatusGone, retractedTranscriptCopy)
}

// retractedTranscriptVisible uses only the recorded audience, not the actor
// retained in the legal audit. An owner receives no special access.
func (h *Handler) retractedTranscriptVisible(ctx context.Context, id pgtype.UUID, user *AuthUser) (bool, error) {
	visibility, err := h.queries.GetLatestRetractionVisibility(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch visibility {
	case "public":
		return true, nil
	case "shared":
		if user == nil {
			return false, nil
		}
		groups, err := h.queries.ListRetractionAudiences(ctx, id)
		if err != nil {
			return false, err
		}
		return h.canReadTranscriptGroups(ctx, user, groups)
	default:
		return false, nil
	}
}

func (h *Handler) canReadTranscriptGroups(ctx context.Context, user *AuthUser, groups []pgtype.UUID) (bool, error) {
	for _, groupID := range groups {
		member, err := h.queries.GetGroupMember(ctx, sqlc.GetGroupMemberParams{GroupID: groupID, UserID: user.PgID()})
		if err == nil && canReadData(member.Role, "contributors") {
			return true, nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
	}
	return false, nil
}
