package handler

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgtype"
)

func (h *Handler) startPublishAttachmentWork(ctx context.Context, owner pgtype.UUID, repo string, narrowedID pgtype.UUID, refresh bool) {
	ctx = context.WithoutCancel(ctx)
	h.attachmentWork.Add(1)
	go func() {
		defer h.attachmentWork.Done()
		if repo != "" {
			err := h.completeAttachmentsForPublishedTranscript(ctx, owner, repo)
			if err != nil {
				log.Printf("pull request attachment completion after publish failed: %v", err)
			}
		}
		if refresh {
			if err := h.refreshAttachmentsForTranscriptVisibility(ctx, narrowedID); err != nil {
				log.Printf("pull request attachment refresh after a republish narrowing failed: %v", err)
			}
		}
	}()
}

// DrainPublishAttachments waits after HTTP shutdown, when no new work can start.
func (h *Handler) DrainPublishAttachments(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		h.attachmentWork.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("publish attachment work still running at shutdown: %w", ctx.Err())
	}
}
