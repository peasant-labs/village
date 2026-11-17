//go:build integration

package contentrepair

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/restore.yaml
var restoreFixtureYAML []byte

type restoreCase struct {
	Name             string `yaml:"name"`
	Source           string `yaml:"source"`
	Attested         string `yaml:"attested"`
	Stored           string `yaml:"stored"`
	ExpectedRevision int64  `yaml:"expected_revision"`
	StateBefore      string `yaml:"state_before"`
	Put              string `yaml:"put"`
	Attempted        bool   `yaml:"attempted"`
	Puts             int    `yaml:"puts"`
	Phase            string `yaml:"phase"`
	FailureClass     string `yaml:"failure_class"`
	StateAfter       string `yaml:"state_after"`
	RevisionAfter    int64  `yaml:"revision_after"`
	Restored         bool   `yaml:"restored"`
}

type memoryObject struct {
	body        []byte
	contentType string
}

// memoryObjects is the object provider only; encryption, source authentication
// and the restore write are the production storage code above it.
type memoryObjects struct {
	objects   map[storage.ObjectKey]memoryObject
	puts      int
	putErr    error
	beforePut func()
	afterPut  func(storage.ObjectKey)
}

func (m *memoryObjects) Put(_ context.Context, key storage.ObjectKey, body []byte, contentType string) error {
	m.puts++
	if m.beforePut != nil {
		m.beforePut()
	}
	if m.putErr != nil {
		return m.putErr
	}
	m.objects[key] = memoryObject{body: append([]byte(nil), body...), contentType: contentType}
	if m.afterPut != nil {
		m.afterPut(key)
	}
	return nil
}

func (m *memoryObjects) Get(ctx context.Context, key storage.ObjectKey) ([]byte, string, error) {
	return m.GetBounded(ctx, key, 1<<40)
}

func (m *memoryObjects) GetBounded(_ context.Context, key storage.ObjectKey, limit int64) ([]byte, string, error) {
	object, ok := m.objects[key]
	if !ok {
		return nil, "", fmt.Errorf("synthetic provider has no object: %w", storage.ErrObjectLookupUncertain)
	}
	if int64(len(object.body)) > limit {
		return nil, "", fmt.Errorf("synthetic object exceeds the bound: %w", storage.ErrObjectLookupUncertain)
	}
	return append([]byte(nil), object.body...), object.contentType, nil
}

func (m *memoryObjects) Delete(_ context.Context, key storage.ObjectKey) error {
	delete(m.objects, key)
	return nil
}

// restoreStore is the production encrypted store, with an optional read-back
// identity override for the one case a real store cannot produce from outside.
type restoreStore struct {
	*storage.EncryptedTranscriptStore
	readback *storage.ContentIdentity
}

func (s restoreStore) RestoreAuthenticatedGeneration(ctx context.Context, id uuid.UUID, d storage.BlobDescriptor, ciphertext []byte) (storage.ContentIdentity, error) {
	actual, err := s.EncryptedTranscriptStore.RestoreAuthenticatedGeneration(ctx, id, d, ciphertext)
	if err == nil && s.readback != nil {
		return *s.readback, nil
	}
	return actual, err
}

