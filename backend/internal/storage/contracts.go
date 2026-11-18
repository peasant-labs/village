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

// CiphertextOverheadBytes is the random 96-bit nonce plus the 128-bit
// authentication tag prepended by AES-GCM with a random nonce.
const CiphertextOverheadBytes int64 = 28

type KeyVersion = config.KeyVersion
type ObjectKey string
type ContentHash string

// CanonicalContentHashPattern is the stored content hash syntax: lowercase
// hexadecimal SHA3-256. It is valid both as a Go regexp and as a PostgreSQL
// POSIX regular expression, so a content-free SQL census can classify rows by
// the same pattern NewContentIdentity enforces.
const CanonicalContentHashPattern = `^[0-9a-f]{64}$`

var canonicalContentHash = regexp.MustCompile(CanonicalContentHashPattern)
var opaqueObjectKey = regexp.MustCompile(`^transcripts/[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\.bin$`)

// NewObjectKey validates the same opaque-generation form as a blob descriptor.
// It deliberately omits the supplied key from errors.
func NewObjectKey(value string) (ObjectKey, error) {
	if !opaqueObjectKey.MatchString(value) {
		return "", errors.New("object key validation failed before object access; no object was read or written; provide an opaque transcript generation key")
	}
	return ObjectKey(value), nil
}

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

// MetadataObject describes the publish metadata stored as a second encrypted
// object beside a transcript's content. It carries no key material of its own:
// the object is sealed under the content's data key, so the content's
// BlobDescriptor is required to read it and rewrap covers it unchanged.
type MetadataObject struct {
	key      ObjectKey
	identity ContentIdentity
}

func NewMetadataObject(key ObjectKey, hash string, size int64) (MetadataObject, error) {
	if !opaqueObjectKey.MatchString(string(key)) {
		return MetadataObject{}, fmt.Errorf("publish metadata object validation failed because its object key is not an opaque generation key in storage.NewMetadataObject during descriptor construction; the metadata cannot be read; provide the stored opaque metadata key")
	}
	identity, err := NewContentIdentity(hash, size)
	if err != nil {
		return MetadataObject{}, err
	}
	return MetadataObject{key: key, identity: identity}, nil
}
func (m MetadataObject) Key() ObjectKey            { return m.key }
func (m MetadataObject) Identity() ContentIdentity { return m.identity }

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

// ErrObjectAbsentProvider is the definite-absence evidence that a deployment
// verification of the provider's exact-key not-found behavior must establish
// before a missing object may be treated as absent. Nothing produces it today:
// the S3 adapter does not claim it, and even NoSuchKey is uncertain until the
// provider's exact-key semantics are verified. Once verified, it is produced in
// one place, the exact-key not-found branch of S3ObjectStore.Get and
// S3ObjectStore.GetBounded in s3.go. The publish path and the operator repair
// branch on it already (handler predecessor classification, contentrepair
// restore); until the adapter returns it, only tests inject it, and those
// branches are unreachable in production.
var ErrObjectAbsentProvider = errors.New("object provider absence established")

// ErrObjectLookupUncertain includes masked or unverified absence, timeouts,
// provider errors and incomplete responses. It is never evidence of absence by
// itself. A publish therefore treats it as retryable and answers 503. The one
// caller that proceeds on it is the operator declare of unavailable content:
// there the operator's recorded authorization reference and reason are the
// evidence that the object is lost, and the lookup only has to fail to show that
// the object exists. No request path may do the same.
var ErrObjectLookupUncertain = errors.New("object lookup outcome uncertain")

// ErrObjectIntegrityUncertain means a returned object could not authenticate.
var ErrObjectIntegrityUncertain = errors.New("object integrity outcome uncertain")

