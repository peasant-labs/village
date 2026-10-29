package storage

import (
	"bytes"
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

// metadataAAD binds the publish metadata object to its transcript and keeps it
// from authenticating as a body, or a body as metadata, under the shared DEK.
func metadataAAD(id uuid.UUID) []byte {
	return []byte("village:transcript-publish-metadata:v1:" + id.String())
}

// gcmNonceSize is the length of the random nonce cipher.NewGCMWithRandomNonce
// draws from crypto/rand for every Seal and prepends to the ciphertext.
const gcmNonceSize = 12

func identity(body []byte) ContentIdentity {
	sum := sha3.Sum256(body)
	i, _ := NewContentIdentity(hex.EncodeToString(sum[:]), int64(len(body)))
	return i
}

func (s *EncryptedTranscriptStore) Write(ctx context.Context, id uuid.UUID, plaintext []byte) (BlobDescriptor, ContentIdentity, error) {
	d, i, _, err := s.WriteGeneration(ctx, id, plaintext, nil)
	return d, i, err
}

// WriteGeneration seals the body and, when metadata is non-nil, the metadata
// under one fresh DEK. Each Seal draws its own random 96-bit nonce; the two
// nonces are compared and an equal pair is refused before anything is
// uploaded, so no nonce is ever used twice under one DEK.
func (s *EncryptedTranscriptStore) WriteGeneration(ctx context.Context, id uuid.UUID, plaintext []byte, metadata []byte) (BlobDescriptor, ContentIdentity, *MetadataObject, error) {
	if id == uuid.Nil {
		return BlobDescriptor{}, ContentIdentity{}, nil, fmt.Errorf("transcript encryption failed because the transcript UUID is nil in storage.EncryptedTranscriptStore.WriteGeneration before encryption; no object was written; generate the final transcript UUID first")
	}
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return BlobDescriptor{}, ContentIdentity{}, nil, fmt.Errorf("transcript encryption failed because a fresh DEK could not be generated in storage.EncryptedTranscriptStore.WriteGeneration during key generation; no object was written; restore the system random source and retry: %w", err)
	}
	block, _ := aes.NewCipher(dek)
	aead, _ := cipher.NewGCMWithRandomNonce(block)
	ciphertext := aead.Seal(nil, nil, plaintext, bodyAAD(id))
	var metadataCiphertext []byte
	if metadata != nil {
		metadataCiphertext = aead.Seal(nil, nil, metadata, metadataAAD(id))
		if bytes.Equal(ciphertext[:gcmNonceSize], metadataCiphertext[:gcmNonceSize]) {
			return BlobDescriptor{}, ContentIdentity{}, nil, fmt.Errorf("transcript encryption failed because the body and metadata drew the same nonce under one DEK in storage.EncryptedTranscriptStore.WriteGeneration during encryption; no object was written; retry the write, which generates a fresh DEK")
		}
	}
	wrapped, version, err := s.keys.Wrap(ctx, dek, dekAAD(id))
	if err != nil {
		return BlobDescriptor{}, ContentIdentity{}, nil, err
	}
	key := ObjectKey("transcripts/" + uuid.NewString() + ".bin")
	d, err := NewBlobDescriptor(key, wrapped, EncryptionAES256GCMRandomNonceV1, version)
	if err != nil {
		return BlobDescriptor{}, ContentIdentity{}, nil, err
	}
	var m *MetadataObject
	if metadata != nil {
		metadataKey := ObjectKey("transcripts/" + uuid.NewString() + ".bin")
		if metadataKey == key {
			return BlobDescriptor{}, ContentIdentity{}, nil, fmt.Errorf("transcript encryption failed because the metadata object key equals the body object key in storage.EncryptedTranscriptStore.WriteGeneration before upload; no object was written; retry the write")
		}
		mi := identity(metadata)
		object, err := NewMetadataObject(metadataKey, string(mi.Hash()), mi.PlaintextSize())
		if err != nil {
			return BlobDescriptor{}, ContentIdentity{}, nil, err
		}
		m = &object
	}
	if err := s.objects.Put(ctx, key, ciphertext, ciphertextContentType); err != nil {
		return BlobDescriptor{}, ContentIdentity{}, nil, fmt.Errorf("ciphertext upload failed because object storage rejected the immutable generation in storage.EncryptedTranscriptStore.WriteGeneration after encryption; no descriptor can be committed; restore object storage and retry: %w", err)
	}
	if m != nil {
		if err := s.objects.Put(ctx, m.key, metadataCiphertext, ciphertextContentType); err != nil {
			cleanup := "the uploaded body generation was deleted"
			if derr := s.objects.Delete(context.WithoutCancel(ctx), key); derr != nil {
				cleanup = "the uploaded body generation could not be deleted and is unreferenced encrypted residue"
			}
			return BlobDescriptor{}, ContentIdentity{}, nil, fmt.Errorf("publish metadata upload failed because object storage rejected the metadata generation in storage.EncryptedTranscriptStore.WriteGeneration after the body upload; %s, and no descriptor can be committed; restore object storage and retry: %w", cleanup, err)
		}
	}
	return d, identity(plaintext), m, nil
}

