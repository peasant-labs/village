package storage

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha3"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
)

const ciphertextContentType = "application/octet-stream"

type EncryptedTranscriptStore struct {
	objects ObjectStore
	keys    KeyCustodian
}

var _ TranscriptBlobStore = (*EncryptedTranscriptStore)(nil)

func NewEncryptedTranscriptStore(objects ObjectStore, keys KeyCustodian) (*EncryptedTranscriptStore, error) {
	if objects == nil || keys == nil {
		return nil, fmt.Errorf("encrypted transcript store construction failed because object storage or key custody is absent in storage.NewEncryptedTranscriptStore during dependency composition; transcript bodies cannot be persisted or read; provide both production dependencies")
	}
	return &EncryptedTranscriptStore{objects: objects, keys: keys}, nil
}
func bodyAAD(id uuid.UUID) []byte { return []byte("village:transcript-body:v1:" + id.String()) }
func dekAAD(id uuid.UUID) []byte  { return []byte("village:transcript-dek:v1:" + id.String()) }
func identity(body []byte) ContentIdentity {
	sum := sha3.Sum256(body)
	i, _ := NewContentIdentity(hex.EncodeToString(sum[:]), int64(len(body)))
	return i
}

func (s *EncryptedTranscriptStore) Write(ctx context.Context, id uuid.UUID, plaintext []byte) (BlobDescriptor, ContentIdentity, error) {
	if id == uuid.Nil {
		return BlobDescriptor{}, ContentIdentity{}, fmt.Errorf("transcript encryption failed because the transcript UUID is nil in storage.EncryptedTranscriptStore.Write before encryption; no object was written; generate the final transcript UUID first")
	}
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return BlobDescriptor{}, ContentIdentity{}, fmt.Errorf("transcript encryption failed because a fresh DEK could not be generated in storage.EncryptedTranscriptStore.Write during key generation; no object was written; restore the system random source and retry: %w", err)
	}
	block, _ := aes.NewCipher(dek)
	aead, _ := cipher.NewGCMWithRandomNonce(block)
	ciphertext := aead.Seal(nil, nil, plaintext, bodyAAD(id))
	wrapped, version, err := s.keys.Wrap(ctx, dek, dekAAD(id))
	if err != nil {
		return BlobDescriptor{}, ContentIdentity{}, err
	}
	key := ObjectKey("transcripts/" + uuid.NewString() + ".bin")
	if err := s.objects.Put(ctx, key, ciphertext, ciphertextContentType); err != nil {
		return BlobDescriptor{}, ContentIdentity{}, fmt.Errorf("ciphertext upload failed because object storage rejected the immutable generation in storage.EncryptedTranscriptStore.Write after encryption; no descriptor can be committed; restore object storage and retry: %w", err)
	}
	d, err := NewBlobDescriptor(key, wrapped, EncryptionAES256GCMRandomNonceV1, version)
	if err != nil {
		return BlobDescriptor{}, ContentIdentity{}, err
	}
	return d, identity(plaintext), nil
}
func (s *EncryptedTranscriptStore) Read(ctx context.Context, id uuid.UUID, d BlobDescriptor, loaded LoadedContentIdentity) ([]byte, ContentIdentity, error) {
	if err := loaded.validate(); err != nil {
		return nil, ContentIdentity{}, err
	}
	if d.algorithm != EncryptionAES256GCMRandomNonceV1 {
		return nil, ContentIdentity{}, fmt.Errorf("transcript decryption failed because descriptor algorithm %q is unsupported in storage.EncryptedTranscriptStore.Read before object access; no plaintext was returned; restore a valid descriptor", d.algorithm)
	}
	ciphertext, contentType, err := s.objects.Get(ctx, d.objectKey)
	if err != nil {
		return nil, ContentIdentity{}, err
	}
	if contentType != ciphertextContentType {
		return nil, ContentIdentity{}, fmt.Errorf("transcript decryption failed because object content type %q is not application/octet-stream in storage.EncryptedTranscriptStore.Read before authentication; no plaintext was returned; repair or restore the immutable generation", contentType)
	}
	dek, err := s.keys.Unwrap(ctx, d.wrappedDEK, d.keyVersion, dekAAD(id))
	if err != nil {
		return nil, ContentIdentity{}, err
	}
	if len(dek) != 32 {
		return nil, ContentIdentity{}, fmt.Errorf("transcript decryption failed because the unwrapped DEK is not 32 bytes in storage.EncryptedTranscriptStore.Read before body authentication; no plaintext was returned; repair key custody and restore the descriptor")
	}
	block, _ := aes.NewCipher(dek)
	aead, _ := cipher.NewGCMWithRandomNonce(block)
	plaintext, err := aead.Open(nil, nil, ciphertext, bodyAAD(id))
	if err != nil {
		return nil, ContentIdentity{}, fmt.Errorf("transcript decryption failed because AES-GCM authentication rejected ciphertext or transcript identity in storage.EncryptedTranscriptStore.Read during authentication; no plaintext was returned; restore the correct immutable generation and descriptor: %w", err)
	}
	actual := identity(plaintext)
	if expected, known := loaded.Known(); known && (expected.Hash() != actual.Hash() || expected.PlaintextSize() != actual.PlaintextSize()) {
		return nil, ContentIdentity{}, fmt.Errorf("transcript identity validation failed because authenticated plaintext hash or size differs from the stored identity in storage.EncryptedTranscriptStore.Read before return; no plaintext was returned; reconcile the descriptor and database identity")
	}
	return plaintext, actual, nil
}
func (s *EncryptedTranscriptStore) Rewrap(ctx context.Context, id uuid.UUID, d BlobDescriptor) (BlobDescriptor, error) {
	wrapped, version, err := s.keys.Rewrap(ctx, d.wrappedDEK, d.keyVersion, dekAAD(id))
	if err != nil {
		return BlobDescriptor{}, err
	}
	return NewBlobDescriptor(d.objectKey, wrapped, d.algorithm, version)
}
func (s *EncryptedTranscriptStore) Delete(ctx context.Context, d BlobDescriptor) error {
	if err := s.objects.Delete(ctx, d.objectKey); err != nil {
		return fmt.Errorf("ciphertext deletion failed because object storage rejected the immutable generation in storage.EncryptedTranscriptStore.Delete during post-commit cleanup; encrypted bytes may remain retained; retry cleanup or reconcile the opaque object key: %w", err)
	}
	return nil
}
