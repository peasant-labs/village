package handler

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/peasant-labs/village/backend/internal/github"
)

// maxGitHubWebhookBodyBytes bounds the raw webhook body before it is buffered.
// GitHub's largest event payloads are well under this; a body over the cap is
// refused rather than read into memory.
const maxGitHubWebhookBodyBytes = 10 << 20 // 10 MiB

// noopGitHubDispatcher is production's placeholder handling point: it accepts
// every subscribed event and does nothing. The matching, digest, and posting
// work replaces it with a real dispatcher; until then dispatch is a no-op, so
// the receiver can land and be exercised independently.
type noopGitHubDispatcher struct{}

func (noopGitHubDispatcher) Installation(context.Context, github.Event) error { return nil }
func (noopGitHubDispatcher) PullRequest(context.Context, github.Event) error  { return nil }
func (noopGitHubDispatcher) CheckRun(context.Context, github.Event) error     { return nil }
func (noopGitHubDispatcher) IssueComment(context.Context, github.Event) error { return nil }

// ReceiveGitHubWebhook is POST /api/v1/integrations/github/webhook. It verifies
// GitHub's HMAC signature over the raw body, records the delivery id exactly
// once, dispatches the four subscribed event types to their handling point, and
// acknowledges everything else. It carries no session auth: the HMAC over the
// raw bytes is the trust boundary.
func (h *Handler) ReceiveGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if h.gh == nil || h.cfg == nil || h.cfg.GitHubAppWebhookSecret == "" {
		writeError(w, http.StatusNotImplemented, "GitHub webhook receiver is not configured on this server")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxGitHubWebhookBodyBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read the webhook request body")
		return
	}
	if len(body) > maxGitHubWebhookBodyBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "webhook request body is too large")
		return
	}

	if !github.VerifySignature(h.cfg.GitHubAppWebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		writeError(w, http.StatusUnauthorized, "webhook signature verification failed")
		return
	}

	deliveryID := strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))
	if deliveryID == "" {
		writeError(w, http.StatusBadRequest, "X-GitHub-Delivery header is required")
		return
	}
	eventType := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
	if eventType == "" {
		writeError(w, http.StatusBadRequest, "X-GitHub-Event header is required")
		return
	}

	inserted, err := h.queries.RecordGitHubWebhookDelivery(r.Context(), deliveryID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not record the webhook delivery")
		return
	}
	if inserted == 0 {
		// Already recorded: GitHub redelivered a delivery this server accepted
		// before. Acknowledge without dispatching it a second time.
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "replay"})
		return
	}

	dispatcher := h.githubDispatcher
	if dispatcher == nil {
		dispatcher = noopGitHubDispatcher{}
	}
	if err := github.Dispatch(r.Context(), dispatcher, github.Event{
		Type:       eventType,
		DeliveryID: deliveryID,
		Payload:    body,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "the webhook event could not be handled")
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}