// openDEK unwraps the data key named by a content descriptor.
func (s *EncryptedTranscriptStore) openDEK(ctx context.Context, id uuid.UUID, d BlobDescriptor, stage string) (cipher.AEAD, error) {
	dek, err := s.keys.Unwrap(ctx, d.wrappedDEK, d.keyVersion, dekAAD(id))
	if err != nil {
		return nil, err
	}
	if len(dek) != 32 {
		return nil, fmt.Errorf("transcript decryption failed because the unwrapped DEK is not 32 bytes in %s before authentication; no plaintext was returned; repair key custody and restore the descriptor", stage)
	}
	block, _ := aes.NewCipher(dek)
	aead, _ := cipher.NewGCMWithRandomNonce(block)
	return aead, nil
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
	aead, err := s.openDEK(ctx, id, d, "storage.EncryptedTranscriptStore.Read")
	if err != nil {
		return nil, ContentIdentity{}, err
	}
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

func (s *EncryptedTranscriptStore) ReadMetadata(ctx context.Context, id uuid.UUID, d BlobDescriptor, m MetadataObject) ([]byte, error) {
	if !opaqueObjectKey.MatchString(string(m.key)) || !canonicalContentHash.MatchString(string(m.identity.hash)) || m.identity.plaintextSize < 0 {
		return nil, fmt.Errorf("publish metadata decryption failed because the metadata object is not a validated descriptor in storage.EncryptedTranscriptStore.ReadMetadata before object access; no plaintext was returned; construct it with storage.NewMetadataObject from the stored columns")
	}
	if d.algorithm != EncryptionAES256GCMRandomNonceV1 {
		return nil, fmt.Errorf("publish metadata decryption failed because descriptor algorithm %q is unsupported in storage.EncryptedTranscriptStore.ReadMetadata before object access; no plaintext was returned; restore a valid descriptor", d.algorithm)
	}
	ciphertext, contentType, err := s.objects.Get(ctx, m.key)
	if err != nil {
		return nil, err
	}
	if contentType != ciphertextContentType {
		return nil, fmt.Errorf("publish metadata decryption failed because object content type %q is not application/octet-stream in storage.EncryptedTranscriptStore.ReadMetadata before authentication; no plaintext was returned; repair or restore the immutable generation", contentType)
	}
	aead, err := s.openDEK(ctx, id, d, "storage.EncryptedTranscriptStore.ReadMetadata")
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, nil, ciphertext, metadataAAD(id))
	if err != nil {
		return nil, fmt.Errorf("publish metadata decryption failed because AES-GCM authentication rejected the ciphertext, its transcript, or its object role in storage.EncryptedTranscriptStore.ReadMetadata during authentication; no plaintext was returned; restore the correct metadata generation and content descriptor: %w", err)
	}
	if actual := identity(plaintext); actual != m.identity {
		return nil, fmt.Errorf("publish metadata identity validation failed because the authenticated plaintext hash or size differs from the stored identity in storage.EncryptedTranscriptStore.ReadMetadata before return; no plaintext was returned; reconcile the metadata object and database identity")
	}
	return plaintext, nil
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
func (s *EncryptedTranscriptStore) DeleteMetadata(ctx context.Context, m MetadataObject) error {
	if !opaqueObjectKey.MatchString(string(m.key)) {
		return fmt.Errorf("publish metadata deletion failed because the metadata object key is not an opaque generation key in storage.EncryptedTranscriptStore.DeleteMetadata before object access; nothing was deleted; construct it with storage.NewMetadataObject from the stored columns")
	}
	if err := s.objects.Delete(ctx, m.key); err != nil {
		return fmt.Errorf("publish metadata deletion failed because object storage rejected the metadata generation in storage.EncryptedTranscriptStore.DeleteMetadata during post-commit cleanup; encrypted bytes may remain retained; retry cleanup or reconcile the opaque object key: %w", err)
	}
	return nil
}
