package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/peasant-labs/village/backend/internal/config"
)

type EncryptionAlgorithm string

const EncryptionAES256GCMRandomNonceV1 EncryptionAlgorithm = "aes-256-gcm-random-nonce-v1"

type KeyVersion = config.KeyVersion
type ObjectKey string
type ContentHash string

var canonicalContentHash = regexp.MustCompile(`^[0-9a-f]{64}$`)
var opaqueObjectKey = regexp.MustCompile(`^transcripts/[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\.bin$`)

type BlobDescriptor struct {
	objectKey  ObjectKey
	wrappedDEK []byte
	algorithm  EncryptionAlgorithm
	keyVersion KeyVersion
}

func NewBlobDescriptor(key ObjectKey, wrapped []byte, algorithm EncryptionAlgorithm, version KeyVersion) (BlobDescriptor, error) {
	if !opaqueObjectKey.MatchString(string(key)) || len(wrapped) == 0 || algorithm != EncryptionAES256GCMRandomNonceV1 || version <= 0 {
		return BlobDescriptor{}, fmt.Errorf("blob descriptor validation failed because its object key, wrapped key, algorithm, or key version is invalid in storage.NewBlobDescriptor during descriptor construction; the descriptor cannot be used to recover transcript content; provide a non-empty opaque key and wrapped key, algorithm %q, and a positive key version", EncryptionAES256GCMRandomNonceV1)
	}
	return BlobDescriptor{objectKey: key, wrappedDEK: append([]byte(nil), wrapped...), algorithm: algorithm, keyVersion: version}, nil
}
func (d BlobDescriptor) ObjectKey() ObjectKey           { return d.objectKey }
func (d BlobDescriptor) WrappedDEK() []byte             { return append([]byte(nil), d.wrappedDEK...) }
func (d BlobDescriptor) Algorithm() EncryptionAlgorithm { return d.algorithm }
func (d BlobDescriptor) KeyVersion() KeyVersion         { return d.keyVersion }
func (d BlobDescriptor) Equal(other BlobDescriptor) bool {
	return d.objectKey == other.objectKey && d.algorithm == other.algorithm && d.keyVersion == other.keyVersion && bytes.Equal(d.wrappedDEK, other.wrappedDEK)
}

type ContentIdentity struct {
	hash          ContentHash
	plaintextSize int64
}

func NewContentIdentity(hash string, size int64) (ContentIdentity, error) {
	if !canonicalContentHash.MatchString(hash) || size < 0 {
		return ContentIdentity{}, fmt.Errorf("content identity validation failed because the hash is not canonical lowercase SHA3-256 or the size is negative in storage.NewContentIdentity during identity construction; transcript integrity cannot be established; provide a 64-character lowercase hexadecimal hash and a non-negative plaintext size")
	}
	return ContentIdentity{hash: ContentHash(hash), plaintextSize: size}, nil
}
func (i ContentIdentity) Hash() ContentHash    { return i.hash }
func (i ContentIdentity) PlaintextSize() int64 { return i.plaintextSize }

type ContentIdentityStatus uint8

const (
	ContentIdentityKnown ContentIdentityStatus = iota + 1
	ContentIdentityPending
)

type LoadedContentIdentity struct {
	status   ContentIdentityStatus
	identity ContentIdentity
}

func NewKnownContentIdentity(identity ContentIdentity) LoadedContentIdentity {
	return LoadedContentIdentity{status: ContentIdentityKnown, identity: identity}
}
func newPendingContentIdentity() LoadedContentIdentity {
	return LoadedContentIdentity{status: ContentIdentityPending}
}
func (i LoadedContentIdentity) Status() ContentIdentityStatus { return i.status }
func (i LoadedContentIdentity) Known() (ContentIdentity, bool) {
	return i.identity, i.status == ContentIdentityKnown
}

// NewLoadedContentIdentity is the persistence boundary for nullable identity
// columns. A NULL hash is pending only when size is also NULL; size zero is a
// real known value and is never treated as absence.
func NewLoadedContentIdentity(hash *string, size *int64) (LoadedContentIdentity, error) {
	if hash == nil {
		if size != nil && *size < 0 {
			return LoadedContentIdentity{}, fmt.Errorf("loaded content identity validation failed because a pending row has a negative plaintext size in storage.NewLoadedContentIdentity during row mapping; transcript integrity cannot be established; preserve SQL NULL as nil or provide a nonnegative stored size")
		}
		return newPendingContentIdentity(), nil
	}
	if size == nil {
		return LoadedContentIdentity{}, fmt.Errorf("loaded content identity validation failed because hash and plaintext size nullability disagree in storage.NewLoadedContentIdentity during row mapping; transcript integrity cannot be established; load both columns together or repair the row")
	}
	identity, err := NewContentIdentity(*hash, *size)
	if err != nil {
		return LoadedContentIdentity{}, err
	}
	return NewKnownContentIdentity(identity), nil
}

func (i LoadedContentIdentity) validate() error {
	switch i.status {
	case ContentIdentityPending:
		if i.identity != (ContentIdentity{}) {
			return fmt.Errorf("loaded content identity validation failed because pending state contains a known identity in storage.LoadedContentIdentity.validate before object access; no plaintext can be returned; reconstruct the state from nullable columns")
		}
		return nil
	case ContentIdentityKnown:
		if !canonicalContentHash.MatchString(string(i.identity.hash)) || i.identity.plaintextSize < 0 {
			return fmt.Errorf("loaded content identity validation failed because known state contains a zero or invalid identity in storage.LoadedContentIdentity.validate before object access; no plaintext can be returned; reconstruct the state with validated hash and size")
		}
		return nil
	default:
		return fmt.Errorf("loaded content identity validation failed because status %d is unknown in storage.LoadedContentIdentity.validate before object access; no plaintext can be returned; construct known or pending identity through the validated mapper", i.status)
	}
}

var ErrObjectNotFound = errors.New("object not found")

type KeyCustodian interface {
	Wrap(context.Context, []byte, []byte) ([]byte, KeyVersion, error)
	Unwrap(context.Context, []byte, KeyVersion, []byte) ([]byte, error)
	Rewrap(context.Context, []byte, KeyVersion, []byte) ([]byte, KeyVersion, error)
}
type ObjectStore interface {
	Put(context.Context, ObjectKey, []byte, string) error
	Get(context.Context, ObjectKey) ([]byte, string, error)
	Delete(context.Context, ObjectKey) error
}
type TranscriptBlobStore interface {
	Write(context.Context, uuid.UUID, []byte) (BlobDescriptor, ContentIdentity, error)
	Read(context.Context, uuid.UUID, BlobDescriptor, LoadedContentIdentity) ([]byte, ContentIdentity, error)
	Rewrap(context.Context, uuid.UUID, BlobDescriptor) (BlobDescriptor, error)
	Delete(context.Context, BlobDescriptor) error
}
