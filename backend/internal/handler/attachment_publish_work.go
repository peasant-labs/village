package handler

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

type publishAttachmentKey struct {
	owner pgtype.UUID
	repo  string
}

type publishAttachmentState struct {
	dirty    bool
	complete bool
	pending  map[pgtype.UUID]struct{}
}

func (h *Handler) startPublishAttachmentWork(ctx context.Context, owner pgtype.UUID, repo string, complete bool, narrowedID pgtype.UUID, refresh bool) {
	key := publishAttachmentKey{owner: owner, repo: strings.ToLower(repo)}
	h.attachmentWorkMu.Lock()
	if h.attachmentWorkPending == nil {
		h.attachmentWorkPending = make(map[publishAttachmentKey]*publishAttachmentState)
	}
	state, exists := h.attachmentWorkPending[key]
	if !exists {
		state = &publishAttachmentState{pending: make(map[pgtype.UUID]struct{})}
		h.attachmentWorkPending[key] = state
		h.attachmentWork.Add(1)
	}
	state.dirty = true
	state.complete = state.complete || complete
	if refresh {
		state.pending[narrowedID] = struct{}{}
	}
	h.attachmentWorkMu.Unlock()
	if exists {
		return
	}
	ctx = context.WithoutCancel(ctx)
	go h.runPublishAttachmentWork(ctx, key, state)
}

func (h *Handler) runPublishAttachmentWork(ctx context.Context, key publishAttachmentKey, state *publishAttachmentState) {
	defer h.attachmentWork.Done()
	for {
		h.attachmentWorkMu.Lock()
		if !state.dirty {
			delete(h.attachmentWorkPending, key)
			h.attachmentWorkMu.Unlock()
			return
		}
		state.dirty = false
		complete := state.complete
		state.complete = false
		pending := state.pending
		state.pending = make(map[pgtype.UUID]struct{})
		h.attachmentWorkMu.Unlock()
		if complete && key.repo != "" {
			err := h.completeAttachmentsForPublishedTranscript(ctx, key.owner, key.repo)
			if err != nil {
				log.Printf("pull request attachment completion after publish failed: %v", err)
			}
		}
		for narrowedID := range pending {
			if err := h.refreshAttachmentsForTranscriptVisibility(ctx, narrowedID); err != nil {
				log.Printf("pull request attachment refresh after a republish narrowing failed: %v", err)
			}
		}
	}
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
