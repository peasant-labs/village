package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/sessionorigin"
)

// resolvePublishedSessionOrigin decodes accepted upload bytes through the one
// migrate-on-read boundary and resolves who drove the session, so the discovery
// listing can group agent-driven sessions without re-reading content.
//
// Content that cannot be decoded carries no declaration and no classifiable
// turns, so it resolves to the fail-safe value and the publish still succeeds:
// a transcript is never hidden because Village could not read it. The caller
// has already validated, capability-checked, and secret-scanned these bytes, so
// a decode failure here is a stored-shape surprise worth logging, not a client
// error.
//
// The one refusal it can return is an out-of-menu declaration, which is a
// client error; see ResolvePublishedOrigin.
func resolvePublishedSessionOrigin(ctx context.Context, content []byte) (sessionorigin.Origin, error) {
	if err := refuseOutOfMenuSessionOrigin(content); err != nil {
		return "", err
	}
	payload, _, err := defaultContentMigrator.Migrate(ctx, content)
	if err != nil {
		// No transcript content, identity, or path is logged - only the failure
		// class and the fail-safe consequence.
		slog.Warn("session origin resolution fell back to the fail-safe value",
			"operation", "publish_transcript",
			"stage", "content_decode",
			"consequence", "the transcript is stored with an unknown origin and stays fully visible in every list",
			"remediation", "no operator action is required; rerun the origin backfill after the content shape is supported to reclassify the row",
			"origin", sessionorigin.Unknown.String(),
			"cause_type", fmt.Sprintf("%T", err))
		return sessionorigin.Unknown, nil
	}
	var declared schema.SessionOrigin
	if payload != nil {
		declared = payload.SessionOrigin
	}
	return ResolvePublishedOrigin(declared, payload)
}

// peekSessionOriginDeclaration reads the producer's sessionOrigin declaration
// from upload bytes without validating them. It reports false when the bytes
// carry no string declaration, so absent, null, undecodable, and mistyped
// values all fall through to the canonical decode path, which governs them:
// absent defers to the classifier, undecodable content keeps the fail-safe
// value, and a mistyped value fails closed there.
func peekSessionOriginDeclaration(content []byte) (schema.SessionOrigin, bool) {
	var envelope struct {
		SessionDetail map[string]json.RawMessage `json:"sessionDetail"`
	}
	if json.Unmarshal(content, &envelope) == nil && envelope.SessionDetail != nil {
		if declared, ok := decodeOriginString(envelope.SessionDetail["sessionOrigin"]); ok {
			return declared, true
		}
		return "", false
	}
	var bare map[string]json.RawMessage
	if json.Unmarshal(content, &bare) == nil && bare != nil {
		if declared, ok := decodeOriginString(bare["sessionOrigin"]); ok {
			return declared, true
		}
	}
	return "", false
}

func decodeOriginString(raw json.RawMessage) (schema.SessionOrigin, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var declared string
	if json.Unmarshal(raw, &declared) != nil {
		return "", false
	}
	return schema.SessionOrigin(declared), true
}

// refuseOutOfMenuSessionOrigin applies the designed 400 precedence for an
// out-of-menu sessionOrigin declaration. The canonical decoder rejects such a
// declaration fail-closed during the first content decode, which would turn
// the documented refusal into a 422, so callers run this peek before decoding.
// The menu still comes from the contract through ResolvePublishedOrigin, not
// from a handler copy. Absent, null, undecodable, and mistyped values return
// nil and stay governed by the decode path.
func refuseOutOfMenuSessionOrigin(content []byte) error {
	declared, ok := peekSessionOriginDeclaration(content)
	if !ok {
		return nil
	}
	_, err := ResolvePublishedOrigin(declared, nil)
	return err
}

// ResolvePublishedOrigin takes the producer's declaration when it made one and
// falls back to the shared classifier when it did not.
//
// The contract rule the declaration binds this server to:
//
//   - "user" and "agent" are decisions. The producer held the raw harness
//     record, where the evidence still existed; Village stores what it was told
//     and does not consult its own classifier.
//   - "unknown" is a decision too: the producer looked and could not tell. It
//     returns the question to this server, which MUST apply the rule it would
//     have applied had the field been absent. Village never stores a declared
//     "unknown" verbatim, because it holds the content and has a working
//     classifier - storing the declaration would be strictly worse than what
//     this server does for the same payload today.
//   - An absent declaration means the producer expressed no opinion, typically
//     a build older than the field. It falls back the same way, so "unknown"
//     and absent behave identically while staying distinct facts.
//   - Anything else is a client error, not a reason to guess. The publisher
//     told us something we cannot honour, and silently classifying instead
//     would hide the disagreement.
//
// content is the decoded payload the classifier reads; a nil payload
// classifies to the fail-safe value.
func ResolvePublishedOrigin(declared schema.SessionOrigin, content *schema.SessionDetailPayload) (sessionorigin.Origin, error) {
	switch declared {
	case schema.SessionOriginUser:
		return sessionorigin.User, nil
	case schema.SessionOriginAgent:
		return sessionorigin.Agent, nil
	case "", schema.SessionOriginUnknown:
		return sessionorigin.Classify(content), nil
	default:
		return "", declaredOriginRefusal(declared)
	}
}

// declaredOriginRefusal renders the refusal a client gets back as a 400. It
// names the value, the accepted menu, where the refusal happened, that nothing
// was stored, why no value was substituted, and the two ways to fix it.
func declaredOriginRefusal(declared schema.SessionOrigin) error {
	return fmt.Errorf(
		"declared session origin %q is not one of %s; the declaration was refused in "+
			"handler.ResolvePublishedOrigin at the publish trust boundary, after the upload was read and "+
			"before any transcript row, blob, or association was written, so nothing was stored; no value "+
			"was substituted because both substitutions are wrong (user would expose an agent session in "+
			"every list, agent would hide a person's session) and classifying instead would hide the "+
			"disagreement with what you declared; set sessionDetail.sessionOrigin to one of %s, or omit it "+
			"to let this server classify the session, then publish again",
		string(declared), declaredSessionOriginMenu(), declaredSessionOriginMenu())
}

// declaredSessionOriginMenu renders the wire menu for an actionable error. It
// is derived from the contract's own menu, so widening the contract widens the
// message with no edit here.
func declaredSessionOriginMenu() string {
	names := make([]string, 0, len(schema.AllSessionOrigins))
	for _, origin := range schema.AllSessionOrigins {
		names = append(names, origin.String())
	}
	return strings.Join(names, ", ")
}
