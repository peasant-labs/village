package contentrepair

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

// ReplaceThird installs the identity that the stored object authenticates to
// when it is neither the stored identity S nor absent: a third value O. The
// content revision advances by exactly one and both identity slots take O.
//
// The row must carry a usable stored identity and be at the expected revision.
// The object is read and authenticated once. An object that authenticates to S
// is not a divergence, so it is refused as a precondition mismatch. Absence, an
// unverifiable lookup, an unbuildable descriptor and a key failure are
// uncertainty and refuse; no identity is installed that the object was not
// shown to carry.
func ReplaceThird(ctx context.Context, deps Deps, opts Options) (Outcome, error) {
	if err := deps.validate(); err != nil {
		return Outcome{}, err
	}
	if opts.Action != ReplaceThirdValue {
		return Outcome{}, fmt.Errorf("operator content repair was refused in contentrepair.ReplaceThird because action %q is not replace-third-value; no database or storage access happened; select the action this executor owns", opts.Action)
	}
	tx, q, row, err := lockTranscript(ctx, deps, opts, "contentrepair.ReplaceThird")
	if err != nil {
		return Outcome{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	stored, usable := storedIdentity(row)
	if !usable || row.ContentRevision != opts.ExpectedRevision {
		return refuse(ctx, deps, tx, opts, row, PreconditionMismatch)
	}
	third, class, proceed, err := classifyForReplace(ctx, deps, row, stored)
	if err != nil {
		return Outcome{}, err
	}
	if !proceed {
		return refuse(ctx, deps, tx, opts, row, class)
	}

	// The identity columns are fenced against writers without the marker. It is
	// set only now, after every check, for this one statement.
	if _, err = tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version', '1', true)"); err != nil {
		return Outcome{}, fmt.Errorf("operator content repair could not mark its transaction as a transcript writer in contentrepair.ReplaceThird; the row is unchanged; restore PostgreSQL connectivity and retry: %w", err)
	}
	next := row.ContentRevision + 1
	affected, err := q.RepairTranscriptContentIdentity(ctx, sqlc.RepairTranscriptContentIdentityParams{
		ID:                      row.ID,
		ContentHash:             pgtype.Text{String: string(third.Hash()), Valid: true},
		BlobSizeBytes:           pgtype.Int8{Int64: third.PlaintextSize(), Valid: true},
		ContentRevision:         next,
		ExpectedContentRevision: row.ContentRevision,
		// The expected values are the row's raw columns, so the statement
		// matches only the row this transaction read and locked.
		ExpectedHash:          row.ContentHash,
		ExpectedSize:          row.BlobSizeBytes,
		ExpectedSegmentedHash: row.SegmentedIdentityHash,
		ExpectedSegmentedSize: row.SegmentedIdentitySize,
	})
	if err != nil {
		return Outcome{}, fmt.Errorf("operator content repair could not install the third identity in contentrepair.ReplaceThird; the row is unchanged; retry after the cause is repaired: %w", err)
	}
	if affected != 1 {
		return Outcome{}, fmt.Errorf("operator content repair changed %d rows instead of 1 in contentrepair.ReplaceThird under a row lock; the transaction was rolled back and nothing changed; inspect the transcript before retrying", affected)
	}
	resulting := ledgerIdentity{
		hash: pgtype.Text{String: string(third.Hash()), Valid: true},
		size: pgtype.Int8{Int64: third.PlaintextSize(), Valid: true},
	}
	if err = insertLedgerResult(ctx, q, opts, Applied, "", row, ledgerPair(row), next, resulting); err != nil {
		return Outcome{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Outcome{}, fmt.Errorf("operator content repair could not confirm its commit in contentrepair.ReplaceThird; the result is unknown; query transcript_content_repairs for this repair ID before retrying: %w", err)
	}
	return Outcome{RepairID: opts.RepairID, TranscriptID: opts.TranscriptID, Action: opts.Action, Phase: Applied,
		ContentRevision: next, RecordedAt: deps.Now()}, nil
}

// classifyForReplace reads the old object once and returns the identity it
// authenticates to. It proceeds only for a third value different from stored.
func classifyForReplace(ctx context.Context, deps Deps, row sqlc.Transcript, stored storage.ContentIdentity) (storage.ContentIdentity, FailureClass, bool, error) {
	descriptor, err := storage.NewBlobDescriptor(storage.ObjectKey(row.BlobKey), row.WrappedDataKey, storage.EncryptionAlgorithm(row.EncryptionAlgorithm), storage.KeyVersion(row.KeyVersion))
	if err != nil {
		// A descriptor that cannot be built cannot show the object either way.
		return storage.ContentIdentity{}, ObjectUncertain, false, nil
	}
	// A third value may be any size, so the read is bounded by the request
	// limit rather than by the stored size.
	pending, _ := storage.NewLoadedContentIdentity(nil, nil)
	_, actual, err := deps.Blobs.ReadBounded(ctx, uuid.UUID(row.ID.Bytes), descriptor, pending, maxDeclareReadBytes)
	switch {
	case err == nil:
		if actual.Hash() == stored.Hash() && actual.PlaintextSize() == stored.PlaintextSize() {
			return storage.ContentIdentity{}, PreconditionMismatch, false, nil
		}
		return actual, "", true, nil
	case ctx.Err() != nil:
		return storage.ContentIdentity{}, "", false, fmt.Errorf("operator content repair was cancelled in contentrepair.ReplaceThird while reading the stored object; nothing was changed; retry: %w", ctx.Err())
	default:
		// Absence, an unverifiable lookup, a failed authentication and a key
		// custody failure are the same reading: the object was not shown to
		// authenticate, so nothing is installed.
		return storage.ContentIdentity{}, ObjectUncertain, false, nil
	}
}
