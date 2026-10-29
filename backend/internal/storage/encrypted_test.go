package storage

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha3"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/peasant-labs/village/backend/internal/config"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/encryption/cases.yaml
var encryptionCasesYAML []byte

type encryptionCase struct {
	Name   string `yaml:"name"`
	Action string `yaml:"action"`
}

func loadEncryptionCases(t *testing.T) []encryptionCase {
	t.Helper()
	dec := yaml.NewDecoder(bytes.NewReader(encryptionCasesYAML))
	dec.KnownFields(true)
	var cases []encryptionCase
	if err := dec.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("fixture trailing document: %v", err)
	}
	if len(cases) != 14 {
		t.Fatalf("fixture rows=%d, want 14", len(cases))
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("invalid fixture row %+v", c)
		}
		if err := validateEncryptionAction(c.Action); err != nil {
			t.Fatal(err)
		}
		seen[c.Name] = true
	}
	return cases
}

func validateEncryptionAction(action string) error {
	switch action {
	case "roundtrip", "pending", "nondeterministic", "wrong_uuid", "tamper_body", "tamper_key", "swap_body", "swap_key", "content_type", "hash", "size", "algorithm", "version", "generation":
		return nil
	default:
		return fmt.Errorf("encryption fixture action %q is unknown; strict fixture loading cannot continue", action)
	}
}

func TestEncryptionFixtureRejectsUnknownAction(t *testing.T) {
	if err := validateEncryptionAction("unrecognized-action"); err == nil {
		t.Fatal("unknown fixture action was accepted")
	}
}

type memoryObject struct {
	body        []byte
	contentType string
}
type memoryStore struct {
	mu      sync.Mutex
	objects map[ObjectKey]memoryObject
}

func (m *memoryStore) Put(_ context.Context, key ObjectKey, b []byte, ct string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = memoryObject{append([]byte(nil), b...), ct}
	return nil
}
func (m *memoryStore) Get(_ context.Context, key ObjectKey) ([]byte, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[key]
	if !ok {
		return nil, "", ErrObjectNotFound
	}
	return append([]byte(nil), o.body...), o.contentType, nil
}
func (m *memoryStore) Delete(_ context.Context, key ObjectKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}
func testEncryptedStore(t *testing.T) (*EncryptedTranscriptStore, *memoryStore) {
	t.Helper()
	ring, err := config.ParseTranscriptKeyring("1", `{"1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`)
	if err != nil {
		t.Fatal(err)
	}
	objects := &memoryStore{objects: map[ObjectKey]memoryObject{}}
	store, _ := NewEncryptedTranscriptStore(objects, ring)
	return store, objects
}

func TestEncryptedTranscriptStoreFixture(t *testing.T) {
	ctx := context.Background()
	body := []byte(`{"private":"transcript"}`)
	for _, tc := range loadEncryptionCases(t) {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			s, objects := testEncryptedStore(t)
			id, other := uuid.New(), uuid.New()
			d, identity, err := s.Write(ctx, id, body)
			if err != nil {
				t.Fatal(err)
			}
			d2, _, _ := s.Write(ctx, other, body)
			expectFailure := false
			switch tc.Action {
			case "roundtrip", "pending":
			case "nondeterministic":
				if bytes.Equal(objects.objects[d.ObjectKey()].body, objects.objects[d2.ObjectKey()].body) {
					t.Fatal("ciphertexts equal")
				}
				return
			case "wrong_uuid":
				id = other
				expectFailure = true
			case "tamper_body":
				o := objects.objects[d.ObjectKey()]
				o.body[len(o.body)-1] ^= 1
				objects.objects[d.ObjectKey()] = o
				expectFailure = true
			case "tamper_key":
				w := d.WrappedDEK()
				w[len(w)-1] ^= 1
				d, _ = NewBlobDescriptor(d.ObjectKey(), w, d.Algorithm(), d.KeyVersion())
				expectFailure = true
			case "swap_body":
				o := objects.objects[d.ObjectKey()]
				o.body = objects.objects[d2.ObjectKey()].body
				objects.objects[d.ObjectKey()] = o
				expectFailure = true
			case "swap_key":
				d, _ = NewBlobDescriptor(d.ObjectKey(), d2.WrappedDEK(), d.Algorithm(), d2.KeyVersion())
				expectFailure = true
			case "content_type":
				o := objects.objects[d.ObjectKey()]
				o.contentType = "application/json"
				objects.objects[d.ObjectKey()] = o
				expectFailure = true
			case "hash":
				identity, _ = NewContentIdentity("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", identity.PlaintextSize())
				expectFailure = true
			case "size":
				identity, _ = NewContentIdentity(string(identity.Hash()), identity.PlaintextSize()+1)
				expectFailure = true
			case "algorithm":
				d.algorithm = "other"
				expectFailure = true
			case "version":
				d.keyVersion = 99
				expectFailure = true
			case "generation":
				if d.ObjectKey() == d2.ObjectKey() {
					t.Fatal("generation key reused")
				}
				return
			default:
				t.Fatalf("unknown encryption fixture action %q", tc.Action)
			}
			loaded := NewKnownContentIdentity(identity)
			if tc.Action == "pending" {
				loaded, err = NewLoadedContentIdentity(nil, nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			plain, actual, err := s.Read(ctx, id, d, loaded)
			if expectFailure {
				if err == nil || plain != nil {
					t.Fatalf("failure expected, plain=%q err=%v", plain, err)
				}
				return
			}
			if err != nil || !bytes.Equal(plain, body) || actual.Hash() != identity.Hash() {
				t.Fatalf("read failed: %v", err)
			}
			plain[0] ^= 1
			again, _, err := s.Read(ctx, id, d, loaded)
			if err != nil || bytes.Equal(plain, again) {
				t.Fatal("independent read not returned")
			}
		})
	}
}

