package handler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/digest"
)

// storeAttachmentArtifacts serializes derived prompt copies with deletion. A
// GitHub post may finish after a transcript is deleted; its earlier digest must
// never restore that transcript's prompt text in the attachment row.
func (h *Handler) storeAttachmentArtifacts(ctx context.Context, arg sqlc.SetPullRequestAttachmentArtifactsParams) error {
	return h.inTxAsSystem(ctx, func(q Querier) error {
		attachment, err := q.LockPullRequestAttachmentArtifacts(ctx, arg.ID)
		if err != nil {
			return err
		}
		if len(arg.Digest) > 0 {
			var stored schema.PromptDigest
			if err := json.Unmarshal(arg.Digest, &stored); err != nil {
				return err
			}
			ids := make([]pgtype.UUID, 0, len(stored.Items))
			seen := map[schema.TranscriptID]bool{}
			for _, item := range stored.Items {
				if seen[item.TranscriptID] {
					continue
				}
				seen[item.TranscriptID] = true
				parsed, err := uuid.Parse(string(item.TranscriptID))
				if err != nil {
					return fmt.Errorf("invalid stored prompt transcript: %w", err)
				}
				ids = append(ids, pgtype.UUID{Bytes: parsed, Valid: true})
			}
			live, err := q.ListLiveOwnedDigestTranscripts(ctx, sqlc.ListLiveOwnedDigestTranscriptsParams{OwnerID: attachment.AuthorID, TranscriptIds: ids})
			if err != nil {
				return err
			}
			keep := map[schema.TranscriptID]bool{}
			for _, id := range live {
				keep[schema.TranscriptID(uuidFromPg(id).String())] = true
			}
			pruned, err := digest.Restrict(stored, func(id schema.TranscriptID) bool { return keep[id] })
			if err != nil {
				return err
			}
			arg.Digest, err = encodeDigest(pruned)
			if err != nil {
				return err
			}
		}
		return q.SetPullRequestAttachmentArtifacts(ctx, arg)
	})
}
