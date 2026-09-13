package handler

import (
	"net/http"

	"github.com/peasant-labs/schema"
)

// The pull request prompt attachment surface. The served contract declares
// these seven routes, and the router mounts them so the route drift gate sees
// the contract and the server agree before the handlers behind them exist.
// Until then each answers 501 with the one fixed body below. The router
// package's attachment stub fixture pins each route's auth expectation and
// this body; a real handler replaces the stub here and deletes its row there.
//
// The webhook body is authenticated by GitHub's HMAC over its raw bytes, never
// validated by shape, so it is deliberately absent from
// ContractEnforcedOperations. The settings PATCH is the one operation on this
// surface with a body the contract enforces.
const pullRequestAttachmentUnavailable = "Pull request prompt attachment is not implemented on this server yet"

func (h *Handler) pullRequestAttachmentNotImplemented(w http.ResponseWriter) {
	writeError(w, http.StatusNotImplemented, pullRequestAttachmentUnavailable)
}

// ReceiveGitHubWebhook is POST /api/v1/integrations/github/webhook. Nothing is
// read from the request until the HMAC-verifying receiver exists.
func (h *Handler) ReceiveGitHubWebhook(w http.ResponseWriter, _ *http.Request) {
	h.pullRequestAttachmentNotImplemented(w)
}

// GetPullRequestAttachment is GET /api/v1/pulls/{owner}/{name}/{number}.
func (h *Handler) GetPullRequestAttachment(w http.ResponseWriter, _ *http.Request) {
	h.pullRequestAttachmentNotImplemented(w)
}

// ConfirmPullRequestAttachment is POST /api/v1/pulls/{owner}/{name}/{number}/confirm.
func (h *Handler) ConfirmPullRequestAttachment(w http.ResponseWriter, _ *http.Request) {
	h.pullRequestAttachmentNotImplemented(w)
}

// DetachPullRequestAttachment is DELETE /api/v1/pulls/{owner}/{name}/{number}.
func (h *Handler) DetachPullRequestAttachment(w http.ResponseWriter, _ *http.Request) {
	h.pullRequestAttachmentNotImplemented(w)
}

// ListMyPromptRequests is GET /api/v1/users/me/prompt-requests.
func (h *Handler) ListMyPromptRequests(w http.ResponseWriter, _ *http.Request) {
	h.pullRequestAttachmentNotImplemented(w)
}

// GetUserSettings is GET /api/v1/users/me/settings.
func (h *Handler) GetUserSettings(w http.ResponseWriter, _ *http.Request) {
	h.pullRequestAttachmentNotImplemented(w)
}

// UpdateUserSettings is PATCH /api/v1/users/me/settings. The body is checked
// against the served contract before anything else, so a malformed body
// answers 400 with the violation today and keeps doing so once the settings
// store exists; a conforming body answers 501 until then.
func (h *Handler) UpdateUserSettings(w http.ResponseWriter, r *http.Request) {
	var req schema.VillageUpdateUserSettingsRequest
	if !h.decodeContractBody(w, r, opUpdateUserSettings, &req) {
		return
	}
	h.pullRequestAttachmentNotImplemented(w)
}