func TestEncryptedTranscriptStoreRewrapDeleteAndAbsence(t *testing.T) {
	ctx := context.Background()
	s, objects := testEncryptedStore(t)
	id := uuid.New()
	d, identity, _ := s.Write(ctx, id, []byte("body"))
	original := append([]byte(nil), objects.objects[d.ObjectKey()].body...)
	rewrapped, err := s.Rewrap(ctx, id, d)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, objects.objects[d.ObjectKey()].body) {
		t.Fatal("rewrap changed ciphertext")
	}
	if _, _, err := s.Read(ctx, id, rewrapped, NewKnownContentIdentity(identity)); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, rewrapped); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Read(ctx, id, rewrapped, NewKnownContentIdentity(identity))
	if !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("absence=%v", err)
	}
}

//go:embed testdata/publish-metadata-object.yaml
var publishMetadataObjectYAML []byte

type publishMetadataObjectCase struct {
	Name string `yaml:"name"`
}

// requiredPublishMetadataObjectCases is the name manifest for the publish
// metadata object fixture. Removing a row hides a real failure; restore it
// rather than deleting it from this manifest.
var requiredPublishMetadataObjectCases = []string{
	"metadata_round_trips",
	"metadata_shares_the_body_wrapped_dek",
	"metadata_as_body_fails_authentication",
	"body_as_metadata_fails_authentication",
	"metadata_of_another_transcript_fails",
	"metadata_hash_mismatch_refused",
	"nil_metadata_writes_one_object",
	"metadata_upload_failure_removes_the_body",
	"rewrap_keeps_metadata_readable_and_bytes_identical",
}

func loadPublishMetadataObjectCases(t *testing.T) []publishMetadataObjectCase {
	t.Helper()
	dec := yaml.NewDecoder(bytes.NewReader(publishMetadataObjectYAML))
	dec.KnownFields(true)
	var cases []publishMetadataObjectCase
	if err := dec.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("publish metadata object fixture trailing document: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("invalid publish metadata object fixture row %+v", c)
		}
		seen[c.Name] = true
	}
	for _, required := range requiredPublishMetadataObjectCases {
		if !seen[required] {
			t.Fatalf("the publish metadata object fixture omits required case %q; restore it rather than deleting it from this manifest", required)
		}
	}
	return cases
}

// failingPutStore fails the Put call numbered failOn (1-based) and records
// every key it was asked to delete.
type failingPutStore struct {
	*memoryStore
	failOn  int
	puts    int
	deleted []ObjectKey
}

