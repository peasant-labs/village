package storage

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/restore-source.yaml
var restoreSourceYAML []byte

type restoreSourceSetup string
type restoreSourceOutcome string

const (
	restoreSourceValid        restoreSourceSetup   = "valid"
	restoreSourceMissing      restoreSourceSetup   = "missing"
	restoreSourceOversized    restoreSourceSetup   = "oversized"
	restoreSourceBadType      restoreSourceSetup   = "bad-type"
	restoreSourceTampered     restoreSourceSetup   = "tampered"
	restoreSourceWrongHash    restoreSourceSetup   = "wrong-hash"
	restoreSourceWrongSize    restoreSourceSetup   = "wrong-size"
	restoreSourceSameKey      restoreSourceSetup   = "same-key"
	restoreSourceMutateAfter  restoreSourceSetup   = "mutate-after-authentication"
	restoreSourceCorruptPut   restoreSourceSetup   = "corrupt-after-put"
	restoreSourceReplacePut   restoreSourceSetup   = "replace-after-put"
	restoreSourceFailedPut    restoreSourceSetup   = "failed-put"
	restoreOutcomeRestored    restoreSourceOutcome = "restored"
	restoreOutcomeUncertain   restoreSourceOutcome = "uncertain"
	restoreOutcomeIntegrity   restoreSourceOutcome = "integrity"
	restoreOutcomeMismatch    restoreSourceOutcome = "mismatch"
	restoreOutcomeInvalid     restoreSourceOutcome = "invalid"
	restoreOutcomeBeforeWrite restoreSourceOutcome = "integrity-before-write"
	restoreOutcomeAfterWrite  restoreSourceOutcome = "integrity-after-write"
	restoreOutcomeReadback    restoreSourceOutcome = "different-readback"
	restoreOutcomeWriteFailed restoreSourceOutcome = "write-failed"
)

type restoreSourceCase struct {
	Name    string               `yaml:"name"`
	Setup   restoreSourceSetup   `yaml:"setup"`
	Outcome restoreSourceOutcome `yaml:"outcome"`
}

var requiredRestoreSourceCases = []string{
	"preserved-source-restores-verbatim",
	"missing-source-is-uncertain",
	"oversized-source-is-uncertain",
	"source-content-type-is-uncertain",
	"tampered-source-is-uncertain",
	"authenticated-different-hash-is-mismatch",
	"authenticated-different-size-is-mismatch",
	"source-must-not-be-target",
	"modified-ciphertext-cannot-be-restored",
	"restore-reauthenticates-after-put",
	"restore-returns-readback-identity",
	"failed-put-does-not-read-back",
}

func loadRestoreSourceCases(t *testing.T) []restoreSourceCase {
	t.Helper()
	var cases []restoreSourceCase
	dec := yaml.NewDecoder(bytes.NewReader(restoreSourceYAML))
	dec.KnownFields(true)
	if err := dec.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("restore source fixture must contain one document: %v", err)
	}
	seen := make(map[string]bool, len(cases))
	for _, c := range cases {
		if c.Name == "" || c.Setup == "" || c.Outcome == "" || seen[c.Name] {
			t.Fatalf("invalid or duplicate restore source fixture: %+v", c)
		}
		seen[c.Name] = true
	}
	for _, name := range requiredRestoreSourceCases {
		if !seen[name] {
			t.Fatalf("required restore source fixture %q is missing", name)
		}
	}
	return cases
}

type restoreCountingObjects struct {
	ObjectStore
	boundedReads int
	writes       int
	putError     error
	afterPut     func()
}

func (s *restoreCountingObjects) GetBounded(ctx context.Context, key ObjectKey, limit int64) ([]byte, string, error) {
	s.boundedReads++
	return s.ObjectStore.GetBounded(ctx, key, limit)
}

func (s *restoreCountingObjects) Put(ctx context.Context, key ObjectKey, body []byte, contentType string) error {
	s.writes++
	if s.putError != nil {
		return s.putError
	}
	if err := s.ObjectStore.Put(ctx, key, body, contentType); err != nil {
		return err
	}
	if s.afterPut != nil {
		s.afterPut()
	}
	return nil
}

