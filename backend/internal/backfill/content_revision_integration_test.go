//go:build integration

package backfill

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/storage"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/content_revision/cases.yaml
var contentRevisionCasesYAML []byte

type contentRevisionCase struct {
	Name      string `yaml:"name"`
	Job       string `yaml:"job"`
	Identity  string `yaml:"identity"`
	Revision  int64  `yaml:"revision"`
	HoldsLock bool   `yaml:"holds_lock"`
	During    string `yaml:"during"`
	Outcome   string `yaml:"outcome"`
	RowAfter  string `yaml:"row_after"`
}

// revisionObjects is the object provider only. Encryption, authentication of
// the stored identity and key re-wrapping are the production storage code.
type revisionObjects struct {
	mu      sync.Mutex
	objects map[storage.ObjectKey][]byte
}

func (o *revisionObjects) Put(_ context.Context, key storage.ObjectKey, body []byte, _ string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.objects[key] = append([]byte(nil), body...)
	return nil
}

func (o *revisionObjects) Get(ctx context.Context, key storage.ObjectKey) ([]byte, string, error) {
	return o.GetBounded(ctx, key, 1<<40)
}

func (o *revisionObjects) GetBounded(_ context.Context, key storage.ObjectKey, limit int64) ([]byte, string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	body, ok := o.objects[key]
	if !ok || int64(len(body)) > limit {
		return nil, "", fmt.Errorf("synthetic provider cannot serve the object: %w", storage.ErrObjectLookupUncertain)
	}
	return append([]byte(nil), body...), "application/octet-stream", nil
}

func (o *revisionObjects) Delete(_ context.Context, key storage.ObjectKey) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.objects, key)
	return nil
}

// readHookStore runs a hook when the job reads the object, with the job's
// publish lock held, so a case can change the row from outside the lock.
type readHookStore struct {
	*storage.EncryptedTranscriptStore
	beforeRead func()
}

func (s readHookStore) Read(ctx context.Context, id uuid.UUID, d storage.BlobDescriptor, loaded storage.LoadedContentIdentity) ([]byte, storage.ContentIdentity, error) {
	if s.beforeRead != nil {
		s.beforeRead()
	}
	return s.EncryptedTranscriptStore.Read(ctx, id, d, loaded)
}

const revisionTestKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

// The primary pair a concurrent writer installs in the revision-advance case: a
// canonical hash the object does not authenticate to, and a size.
const (
	concurrentReplacementHash       = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	concurrentReplacementSize int64 = 3
)

