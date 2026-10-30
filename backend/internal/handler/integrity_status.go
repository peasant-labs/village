package handler

import (
	"context"
	"fmt"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// servedIntegrity is what the detail route serves as integrity. The caller has
// already passed transcriptViewAccess, so whoever may read the transcript sees
// its flag and this tests no visibility.
//
// It is nil unless the row has a content hash and an open flag. The positions
// are those of the latest checked answer for the row's current content hash,
// the same hash the response names as transcript.content_hash. They are empty
// (never nil) when the flag is open but nothing applies to the current
// content: a flag kept from an earlier copy, or inherited. Positions of any
// other content are never served. It does not depend on the checker being
// configured: stored flags are served as stored.
//
// One query answers both questions, so the read costs one round trip.
func (h *Handler) servedIntegrity(ctx context.Context, row sqlc.Transcript) (*schema.VillageTranscriptIntegrity, error) {
	if !row.ContentHash.Valid || row.ContentHash.String == "" {
		return nil, nil
	}
	state, err := h.queries.ServedIntegrityForContent(ctx, sqlc.ServedIntegrityForContentParams{
		TranscriptID: row.ID,
		ContentHash:  row.ContentHash.String,
	})
	if err != nil {
		return nil, fmt.Errorf("read the served integrity status: %w", err)
	}
	if !state.Open {
		return nil, nil
	}
	turns := make([]int, len(state.FlaggedTurns))
	for i, turn := range state.FlaggedTurns {
		turns[i] = int(turn)
	}
	return &schema.VillageTranscriptIntegrity{FlaggedTurns: turns}, nil
}
