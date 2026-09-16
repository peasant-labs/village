package handler

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/github"
)

// maxGitHubWebhookBodyBytes bounds the raw webhook body before it is buffered.
// GitHub's largest event payloads are well under this; a body over the cap is
// refused rather than read into memory.
const maxGitHubWebhookBodyBytes = 10 << 20 // 10 MiB

// The github_webhook_deliveries.status menu, mirroring migration 040's CHECK.
const (
	webhookDeliveryPending = "pending"
	webhookDeliveryHandled = "handled"
	webhookDeliveryFailed  = "failed"
)

// maxWebhookErrorBytes bounds the dispatcher error kept on the ledger row for an
// operator. It is stored verbatim, so it is truncated rather than trusted to be
// short.
const maxWebhookErrorBytes = 1024

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
// GitHub's HMAC signature over the raw body, records the delivery with the type
// and payload it needs to be resumed, dispatches the subscribed event types to
// their handling point, and acknowledges everything else. It carries no session
// auth: the HMAC over the raw bytes is the trust boundary.
//
// The delivery is recorded BEFORE dispatch, so the work survives a crash. A
// delivery that was already handled is acknowledged as a replay without being
// dispatched again; one whose earlier attempt failed (or never finished) is
// dispatched again, which is how GitHub's only recovery path — a redelivery,
// which GitHub does not perform automatically — can actually recover it.
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

	state, err := h.queries.RecordGitHubWebhookDelivery(r.Context(), sqlc.RecordGitHubWebhookDeliveryParams{
		DeliveryID: deliveryID,
		EventType:  eventType,
		Payload:    body,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not record the webhook delivery")
		return
	}
	if state.Status == webhookDeliveryHandled {
		// Already handled: GitHub redelivered a delivery this server finished.
		// Acknowledge without dispatching it a second time.
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
		h.failGitHubWebhookDelivery(r.Context(), deliveryID, err)
		writeError(w, http.StatusInternalServerError, "the webhook event could not be handled")
		return
	}

	if err := h.queries.CompleteGitHubWebhookDelivery(r.Context(), sqlc.CompleteGitHubWebhookDeliveryParams{
		DeliveryID: deliveryID,
		Status:     webhookDeliveryHandled,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not record the webhook delivery outcome")
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

// failGitHubWebhookDelivery records a failed attempt so the delivery reads as
// failed for an operator and a redelivery resumes it.
//
// It is best effort: if this write fails too, the row is still pending from the
// record, and a pending row resumes on redelivery just as a failed one does, so
// the 500 the caller returns is honest in either case.
func (h *Handler) failGitHubWebhookDelivery(ctx context.Context, deliveryID string, cause error) {
	message := cause.Error()
	if len(message) > maxWebhookErrorBytes {
		message = message[:maxWebhookErrorBytes]
	}
	if err := h.queries.CompleteGitHubWebhookDelivery(ctx, sqlc.CompleteGitHubWebhookDeliveryParams{
		DeliveryID: deliveryID,
		Status:     webhookDeliveryFailed,
		LastError:  pgtype.Text{String: message, Valid: message != ""},
	}); err != nil {
		// The row stays pending, which is also resumable; the response already
		// reports the failure, so there is nothing further to change.
		return
	}
}
