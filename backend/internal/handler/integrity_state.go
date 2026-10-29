package handler

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// isFlagged reports whether an answer flags its content: it examined the
// content and named at least one turn.
func isFlagged(r schema.ScryCheckResponse) bool {
	return r.Verdict == schema.ScryVerdictChecked && len(r.FlaggedTurns) > 0
}

// recordIntegrityResult stores one answer for one content and does nothing
// else. It never re-tests visibility, shares or the current content: the
// check was asked for while that copy was readable by others, so an answer
// that arrives after the transcript left the checked set, or after its content
// was replaced, still counts. A later checked answer with no turns on the same
// content voids an earlier flag through the open-flag query, with no write.
func recordIntegrityResult(ctx context.Context, q Querier, transcriptID pgtype.UUID, contentHash string, resp schema.ScryCheckResponse) error {
	turns := make([]int32, len(resp.FlaggedTurns))
	for i, turn := range resp.FlaggedTurns {
		turns[i] = int32(turn)
	}
	if err := q.InsertIntegrityResult(ctx, sqlc.InsertIntegrityResultParams{
		TranscriptID: transcriptID,
		ContentHash:  contentHash,
		ScryVersion:  resp.ScryVersion,
		Verdict:      string(resp.Verdict),
		FlaggedTurns: turns,
	}); err != nil {
		return fmt.Errorf("store the integrity result: %w", err)
	}
	return nil
}
