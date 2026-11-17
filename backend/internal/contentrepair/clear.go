package contentrepair

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

// Clear removes a malformed identity pair from a revision-one transcript so the
// row carries no identity at all. It never invents an identity and never
// changes the content revision.
//
// A clear is refused unless the locked row is at revision one, carries a
// non-NULL malformed content hash and has no usable identity. The old object is
// read and authenticated once. A clear has no expected bytes to compare, so
// whether the bytes equal or differ from anything decides nothing. What matters
// is only that the object authenticates: absence, an unverifiable lookup and a
// key failure are uncertainty and refuse.
func Clear(ctx context.Context, deps Deps, opts Options) (Outcome, error) {
	if err := deps.validate(); err != nil {
		return Outcome{}, err
	}
	if opts.Action != ClearMalformedIdentity {
		return Outcome{}, fmt.Errorf("operator content repair was refused in contentrepair.Clear because action %q is not clear-malformed-identity; no database or storage access happened; select the action this executor owns", opts.Action)
	}
	return withPublishLock(ctx, deps, opts, "contentrepair.Clear", clearLocked)
}

func clearLocked(ctx context.Context, deps Deps, opts Options) (Outcome, error) {
	tx, q, row, err := lockTranscript(ctx, deps, opts, "contentrepair.Clear")
	if err != nil {
		return Outcome{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if !clearable(row) || row.ContentRevision != opts.ExpectedRevision {
		return refuse(ctx, deps, tx, opts, row, PreconditionMismatch)
	}
	class, proceed, err := classifyForClear(ctx, deps, row)
	if err != nil {
		return Outcome{}, err
	}
	if !proceed {
		return refuse(ctx, deps, tx, opts, row, class)
	}

	// The identity columns are fenced against writers without the marker. It is
	// set only now, after every check, for this one statement.
	if _, err = tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version', '1', true)"); err != nil {
		return Outcome{}, fmt.Errorf("operator content repair could not mark its transaction as a transcript writer in contentrepair.Clear; the row is unchanged; restore PostgreSQL connectivity and retry: %w", err)
	}
	// The expected values are the row's raw columns, not the NULL identity the
	// ledger records, so a malformed hash matches only the row it was read from.
	affected, err := q.RepairTranscriptContentIdentity(ctx, sqlc.RepairTranscriptContentIdentityParams{
		ID:                      row.ID,
		ContentRevision:         row.ContentRevision,
		ExpectedContentRevision: row.ContentRevision,
		ExpectedHash:            row.ContentHash,
		ExpectedSize:            row.BlobSizeBytes,
		ExpectedSegmentedHash:   row.SegmentedIdentityHash,
		ExpectedSegmentedSize:   row.SegmentedIdentitySize,
		// ContentHash and BlobSizeBytes stay NULL: the new pair is NULL/NULL.
	})
	if err != nil {
		return Outcome{}, fmt.Errorf("operator content repair could not clear the malformed identity in contentrepair.Clear; the row is unchanged; retry after the cause is repaired: %w", err)
	}
	if affected != 1 {
		return Outcome{}, fmt.Errorf("operator content repair changed %d rows instead of 1 in contentrepair.Clear under a row lock; the transaction was rolled back and nothing changed; inspect the transcript before retrying", affected)
	}
	if err = insertLedger(ctx, q, opts, Applied, "", row, ledgerPair(row)); err != nil {
		return Outcome{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Outcome{}, fmt.Errorf("operator content repair could not confirm its commit in contentrepair.Clear; the result is unknown; query transcript_content_repairs for this repair ID before retrying: %w", err)
	}
	return Outcome{RepairID: opts.RepairID, TranscriptID: opts.TranscriptID, Action: opts.Action, Phase: Applied,
		ContentRevision: row.ContentRevision, RecordedAt: deps.Now()}, nil
}

// clearable reports whether the locked row is a revision-one row whose content
// hash is non-NULL but not a canonical hash, with no usable identity.
func clearable(row sqlc.Transcript) bool {
	if row.ContentRevision != 1 || !row.ContentHash.Valid {
		return false
	}
	if _, usable := storedIdentity(row); usable {
		return false
	}
	// Size zero is always valid, so this fails only for a malformed hash.
	_, err := storage.NewContentIdentity(row.ContentHash.String, 0)
	return err != nil
}

// classifyForClear reads the old object once. Authentication permits the
// clear; any failure to authenticate is uncertainty and refuses.
func classifyForClear(ctx context.Context, deps Deps, row sqlc.Transcript) (FailureClass, bool, error) {
	descriptor, err := storage.NewBlobDescriptor(storage.ObjectKey(row.BlobKey), row.WrappedDataKey, storage.EncryptionAlgorithm(row.EncryptionAlgorithm), storage.KeyVersion(row.KeyVersion))
	if err != nil {
		// A descriptor that cannot be built cannot show the object either way.
		return ObjectUncertain, false, nil
	}
	pending, _ := storage.NewLoadedContentIdentity(nil, nil)
	_, _, err = deps.Blobs.ReadBounded(ctx, uuid.UUID(row.ID.Bytes), descriptor, pending, maxDeclareReadBytes)
	switch {
	case err == nil:
		return "", true, nil
	case ctx.Err() != nil:
		return "", false, fmt.Errorf("operator content repair was cancelled in contentrepair.Clear while reading the stored object; nothing was changed; retry: %w", ctx.Err())
	default:
		// Absence, an unverifiable lookup, a failed authentication and a key
		// custody failure are the same reading: the object was not shown to
		// authenticate, so nothing is cleared.
		return ObjectUncertain, false, nil
	}
}
