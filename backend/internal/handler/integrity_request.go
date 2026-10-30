package handler

import (
	"errors"
	"fmt"
	"time"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// buildScryRequest describes one transcript to the integrity service: the
// session detail exactly as the content route serves it, the publish metadata
// as stored (nil when the row holds none), and only village's own publish and
// update times. Nothing else from the row is copied: everything the publisher
// reported reaches the service inside the publish metadata.
func buildScryRequest(row sqlc.Transcript, payload *schema.SessionDetailPayload, metadata *schema.AuthoritativePublishRequest) (schema.ScryCheckRequest, error) {
	if payload == nil {
		return schema.ScryCheckRequest{}, errors.New("integrity request has no session detail")
	}
	id, err := schema.NewTranscriptID(uuidFromPg(row.ID).String())
	if err != nil {
		return schema.ScryCheckRequest{}, fmt.Errorf("integrity request transcript id: %w", err)
	}
	req := schema.ScryCheckRequest{
		ContractVersion: schema.ScryContractVersion,
		TranscriptID:    id,
		SessionDetail:   *payload,
		PublishMetadata: metadata,
		Record: schema.ScryTranscriptRecord{
			PublishedAtMs: epochMillis(row.PublishedAt.Time, row.PublishedAt.Valid),
			UpdatedAtMs:   epochMillis(row.UpdatedAt.Time, row.UpdatedAt.Valid),
		},
	}
	if err := req.Validate(); err != nil {
		return schema.ScryCheckRequest{}, err
	}
	return req, nil
}

// epochMillis is a stored time in epoch milliseconds, or 0 when it is unset,
// which the request's validation refuses.
func epochMillis(t time.Time, valid bool) int64 {
	if !valid {
		return 0
	}
	return t.UnixMilli()
}
