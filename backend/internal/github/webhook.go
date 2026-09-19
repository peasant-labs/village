package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// The four events the Village App subscribes to and the receiver dispatches.
// Every other event is acknowledged and dropped. These are the exact values
// GitHub sends in the X-GitHub-Event header.
const (
	EventInstallation = "installation"
	EventPullRequest  = "pull_request"
	EventCheckRun     = "check_run"
	EventIssueComment = "issue_comment"
)

// Event is one verified webhook delivery handed to a dispatcher. Payload is the
// raw request body bytes, already authenticated by the HMAC signature; the
// dispatcher owns decoding it into the shape its event needs.
type Event struct {
	Type       string
	DeliveryID string
	Payload    []byte
}

// Dispatcher is the handling point for each subscribed event type. The receiver
// calls exactly one method per delivery, chosen by the event type. Implementations
// own everything downstream: matching, digest computation, posting, storage.
type Dispatcher interface {
	Installation(ctx context.Context, event Event) error
	PullRequest(ctx context.Context, event Event) error
	CheckRun(ctx context.Context, event Event) error
	IssueComment(ctx context.Context, event Event) error
}

// Dispatch routes a verified event to its own handling point. An event type the
// receiver does not subscribe to is acknowledged and dropped: no handler runs and
// no error is returned, because an unsubscribed event is not a failure.
func Dispatch(ctx context.Context, dispatcher Dispatcher, event Event) error {
	switch event.Type {
	case EventInstallation:
		return dispatcher.Installation(ctx, event)
	case EventPullRequest:
		return dispatcher.PullRequest(ctx, event)
	case EventCheckRun:
		return dispatcher.CheckRun(ctx, event)
	case EventIssueComment:
		return dispatcher.IssueComment(ctx, event)
	default:
		return nil
	}
}

// signaturePrefix is the scheme marker GitHub puts before the hex digest in
// X-Hub-Signature-256.
const signaturePrefix = "sha256="

// VerifySignature reports whether signatureHeader is a valid HMAC-SHA256 of
// payload under secret, using a constant-time comparison. It fails closed:
// an empty secret or header, or a header that is not `sha256=<hex>`, is never
// valid. The raw body bytes must be the exact bytes GitHub signed; verifying a
// re-serialized body would silently accept a mutated payload.
func VerifySignature(secret string, payload []byte, signatureHeader string) bool {
	if secret == "" {
		return false
	}
	header := strings.TrimSpace(signatureHeader)
	if !strings.HasPrefix(header, signaturePrefix) {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(header, signaturePrefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hmac.Equal(provided, mac.Sum(nil))
}
