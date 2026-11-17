package storage

import (
	"context"
	_ "embed"
	"errors"
	"testing"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/storage_outcomes.yaml
var outcomeFixturesYAML []byte

type outcomeCase struct {
	Name    string             `yaml:"name"`
	Fault   outcomeFixtureKind `yaml:"fault"`
	Outcome outcomeFixtureKind `yaml:"outcome"`
}

type outcomeFixtureKind string

const (
	outcomeAbsent      outcomeFixtureKind = "absent"
	outcomeLookup      outcomeFixtureKind = "lookup"
	outcomeContentType outcomeFixtureKind = "content-type"
	outcomeCiphertext  outcomeFixtureKind = "ciphertext"
	outcomeIntegrity   outcomeFixtureKind = "integrity"
	outcomeKey         outcomeFixtureKind = "key"
)

var requiredOutcomeCases = []string{
	"evidence-split-absent",
	"evidence-split-lookup-uncertain",
	"evidence-split-integrity-uncertain",
	"evidence-split-aead-uncertain",
	"evidence-split-key-permanently-unavailable",
}

type outcomeObjects struct {
	*memoryStore
	failure error
}

func (s *outcomeObjects) Get(ctx context.Context, key ObjectKey) ([]byte, string, error) {
	if s.failure != nil {
		return nil, "", s.failure
	}
	return s.memoryStore.Get(ctx, key)
}

func (s *outcomeObjects) GetBounded(ctx context.Context, key ObjectKey, limit int64) ([]byte, string, error) {
	if s.failure != nil {
		return nil, "", s.failure
	}
	return s.memoryStore.GetBounded(ctx, key, limit)
}

type outcomeCustodian struct{ KeyCustodian }

func (outcomeCustodian) Unwrap(context.Context, []byte, KeyVersion, []byte) ([]byte, error) {
	return nil, ErrKeyPermanentlyUnavailable
}

func TestStorageOutcomes(t *testing.T) {
	var fixture struct {
		Cases []outcomeCase `yaml:"cases"`
	}
	if err := yaml.Unmarshal(outcomeFixturesYAML, &fixture); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("empty or repeated outcome case %q", c.Name)
		}
		seen[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			ctx := context.Background()
			base, memory := testEncryptedStore(t)
			id := uuid.New()
			descriptor, identity, err := base.Write(ctx, id, []byte("synthetic content"))
			if err != nil {
				t.Fatal(err)
			}
			objects := &outcomeObjects{memoryStore: memory}
			store := &EncryptedTranscriptStore{objects: objects, keys: base.keys}
			switch c.Fault {
			case outcomeAbsent:
				objects.failure = ErrObjectAbsentProvider
			case outcomeLookup:
				objects.failure = &objectLookupError{cause: errors.New("synthetic network failure")}
			case outcomeContentType:
				object := memory.objects[descriptor.ObjectKey()]
				object.contentType = "application/json"
				memory.objects[descriptor.ObjectKey()] = object
			case outcomeCiphertext:
				object := memory.objects[descriptor.ObjectKey()]
				object.body[len(object.body)-1] ^= 1
				memory.objects[descriptor.ObjectKey()] = object
			case outcomeKey:
				store.keys = outcomeCustodian{KeyCustodian: base.keys}
			default:
				t.Fatalf("unhandled fixture fault %q", c.Fault)
			}
			var expected error
			switch c.Outcome {
			case outcomeAbsent:
				expected = ErrObjectAbsentProvider
			case outcomeLookup:
				expected = ErrObjectLookupUncertain
			case outcomeIntegrity:
				expected = ErrObjectIntegrityUncertain
			case outcomeKey:
				expected = ErrKeyPermanentlyUnavailable
			default:
				t.Fatalf("unhandled fixture outcome %q", c.Outcome)
			}
			plaintext, _, err := store.Read(ctx, id, descriptor, NewKnownContentIdentity(identity))
			if plaintext != nil || !errors.Is(err, expected) {
				t.Fatalf("got plaintext=%q error=%v, want typed outcome %v", plaintext, err, expected)
			}
		})
	}
	for _, name := range requiredOutcomeCases {
		if !seen[name] {
			t.Fatalf("storage outcome fixture omits %q", name)
		}
	}
}
