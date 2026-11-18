//go:build integration

package handler

import (
	"context"
	"testing"
)

// installHistoricalNumberingContent writes encrypted historical bytes and
// advances the row under the same writer fence as the production writer.
func installHistoricalNumberingContent(t *testing.T, w *orderingWorld, raw []byte, harness string) {
	t.Helper()
	d, identity, err := w.handler.blobs.Write(w.ctx, uuidFromPg(w.id), raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.objects.Delete(context.Background(), d.ObjectKey()); err != nil {
			t.Errorf("delete historical object: %v", err)
		}
	})
	tx, err := w.pool.Begin(w.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(w.ctx) }()
	if _, err := tx.Exec(w.ctx, "SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)", w.owner.ID.String()); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(w.ctx, `UPDATE transcripts SET blob_key=$2, wrapped_data_key=$3, encryption_algorithm=$4, key_version=$5,
		content_hash=$6, blob_size_bytes=$7, segmented_identity_hash=NULL, segmented_identity_size=NULL,
		content_revision=content_revision+1, model_provider=$8 WHERE id=$1`,
		w.id, string(d.ObjectKey()), d.WrappedDEK(), string(d.Algorithm()), int32(d.KeyVersion()), string(identity.Hash()), identity.PlaintextSize(), harness)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(w.ctx); err != nil {
		t.Fatal(err)
	}
	w.handler.contentCache = newTranscriptContentCache(transcriptContentCacheConfig{})
}
