package backfill

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

type RewrapResult struct {
	Scanned, Installed, Stale, Failed, Uncertain, Remaining int
	ActiveVersion                                           storage.KeyVersion
}

var commitRewrapTransaction = func(ctx context.Context, tx pgx.Tx) error { return tx.Commit(ctx) }

func Rewrap(ctx context.Context, pool *pgxpool.Pool, blobs storage.TranscriptBlobStore, active storage.KeyVersion, limit int) (RewrapResult, error) {
	if limit < 1 || limit > 1000 {
		return RewrapResult{}, fmt.Errorf("key rewrap failed because limit %d is outside 1..1000 in backfill.Rewrap before enumeration; no keys changed; choose a bounded limit and retry", limit)
	}
	q := sqlc.New(pool)
	result := RewrapResult{ActiveVersion: active}
	afterVersion := int32(0)
	var afterID pgtype.UUID
	for result.Scanned < limit {
		rows, err := q.ListTranscriptDescriptorsForRewrap(ctx, sqlc.ListTranscriptDescriptorsForRewrapParams{ActiveKeyVersion: int32(active), AfterKeyVersion: afterVersion, AfterID: afterID, BatchSize: int32(limit - result.Scanned + 1)})
		if err != nil {
			return result, fmt.Errorf("key rewrap failed because descriptors could not be listed in backfill.Rewrap during keyset enumeration; completed progress is retained; restore PostgreSQL and retry: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		if len(rows) > limit-result.Scanned {
			result.Remaining = 1
			rows = rows[:limit-result.Scanned]
		}
		for _, row := range rows {
			result.Scanned++
			afterVersion = row.KeyVersion
			afterID = row.ID
			d, err := storage.NewBlobDescriptor(storage.ObjectKey(row.BlobKey), row.WrappedDataKey, storage.EncryptionAlgorithm(row.EncryptionAlgorithm), storage.KeyVersion(row.KeyVersion))
			if err != nil {
				result.Failed++
				continue
			}
			next, err := blobs.Rewrap(ctx, uuid.UUID(row.ID.Bytes), d)
			if err != nil {
				result.Failed++
				continue
			}
			installed, uncertain, err := installRewrap(ctx, pool, row, next)
			if uncertain {
				result.Uncertain++
				continue
			}
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
		if result.Remaining > 0 {
			break
		}
	}
	return result, nil
}

func installRewrap(ctx context.Context, pool *pgxpool.Pool, row sqlc.ListTranscriptDescriptorsForRewrapRow, next storage.BlobDescriptor) (installed, uncertain bool, err error) {
	tx, e := pool.Begin(ctx)
	if e != nil {
		return false, false, e
	}
	defer rollbackMaintenanceTransaction(tx, "wrapped_data_key_install")
	if _, e = tx.Exec(ctx, "SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)", database.SystemActorID); e != nil {
		return false, false, e
	}
	n, e := sqlc.New(tx).CompareAndSwapWrappedDataKey(ctx, sqlc.CompareAndSwapWrappedDataKeyParams{WrappedDataKey: next.WrappedDEK(), KeyVersion: int32(next.KeyVersion()), ID: row.ID, ExpectedBlobKey: row.BlobKey, ExpectedWrappedDataKey: row.WrappedDataKey, ExpectedEncryptionAlgorithm: row.EncryptionAlgorithm, ExpectedKeyVersion: row.KeyVersion})
	if e != nil {
		return false, false, e
	}
	if e = commitRewrapTransaction(ctx, tx); e != nil {
		return false, true, e
	}
	return n == 1, false, nil
}
