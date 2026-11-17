package contentrepair

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/database"
)

// beginTx starts a transaction on the connection that holds the publish lock
// while one is held, and on the pool otherwise.
func (d Deps) beginTx(ctx context.Context) (pgx.Tx, error) {
	if d.conn != nil {
		return d.conn.Begin(ctx)
	}
	return d.Pool.Begin(ctx)
}

// withPublishLock runs one repair while holding the transcript's source-session
// publish lock, the same lock publication, deletion and the maintenance writers
// take. Publication and deletion therefore wait for a repair instead of
// interleaving with it, and a repair waits for a publication in flight. The
// lock is taken before the row lock, the order every other writer uses, and is
// held across a restore's whole attempt, object write and terminal row.
//
// An account deletion cascades without taking this lock. A repair therefore
// still re-locks the row inside its own transaction and treats a vanished row as
// a normal outcome.
func withPublishLock(ctx context.Context, deps Deps, opts Options, op string, run func(context.Context, Deps, Options) (Outcome, error)) (Outcome, error) {
	row, err := deps.Queries.GetTranscriptByID(ctx, pgtype.UUID{Bytes: opts.TranscriptID, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return Outcome{}, fmt.Errorf("operator content repair found no transcript in %s while resolving its publish lock; nothing was changed and no ledger row exists for a missing transcript; check -repair-transcript", op)
	}
	if err != nil {
		return Outcome{}, fmt.Errorf("operator content repair could not resolve the transcript's publish lock in %s; nothing was changed; retry after PostgreSQL recovers: %w", op, err)
	}
	var outcome Outcome
	err = database.WithSessionPublishLock(ctx, deps.Pool, row.OwnerID, row.LocalID, func(conn *pgxpool.Conn) error {
		locked := deps
		locked.conn = conn
		var runErr error
		outcome, runErr = run(ctx, locked, opts)
		return runErr
	})
	return outcome, err
}
