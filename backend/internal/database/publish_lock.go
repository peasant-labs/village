package database

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SessionPublishLockKey is the shared source-session lock identity used by
// publication and maintenance writers. It is never an authorization token.
func SessionPublishLockKey(owner pgtype.UUID, localID string) string {
	return fmt.Sprintf("village:association-publish:%x:session:%s", owner.Bytes, localID)
}

// WithSessionPublishLock runs a maintenance mutation on the same pinned
// physical session that owns the publish lock. A session whose unlock cannot
// be proved is closed rather than returned to the pool.
func WithSessionPublishLock(ctx context.Context, pool *pgxpool.Pool, owner pgtype.UUID, localID string, fn func(*pgxpool.Conn) error) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("maintenance could not acquire the publish-lock connection; no row changed; restore PostgreSQL and retry: %w", err)
	}
	key := SessionPublishLockKey(owner, localID)
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(cleanupCtx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", key).Scan(&unlocked); err != nil {
			physical := conn.Hijack()
			if closeErr := physical.Close(cleanupCtx); closeErr != nil {
				slog.Error("maintenance publish-lock session could not be closed", "error", closeErr)
			}
			return
		}
		conn.Release()
	}()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended($1, 0))", key); err != nil {
		return fmt.Errorf("maintenance could not acquire the transcript publish lock; no row changed; restore PostgreSQL and retry: %w", err)
	}
	return fn(conn)
}
