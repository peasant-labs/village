package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

var (
	errUncertainPredecessor = errors.New("republish could not verify the previous transcript in handler.PublishTranscript; nothing was replaced; restore object storage or key access and retry")
	errPreImageMoved        = errors.New("republish could not use the previous transcript because its storage generation moved in handler.PublishTranscript; nothing was replaced; reload and retry")
	errGenerationGone       = errors.New("republish could not use the previous transcript because it was deleted in handler.PublishTranscript; nothing was replaced; reload and retry")
	errPreImageDiverged     = errors.New("republish could not verify the previous transcript because its authenticated content matches neither the stored identity nor the replacement in handler.PublishTranscript; nothing was replaced; ask an operator to reconcile the previous generation")
)

type publishPredecessor struct {
	row        sqlc.Transcript
	descriptor storage.BlobDescriptor
	identity   storage.ContentIdentity
	known      bool
	baseline   bool
}

const predecessorReadTimeout = 30 * time.Second

// readPredecessor authenticates the ciphertext but deliberately computes its
// identity independently of the row. This lets the locked check detect an
// out-of-band overwrite at the same key rather than mistaking it for a
// successful read of the previously observed generation.
func (h *Handler) readPredecessor(ctx context.Context, row sqlc.Transcript, d storage.BlobDescriptor) (storage.ContentIdentity, error) {
	pending, err := storage.NewLoadedContentIdentity(nil, nil)
	if err != nil {
		return storage.ContentIdentity{}, err
	}
	// The published body was admitted through the multipart request bound. The
	// provider's own Content-Length bounds each response within that ceiling;
	// the additional timeout also keeps a request without a deadline bounded.
	readCtx, cancel := context.WithTimeout(ctx, predecessorReadTimeout)
	defer cancel()
	_, actual, err := h.blobs.ReadBounded(readCtx, uuidFromPg(row.ID), d, pending, int64(maxPublishRequestBytes)+storage.CiphertextOverheadBytes)
	if err != nil {
		if ctx.Err() != nil {
			return storage.ContentIdentity{}, ctx.Err()
		}
		// The current S3 adapter and keyring do not produce these definitive
		// outcomes. Preserve them for a provider or custodian that has actually
		// established absence or permanent key loss.
		if errors.Is(err, storage.ErrObjectAbsentProvider) || errors.Is(err, storage.ErrKeyPermanentlyUnavailable) {
			return storage.ContentIdentity{}, err
		}
		return storage.ContentIdentity{}, errUncertainPredecessor
	}
	return actual, nil
}

func equalContentIdentity(a, b storage.ContentIdentity) bool {
	return a.Hash() == b.Hash() && a.PlaintextSize() == b.PlaintextSize()
}

// classifyPredecessor runs under the session advisory lock, before narrowing
// or staging anything. A changed same-descriptor identity may have been
// installed by a concurrent first read; one refresh observes that case.
func (h *Handler) classifyPredecessor(ctx context.Context, q Querier, row sqlc.Transcript, candidate storage.ContentIdentity) (publishPredecessor, error) {
	expected, known, err := establishedRevisionIdentity(row)
	if err != nil {
		return publishPredecessor{}, err
	}
	descriptor, err := descriptorFromTranscript(row)
	if err != nil {
		return publishPredecessor{}, errUncertainPredecessor
	}
	actual, err := h.readPredecessor(ctx, row, descriptor)
	if err != nil {
		if ctx.Err() != nil {
			return publishPredecessor{}, ctx.Err()
		}
		fresh, refreshErr := q.GetTranscriptByID(ctx, row.ID)
		if errors.Is(refreshErr, pgx.ErrNoRows) {
			return publishPredecessor{}, errGenerationGone
		}
		if refreshErr != nil {
			return publishPredecessor{}, errUncertainPredecessor
		}
		freshDescriptor, descErr := descriptorFromTranscript(fresh)
		if descErr != nil || !descriptorsEqual(descriptor, freshDescriptor) || fresh.ContentRevision != row.ContentRevision {
			return publishPredecessor{}, errPreImageMoved
		}
		if errors.Is(err, storage.ErrObjectAbsentProvider) || errors.Is(err, storage.ErrKeyPermanentlyUnavailable) {
			_, freshKnown, identityErr := establishedRevisionIdentity(fresh)
			if identityErr != nil {
				return publishPredecessor{}, identityErr
			}
			if freshKnown {
				return publishPredecessor{}, errPreImageMoved
			}
			// A malformed or incomplete non-NULL primary hash is not the
			// user-approved identity-free baseline.
			if fresh.ContentHash.Valid {
				return publishPredecessor{}, errUncertainPredecessor
			}
			return publishPredecessor{row: fresh, descriptor: freshDescriptor, baseline: true}, nil
		}
		return publishPredecessor{}, errUncertainPredecessor
	}
	if known && !equalContentIdentity(expected, actual) && !equalContentIdentity(candidate, actual) {
		return publishPredecessor{}, errPreImageDiverged
	}
	// A valid legacy digest with a missing or invalid size is still evidence.
	// Only a genuinely malformed digest can be healed without matching it.
	if !known && row.ContentHash.Valid {
		if hashOnly, hashErr := storage.NewContentIdentity(row.ContentHash.String, 0); hashErr == nil && hashOnly.Hash() != actual.Hash() {
			return publishPredecessor{}, errPreImageDiverged
		}
	}
	if !known {
		expected = actual
	}
	return publishPredecessor{row: row, descriptor: descriptor, identity: expected, known: known}, nil
}

