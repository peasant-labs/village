package storage

import (
	"bytes"
	"context"
	_ "embed"
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
