package handler

import (
	"context"
	"errors"
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

// metadataObjectFromTranscript maps the row's publish metadata columns. It
// returns nil when the row carries no metadata object (all three NULL); any
// other partial or malformed state is an error.
func metadataObjectFromTranscript(row sqlc.Transcript) (*storage.MetadataObject, error) {
	return metadataObjectFromColumns(row.PublishMetadataKey, row.PublishMetadataHash, row.PublishMetadataSize)
}

func metadataObjectFromColumns(key, hash pgtype.Text, size pgtype.Int8) (*storage.MetadataObject, error) {
	if !key.Valid && !hash.Valid && !size.Valid {
		return nil, nil
	}
	if !key.Valid || !hash.Valid || !size.Valid {
		return nil, fmt.Errorf("publish metadata load failed because the persisted metadata key, hash and size are only partly set in handler.metadataObjectFromColumns during database row mapping; the metadata object cannot be authenticated; repair all three columns before retrying")
	}
	m, err := storage.NewMetadataObject(storage.ObjectKey(key.String), hash.String, size.Int64)
	if err != nil {
		return nil, fmt.Errorf("publish metadata load failed because the persisted metadata columns are invalid in handler.metadataObjectFromColumns during database row mapping; the metadata object cannot be authenticated; repair the columns before retrying: %w", err)
	}
	return &m, nil
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
	emitObjectReconciliation(operation, transcriptID, descriptor.ObjectKey(), completion)
}

// emitMetadataReconciliation is emitBlobReconciliation for a publish metadata
// object, which the same decision retains.
func emitMetadataReconciliation(operation string, transcriptID pgtype.UUID, m *storage.MetadataObject, completion TransactionCompletion) {
	if m != nil {
		emitObjectReconciliation(operation, transcriptID, m.Key(), completion)
	}
}

// emitUncommittedWriteReconciliation reports a body generation that a failed
// metadata upload left behind with no descriptor referencing it. It returns
// false when err is not that failure.
func emitUncommittedWriteReconciliation(operation blobCleanupOperation, transcriptID pgtype.UUID, err error) bool {
	var orphan *storage.UncommittedObjectCleanupError
	if !errors.As(err, &orphan) {
		return false
	}
	emitObjectReconciliation(string(operation), transcriptID, orphan.ObjectKey(), TransactionKnownRollback)
	return true
}

// emitObjectReconciliation is the one transcript_blob_reconciliation_required
// event, keyed by the opaque object key it retains.
func emitObjectReconciliation(operation string, transcriptID pgtype.UUID, key storage.ObjectKey, completion TransactionCompletion) {
	slog.Error("transcript_blob_reconciliation_required",
		"operation", operation,
		"transcript_id", uuidFromPg(transcriptID).String(),
		"object_key", string(key),
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

// deleteMetadataForCleanup deletes a publish metadata object under the same
// cleanup decision that deleted its body generation. A failure leaves the same
// reconciliation evidence a body failure does.
func (h *Handler) deleteMetadataForCleanup(ctx context.Context, operation blobCleanupOperation, transcriptID pgtype.UUID, m storage.MetadataObject, completion TransactionCompletion) error {
	if err := h.blobs.DeleteMetadata(ctx, m); err != nil {
		emitObjectReconciliation(string(operation), transcriptID, m.Key(), completion)
		return fmt.Errorf("publish metadata cleanup failed because object storage did not delete the retained metadata ciphertext in handler.deleteMetadataForCleanup during %s; database authority is unchanged and reconciliation is required; inspect the transcript_blob_reconciliation_required event, restore object storage, then reconcile before retrying: %w", operation, err)
	}
	return nil
}

// deleteGenerationForCleanup deletes a body generation and, when it has one,
// its metadata object, both under one cleanup decision. Both deletions are
// attempted; the first failure is returned with the key of the object it
// retained.
func (h *Handler) deleteGenerationForCleanup(ctx context.Context, operation blobCleanupOperation, transcriptID pgtype.UUID, descriptor storage.BlobDescriptor, m *storage.MetadataObject, completion TransactionCompletion) (storage.ObjectKey, error) {
	var retained storage.ObjectKey
	err := h.deleteBlobForCleanup(ctx, operation, transcriptID, descriptor, completion)
	if err != nil {
		retained = descriptor.ObjectKey()
	}
	if m != nil {
		if metadataErr := h.deleteMetadataForCleanup(ctx, operation, transcriptID, *m, completion); metadataErr != nil && err == nil {
			retained, err = m.Key(), metadataErr
		}
	}
	return retained, err
}

// metadataObjectParams renders a metadata object as its three nullable
// columns; nil renders all three NULL.
func metadataObjectParams(m *storage.MetadataObject) (pgtype.Text, pgtype.Text, pgtype.Int8) {
	if m == nil {
		return pgtype.Text{}, pgtype.Text{}, pgtype.Int8{}
	}
	return pgtype.Text{String: string(m.Key()), Valid: true}, pgtype.Text{String: string(m.Identity().Hash()), Valid: true}, pgtype.Int8{Int64: m.Identity().PlaintextSize(), Valid: true}
}

// setPublishMetadataObject records the generation's metadata object, or its
// absence, inside the transaction that installs the generation.
func setPublishMetadataObject(ctx context.Context, q Querier, transcriptID pgtype.UUID, m *storage.MetadataObject) error {
	key, hash, size := metadataObjectParams(m)
	if err := q.SetTranscriptPublishMetadataObject(ctx, sqlc.SetTranscriptPublishMetadataObjectParams{ID: transcriptID, PublishMetadataKey: key, PublishMetadataHash: hash, PublishMetadataSize: size}); err != nil {
		return fmt.Errorf("record publish metadata object: %w", err)
	}
	return nil
}

// contentHashHeader names the generation the content route served: its value
// is the bare lowercase hex content hash of that generation.
const contentHashHeader = "Village-Content-Hash"