func TestMaintenanceWritersKeepRevisionAndWaitForThePublishLockRealPostgres(t *testing.T) {
	var fixture struct {
		Required []string              `yaml:"required"`
		Cases    []contentRevisionCase `yaml:"cases"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(contentRevisionCasesYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	// The manifest is the exact set of cases: an unlisted case and a deleted
	// case both fail.
	required := map[string]bool{}
	for _, name := range fixture.Required {
		required[name] = false
	}
	for _, c := range fixture.Cases {
		if seen, known := required[c.Name]; !known || seen {
			t.Fatalf("unknown or duplicate maintenance revision case %q", c.Name)
		}
		required[c.Name] = true
	}
	for name, seen := range required {
		if !seen {
			t.Fatalf("missing maintenance revision fixture case %q", name)
		}
	}

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL is required; refusing to skip real PostgreSQL maintenance proof")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := database.RunMigrations(pool); err != nil {
		t.Fatal(err)
	}
	// Version 2 is the active key for the rewrap; version 1 wrapped the rows.
	ringV1, err := config.ParseTranscriptKeyring("1", fmt.Sprintf(`{"1":%q,"2":%q}`, revisionTestKey, revisionTestKey))
	if err != nil {
		t.Fatal(err)
	}
	ringV2, err := config.ParseTranscriptKeyring("2", fmt.Sprintf(`{"1":%q,"2":%q}`, revisionTestKey, revisionTestKey))
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			objects := &revisionObjects{objects: map[storage.ObjectKey][]byte{}}
			writer, err := storage.NewEncryptedTranscriptStore(objects, ringV1)
			if err != nil {
				t.Fatal(err)
			}
			id := uuid.New()
			content := []byte("synthetic transcript " + c.Name)
			descriptor, actual, err := writer.Write(ctx, id, content)
			if err != nil {
				t.Fatal(err)
			}
			owner := seedRevisionRow(t, ctx, pool, id, descriptor, actual, c)
			defer deleteMaintenanceOwner(t, ctx, pool, owner)
			before := readRevisionRow(t, ctx, pool, id)

			var hook func()
			if c.During == "revision-advance" {
				hook = func() {
					// The transition guard refuses a revision change with no change of
					// identity, so the concurrent writer is a real replacement of the
					// stored identity: another primary pair and the next revision.
					markedExec(t, ctx, pool, "UPDATE transcripts SET content_hash=$2, blob_size_bytes=$3, segmented_identity_hash=NULL, segmented_identity_size=NULL, content_revision=content_revision+1 WHERE id=$1", id, concurrentReplacementHash, concurrentReplacementSize)
				}
			} else if c.During != "" {
				t.Fatalf("fixture during %q is unknown", c.During)
			}
			var run func() (installed, failed, stale int, err error)
			switch c.Job {
			case "backfill":
				store := readHookStore{EncryptedTranscriptStore: writer, beforeRead: hook}
				run = func() (int, int, int, error) {
					r, err := ContentIdentity(ctx, pool, store)
					return r.Installed, r.Failed, r.Stale, err
				}
			case "rewrap":
				rewrapper, err := storage.NewEncryptedTranscriptStore(objects, ringV2)
				if err != nil {
					t.Fatal(err)
				}
				run = func() (int, int, int, error) {
					r, err := Rewrap(ctx, pool, rewrapper, ringV2.ActiveVersion(), 100)
					return r.Installed, r.Failed + r.Uncertain, r.Stale, err
				}
			default:
				t.Fatalf("fixture job %q is unknown", c.Job)
			}

			var installed, failed, stale int
			if c.HoldsLock {
				installed, failed, stale, err = runBehindPublishLock(t, ctx, pool, owner, id, run)
			} else {
				installed, failed, stale, err = run()
			}
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]int{"installed": installed, "failed": failed, "stale": stale}
			for outcome, n := range got {
				want := 0
				if outcome == c.Outcome {
					want = 1
				}
				if n != want {
					t.Fatalf("job counted installed=%d failed=%d stale=%d, want exactly one %s", installed, failed, stale, c.Outcome)
				}
			}

			after := readRevisionRow(t, ctx, pool, id)
			switch c.RowAfter {
			case "consolidated":
				if after.hash != string(actual.Hash()) || after.size != actual.PlaintextSize() || after.segHash != "" || after.segSize != -1 || after.revision != c.Revision {
					t.Fatalf("row after backfill = %+v, want the object's identity as the primary pair, no segmented pair, revision %d", after, c.Revision)
				}
			case "identity-kept":
				if c.During == "revision-advance" {
					if after.hash != concurrentReplacementHash || after.size != concurrentReplacementSize || after.segHash != "" || after.segSize != -1 || after.revision != before.revision+1 {
						t.Fatalf("row changed from %+v to %+v, want only the concurrent replacement's primary pair at revision %d", before, after, before.revision+1)
					}
					break
				}
				wantRevision := before.revision
				if after.hash != before.hash || after.size != before.size || after.segHash != before.segHash || after.segSize != before.segSize || after.revision != wantRevision {
					t.Fatalf("row changed from %+v to %+v, want identity unchanged at revision %d", before, after, wantRevision)
				}
			case "rewrapped":
				if after.keyVersion != int32(ringV2.ActiveVersion()) || after.wrapped == string(descriptor.WrappedDEK()) {
					t.Fatalf("the data key was not re-wrapped under the active key: %+v", after)
				}
				if after.hash != before.hash || after.size != before.size || after.segHash != before.segHash || after.segSize != before.segSize || after.revision != c.Revision || after.blobKey != before.blobKey {
					t.Fatalf("a rewrap changed identity, revision or object key: before %+v after %+v", before, after)
				}
				// The new descriptor still opens the same object to the same identity.
				next, err := storage.NewBlobDescriptor(storage.ObjectKey(after.blobKey), []byte(after.wrapped), descriptor.Algorithm(), storage.KeyVersion(after.keyVersion))
				if err != nil {
					t.Fatal(err)
				}
				rewrapper, _ := storage.NewEncryptedTranscriptStore(objects, ringV2)
				known := storage.NewKnownContentIdentity(actual)
				if _, read, err := rewrapper.Read(ctx, id, next, known); err != nil || read != actual {
					t.Fatalf("the re-wrapped descriptor does not open the object to its identity: %v", err)
				}
			default:
				t.Fatalf("fixture row_after %q is unknown", c.RowAfter)
			}
		})
	}
}

type revisionRow struct {
	hash, segHash, wrapped, blobKey string
	size, segSize                   int64
	revision                        int64
	keyVersion                      int32
}

// readRevisionRow reads the identity columns as text, with an absent hash as ""
// and an absent size as -1, so rows compare with ==.
func readRevisionRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) revisionRow {
	t.Helper()
	var hash, segHash pgtype.Text
	var size, segSize pgtype.Int8
	var row revisionRow
	var wrapped []byte
	if err := pool.QueryRow(ctx, `SELECT content_hash, blob_size_bytes, segmented_identity_hash, segmented_identity_size, content_revision, wrapped_data_key, key_version, blob_key FROM transcripts WHERE id=$1`, id).
		Scan(&hash, &size, &segHash, &segSize, &row.revision, &wrapped, &row.keyVersion, &row.blobKey); err != nil {
		t.Fatal(err)
	}
	row.hash, row.segHash, row.wrapped = hash.String, segHash.String, string(wrapped)
	row.size, row.segSize = -1, -1
	if size.Valid {
		row.size = size.Int64
	}
	if segSize.Valid {
		row.segSize = segSize.Int64
	}
	return row
}

func seedRevisionRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, d storage.BlobDescriptor, actual storage.ContentIdentity, c contentRevisionCase) uuid.UUID {
	t.Helper()
	owner := uuid.New()
	markedExec(t, ctx, pool, "INSERT INTO users(id,github_id,github_username,provider,provider_user_id) VALUES($1,$2,$3,'github',$4)", owner, int64(owner[0])<<16+int64(owner[1])+900000, owner.String(), owner.String())
	var hash, segHash *string
	var size, segSize *int64
	switch c.Identity {
	case "primary-pair":
		h, s := string(actual.Hash()), actual.PlaintextSize()
		hash, size = &h, &s
	case "segmented-equal":
		h, s := string(actual.Hash()), actual.PlaintextSize()
		segHash, segSize = &h, &s
	case "segmented-mismatch":
		h, s := schema.ComputeTranscriptHash([]byte("some other content")), int64(18)
		segHash, segSize = &h, &s
	default:
		t.Fatalf("fixture identity %q is unknown", c.Identity)
	}
	markedExec(t, ctx, pool, "INSERT INTO transcripts(id,owner_id,local_id,visibility,model_provider,blob_key,blob_size_bytes,schema_version,content_hash,wrapped_data_key,encryption_algorithm,key_version,project_hash,segmented_identity_hash,segmented_identity_size,content_revision) VALUES($1,$2,$3,'private','claude',$4,$5,'2',$6,$7,$8,$9,$10,$11,$12,$13)",
		id, owner, id.String(), string(d.ObjectKey()), size, hash, d.WrappedDEK(), string(d.Algorithm()), int32(d.KeyVersion()), "b7c2a90e4d18", segHash, segSize, c.Revision)
	return owner
}

// runBehindPublishLock starts the job while another session holds the
// transcript's publish lock. It proves the job is waiting on the lock, then
// releases it and returns the job's result.
func runBehindPublishLock(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner, id uuid.UUID, run func() (int, int, int, error)) (installed, failed, stale int, err error) {
	t.Helper()
	held, release, holderDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		holderDone <- database.WithSessionPublishLock(ctx, pool, pgtype.UUID{Bytes: owner, Valid: true}, id.String(), func(*pgxpool.Conn) error {
			close(held)
			<-release
			return nil
		})
	}()
	select {
	case <-held:
	case <-time.After(15 * time.Second):
		t.Fatal("the test could not take the publish lock")
	}
	type result struct {
		installed, failed, stale int
		err                      error
	}
	finished := make(chan result, 1)
	before := readRevisionRow(t, ctx, pool, id)
	go func() {
		i, f, s, e := run()
		finished <- result{i, f, s, e}
	}()
	deadline := time.Now().Add(15 * time.Second)
	for advisoryWaiters(t, ctx, pool) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if advisoryWaiters(t, ctx, pool) == 0 {
		close(release)
		<-holderDone
		t.Fatal("the job never waited on the transcript's publish lock")
	}
	select {
	case r := <-finished:
		close(release)
		<-holderDone
		t.Fatalf("the job finished while another session held the publish lock: %+v", r)
	case <-time.After(300 * time.Millisecond):
	}
	if got := readRevisionRow(t, ctx, pool, id); got != before {
		close(release)
		<-holderDone
		t.Fatalf("the job changed the row while the publish lock was held: %+v to %+v", before, got)
	}
	close(release)
	if err := <-holderDone; err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-finished:
		return r.installed, r.failed, r.stale, r.err
	case <-time.After(30 * time.Second):
		return 0, 0, 0, errors.New("the job did not finish after the publish lock was released")
	}
}

// advisoryWaiters counts sessions waiting for an advisory lock in this database.
func advisoryWaiters(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND NOT granted
		AND database=(SELECT oid FROM pg_database WHERE datname=current_database())`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
