//go:build integration

package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/peasant-labs/village/backend/internal/config"
)

func minioEnv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func TestMinIOEncryptedTranscriptProductionPath(t *testing.T) {
	cfg := &config.Config{S3Endpoint: minioEnv("TEST_S3_ENDPOINT", "http://127.0.0.1:9000"), S3Bucket: minioEnv("TEST_S3_BUCKET", "peasant-transcripts"), S3AccessKey: minioEnv("TEST_S3_ACCESS_KEY", "minioadmin"), S3SecretKey: minioEnv("TEST_S3_SECRET_KEY", "minioadmin"), S3UsePathStyle: true}
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
	otherID := uuid.New()
	body := []byte(`{"contractVersion":"0.1.0","private":"minio-proof"}`)
	otherBody := []byte(`{"contractVersion":"0.1.0","private":"independent-generation"}`)
	d, identity, err := store.Write(ctx, id, body)
	if err != nil {
		t.Fatalf("real MinIO encryption gate failed during write; verify TEST_S3_* points to a reachable initialized bucket: %v", err)
	}
	t.Cleanup(func() { _ = store.Delete(context.Background(), d) })
	otherDescriptor, otherIdentity, err := store.Write(ctx, otherID, otherBody)
	if err != nil {
		t.Fatalf("second immutable generation write failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Delete(context.Background(), otherDescriptor) })
	if d.ObjectKey() == otherDescriptor.ObjectKey() {
		t.Fatal("independent writes reused an object generation")
	}
	raw, contentType, err := objects.Get(ctx, d.ObjectKey())
	if err != nil {
		t.Fatal(err)
	}
	if contentType != ciphertextContentType || bytes.Equal(raw, body) || bytes.Contains(raw, []byte("minio-proof")) {
		t.Fatalf("raw object is not opaque ciphertext: content-type=%q", contentType)
	}
	plain, _, err := store.Read(ctx, id, d, NewKnownContentIdentity(identity))
	if err != nil || !bytes.Equal(plain, body) {
		t.Fatalf("authenticated read failed: %v", err)
	}
	if got, _, err := store.Read(ctx, otherID, d, NewKnownContentIdentity(identity)); err == nil || got != nil {
		t.Fatal("wrong transcript AAD returned plaintext")
	}
	otherRaw, _, err := objects.Get(ctx, otherDescriptor.ObjectKey())
	if err != nil {
		t.Fatal(err)
	}
	if err := objects.Put(ctx, d.ObjectKey(), otherRaw, ciphertextContentType); err != nil {
		t.Fatal(err)
	}
	if got, _, err := store.Read(ctx, id, d, NewKnownContentIdentity(identity)); err == nil || got != nil {
		t.Fatal("cross-row ciphertext swap returned plaintext")
	}
	if err := objects.Put(ctx, d.ObjectKey(), raw, ciphertextContentType); err != nil {
		t.Fatal(err)
	}
	swappedKeyDescriptor, err := NewBlobDescriptor(d.ObjectKey(), otherDescriptor.WrappedDEK(), d.Algorithm(), otherDescriptor.KeyVersion())
	if err != nil {
		t.Fatal(err)
	}
	if got, _, err := store.Read(ctx, id, swappedKeyDescriptor, NewKnownContentIdentity(identity)); err == nil || got != nil {
		t.Fatal("cross-row wrapped-key swap returned plaintext")
	}
	rewrapped, err := store.Rewrap(ctx, id, d)
	if err != nil {
		t.Fatal(err)
	}
	afterRewrap, _, err := objects.Get(ctx, d.ObjectKey())
	if err != nil {
		t.Fatal(err)
	}
	if rewrapped.ObjectKey() != d.ObjectKey() || bytes.Equal(rewrapped.WrappedDEK(), d.WrappedDEK()) || rewrapped.Algorithm() != d.Algorithm() || rewrapped.KeyVersion() != d.KeyVersion() || !bytes.Equal(afterRewrap, raw) {
		t.Fatal("rewrap changed object identity/body/algorithm/version or failed to rotate wrapped bytes")
	}
	plain, actualIdentity, err := store.Read(ctx, id, rewrapped, NewKnownContentIdentity(identity))
	if err != nil || !bytes.Equal(plain, body) || actualIdentity.Hash() != identity.Hash() || actualIdentity.PlaintextSize() != identity.PlaintextSize() {
		t.Fatalf("rewrap changed plaintext identity: %v", err)
	}
	if _, _, err := store.Read(ctx, otherID, otherDescriptor, NewKnownContentIdentity(otherIdentity)); err != nil {
		t.Fatalf("independent generation became unreadable: %v", err)
	}
	raw[len(raw)-1] ^= 1
	if err := objects.Put(ctx, d.ObjectKey(), raw, ciphertextContentType); err != nil {
		t.Fatal(err)
	}
	if got, _, err := store.Read(ctx, id, rewrapped, NewKnownContentIdentity(identity)); err == nil || got != nil {
		t.Fatal("tampered ciphertext returned plaintext")
	}
	if err := store.Delete(ctx, rewrapped); err != nil {
		t.Fatal(err)
	}
	if _, _, err := objects.Get(ctx, d.ObjectKey()); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("deleted generation absence classification failed: %v", err)
	}
}
