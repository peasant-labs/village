//go:build integration

package database

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestMigration032AcceptedFingerprintRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	migrateTestDatabaseThrough(t, pool, migrationTestBoundary(31))
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ownerID := insertFenceOwner(t, ctx, tx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','1',true), set_config('app.actor_id',$1,true)", SystemActorID); err != nil {
		t.Fatal(err)
	}
	tid, err := insertFenceTranscript(ctx, tx, ownerID)
	if err != nil {
		t.Fatalf("insert encrypted transcript descriptor for migration 032 fingerprint proof: %v", err)
	}
	var beforeKey string
	var beforeWrapped []byte
	var beforeAlgorithm string
	var beforeVersion int32
	if err := tx.QueryRow(ctx, `SELECT blob_key, wrapped_data_key, encryption_algorithm, key_version FROM transcripts WHERE id=$1`, tid).Scan(&beforeKey, &beforeWrapped, &beforeAlgorithm, &beforeVersion); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit encrypted pre-migration fixture: %v", err)
	}
	if err := runMigration(pool, requireMigrationVersion(t, 32)); err != nil {
		t.Fatalf("apply exact migration 032 over encrypted boundary: %v", err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var afterKey string
	var afterWrapped []byte
	var afterAlgorithm string
	var afterVersion int32
	if err := tx.QueryRow(ctx, `SELECT blob_key, wrapped_data_key, encryption_algorithm, key_version FROM transcripts WHERE id=$1`, tid).Scan(&afterKey, &afterWrapped, &afterAlgorithm, &afterVersion); err != nil {
		t.Fatal(err)
	}
	if beforeKey != afterKey || !bytes.Equal(beforeWrapped, afterWrapped) || beforeAlgorithm != afterAlgorithm || beforeVersion != afterVersion {
		t.Fatalf("migration 032 changed encrypted descriptor: before=(%q,%x,%q,%d) after=(%q,%x,%q,%d)", beforeKey, beforeWrapped, beforeAlgorithm, beforeVersion, afterKey, afterWrapped, afterAlgorithm, afterVersion)
	}
	var initial *string
	if err := tx.QueryRow(ctx, `SELECT accepted_request_operation_fingerprint FROM transcripts WHERE id=$1`, tid).Scan(&initial); err != nil {
		t.Fatal(err)
	}
	if initial != nil {
		t.Fatalf("initial fingerprint=%q, want NULL", *initial)
	}
	want := strings.Repeat("a", 64)
	if _, err := tx.Exec(ctx, `UPDATE transcripts SET accepted_request_operation_fingerprint=$2 WHERE id=$1`, tid, want); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := tx.QueryRow(ctx, `SELECT accepted_request_operation_fingerprint FROM transcripts WHERE id=$1`, tid).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("fingerprint=%q want=%q", got, want)
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = sp.Exec(ctx, `UPDATE transcripts SET accepted_request_operation_fingerprint='NOT-A-DIGEST' WHERE id=$1`, tid)
	_ = sp.Rollback(ctx)
	if err == nil {
		t.Fatal("malformed accepted fingerprint unexpectedly passed the database constraint")
	}
}
