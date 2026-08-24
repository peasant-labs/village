package handler

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/peasant-labs/village/backend/internal/sessionorigin"
)

// classifyPublishedSessionOrigin decodes accepted upload bytes through the one
// migrate-on-read boundary and classifies who drove the session, so the
// discovery listing can group agent-driven sessions without re-reading content.
//
// It never rejects a publish. Content that cannot be decoded classifies
// Unknown, which behaves exactly like a user session: a transcript is never
// hidden because Village could not read it. The caller has already validated,
// capability-checked, and secret-scanned these bytes, so a decode failure here
// is a stored-shape surprise worth logging, not a client error.
func classifyPublishedSessionOrigin(ctx context.Context, content []byte) sessionorigin.Origin {
	payload, _, err := defaultContentMigrator.Migrate(ctx, content)
	if err != nil {
		// No transcript content, identity, or path is logged - only the failure
		// class and the fail-safe consequence.
		slog.Warn("session origin classification fell back to the fail-safe value",
			"operation", "publish_transcript",
			"stage", "content_decode",
			"consequence", "the transcript is stored with an unknown origin and stays fully visible in every list",
			"remediation", "no operator action is required; rerun the origin backfill after the content shape is supported to reclassify the row",
			"origin", sessionorigin.Unknown.String(),
			"cause_type", fmt.Sprintf("%T", err))
		return sessionorigin.Unknown
	}
	return sessionorigin.Classify(payload)
}
