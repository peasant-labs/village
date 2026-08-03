package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// This file is the ONE transaction scaffold in the handler package — the five
// hand-rolled Begin/Rollback/Commit copies it replaced live on in git history.
// The public package paths select an attribution mode and share one private
// pinned-connection implementation:
//
//	inTxRawOnConn private scaffold (fn sees the Querier and, in production, the Tx)
//	inTxAs        authenticated-user mutations — THE entry point for transcript writes
//	inTxAsOnConn  authenticated mutations on an already-pinned physical connection
//	inTxAsSystem  sanctioned non-user mutations (seeds, backfills, ops runbooks)
//
// The migration-026 governance-audit triggers are FAIL-CLOSED: every
// INSERT / governance-axis UPDATE / DELETE on transcripts must carry the
// txn-local GUC app.actor_id or the statement aborts. inTxAs/inTxAsSystem are
// how handlers carry it; there is no owner fallback and no other sanctioned
// path (docs/deletion-data-lifecycle-model.md §7).

// inTxRaw runs fn inside one transaction against a Querier bound to that tx.
// pool == nil is the unit-test seam (mockQuerier, no real txn, no triggers):
// fn runs against h.queries with a nil tx. Real servers always have a pool.
// Rollback after Commit is a no-op, so it is always deferred.
type txBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

// SystemTranscriptCreateResult preserves the transaction outcome needed by
// runtime seed callers to decide whether candidate ciphertext may be deleted.
type SystemTranscriptCreateResult struct {
	Row        sqlc.Transcript
	Completion TransactionCompletion
	Err        error
}

func rollbackTxBestEffort(operation string, tx pgx.Tx) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tx.Rollback(cleanupCtx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		slog.Error("handler transaction cleanup failed",
			"operation", operation,
			"stage", "rollback",
			"consequence", "the connection may be discarded by pgx instead of reused",
			"remediation", "inspect PostgreSQL connectivity and transaction health",
			"error", fmt.Errorf("transaction rollback failed: %w", err))
	}
}