// ErrKeyPermanentlyUnavailable is the explicit custodian result for a key that
// is gone for good, the second piece of the same deployment verification.
// Nothing produces it today: the transcript keyring does not claim permanent
// key loss, and every unwrap failure is uncertain. Once a custodian reports it, it is
// produced by the KeyCustodian implementation (config.TranscriptKeyring.Unwrap
// or its successor) and nowhere else. The publish path already branches on it,
// and until then only tests inject it.
var ErrKeyPermanentlyUnavailable = errors.New("key permanently unavailable")

// objectLookupError exposes the cause for cancellation and diagnostics without
// putting provider error text, which may include object keys, into logs.
type objectLookupError struct{ cause error }

func (e *objectLookupError) Error() string {
	return "object lookup could not establish the requested generation in storage during retrieval; no plaintext was returned; restore provider access and retry"
}

func (e *objectLookupError) Unwrap() []error {
	return []error{ErrObjectLookupUncertain, e.cause}
}

// UncommittedObjectCleanupError reports an uploaded generation that no
// descriptor references and that could not be deleted after a later step of the
// same write failed. The caller must emit reconciliation evidence for ObjectKey;
// the key is kept out of Error so it reaches only that evidence.
type UncommittedObjectCleanupError struct {
	key        ObjectKey
	uploadErr  error
	cleanupErr error
}

func (e *UncommittedObjectCleanupError) ObjectKey() ObjectKey { return e.key }
func (e *UncommittedObjectCleanupError) Error() string {
	return fmt.Sprintf("%v; deleting the uncommitted body generation also failed, so encrypted residue that no descriptor references remains and reconciliation is required; emit reconciliation evidence for the opaque object key, restore object storage, and delete it: %v", e.uploadErr, e.cleanupErr)
}
func (e *UncommittedObjectCleanupError) Unwrap() []error { return []error{e.uploadErr, e.cleanupErr} }

type KeyCustodian interface {
	Wrap(context.Context, []byte, []byte) ([]byte, KeyVersion, error)
	Unwrap(context.Context, []byte, KeyVersion, []byte) ([]byte, error)
	Rewrap(context.Context, []byte, KeyVersion, []byte) ([]byte, KeyVersion, error)
}
type ObjectStore interface {
	Put(context.Context, ObjectKey, []byte, string) error
	Get(context.Context, ObjectKey) ([]byte, string, error)
	// GetBounded refuses an unknown, incomplete or oversized object. It never
	// reads more than the provider's stated size plus one sentinel byte.
	GetBounded(context.Context, ObjectKey, int64) ([]byte, string, error)
	Delete(context.Context, ObjectKey) error
}
type TranscriptBlobStore interface {
	Write(context.Context, uuid.UUID, []byte) (BlobDescriptor, ContentIdentity, error)
	// WriteGeneration writes the content and, when metadata is non-nil, the
	// publish metadata as a second object under the same fresh data key. A nil
	// metadata writes one object and returns a nil MetadataObject.
	WriteGeneration(ctx context.Context, id uuid.UUID, body []byte, metadata []byte) (BlobDescriptor, ContentIdentity, *MetadataObject, error)
	Read(context.Context, uuid.UUID, BlobDescriptor, LoadedContentIdentity) ([]byte, ContentIdentity, error)
	// ReadBounded authenticates a generation through a bounded ciphertext
	// read; maxBytes is a ciphertext limit, including the nonce and tag.
	ReadBounded(context.Context, uuid.UUID, BlobDescriptor, LoadedContentIdentity, int64) ([]byte, ContentIdentity, error)
	// ReadMetadata unwraps the data key from the content descriptor and returns
	// the metadata only after authentication and hash and size validation.
	ReadMetadata(ctx context.Context, id uuid.UUID, d BlobDescriptor, m MetadataObject) ([]byte, error)
	Rewrap(context.Context, uuid.UUID, BlobDescriptor) (BlobDescriptor, error)
	Delete(context.Context, BlobDescriptor) error
	DeleteMetadata(ctx context.Context, m MetadataObject) error
}