func TestRestoreAttestedGenerationRealPostgres(t *testing.T) {
	var fixture struct {
		Cases []restoreCase `yaml:"cases"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(restoreFixtureYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"preserved-copy-restores-and-marks-present":             false,
		"missing-source-is-refused-without-an-attempt":          false,
		"source-with-a-different-identity-is-a-mismatch":        false,
		"failed-write-is-refused-as-write-failed":               false,
		"unproved-write-is-refused-as-uncertain":                false,
		"row-replaced-during-the-write-is-refused":              false,
		"wrong-expected-revision-is-refused-without-an-attempt": false,
	}
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if seen[c.Name] {
			t.Fatalf("duplicate restore case %q", c.Name)
		}
		seen[c.Name] = true
		if _, isRequired := required[c.Name]; isRequired {
			required[c.Name] = true
		}
	}
	for name, present := range required {
		if !present {
			t.Fatalf("missing restore fixture case %q", name)
		}
	}

	ctx := context.Background()
	pool := scratchDatabase(t)
	if err := database.RunMigrations(pool); err != nil {
		t.Fatal(err)
	}
	ring, err := config.ParseTranscriptKeyring("1", `{"1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`)
	if err != nil {
		t.Fatal(err)
	}
	otherHash := schema.ComputeTranscriptHash([]byte("content the row wrongly attests"))
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			objects := &memoryObjects{objects: map[storage.ObjectKey]memoryObject{}}
			encrypted, err := storage.NewEncryptedTranscriptStore(objects, ring)
			if err != nil {
				t.Fatal(err)
			}
			id := uuid.New()
			descriptor, written, err := encrypted.Write(ctx, id, []byte("synthetic preserved transcript"))
			if err != nil {
				t.Fatal(err)
			}
			preserved := objects.objects[descriptor.ObjectKey()]
			source := storage.ObjectKey("transcripts/" + uuid.NewString() + ".bin")
			switch fixtureValue(c.Source) {
			case "preserved":
				objects.objects[source] = memoryObject{body: append([]byte(nil), preserved.body...), contentType: preserved.contentType}
			case "tampered":
				body := append([]byte(nil), preserved.body...)
				body[len(body)-1] ^= 1
				objects.objects[source] = memoryObject{body: body, contentType: preserved.contentType}
			case fixtureMissing:
			default:
				t.Fatalf("fixture source %q is unknown", c.Source)
			}
			divergent := memoryObject{body: []byte("synthetic divergent ciphertext"), contentType: preserved.contentType}
			objects.objects[descriptor.ObjectKey()] = divergent

			attested := written
			switch c.Attested {
			case "matches":
			case "differs":
				if attested, err = storage.NewContentIdentity(otherHash, written.PlaintextSize()); err != nil {
					t.Fatal(err)
				}
			default:
				t.Fatalf("fixture attested %q is unknown", c.Attested)
			}
			row := seedRow{id: id, blobKey: string(descriptor.ObjectKey()), wrappedKey: descriptor.WrappedDEK(), keyVersion: int(descriptor.KeyVersion())}
			hash, size := string(attested.Hash()), attested.PlaintextSize()
			switch fixtureValue(c.Stored) {
			case "primary":
				row.hash, row.size = &hash, &size
			case fixtureNone:
			default:
				t.Fatalf("fixture stored %q is unknown", c.Stored)
			}
			seedTranscriptRow(t, ctx, pool, row)
			if _, err := pool.Exec(ctx, `UPDATE transcripts SET content_state=$2 WHERE id=$1`, id, c.StateBefore); err != nil {
				t.Fatal(err)
			}

			opts := Options{
				RepairID: uuid.New(), TranscriptID: id, Action: RestoreAttestedGeneration,
				ActorID: uuid.New(), AuthorizationRef: "ticket-4", Reason: "restore preserved copy",
				ExpectedRevision: max(c.ExpectedRevision, 1), SourceKey: source,
			}
			attemptedAtPut := -1
			objects.puts = 0
			objects.beforePut = func() {
				attemptedAtPut = countLedgerPhase(t, ctx, pool, opts.RepairID, Attempted)
			}
			store := restoreStore{EncryptedTranscriptStore: encrypted}
			switch c.Put {
			case "ok":
			case "fail":
				objects.putErr = errors.New("synthetic provider write failure")
			case "corrupt":
				objects.afterPut = func(key storage.ObjectKey) {
					object := objects.objects[key]
					object.body[len(object.body)-1] ^= 1
					objects.objects[key] = object
				}
			case "different-readback":
				other, err := storage.NewContentIdentity(otherHash, 3)
				if err != nil {
					t.Fatal(err)
				}
				store.readback = &other
			case "concurrent-change":
				objects.afterPut = func(storage.ObjectKey) { advanceRevision(t, ctx, pool, id) }
			default:
				t.Fatalf("fixture put %q is unknown", c.Put)
			}

			outcome, err := Restore(ctx, Deps{Pool: pool, Queries: sqlc.New(pool), Blobs: store, Now: time.Now}, opts)
			if err != nil {
				t.Fatal(err)
			}
			if outcome.Phase.String() != c.Phase || outcome.FailureClass.String() != c.FailureClass || outcome.ContentRevision != 1 {
				t.Fatalf("outcome phase=%s class=%q revision=%d, want %s %q 1", outcome.Phase, outcome.FailureClass, outcome.ContentRevision, c.Phase, c.FailureClass)
			}
			if objects.puts != c.Puts {
				t.Fatalf("object writes = %d, want %d", objects.puts, c.Puts)
			}
			if c.Puts > 0 && attemptedAtPut != 1 {
				t.Fatalf("attempt rows committed when the object was written = %d, want 1", attemptedAtPut)
			}
			target := objects.objects[descriptor.ObjectKey()].body
			if got := bytes.Equal(target, preserved.body); got != c.Restored {
				t.Fatalf("object holds the preserved bytes = %v, want %v", got, c.Restored)
			}

			var state string
			var revision int64
			var gotHash *string
			var gotSize, segmentedSize *int64
			var segmentedHash *string
			if err := pool.QueryRow(ctx, `SELECT content_state, content_revision, content_hash, blob_size_bytes, segmented_identity_hash, segmented_identity_size FROM transcripts WHERE id=$1`, id).
				Scan(&state, &revision, &gotHash, &gotSize, &segmentedHash, &segmentedSize); err != nil {
				t.Fatal(err)
			}
			wantRevision := max(c.RevisionAfter, 1)
			if state != c.StateAfter || revision != wantRevision || !equalPtr(gotHash, row.hash) || !equalPtr(gotSize, row.size) || segmentedHash != nil || segmentedSize != nil {
				t.Fatalf("row after restore: state=%s revision=%d identity=%v/%v, want state=%s revision=%d identity=%v/%v",
					state, revision, deref(gotHash), deref(gotSize), c.StateAfter, wantRevision, deref(row.hash), deref(row.size))
			}

			rows, err := pool.Query(ctx, `SELECT phase, COALESCE(failure_class,''), prior_revision, resulting_revision,
				prior_identity_hash, prior_identity_size, resulting_identity_hash, resulting_identity_size, operator_actor
				FROM transcript_content_repairs WHERE repair_id=$1 ORDER BY seq`, opts.RepairID)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var phases []string
			for rows.Next() {
				var phase, class string
				var priorRev, resultingRev int64
				var priorHash, resultingHash *string
				var priorSize, resultingSize *int64
				var actor uuid.UUID
				if err := rows.Scan(&phase, &class, &priorRev, &resultingRev, &priorHash, &priorSize, &resultingHash, &resultingSize, &actor); err != nil {
					t.Fatal(err)
				}
				// A restore never changes the revision or the identity, so every
				// row records the same pair before and after, and NULL/NULL when
				// the row carried no identity.
				if priorRev != 1 || resultingRev != 1 || actor != opts.ActorID ||
					!equalPtr(priorHash, row.hash) || !equalPtr(priorSize, row.size) ||
					!equalPtr(resultingHash, row.hash) || !equalPtr(resultingSize, row.size) {
					t.Fatalf("ledger row %s revisions=%d/%d pair=%v/%v->%v/%v", phase, priorRev, resultingRev, deref(priorHash), deref(priorSize), deref(resultingHash), deref(resultingSize))
				}
				if phase != Attempted.String() && class != c.FailureClass {
					t.Fatalf("terminal ledger class = %q, want %q", class, c.FailureClass)
				}
				phases = append(phases, phase)
			}
			want := []string{c.Phase}
			if c.Attempted {
				want = []string{Attempted.String(), c.Phase}
			}
			if fmt.Sprint(phases) != fmt.Sprint(want) {
				t.Fatalf("ledger phases = %v, want %v", phases, want)
			}
		})
	}
}

func countLedgerPhase(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repair uuid.UUID, phase Phase) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcript_content_repairs WHERE repair_id=$1 AND phase=$2`, repair, phase.String()).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// advanceRevision moves the transcript to revision two as another writer
// would, under the identity writer marker the fence requires.
func advanceRevision(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)", database.SystemActorID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE transcripts SET content_revision=2 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