func (f *failingPutStore) Put(ctx context.Context, key ObjectKey, b []byte, ct string) error {
	f.puts++
	if f.puts == f.failOn {
		return errors.New("simulated object storage outage")
	}
	return f.memoryStore.Put(ctx, key, b, ct)
}
func (f *failingPutStore) Delete(ctx context.Context, key ObjectKey) error {
	f.deleted = append(f.deleted, key)
	return f.memoryStore.Delete(ctx, key)
}

func TestPublishMetadataObject(t *testing.T) {
	ctx := context.Background()
	body := []byte(`{"private":"transcript"}`)
	metadata := []byte(`{"client":{"name":"peasant","version":"1.2.3"},"private":"metadata"}`)
	for _, tc := range loadPublishMetadataObjectCases(t) {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			s, objects := testEncryptedStore(t)
			id, other := uuid.New(), uuid.New()
			switch tc.Name {
			case "nil_metadata_writes_one_object":
				d, identity, m, err := s.WriteGeneration(ctx, id, body, nil)
				if err != nil || m != nil {
					t.Fatalf("nil metadata wrote an object or failed: m=%v err=%v", m, err)
				}
				if len(objects.objects) != 1 {
					t.Fatalf("objects=%d, want 1", len(objects.objects))
				}
				if plain, _, err := s.Read(ctx, id, d, NewKnownContentIdentity(identity)); err != nil || !bytes.Equal(plain, body) {
					t.Fatalf("body read failed: %v", err)
				}
				return
			case "metadata_upload_failure_removes_the_body":
				failing := &failingPutStore{memoryStore: &memoryStore{objects: map[ObjectKey]memoryObject{}}, failOn: 2}
				ring, err := config.ParseTranscriptKeyring("1", `{"1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`)
				if err != nil {
					t.Fatal(err)
				}
				fs, _ := NewEncryptedTranscriptStore(failing, ring)
				d, identity, m, err := fs.WriteGeneration(ctx, id, body, metadata)
				if err == nil || m != nil || d.ObjectKey() != "" || identity != (ContentIdentity{}) {
					t.Fatalf("failed metadata upload returned a committable result: d=%v m=%v err=%v", d, m, err)
				}
				if len(failing.objects) != 0 || len(failing.deleted) != 1 {
					t.Fatalf("body object not removed: remaining=%d deleted=%v", len(failing.objects), failing.deleted)
				}
				return
			}
			d, identity, m, err := s.WriteGeneration(ctx, id, body, metadata)
			if err != nil || m == nil {
				t.Fatalf("write generation failed: m=%v err=%v", m, err)
			}
			if m.Key() == d.ObjectKey() || len(objects.objects) != 2 {
				t.Fatalf("metadata object key=%q body key=%q objects=%d", m.Key(), d.ObjectKey(), len(objects.objects))
			}
			if plain, _, err := s.Read(ctx, id, d, NewKnownContentIdentity(identity)); err != nil || !bytes.Equal(plain, body) {
				t.Fatalf("body read failed: %v", err)
			}
			expectRefused := func(plain []byte, err error) {
				t.Helper()
				if err == nil || plain != nil {
					t.Fatalf("refusal expected, plain=%q err=%v", plain, err)
				}
			}
			switch tc.Name {
			case "metadata_round_trips":
				raw := objects.objects[m.Key()]
				if raw.contentType != ciphertextContentType || bytes.Contains(raw.body, []byte("metadata")) {
					t.Fatalf("metadata object is not opaque ciphertext: content-type=%q", raw.contentType)
				}
				plain, err := s.ReadMetadata(ctx, id, d, *m)
				if err != nil || !bytes.Equal(plain, metadata) {
					t.Fatalf("metadata read failed: %v", err)
				}
				if m.Identity() != identityOf(metadata) {
					t.Fatal("metadata identity is not SHA3-256 and size of the plaintext")
				}
				if err := s.DeleteMetadata(ctx, *m); err != nil {
					t.Fatal(err)
				}
				if _, err := s.ReadMetadata(ctx, id, d, *m); !errors.Is(err, ErrObjectNotFound) {
					t.Fatalf("absence=%v", err)
				}
				if _, _, err := s.Read(ctx, id, d, NewKnownContentIdentity(identity)); err != nil {
					t.Fatalf("deleting metadata removed the body: %v", err)
				}
			case "metadata_shares_the_body_wrapped_dek":
				ring := s.keys
				dek, err := ring.Unwrap(ctx, d.WrappedDEK(), d.KeyVersion(), dekAAD(id))
				if err != nil {
					t.Fatal(err)
				}
				block, _ := aes.NewCipher(dek)
				aead, _ := cipher.NewGCMWithRandomNonce(block)
				bodyCT, metaCT := objects.objects[d.ObjectKey()].body, objects.objects[m.Key()].body
				if plain, err := aead.Open(nil, nil, bodyCT, bodyAAD(id)); err != nil || !bytes.Equal(plain, body) {
					t.Fatalf("body does not open under the descriptor DEK: %v", err)
				}
				if plain, err := aead.Open(nil, nil, metaCT, metadataAAD(id)); err != nil || !bytes.Equal(plain, metadata) {
					t.Fatalf("metadata does not open under the descriptor DEK: %v", err)
				}
				if bytes.Equal(bodyCT[:gcmNonceSize], metaCT[:gcmNonceSize]) {
					t.Fatal("the two objects under one DEK share a nonce")
				}
			case "metadata_as_body_fails_authentication":
				swapped, err := NewBlobDescriptor(m.Key(), d.WrappedDEK(), d.Algorithm(), d.KeyVersion())
				if err != nil {
					t.Fatal(err)
				}
				plain, _, err := s.Read(ctx, id, swapped, newPendingContentIdentity())
				expectRefused(plain, err)
			case "body_as_metadata_fails_authentication":
				swapped, err := NewMetadataObject(d.ObjectKey(), string(identity.Hash()), identity.PlaintextSize())
				if err != nil {
					t.Fatal(err)
				}
				expectRefused(s.ReadMetadata(ctx, id, d, swapped))
			case "metadata_of_another_transcript_fails":
				d2, _, m2, err := s.WriteGeneration(ctx, other, body, metadata)
				if err != nil {
					t.Fatal(err)
				}
				expectRefused(s.ReadMetadata(ctx, other, d, *m))
				expectRefused(s.ReadMetadata(ctx, id, d, *m2))
				expectRefused(s.ReadMetadata(ctx, id, d2, *m))
				o := objects.objects[m.Key()]
				o.body = objects.objects[m2.Key()].body
				objects.objects[m.Key()] = o
				expectRefused(s.ReadMetadata(ctx, id, d, *m))
			case "metadata_hash_mismatch_refused":
				wrongHash, err := NewMetadataObject(m.Key(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", m.Identity().PlaintextSize())
				if err != nil {
					t.Fatal(err)
				}
				expectRefused(s.ReadMetadata(ctx, id, d, wrongHash))
				wrongSize, err := NewMetadataObject(m.Key(), string(m.Identity().Hash()), m.Identity().PlaintextSize()+1)
				if err != nil {
					t.Fatal(err)
				}
				expectRefused(s.ReadMetadata(ctx, id, d, wrongSize))
				if _, err := NewMetadataObject("transcripts/not-opaque.bin", string(m.Identity().Hash()), 1); err == nil {
					t.Fatal("non-opaque metadata key accepted")
				}
				if _, err := NewMetadataObject(m.Key(), "AA", 1); err == nil {
					t.Fatal("non-canonical metadata hash accepted")
				}
				var zero MetadataObject
				expectRefused(s.ReadMetadata(ctx, id, d, zero))
			case "rewrap_keeps_metadata_readable_and_bytes_identical":
				before := append([]byte(nil), objects.objects[m.Key()].body...)
				rewrapped, err := s.Rewrap(ctx, id, d)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Equal(rewrapped.WrappedDEK(), d.WrappedDEK()) {
					t.Fatal("rewrap did not change the wrapped DEK")
				}
				plain, err := s.ReadMetadata(ctx, id, rewrapped, *m)
				if err != nil || !bytes.Equal(plain, metadata) {
					t.Fatalf("metadata unreadable after rewrap: %v", err)
				}
				if !bytes.Equal(before, objects.objects[m.Key()].body) {
					t.Fatal("rewrap changed the metadata ciphertext")
				}
			default:
				t.Fatalf("unknown publish metadata object fixture case %q", tc.Name)
			}
		})
	}
}

func identityOf(plaintext []byte) ContentIdentity {
	sum := sha3.Sum256(plaintext)
	i, _ := NewContentIdentity(hex.EncodeToString(sum[:]), int64(len(plaintext)))
	return i
}
