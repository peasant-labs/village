package storage

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
)

// ErrRestoreSourceIdentityMismatch means a preserved source authenticated but
// did not contain the hash and size that the transcript row attests.
var ErrRestoreSourceIdentityMismatch = errors.New("restore source authenticates to a different identity")

// AuthenticateRestoreSource reads and authenticates one preserved ciphertext
// object. It returns ciphertext only; plaintext never leaves this method.
// The caller must commit its attempt record before restoring the target.
func (s *EncryptedTranscriptStore) AuthenticateRestoreSource(ctx context.Context, id uuid.UUID, d BlobDescriptor, source ObjectKey, attested ContentIdentity) ([]byte, error) {
	if err := validateRestoreDescriptor(id, d); err != nil {
		return nil, err
	}
	if !opaqueObjectKey.MatchString(string(source)) || source == d.objectKey {
		return nil, errors.New("restore source validation failed in storage.AuthenticateRestoreSource before object access; no object was written; supply a separate opaque preserved-generation key")
	}
	if err := NewKnownContentIdentity(attested).validate(); err != nil {
		return nil, err
	}
	// GetBounded reserves one extra byte to detect an inconsistent response.
	if attested.plaintextSize >= math.MaxInt64-CiphertextOverheadBytes {
		return nil, &objectLookupError{cause: errors.New("attested size exceeds the bounded restore-source limit")}
	}
	ciphertext, contentType, err := s.objects.GetBounded(ctx, source, attested.plaintextSize+CiphertextOverheadBytes)
	if err != nil {
		// Even a provider-specific absence is not evidence that an operator's
		// preserved source is gone; this operation refuses without writing.
		return nil, &objectLookupError{cause: err}
	}
	if contentType != ciphertextContentType {
		return nil, fmt.Errorf("restore source authentication failed because its content type is not ciphertext in storage.AuthenticateRestoreSource before any write; provide the preserved encrypted generation: %w", ErrObjectIntegrityUncertain)
	}
	actual, err := s.authenticateRestoreCiphertext(ctx, id, d, ciphertext)
	if err != nil {
		return nil, err
	}
	if actual != attested {
		return nil, fmt.Errorf("restore source authentication found different content in storage.AuthenticateRestoreSource before any write; the stored identity is unchanged; provide the preserved attested generation: %w", ErrRestoreSourceIdentityMismatch)
	}
	return ciphertext, nil
}

// RestoreAuthenticatedGeneration writes the previously authenticated
// ciphertext verbatim to the existing descriptor, then re-reads and
// authenticates it. The caller must first commit the attempt record.
func (s *EncryptedTranscriptStore) RestoreAuthenticatedGeneration(ctx context.Context, id uuid.UUID, d BlobDescriptor, ciphertext []byte) (ContentIdentity, error) {
	if err := validateRestoreDescriptor(id, d); err != nil {
		return ContentIdentity{}, err
	}
	// Refuse a buffer changed after the source authentication before it can
	// destroy the target. The mode still verifies the returned identity.
	if _, err := s.authenticateRestoreCiphertext(ctx, id, d, ciphertext); err != nil {
		return ContentIdentity{}, err
	}
	if err := s.objects.Put(ctx, d.objectKey, ciphertext, ciphertextContentType); err != nil {
		return ContentIdentity{}, err
	}
	_, actual, err := s.ReadBounded(ctx, id, d, newPendingContentIdentity(), int64(len(ciphertext)))
	return actual, err
}

func validateRestoreDescriptor(id uuid.UUID, d BlobDescriptor) error {
	if id == uuid.Nil || !opaqueObjectKey.MatchString(string(d.objectKey)) ||
		len(d.wrappedDEK) == 0 || d.algorithm != EncryptionAES256GCMRandomNonceV1 || d.keyVersion <= 0 {
		return errors.New("restore descriptor validation failed in storage before object access; no object was written; provide the transcript and its complete stored descriptor")
	}
	return nil
}

func (s *EncryptedTranscriptStore) authenticateRestoreCiphertext(ctx context.Context, id uuid.UUID, d BlobDescriptor, ciphertext []byte) (ContentIdentity, error) {
	aead, err := s.openDEK(ctx, id, d, "storage transcript restore")
	if err != nil {
		return ContentIdentity{}, err
	}
	plaintext, err := aead.Open(nil, nil, ciphertext, bodyAAD(id))
	if err != nil {
		return ContentIdentity{}, fmt.Errorf("restore ciphertext authentication failed in storage before return; provide the preserved encrypted generation and its original descriptor: %w", ErrObjectIntegrityUncertain)
	}
	return identity(plaintext), nil
}
