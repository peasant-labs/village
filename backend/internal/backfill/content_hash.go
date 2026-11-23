package backfill

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

type IdentityResult struct{ Scanned, Installed, Stale, Failed int }

func ContentIdentity(ctx context.Context, pool *pgxpool.Pool, blobs storage.TranscriptBlobStore) (IdentityResult, error) {
	rows, err := sqlc.New(pool).ListTranscriptsMissingContentHash(ctx)
	if err != nil {
		return IdentityResult{}, fmt.Errorf("content identity backfill failed because pending rows could not be listed in backfill.ContentIdentity during enumeration; no identities were installed; restore PostgreSQL connectivity and retry: %w", err)
	}
	result := IdentityResult{Scanned: len(rows)}
	for _, row := range rows {
		var installed bool
		err := database.WithSessionPublishLock(ctx, pool, row.OwnerID, row.LocalID, func(conn *pgxpool.Conn) error {
			current, err := sqlc.New(conn).GetTranscriptByID(ctx, row.ID)
			if err != nil {
				return err
			}
			if current.ContentHash.Valid {
				return nil
			}
			row.BlobKey, row.WrappedDataKey = current.BlobKey, current.WrappedDataKey
			row.EncryptionAlgorithm, row.KeyVersion = current.EncryptionAlgorithm, current.KeyVersion
			row.BlobSizeBytes, row.ContentHash, row.ContentRevision = current.BlobSizeBytes, current.ContentHash, current.ContentRevision
			row.SegmentedIdentityHash, row.SegmentedIdentitySize = current.SegmentedIdentityHash, current.SegmentedIdentitySize
			descriptor, err := storage.NewBlobDescriptor(storage.ObjectKey(row.BlobKey), row.WrappedDataKey, storage.EncryptionAlgorithm(row.EncryptionAlgorithm), storage.KeyVersion(row.KeyVersion))
			if err != nil {
				return err
			}
			loaded, err := storage.NewLoadedContentIdentity(nil, nil)
			if row.SegmentedIdentityHash.Valid && row.SegmentedIdentitySize.Valid {
				known, identityErr := storage.NewContentIdentity(row.SegmentedIdentityHash.String, row.SegmentedIdentitySize.Int64)
				if identityErr != nil {
					return identityErr
				}
				loaded = storage.NewKnownContentIdentity(known)
			}
			if err != nil {
				return err
			}
			_, identity, err := blobs.Read(ctx, uuid.UUID(row.ID.Bytes), descriptor, loaded)
			if err != nil {
				return err
			}
			installed, err = installIdentity(ctx, conn, row, identity)
			return err
		})
		if err != nil {
			result.Failed++
			continue
		}
		if installed {
			result.Installed++
		} else {
			result.Stale++
		}
	}
	return result, nil
}

func installIdentity(ctx context.Context, conn *pgxpool.Conn, row sqlc.ListTranscriptsMissingContentHashRow, identity storage.ContentIdentity) (bool, error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollbackMaintenanceTransaction(tx, "content_identity_install")
	if _, err = tx.Exec(ctx, "SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)", database.SystemActorID); err != nil {
		return false, err
	}
	n, err := sqlc.New(tx).CompareAndSwapContentIdentity(ctx, sqlc.CompareAndSwapContentIdentityParams{ContentHash: pgtype.Text{String: string(identity.Hash()), Valid: true}, PlaintextSize: pgtype.Int8{Int64: identity.PlaintextSize(), Valid: true}, ID: row.ID, ExpectedBlobKey: row.BlobKey, ExpectedWrappedDataKey: row.WrappedDataKey, ExpectedEncryptionAlgorithm: row.EncryptionAlgorithm, ExpectedKeyVersion: row.KeyVersion, ExpectedPriorContentHash: row.ContentHash, ExpectedPriorBlobSizeBytes: row.BlobSizeBytes, ExpectedSegmentedIdentityHash: row.SegmentedIdentityHash, ExpectedSegmentedIdentitySize: row.SegmentedIdentitySize, ExpectedContentRevision: row.ContentRevision})
	if err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return n == 1, nil
}
