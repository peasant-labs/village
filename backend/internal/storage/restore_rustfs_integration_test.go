//go:build integration

package storage

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/peasant-labs/village/backend/internal/config"
)

// A real object store is needed here: a memory store cannot prove that bounded
// reads and the verbatim overwrite use the S3-compatible production adapter.
func TestRustFSRestoreAuthenticatedGeneration(t *testing.T) {
	cfg := &config.Config{S3Endpoint: rustfsEnv("TEST_S3_ENDPOINT", "http://127.0.0.1:9000"), S3Bucket: rustfsEnv("TEST_S3_BUCKET", "peasant-transcripts"), S3AccessKey: rustfsEnv("TEST_S3_ACCESS_KEY", "minioadmin"), S3SecretKey: rustfsEnv("TEST_S3_SECRET_KEY", "minioadmin"), S3UsePathStyle: true}
	objects, err := NewS3ObjectStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := config.ParseTranscriptKeyring("1", `{"1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewEncryptedTranscriptStore(objects, ring)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	id := uuid.New()
	body := []byte("synthetic preserved generation")
	d, attested, err := store.Write(ctx, id, body)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Delete(context.Background(), d) })
	preserved, contentType, err := objects.Get(ctx, d.ObjectKey())
	if err != nil {
		t.Fatal(err)
	}
	source := ObjectKey("transcripts/" + uuid.NewString() + ".bin")
	if err := objects.Put(ctx, source, preserved, contentType); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = objects.Delete(context.Background(), source) })
	bad := append([]byte(nil), preserved...)
	bad[len(bad)-1] ^= 1
	if err := objects.Put(ctx, d.ObjectKey(), bad, ciphertextContentType); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Read(ctx, id, d, NewKnownContentIdentity(attested)); !errors.Is(err, ErrObjectIntegrityUncertain) {
		t.Fatalf("divergent generation was not refused before repair: %v", err)
	}
	ciphertext, err := store.AuthenticateRestoreSource(ctx, id, d, source, attested)
	if err != nil || !bytes.Equal(ciphertext, preserved) {
		t.Fatalf("preserved source did not authenticate: %v", err)
	}
	before, _, err := objects.Get(ctx, d.ObjectKey())
	if err != nil || !bytes.Equal(before, bad) {
		t.Fatalf("source authentication altered the target: %v", err)
	}
	actual, err := store.RestoreAuthenticatedGeneration(ctx, id, d, ciphertext)
	if err != nil || actual != attested {
		t.Fatalf("restore did not authenticate to the attested identity: %v", err)
	}
	after, _, err := objects.Get(ctx, d.ObjectKey())
	if err != nil || !bytes.Equal(after, preserved) {
		t.Fatalf("restored object differs from the preserved ciphertext: %v", err)
	}
	plaintext, _, err := store.Read(ctx, id, d, NewKnownContentIdentity(attested))
	if err != nil || !bytes.Equal(plaintext, body) {
		t.Fatalf("production read did not recover the attested content: %v", err)
	}
}
