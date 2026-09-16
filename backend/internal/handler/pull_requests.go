package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/promptattach"
)

// The pull request prompt-attachment lifecycle routes. Every one of them is
// declared by the served contract; two are reachable without a session because a
// public pull request's attachment is readable, and the rest require the author.
//
// The routes own HTTP only: reading, confirming, detaching, and the two
// settings endpoints. The lifecycle itself lives in attachment_lifecycle.go and
// attachment_effects.go, so the dispatcher (#107) and the publish hook can act
// on a webhook without going through HTTP.

// GET /api/v1/pulls/{owner}/{name}/{number} (AuthOptional)
//
// A public repository's attachment is readable by anyone, which is what lets
// the pull request page render for a reviewer with no Village account. A private
// repository's attachment is readable only by its author or a member of the
// collective that linked the repository; anyone else gets 404, never 403, so the
// route does not confirm that an attachment exists.
func (h *Handler) GetPullRequestAttachment(w http.ResponseWriter, r *http.Request) {
	owner, name, number, ok := pullRequestTarget(w, r)
	if !ok {
		return
	}
	attachment, err := h.queries.GetPullRequestAttachmentForPull(r.Context(), sqlc.GetPullRequestAttachmentForPullParams{
		Lower:   strings.ToLower(owner),
		Lower_2: strings.ToLower(name),
		Number:  int32(number),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "No attachment exists for this pull request")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not read the pull request attachment")
		return
	}

	// The repository's privacy comes from the collective's current link, so a
	// re-link is reflected. An unbound attachment has no repository to describe
	// and could be either private or public, so it is not served at all.
	repo, err := h.resolveAttachmentRepository(r.Context(), h.queries, attachment)
	if err != nil {
		writeError(w, http.StatusNotFound, "No attachment exists for this pull request")
		return
	}

	viewer := GetUser(r.Context())
	var viewerID pgtype.UUID
	viewerKnown := viewer != nil
	if viewerKnown {
		viewerID = viewer.PgID()
	}
	if repo.isPrivate && !(viewerKnown && viewerID == attachment.AuthorID) {
		if !viewerKnown || !h.isCollectiveMember(r.Context(), viewerID, attachment.GroupID) {
			writeError(w, http.StatusNotFound, "No attachment exists for this pull request")
			return
		}
	}

	response, err := h.attachmentResponseOf(r.Context(), attachment, repo.isPrivate, viewerID, viewerKnown)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read the pull request attachment")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// POST /api/v1/pulls/{owner}/{name}/{number}/confirm (AuthRequired)
//
// Author only. It confirms a preview into attached: the same widening and
// posting a direct attach performs. A pull request not in preview answers 409,
// and a GitHub failure answers 502 with the state unchanged.
func (h *Handler) ConfirmPullRequestAttachment(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	attachment, ok := h.loadAttachmentForAction(w, r, user)
	if !ok {
		return
	}

	var updated sqlc.PullRequestAttachment
	err := h.withAttachmentLock(r.Context(), attachment.ID, func() error {
		// Re-read inside the lock: a concurrent confirm may have attached it
		// between the load above and here, and the state that allows this action
		// must be checked against the row as it is now.
		fresh, err := h.queries.GetPullRequestAttachment(r.Context(), attachment.ID)
		if err != nil {
			return err
		}
		if fresh.State != string(promptattach.Preview) {
			return promptattach.ErrTransitionNotAllowed
		}
		updated, err = h.attachAcceptedAndPost(r.Context(), fresh)
		return err
	})
	if err != nil {
		writeAttachmentActionError(w, err)
		return
	}
	repo, err := h.resolveAttachmentRepository(r.Context(), h.queries, updated)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read the pull request attachment")
		return
	}
	response, err := h.attachmentResponseOf(r.Context(), updated, repo.isPrivate, user.PgID(), true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read the pull request attachment")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// DELETE /api/v1/pulls/{owner}/{name}/{number} (AuthRequired)
//
// Author only. It deletes the comment, resets the check, restores each bound
// transcript's recorded visibility, and moves the attachment to detached.
func (h *Handler) DetachPullRequestAttachment(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	attachment, ok := h.loadAttachmentForAction(w, r, user)
	if !ok {
		return
	}

	var updated sqlc.PullRequestAttachment
	err := h.withAttachmentLock(r.Context(), attachment.ID, func() error {
		fresh, err := h.queries.GetPullRequestAttachment(r.Context(), attachment.ID)
		if err != nil {
			return err
		}
		if !promptattach.CanTransition(promptattach.State(fresh.State), promptattach.Detached) {
			return promptattach.ErrTransitionNotAllowed
		}
		updated, err = h.detachAttachment(r.Context(), fresh)
		return err
	})
	if err != nil {
		writeAttachmentActionError(w, err)
		return
	}
	repo, err := h.resolveAttachmentRepository(r.Context(), h.queries, updated)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read the pull request attachment")
		return
	}
	response, err := h.attachmentResponseOf(r.Context(), updated, repo.isPrivate, user.PgID(), true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read the pull request attachment")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// GET /api/v1/users/me/prompt-requests (AuthRequired)
func (h *Handler) ListMyPromptRequests(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	attachments, err := h.queries.ListAuthorWaitingPromptRequests(r.Context(), user.PgID())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read your prompt requests")
		return
	}
	writeJSON(w, http.StatusOK, mapVillagePromptRequests(attachments))
}

// GET /api/v1/users/me/settings (AuthRequired)
func (h *Handler) GetUserSettings(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	row, err := h.queries.GetUserByID(r.Context(), user.PgID())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read your settings")
		return
	}
	writeJSON(w, http.StatusOK, mapVillageUserSettings(row))
}

// PATCH /api/v1/users/me/settings (AuthRequired)
func (h *Handler) UpdateUserSettings(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	var req schema.VillageUpdateUserSettingsRequest
	if !h.decodeContractBody(w, r, opUpdateUserSettings, &req) {
		return
	}
	if req.PreviewBeforeAttach == nil {
		// Nothing to change: report the current settings rather than a no-op
		// error, which is what a PATCH with an omitted field means.
		row, err := h.queries.GetUserByID(r.Context(), user.PgID())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not read your settings")
			return
		}
		writeJSON(w, http.StatusOK, mapVillageUserSettings(row))
		return
	}

	row, err := h.queries.SetUserPreviewBeforeAttach(r.Context(), sqlc.SetUserPreviewBeforeAttachParams{
		ID:                  user.PgID(),
		PreviewBeforeAttach: *req.PreviewBeforeAttach,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not save your settings")
		return
	}
	writeJSON(w, http.StatusOK, mapVillageUserSettings(row))
}

// pullRequestTarget reads the route's repository and pull request number.
// Invalid input is answered 400 here so every route agrees on the shape.
func pullRequestTarget(w http.ResponseWriter, r *http.Request) (owner, name string, number int, ok bool) {
	owner = strings.TrimSpace(chi.URLParam(r, "owner"))
	name = strings.TrimSpace(chi.URLParam(r, "name"))
	raw := strings.TrimSpace(chi.URLParam(r, "number"))
	if owner == "" || name == "" || raw == "" {
		writeError(w, http.StatusBadRequest, "owner, name, and pull request number are required")
		return "", "", 0, false
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		writeError(w, http.StatusBadRequest, "the pull request number must be a positive integer")
		return "", "", 0, false
	}
	return owner, name, parsed, true
}

// loadAttachmentForAction reads the attachment a route acts on and enforces the
// author-only rule, answering 404 for a missing attachment and 403 for a caller
// who is not its author.
func (h *Handler) loadAttachmentForAction(w http.ResponseWriter, r *http.Request, user *AuthUser) (sqlc.PullRequestAttachment, bool) {
	owner, name, number, ok := pullRequestTarget(w, r)
	if !ok {
		return sqlc.PullRequestAttachment{}, false
	}
	attachment, err := h.queries.GetPullRequestAttachmentForPull(r.Context(), sqlc.GetPullRequestAttachmentForPullParams{
		Lower:   strings.ToLower(owner),
		Lower_2: strings.ToLower(name),
		Number:  int32(number),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "No attachment exists for this pull request")
			return sqlc.PullRequestAttachment{}, false
		}
		writeError(w, http.StatusInternalServerError, "Could not read the pull request attachment")
		return sqlc.PullRequestAttachment{}, false
	}
	if attachment.AuthorID != user.PgID() {
		writeError(w, http.StatusForbidden, "Only the pull request's author may change this attachment")
		return sqlc.PullRequestAttachment{}, false
	}
	return attachment, true
}

// writeAttachmentActionError maps the lifecycle's failures to the contract's
// statuses: a state that does not allow the action is 409, a GitHub failure is
// 502 with the state unchanged, and anything else is a server error.
func writeAttachmentActionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, promptattach.ErrTransitionNotAllowed),
		errors.Is(err, promptattach.ErrStaleState),
		errors.Is(err, errAttachmentNothingAccepted),
		errors.Is(err, errAttachmentUnbound):
		writeError(w, http.StatusConflict, "This pull request is not in a state that allows that action")
	case errors.Is(err, errAttachmentGitHub), errors.Is(err, errAttachmentGitHubUnavailable):
		writeError(w, http.StatusBadGateway, "GitHub could not be reached, so nothing was changed; retry, or refresh the pull request")
	default:
		writeError(w, http.StatusInternalServerError, "The pull request attachment action could not be completed")
	}
}
