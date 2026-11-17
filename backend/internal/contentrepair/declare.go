package contentrepair

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/contentstate"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

// maxDeclareReadBytes bounds the ciphertext read while the row lock is held
// when the row carries no established identity to size the read from. It is the
// publish request limit plus the ciphertext overhead.
const maxDeclareReadBytes int64 = 256<<20 + storage.CiphertextOverheadBytes

// Deps are the collaborators of the repair executors. Queries is bound to the
// pool; each executor rebinds it to its own transaction.
type Deps struct {
	Pool    *pgxpool.Pool
	Queries *sqlc.Queries
	Blobs   storage.TranscriptBlobStore
	Now     func() time.Time
}

func (d Deps) validate() error {
	if d.Pool == nil || d.Queries == nil || d.Blobs == nil || d.Now == nil {
		return errors.New("operator content repair could not start because its pool, queries, blob store or clock is missing in contentrepair.Deps before any database or storage access; no repair was attempted; construct every dependency in the server runtime and retry")
	}
	return nil
}

// Outcome is the terminal result of one repair run. A refusal is a normal
// outcome with its closed failure class, not an error.
type Outcome struct {
	RepairID        uuid.UUID
	TranscriptID    uuid.UUID
	Action          Action
	Phase           Phase
	FailureClass    FailureClass // empty unless Phase is Refused
	ContentRevision int64
	RecordedAt      time.Time
}

