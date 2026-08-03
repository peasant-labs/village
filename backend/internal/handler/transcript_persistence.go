package handler

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

func uuidFromPg(id pgtype.UUID) uuid.UUID { return uuid.UUID(id.Bytes) }

type CASOutcome uint8

const (
	CASInstalled CASOutcome = iota + 1
	CASStale
)

type TransactionCompletion uint8

const (
	TransactionCommitted TransactionCompletion = iota + 1
	TransactionKnownRollback
	TransactionCommitAmbiguous
)

func (c TransactionCompletion) String() string {
	switch c {
	case TransactionCommitted:
		return "committed"
	case TransactionKnownRollback:
		return "known_rollback"
	case TransactionCommitAmbiguous:
		return "commit_ambiguous"
	default:
		return fmt.Sprintf("unknown(%d)", c)
	}
}

type DescriptorObservationKind uint8

const (
	ObservationExactDescriptor DescriptorObservationKind = iota + 1
	ObservationSameObjectKeyAdvanced
	ObservationDifferentObjectKey
	ObservationMissingRow
	ObservationUnreadable
)

type transactionResult struct {
	Completion TransactionCompletion
	Err        error
}

type blobCleanupOperation string

const (
	cleanupCreateCandidate     blobCleanupOperation = "create_candidate_cleanup"
	cleanupRepublishCandidate  blobCleanupOperation = "republish_candidate_cleanup"
	cleanupRepublishSuperseded blobCleanupOperation = "republish_cleanup"
	cleanupRewriteCandidate    blobCleanupOperation = "rewrite_candidate_cleanup"
	cleanupRewriteSuperseded   blobCleanupOperation = "rewrite_superseded_cleanup"
	cleanupDeleteTarget        blobCleanupOperation = "delete_target_cleanup"
)

func descriptorFromTranscript(row sqlc.Transcript) (storage.BlobDescriptor, error) {
	return descriptorFromColumns(row.BlobKey, row.WrappedDataKey, row.EncryptionAlgorithm, row.KeyVersion)
}

func descriptorFromColumns(key string, wrapped []byte, algorithm string, version int32) (storage.BlobDescriptor, error) {
	descriptor, err := storage.NewBlobDescriptor(storage.ObjectKey(key), wrapped, storage.EncryptionAlgorithm(algorithm), storage.KeyVersion(version))
	if err != nil {
		return storage.BlobDescriptor{}, fmt.Errorf("transcript descriptor load failed because persisted storage metadata is invalid in handler.descriptorFromColumns during database row mapping; the transcript cannot be decrypted safely; repair or restore the descriptor before retrying: %w", err)
	}
	return descriptor, nil
}

func identityFromTranscript(row sqlc.Transcript) (storage.LoadedContentIdentity, error) {
	var hash *string
	if row.ContentHash.Valid {
		hash = &row.ContentHash.String
	}
	var size *int64
	if row.BlobSizeBytes.Valid {
		size = &row.BlobSizeBytes.Int64
	}
	loaded, err := storage.NewLoadedContentIdentity(hash, size)
	if err != nil {
		return storage.LoadedContentIdentity{}, fmt.Errorf("transcript identity load failed because persisted hash and size do not form a valid known or pending state in handler.identityFromTranscript during database row mapping; integrity cannot be validated and no body will be served; repair both identity columns before retrying: %w", err)
	}
	return loaded, nil
}

func nullableInt8(value pgtype.Int8) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func emitBlobReconciliation(operation string, transcriptID pgtype.UUID, descriptor storage.BlobDescriptor, completion TransactionCompletion) {
	slog.Error("transcript_blob_reconciliation_required",
		"operation", operation,
		"transcript_id", uuidFromPg(transcriptID).String(),
		"object_key", string(descriptor.ObjectKey()),
		"completion", completion.String(),
		"meaning", "ciphertext was retained because database authority is uncertain",
		"remediation", "wait beyond the transaction recovery window, query the authoritative primary for this object key, and delete only when no row or in-flight transaction can reference it")
}

// deleteBlobForCleanup is the single cleanup boundary for ciphertext whose
// database lifecycle has completed. A failed object deletion always leaves
// secret-safe operator evidence rather than silently retaining ciphertext.
func (h *Handler) deleteBlobForCleanup(ctx context.Context, operation blobCleanupOperation, transcriptID pgtype.UUID, descriptor storage.BlobDescriptor, completion TransactionCompletion) error {
	if err := h.blobs.Delete(ctx, descriptor); err != nil {
		emitBlobReconciliation(string(operation), transcriptID, descriptor, completion)
		return fmt.Errorf("transcript blob cleanup failed because object storage did not delete the retained ciphertext in handler.deleteBlobForCleanup during %s; database authority is unchanged and reconciliation is required; inspect the transcript_blob_reconciliation_required event, restore object storage, then reconcile before retrying: %w", operation, err)
	}
	return nil
}