// lockedPredecessor verifies the same descriptor and stored identity, then
// authenticates the object once. Its return value is the stored identity, not
// the object observation, so the writer and the transition guard compare the
// same predecessor against the candidate.
func (h *Handler) lockedPredecessor(ctx context.Context, previous publishPredecessor, locked sqlc.Transcript, candidate storage.ContentIdentity) (storage.ContentIdentity, error) {
	d, err := descriptorFromTranscript(locked)
	if err != nil || !descriptorsEqual(previous.descriptor, d) || locked.ContentRevision != previous.row.ContentRevision {
		return storage.ContentIdentity{}, errPreImageMoved
	}
	stored, known, err := establishedRevisionIdentity(locked)
	if err != nil {
		return storage.ContentIdentity{}, err
	}
	if known {
		if previous.baseline {
			return storage.ContentIdentity{}, errPreImageMoved
		}
		if !equalContentIdentity(stored, previous.identity) {
			return storage.ContentIdentity{}, errPreImageMoved
		}
	} else {
		if previous.known || locked.ContentHash != previous.row.ContentHash ||
			locked.BlobSizeBytes != previous.row.BlobSizeBytes ||
			locked.SegmentedIdentityHash != previous.row.SegmentedIdentityHash ||
			locked.SegmentedIdentitySize != previous.row.SegmentedIdentitySize {
			return storage.ContentIdentity{}, errPreImageMoved
		}
		if !previous.baseline {
			stored = previous.identity
		}
	}
	actual, err := h.readPredecessor(ctx, locked, d)
	if err != nil {
		if errors.Is(err, storage.ErrObjectAbsentProvider) || errors.Is(err, storage.ErrKeyPermanentlyUnavailable) {
			if previous.baseline {
				return storage.ContentIdentity{}, nil
			}
			return storage.ContentIdentity{}, errPreImageMoved
		}
		return storage.ContentIdentity{}, err
	}
	if previous.baseline {
		// A generation reappearing after classification is a movement, not
		// permission to make a second revision decision.
		return storage.ContentIdentity{}, errPreImageMoved
	}
	if !equalContentIdentity(actual, stored) && !equalContentIdentity(actual, candidate) {
		return storage.ContentIdentity{}, errPreImageDiverged
	}
	return stored, nil
}

// installPredecessor makes an authenticated legacy identity visible on its
// unchanged descriptor within the replacement transaction. A concurrent first
// read may have installed its own identity before the row lock was taken; that
// case is already known and is not overwritten here.
func installPredecessor(ctx context.Context, q Querier, row *sqlc.Transcript, identity storage.ContentIdentity) error {
	_, known, err := establishedRevisionIdentity(*row)
	if err != nil || known {
		return err
	}
	n, err := q.CompareAndSwapContentIdentity(ctx, sqlc.CompareAndSwapContentIdentityParams{
		ID: row.ID, ContentHash: pgtype.Text{String: string(identity.Hash()), Valid: true},
		PlaintextSize:   pgtype.Int8{Int64: identity.PlaintextSize(), Valid: true},
		ExpectedBlobKey: row.BlobKey, ExpectedWrappedDataKey: row.WrappedDataKey,
		ExpectedEncryptionAlgorithm: row.EncryptionAlgorithm, ExpectedKeyVersion: row.KeyVersion,
		ExpectedPriorContentHash: row.ContentHash, ExpectedPriorBlobSizeBytes: row.BlobSizeBytes,
		ExpectedSegmentedIdentityHash: row.SegmentedIdentityHash, ExpectedSegmentedIdentitySize: row.SegmentedIdentitySize,
		ExpectedContentRevision: row.ContentRevision,
	})
	if err != nil {
		return err
	}
	if n != 1 {
		return errPreImageMoved
	}
	row.ContentHash = pgtype.Text{String: string(identity.Hash()), Valid: true}
	row.BlobSizeBytes = pgtype.Int8{Int64: identity.PlaintextSize(), Valid: true}
	return nil
}

func writePublishRevisionRefusal(w http.ResponseWriter, err error, audienceNote string) bool {
	switch {
	case errors.Is(err, errUncertainPredecessor):
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, errUncertainPredecessor.Error()+audienceNote)
	case errors.Is(err, errPreImageMoved):
		writeError(w, http.StatusConflict, errPreImageMoved.Error()+audienceNote)
	case errors.Is(err, errGenerationGone):
		writeError(w, http.StatusConflict, errGenerationGone.Error()+audienceNote)
	case errors.Is(err, errPreImageDiverged):
		writeError(w, http.StatusInternalServerError, errPreImageDiverged.Error()+audienceNote)
	case errors.Is(err, errDivergentRevision):
		writeError(w, http.StatusConflict, errDivergentRevision.Error()+audienceNote)
	case errors.Is(err, errRevisionOverflow):
		writeError(w, http.StatusConflict, errRevisionOverflow.Error()+audienceNote)
	default:
		return false
	}
	return true
}
