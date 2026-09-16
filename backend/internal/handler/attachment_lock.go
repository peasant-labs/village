package handler

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
)

// attachmentLockNamespace separates an attachment's advisory-lock key from every
// other advisory lock the process takes.
const attachmentLockNamespace = "village:attachment:"

// withAttachmentLock serializes the lifecycle work for one attachment.
//
// The work spans several transactions and at least one GitHub call, so a
// transaction-scoped lock cannot span it. A session advisory lock on a pinned
// connection can: two concurrent confirms (or a refresh racing a publish) then
// cannot both post, both bind, or half-compensate each other. The unlock runs on
// a context that cannot be cancelled, so a client disconnect still releases it.
func (h *Handler) withAttachmentLock(ctx context.Context, attachmentID pgtype.UUID, fn func() error) error {
	if h.pool == nil {
		// Unit tests compose the handler without a pool; there is nothing to
		// serialize against in-process state that no other goroutine shares.
		return fn()
	}
	conn, err := h.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("could not acquire a connection for the attachment lock: %w", err)
	}
	defer conn.Release()

	key := attachmentLockNamespace + uuidFromPg(attachmentID).String()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtext($1))", key); err != nil {
		return fmt.Errorf("could not take the attachment lock: %w", err)
	}
	defer func() {
		// Released on a context that cannot be cancelled, so a disconnected
		// client still lets go; returning the connection would release it anyway.
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock(hashtext($1))", key)
	}()

	return fn()
}