// Declare marks a present transcript's content unavailable when its stored
// object cannot be shown to exist. It never changes the identity pair or the
// content revision. An object that authenticates is refused, because the
// content is not lost.
func Declare(ctx context.Context, deps Deps, opts Options) (Outcome, error) {
	if err := deps.validate(); err != nil {
		return Outcome{}, err
	}
	if opts.Action != DeclareUnavailable {
		return Outcome{}, fmt.Errorf("operator content repair was refused in contentrepair.Declare because action %q is not declare-unavailable; no database or storage access happened; select the action this executor owns", opts.Action)
	}
	tx, q, row, err := lockTranscript(ctx, deps, opts, "contentrepair.Declare")
	if err != nil {
		return Outcome{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	prior := ledgerPair(row)
	if contentstate.State(row.ContentState) != contentstate.Present || row.ContentRevision != opts.ExpectedRevision {
		return refuse(ctx, deps, tx, opts, row, PreconditionMismatch)
	}

	class, proceed, err := classifyObject(ctx, deps, row)
	if err != nil {
		return Outcome{}, err
	}
	if !proceed {
		return refuse(ctx, deps, tx, opts, row, class)
	}

	affected, err := q.DeclareTranscriptContentUnavailable(ctx, sqlc.DeclareTranscriptContentUnavailableParams{
		ID:                      row.ID,
		ExpectedBlobKey:         row.BlobKey,
		ExpectedContentRevision: row.ContentRevision,
		ExpectedHash:            row.ContentHash,
		ExpectedSize:            row.BlobSizeBytes,
		ExpectedSegmentedHash:   row.SegmentedIdentityHash,
		ExpectedSegmentedSize:   row.SegmentedIdentitySize,
	})
	if err != nil {
		return Outcome{}, fmt.Errorf("operator content repair could not mark the content unavailable in contentrepair.Declare; the row is unchanged; retry after the cause is repaired: %w", err)
	}
	if affected != 1 {
		return Outcome{}, fmt.Errorf("operator content repair changed %d rows instead of 1 in contentrepair.Declare under a row lock; the transaction was rolled back and nothing changed; inspect the transcript before retrying", affected)
	}
	if err = insertLedger(ctx, q, opts, Applied, "", row, prior); err != nil {
		return Outcome{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Outcome{}, fmt.Errorf("operator content repair could not confirm its commit in contentrepair.Declare; the result is unknown; query transcript_content_repairs for this repair ID before retrying: %w", err)
	}
	return Outcome{RepairID: opts.RepairID, TranscriptID: opts.TranscriptID, Action: opts.Action, Phase: Applied,
		ContentRevision: row.ContentRevision, RecordedAt: deps.Now()}, nil
}

// lockTranscript begins the repair transaction, attributes it to the operator
// and locks the transcript row. The caller owns the returned transaction and
// must roll it back. op names the calling executor in error messages.
func lockTranscript(ctx context.Context, deps Deps, opts Options, op string) (pgx.Tx, *sqlc.Queries, sqlc.Transcript, error) {
	tx, err := deps.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, sqlc.Transcript{}, fmt.Errorf("operator content repair could not begin its transaction in %s; nothing was changed; restore PostgreSQL connectivity and retry: %w", op, err)
	}
	if err = setActor(ctx, tx, opts.ActorID); err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, nil, sqlc.Transcript{}, err
	}
	q := deps.Queries.WithTx(tx)
	row, err := q.GetTranscriptByIDForUpdate(ctx, pgtype.UUID{Bytes: opts.TranscriptID, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, nil, sqlc.Transcript{}, fmt.Errorf("operator content repair found no transcript in %s while locking the row; nothing was changed and no ledger row exists for a missing transcript; check -repair-transcript", op)
	}
	if err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, nil, sqlc.Transcript{}, fmt.Errorf("operator content repair could not lock the transcript in %s; nothing was changed; retry after PostgreSQL recovers: %w", op, err)
	}
	return tx, q, row, nil
}

// classifyObject reads the stored generation and decides whether declaring it
// unavailable may proceed. When it may not, the closed failure class is
// returned. Any other failure is an error: it records nothing.
func classifyObject(ctx context.Context, deps Deps, row sqlc.Transcript) (FailureClass, bool, error) {
	stored, known := storedIdentity(row)
	descriptor, err := storage.NewBlobDescriptor(storage.ObjectKey(row.BlobKey), row.WrappedDataKey, storage.EncryptionAlgorithm(row.EncryptionAlgorithm), storage.KeyVersion(row.KeyVersion))
	if err != nil {
		// A stored descriptor that cannot be built says nothing about whether
		// the object exists: the row may carry a descriptor this build cannot
		// decode for a live object. Absence is never inferred from it.
		return ObjectUncertain, false, nil
	}
	limit := maxDeclareReadBytes
	if known {
		limit = stored.PlaintextSize() + storage.CiphertextOverheadBytes
	}
	// A pending identity makes the read return what the object authenticates
	// to, so a different value can be told apart from an unreadable object.
	pending, _ := storage.NewLoadedContentIdentity(nil, nil)
	_, actual, err := deps.Blobs.ReadBounded(ctx, uuid.UUID(row.ID.Bytes), descriptor, pending, limit)
	switch {
	case err == nil:
		if known && actual.Hash() == stored.Hash() && actual.PlaintextSize() == stored.PlaintextSize() {
			return PreconditionMismatch, false, nil
		}
		return ObjectMismatch, false, nil
	case ctx.Err() != nil:
		return "", false, fmt.Errorf("operator content repair was cancelled in contentrepair.Declare while reading the stored object; nothing was changed; retry: %w", ctx.Err())
	case errors.Is(err, storage.ErrObjectAbsentProvider), errors.Is(err, storage.ErrObjectLookupUncertain):
		return "", true, nil
	case errors.Is(err, storage.ErrObjectIntegrityUncertain):
		return ObjectUncertain, false, nil
	default:
		return "", false, fmt.Errorf("operator content repair could not classify the stored object in contentrepair.Declare; nothing was changed and no ledger row was written; restore key custody or storage access and retry: %w", err)
	}
}

// refuse ends the locked transaction and records one refused row in a fresh
// one. It never writes the unavailable state.
func refuse(ctx context.Context, deps Deps, tx pgx.Tx, opts Options, row sqlc.Transcript, class FailureClass) (Outcome, error) {
	prior := ledgerPair(row)
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return Outcome{}, fmt.Errorf("operator content repair could not release its row lock in contentrepair.refuse before recording a refusal; no refusal was recorded; retry: %w", err)
	}
	fresh, err := deps.Pool.Begin(ctx)
	if err != nil {
		return Outcome{}, fmt.Errorf("operator content repair could not begin the refusal transaction in contentrepair.refuse; the refusal was not recorded; retry: %w", err)
	}
	defer func() { _ = fresh.Rollback(context.WithoutCancel(ctx)) }()
	if err = setActor(ctx, fresh, opts.ActorID); err != nil {
		return Outcome{}, err
	}
	if err = insertLedger(ctx, deps.Queries.WithTx(fresh), opts, Refused, class, row, prior); err != nil {
		return Outcome{}, err
	}
	if err = fresh.Commit(ctx); err != nil {
		return Outcome{}, fmt.Errorf("operator content repair could not confirm its refusal commit in contentrepair.refuse; the record is unknown; query transcript_content_repairs for this repair ID before retrying: %w", err)
	}
	return Outcome{RepairID: opts.RepairID, TranscriptID: opts.TranscriptID, Action: opts.Action, Phase: Refused,
		FailureClass: class, ContentRevision: row.ContentRevision, RecordedAt: deps.Now()}, nil
}

