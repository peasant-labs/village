package contentrepair

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

// terminalRecordTimeout bounds the transaction that records a restore's
// terminal row. It runs detached from the caller's context, because the
// attempt row is already committed and must be resolved even after the caller
// is cancelled.
const terminalRecordTimeout = 30 * time.Second

// Restorer is the object-store capability a restore needs beyond the ordinary
// blob store: authenticating a preserved ciphertext copy without writing, and
// then writing exactly those bytes over the transcript's own object.
type Restorer interface {
	AuthenticateRestoreSource(ctx context.Context, id uuid.UUID, d storage.BlobDescriptor, source storage.ObjectKey, attested storage.ContentIdentity) ([]byte, error)
	RestoreAuthenticatedGeneration(ctx context.Context, id uuid.UUID, d storage.BlobDescriptor, ciphertext []byte) (storage.ContentIdentity, error)
}

// Restore puts a preserved ciphertext copy back as the transcript's stored
// object and marks the content present. The stored identity and the content
// revision never change: the preserved bytes must authenticate to exactly the
// identity the row already attests.
//
// The source is authenticated before any write; a source that is missing,
// unreadable or does not authenticate refuses and leaves no attempt row. Once
// it authenticates, the attempt row is committed before the object is touched,
// because the write cannot be rolled back with a database transaction. The
// terminal row is written after the object is re-read: it is applied only when
// the write was proved by a read-back that authenticates to the stored identity.
func Restore(ctx context.Context, deps Deps, opts Options) (Outcome, error) {
	if err := deps.validate(); err != nil {
		return Outcome{}, err
	}
	if opts.Action != RestoreAttestedGeneration {
		return Outcome{}, fmt.Errorf("operator content repair was refused in contentrepair.Restore because action %q is not restore-attested-generation; no database or storage access happened; select the action this executor owns", opts.Action)
	}
	restorer, ok := deps.Blobs.(Restorer)
	if !ok {
		return Outcome{}, errors.New("operator content repair could not restore in contentrepair.Restore because the blob store cannot authenticate a restore source or write an authenticated generation; no database or storage access happened; compose the encrypted transcript store and retry")
	}
	tx, q, row, err := lockTranscript(ctx, deps, opts, "contentrepair.Restore")
	if err != nil {
		return Outcome{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// A restore attests the identity the row already carries, so it needs one.
	stored, usable := storedIdentity(row)
	if !usable || row.ContentRevision != opts.ExpectedRevision {
		return refuse(ctx, deps, tx, opts, row, PreconditionMismatch)
	}
	descriptor, err := storage.NewBlobDescriptor(storage.ObjectKey(row.BlobKey), row.WrappedDataKey, storage.EncryptionAlgorithm(row.EncryptionAlgorithm), storage.KeyVersion(row.KeyVersion))
	if err != nil {
		// A descriptor that cannot be built cannot authenticate any source.
		return refuse(ctx, deps, tx, opts, row, ObjectUncertain)
	}
	if opts.SourceKey == descriptor.ObjectKey() {
		// Restoring an object from itself would copy the divergent bytes.
		return refuse(ctx, deps, tx, opts, row, GuardRefused)
	}
	transcriptID := uuid.UUID(row.ID.Bytes)
	ciphertext, err := restorer.AuthenticateRestoreSource(ctx, transcriptID, descriptor, opts.SourceKey, stored)
	switch {
	case err == nil:
	case ctx.Err() != nil:
		return Outcome{}, fmt.Errorf("operator content repair was cancelled in contentrepair.Restore while authenticating the restore source; nothing was changed and no attempt was recorded; retry: %w", ctx.Err())
	case errors.Is(err, storage.ErrRestoreSourceIdentityMismatch):
		return refuse(ctx, deps, tx, opts, row, ObjectMismatch)
	default:
		// A missing, unreadable, oversized or unauthenticating source, and a
		// key custody failure, are the same reading: the source was not shown
		// to hold the attested generation, so nothing is written.
		return refuse(ctx, deps, tx, opts, row, ObjectUncertain)
	}

	prior := ledgerPair(row)
	if err = insertLedger(ctx, q, opts, Attempted, "", row, prior); err != nil {
		return Outcome{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Outcome{}, fmt.Errorf("operator content repair could not confirm the attempt commit in contentrepair.Restore; the object was not written; query transcript_content_repairs for this repair ID before retrying: %w", err)
	}

	actual, putErr := restorer.RestoreAuthenticatedGeneration(ctx, transcriptID, descriptor, ciphertext)
	phase, class := Applied, FailureClass("")
	switch {
	case putErr == nil && actual == stored:
	case putErr == nil:
		// The read-back authenticated to something else: the write is unproved.
		phase, class = Refused, ObjectUncertain
	case ctx.Err() != nil,
		errors.Is(putErr, storage.ErrObjectIntegrityUncertain),
		errors.Is(putErr, storage.ErrObjectLookupUncertain),
		errors.Is(putErr, storage.ErrObjectAbsentProvider):
		// The write may have landed but the read-back did not prove it.
		phase, class = Refused, ObjectUncertain
	default:
		phase, class = Refused, ObjectWriteFailed
	}
	return recordRestoreTerminal(ctx, deps, opts, row, phase, class)
}

// recordRestoreTerminal writes the one terminal row of a restore whose attempt
// is committed. It re-locks the row and applies the present state only when the
// row still carries the revision, object key and identity the attempt saw.
func recordRestoreTerminal(ctx context.Context, deps Deps, opts Options, attempt sqlc.Transcript, phase Phase, class FailureClass) (Outcome, error) {
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), terminalRecordTimeout)
	defer cancel()
	unresolved := func(err error) error {
		return fmt.Errorf("operator content repair could not record the terminal row of repair %s in contentrepair.Restore after its attempt was committed; the object may have been written and the attempt is unresolved; inspect the object and the transcript, then record a reconciling repair that cites this repair ID: %w", opts.RepairID, err)
	}
	tx, q, locked, err := lockTranscript(detached, deps, opts, "contentrepair.Restore")
	if err != nil {
		return Outcome{}, unresolved(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(detached)) }()

	if phase == Applied && (locked.ContentRevision != attempt.ContentRevision || locked.BlobKey != attempt.BlobKey || ledgerPair(locked) != ledgerPair(attempt)) {
		// Another writer replaced the generation after the attempt: the object
		// that was restored is no longer the row's, so presence is not declared.
		phase, class = Refused, PreconditionMismatch
	}
	if phase == Applied {
		affected, err := q.MarkTranscriptContentPresent(detached, sqlc.MarkTranscriptContentPresentParams{
			ID:                      attempt.ID,
			ExpectedContentRevision: attempt.ContentRevision,
		})
		if err != nil {
			return Outcome{}, unresolved(err)
		}
		if affected != 1 {
			return Outcome{}, unresolved(fmt.Errorf("marking the content present changed %d rows instead of 1 under a row lock", affected))
		}
	}
	if err = insertLedger(detached, q, opts, phase, class, attempt, ledgerPair(attempt)); err != nil {
		return Outcome{}, unresolved(err)
	}
	if err = tx.Commit(detached); err != nil {
		return Outcome{}, unresolved(err)
	}
	return Outcome{RepairID: opts.RepairID, TranscriptID: opts.TranscriptID, Action: opts.Action, Phase: phase,
		FailureClass: class, ContentRevision: attempt.ContentRevision, RecordedAt: deps.Now()}, nil
}