func (h *Handler) inTxRawOnConn(ctx context.Context, beginner txBeginner, actorID string, fn func(q Querier, tx pgx.Tx) error) error {
	if beginner == nil {
		return fn(h.queries, nil)
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTxBestEffort("database_mutation", tx)
	if actorID != "" {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", actorID); err != nil {
			return err
		}
	}
	if err := fn(sqlc.New(tx), tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (h *Handler) inTxRaw(ctx context.Context, actorID string, fn func(q Querier, tx pgx.Tx) error) error {
	if h.pool == nil {
		return h.inTxRawOnConn(ctx, nil, actorID, fn)
	}
	return h.inTxRawOnConn(ctx, h.pool, actorID, fn)
}

// inTxAs stamps the transaction with the authenticated actor's UUID and runs fn.
// The actor MUST be a Valid, real user UUID: a zero pgtype.UUID would render as
// 00000000-… and silently impersonate the SYSTEM actor, so it fails closed here
// before any transaction is opened. System attribution has exactly one route:
// inTxAsSystem.
func (h *Handler) inTxAs(ctx context.Context, actor pgtype.UUID, fn func(q Querier) error) error {
	if !actor.Valid {
		return fmt.Errorf("inTxAs: actor not set (fail-closed attribution) — authenticate the user, or use inTxAsSystem for sanctioned system mutations")
	}
	return h.inTxRaw(ctx, uuid.UUID(actor.Bytes).String(), func(q Querier, _ pgx.Tx) error {
		return fn(q)
	})
}

func (h *Handler) inTxAsOnConn(ctx context.Context, conn *pgxpool.Conn, actor pgtype.UUID, fn func(q Querier) error) error {
	if !actor.Valid {
		return fmt.Errorf("inTxAsOnConn: actor not set (fail-closed attribution) — authenticate the user")
	}
	if conn == nil {
		return h.inTxRawOnConn(ctx, nil, uuid.UUID(actor.Bytes).String(), func(q Querier, _ pgx.Tx) error { return fn(q) })
	}
	return h.inTxRawOnConn(ctx, conn, uuid.UUID(actor.Bytes).String(), func(q Querier, _ pgx.Tx) error { return fn(q) })
}

// inEncryptedTxAsOnConn preserves commit classification while using the same
// physical connection that owns the publish advisory locks.
func (h *Handler) inEncryptedTxAsOnConn(ctx context.Context, conn *pgxpool.Conn, actor pgtype.UUID, fn func(q Querier) error) transactionResult {
	if !actor.Valid {
		return transactionResult{Completion: TransactionKnownRollback, Err: errors.New("encrypted transcript transaction did not start because the authenticated actor is absent in handler.inEncryptedTxAsOnConn during locked publish persistence; no database state changed; authenticate the caller and retry")}
	}
	var beginner txBeginner
	if conn != nil {
		beginner = conn
	}
	return h.inEncryptedTxWithActor(ctx, beginner, uuid.UUID(actor.Bytes).String(), fn)
}

// inTxAsSystem attributes the transaction's governance events to the reserved
// system actor (database.SystemActorID).
func (h *Handler) inTxAsSystem(ctx context.Context, fn func(q Querier) error) error {
	return h.inTxRaw(ctx, database.SystemActorID, func(q Querier, _ pgx.Tx) error {
		return fn(q)
	})
}

// CreateTranscriptAsSystemResult is the narrow runtime seed boundary. It installs
// both fixed transaction-local compatibility markers and exposes no arbitrary
// GUC or general query callback to callers.
func (h *Handler) CreateTranscriptAsSystemResult(ctx context.Context, params sqlc.CreateTranscriptParams) SystemTranscriptCreateResult {
	var beginner txBeginner
	if h.pool != nil {
		beginner = h.pool
	}
	return h.createTranscriptAsSystem(ctx, beginner, params, func(q Querier) (sqlc.Transcript, error) {
		return q.CreateTranscript(ctx, params)
	})
}

func (h *Handler) createTranscriptAsSystem(ctx context.Context, beginner txBeginner, _ sqlc.CreateTranscriptParams, create func(Querier) (sqlc.Transcript, error)) SystemTranscriptCreateResult {
	var created sqlc.Transcript
	result := h.inEncryptedTxWithActor(ctx, beginner, database.SystemActorID, func(q Querier) error {
		var err error
		created, err = create(q)
		return err
	})
	if result.Err != nil {
		result.Err = fmt.Errorf("system transcript creation failed because the fixed actor/writer-marked transaction did not complete in handler.CreateTranscriptAsSystemResult during runtime seed persistence; completion=%d determines whether candidate ciphertext may be deleted; verify PostgreSQL connectivity, migration 031, and the supplied transcript values before retrying: %w", result.Completion, result.Err)
	}
	return SystemTranscriptCreateResult{Row: created, Completion: result.Completion, Err: result.Err}
}

func (h *Handler) inEncryptedTx(ctx context.Context, actor pgtype.UUID, fn func(q Querier) error) transactionResult {
	if !actor.Valid {
		return transactionResult{Completion: TransactionKnownRollback, Err: errors.New("encrypted transcript transaction did not start because the authenticated actor is absent in handler.inEncryptedTx during storage mutation; no database state changed; authenticate the caller and retry")}
	}
	var beginner txBeginner
	if h.pool != nil {
		beginner = h.pool
	}
	return h.inEncryptedTxWithActor(ctx, beginner, uuid.UUID(actor.Bytes).String(), fn)
}

func (h *Handler) inEncryptedTxWithActor(ctx context.Context, beginner txBeginner, actorID string, fn func(q Querier) error) transactionResult {
	if beginner == nil {
		if err := fn(h.queries); err != nil {
			return transactionResult{Completion: TransactionKnownRollback, Err: err}
		}
		return transactionResult{Completion: TransactionCommitted}
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return transactionResult{Completion: TransactionKnownRollback, Err: fmt.Errorf("encrypted transcript transaction did not begin because PostgreSQL rejected the begin operation in handler.inEncryptedTx during storage mutation; no row was changed; restore database connectivity and retry: %w", err)}
	}
	defer rollbackTxBestEffort("encrypted_transcript_mutation", tx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", actorID); err != nil {
		return transactionResult{Completion: TransactionKnownRollback, Err: fmt.Errorf("encrypted transcript transaction was rolled back because actor and writer markers could not be installed in handler.inEncryptedTx before storage mutation; no row was changed; verify migration 031 and retry: %w", err)}
	}
	if err := fn(sqlc.New(tx)); err != nil {
		return transactionResult{Completion: TransactionKnownRollback, Err: err}
	}
	if err := tx.Commit(ctx); err != nil {
		if errors.Is(err, pgx.ErrTxCommitRollback) {
			return transactionResult{Completion: TransactionKnownRollback, Err: err}
		}
		return transactionResult{Completion: TransactionCommitAmbiguous, Err: fmt.Errorf("encrypted transcript transaction outcome is ambiguous because PostgreSQL did not confirm commit in handler.inEncryptedTx after storage mutation; referenced objects must be retained and reconciled against the authoritative primary before cleanup: %w", err)}
	}
	return transactionResult{Completion: TransactionCommitted}
}