func TestRestoreSourceFixtures(t *testing.T) {
	for _, c := range loadRestoreSourceCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			ctx := context.Background()
			store, objects := testEncryptedStore(t)
			id := uuid.New()
			d, attested, err := store.Write(ctx, id, []byte("synthetic preserved transcript"))
			if err != nil {
				t.Fatal(err)
			}
			preserved := objects.objects[d.ObjectKey()]
			source := ObjectKey("transcripts/" + uuid.NewString() + ".bin")
			objects.objects[source] = memoryObject{body: append([]byte(nil), preserved.body...), contentType: preserved.contentType}
			diverged := []byte("synthetic divergent ciphertext")
			objects.objects[d.ObjectKey()] = memoryObject{body: append([]byte(nil), diverged...), contentType: ciphertextContentType}
			switch c.Setup {
			case restoreSourceValid, restoreSourceMutateAfter, restoreSourceCorruptPut, restoreSourceReplacePut, restoreSourceFailedPut:
			case restoreSourceMissing:
				delete(objects.objects, source)
			case restoreSourceOversized:
				attested, err = NewContentIdentity(string(attested.Hash()), attested.PlaintextSize()-1)
			case restoreSourceBadType:
				o := objects.objects[source]
				o.contentType = "text/plain"
				objects.objects[source] = o
			case restoreSourceTampered:
				o := objects.objects[source]
				o.body[len(o.body)-1] ^= 1
				objects.objects[source] = o
			case restoreSourceWrongHash:
				other := identity([]byte("synthetic different transcript"))
				attested, err = NewContentIdentity(string(other.Hash()), attested.PlaintextSize())
			case restoreSourceWrongSize:
				attested, err = NewContentIdentity(string(attested.Hash()), attested.PlaintextSize()+1)
			case restoreSourceSameKey:
				source = d.ObjectKey()
			default:
				t.Fatalf("unknown source setup %q", c.Setup)
			}
			if err != nil {
				t.Fatal(err)
			}
			observed := &restoreCountingObjects{ObjectStore: objects}
			store.objects = observed
			expectedReadback := ContentIdentity{}
			switch c.Setup {
			case restoreSourceCorruptPut:
				observed.afterPut = func() {
					o := objects.objects[d.ObjectKey()]
					o.body[len(o.body)-1] ^= 1
					objects.objects[d.ObjectKey()] = o
				}
			case restoreSourceReplacePut:
				other := []byte("synthetic moved generation")
				aead, err := store.openDEK(ctx, id, d, "restore fixture")
				if err != nil {
					t.Fatal(err)
				}
				replacement := aead.Seal(nil, nil, other, bodyAAD(id))
				expectedReadback = identity(other)
				observed.afterPut = func() {
					objects.objects[d.ObjectKey()] = memoryObject{body: replacement, contentType: ciphertextContentType}
				}
			case restoreSourceFailedPut:
				observed.putError = errors.New("synthetic provider write failure")
			}
			ciphertext, err := store.AuthenticateRestoreSource(ctx, id, d, source, attested)
			if observed.writes != 0 {
				t.Fatal("authentication wrote to object storage")
			}
			if target := objects.objects[d.ObjectKey()].body; !bytes.Equal(target, diverged) {
				t.Fatal("source authentication changed the divergent target")
			}
			switch c.Outcome {
			case restoreOutcomeUncertain:
				if !errors.Is(err, ErrObjectLookupUncertain) || ciphertext != nil {
					t.Fatalf("source read failure must be uncertain: %v", err)
				}
			case restoreOutcomeIntegrity:
				if !errors.Is(err, ErrObjectIntegrityUncertain) || ciphertext != nil {
					t.Fatalf("unauthenticated source must be uncertain: %v", err)
				}
			case restoreOutcomeMismatch:
				if !errors.Is(err, ErrRestoreSourceIdentityMismatch) || ciphertext != nil {
					t.Fatalf("authenticated different source must be a mismatch: %v", err)
				}
			case restoreOutcomeInvalid:
				if err == nil || observed.boundedReads != 0 || ciphertext != nil {
					t.Fatalf("invalid source must refuse before reading: %v", err)
				}
			case restoreOutcomeRestored, restoreOutcomeBeforeWrite, restoreOutcomeAfterWrite, restoreOutcomeReadback, restoreOutcomeWriteFailed:
				if err != nil || observed.boundedReads != 1 || !bytes.Equal(ciphertext, preserved.body) {
					t.Fatalf("source authentication did not return exactly the preserved ciphertext: %v", err)
				}
				if c.Outcome == restoreOutcomeBeforeWrite {
					ciphertext[len(ciphertext)-1] ^= 1
				}
				actual, restoreErr := store.RestoreAuthenticatedGeneration(ctx, id, d, ciphertext)
				if c.Outcome == restoreOutcomeBeforeWrite {
					if !errors.Is(restoreErr, ErrObjectIntegrityUncertain) || observed.writes != 0 ||
						!bytes.Equal(objects.objects[d.ObjectKey()].body, diverged) {
						t.Fatalf("modified ciphertext was not refused before write: %v", restoreErr)
					}
				} else {
					switch c.Outcome {
					case restoreOutcomeRestored:
						if restoreErr != nil || actual != attested || observed.writes != 1 ||
							!bytes.Equal(objects.objects[d.ObjectKey()].body, preserved.body) {
							t.Fatalf("restore did not put and reauthenticate the preserved bytes: %v", restoreErr)
						}
					case restoreOutcomeAfterWrite:
						if !errors.Is(restoreErr, ErrObjectIntegrityUncertain) || observed.writes != 1 || observed.boundedReads != 2 {
							t.Fatalf("restore did not detect a corrupted readback: %v", restoreErr)
						}
					case restoreOutcomeReadback:
						if restoreErr != nil || actual != expectedReadback || actual == attested || observed.boundedReads != 2 {
							t.Fatalf("restore did not return the readback's actual identity: %v", restoreErr)
						}
					case restoreOutcomeWriteFailed:
						if !errors.Is(restoreErr, observed.putError) || observed.boundedReads != 1 ||
							!bytes.Equal(objects.objects[d.ObjectKey()].body, diverged) {
							t.Fatalf("failed write was not reported without readback: %v", restoreErr)
						}
					}
				}
			default:
				t.Fatalf("unknown source outcome %q", c.Outcome)
			}
		})
	}
}