func setActor(ctx context.Context, tx pgx.Tx, actor uuid.UUID) error {
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true)", actor.String()); err != nil {
		return fmt.Errorf("operator content repair could not attribute its transaction in contentrepair.setActor; nothing was changed; restore PostgreSQL connectivity and retry: %w", err)
	}
	return nil
}

// ledgerIdentity is the established identity pair as the ledger stores it:
// both columns set or both NULL.
type ledgerIdentity struct {
	hash pgtype.Text
	size pgtype.Int8
}

func ledgerPair(row sqlc.Transcript) ledgerIdentity {
	if identity, ok := storedIdentity(row); ok {
		return ledgerIdentity{
			hash: pgtype.Text{String: string(identity.Hash()), Valid: true},
			size: pgtype.Int8{Int64: identity.PlaintextSize(), Valid: true},
		}
	}
	return ledgerIdentity{}
}

// storedIdentity is the established identity S of a locked row: the segmented
// pair when present, else a well-formed primary pair. A malformed raw pair is
// not an identity, so the ledger records NULL for it.
func storedIdentity(row sqlc.Transcript) (storage.ContentIdentity, bool) {
	if row.SegmentedIdentityHash.Valid && row.SegmentedIdentitySize.Valid {
		identity, err := storage.NewContentIdentity(row.SegmentedIdentityHash.String, row.SegmentedIdentitySize.Int64)
		return identity, err == nil
	}
	if row.ContentHash.Valid && row.BlobSizeBytes.Valid {
		identity, err := storage.NewContentIdentity(row.ContentHash.String, row.BlobSizeBytes.Int64)
		return identity, err == nil
	}
	return storage.ContentIdentity{}, false
}

func insertLedger(ctx context.Context, q *sqlc.Queries, opts Options, phase Phase, class FailureClass, row sqlc.Transcript, prior ledgerIdentity) error {
	params := sqlc.InsertTranscriptContentRepairParams{
		RepairID:              pgtype.UUID{Bytes: opts.RepairID, Valid: true},
		Phase:                 phase.String(),
		Action:                opts.Action.String(),
		TranscriptID:          pgtype.UUID{Bytes: opts.TranscriptID, Valid: true},
		AuthorizationRef:      opts.AuthorizationRef,
		Reason:                opts.Reason,
		PriorRevision:         row.ContentRevision,
		PriorIdentityHash:     prior.hash,
		PriorIdentitySize:     prior.size,
		ResultingRevision:     row.ContentRevision,
		ResultingIdentityHash: prior.hash,
		ResultingIdentitySize: prior.size,
	}
	if class != "" {
		params.FailureClass = pgtype.Text{String: class.String(), Valid: true}
	}
	if opts.Reconciles != uuid.Nil {
		params.ReconcilesRepairID = pgtype.UUID{Bytes: opts.Reconciles, Valid: true}
	}
	if err := q.InsertTranscriptContentRepair(ctx, params); err != nil {
		return fmt.Errorf("operator content repair could not write its %s ledger row in contentrepair.insertLedger; the transaction was not committed; check the repair ID is unused and the ledger migration is applied: %w", phase, err)
	}
	return nil
}
