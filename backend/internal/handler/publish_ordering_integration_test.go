//go:build integration

package handler

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/schema/route"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/contentstate"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

// This integration family tests production publication and deletion with real
// PostgreSQL and encrypted objects. A unit test cannot observe cross-session
// locks, transaction rollback, or object cleanup. Faults and barriers wrap the
// real store; no publisher or SQL is copied. A test owns its synthetic rows
// and object keys; cleanup removes them even on failure. The disposable
// aggregate runs the exact suite in CI. Mutating the lock, locked read, or
// cleanup changes observable receipts, rows, objects, or lock probes. The
// harness can shrink when an equivalent provider transaction primitive exists.

//go:embed testdata/publish_ordering.yaml
var publishOrderingYAML []byte

//go:embed testdata/canonical-rewrite-preimage.yaml
var canonicalRewritePreimageYAML []byte

type canonicalRewritePreimageCase struct {
	Name   string `yaml:"name"`
	Source string `yaml:"source"`
	Before string `yaml:"before"`
	After  string `yaml:"after"`
	Why    string `yaml:"why"`
}

var requiredCanonicalRewritePreimageCases = []string{
	"rewrite-pending-preimage-move-skips",
}

type publishOrderingExpectation struct {
	Status     int    `yaml:"status"`
	Revision   int64  `yaml:"revision"`
	Reads      int    `yaml:"reads"`
	Writes     int    `yaml:"writes"`
	Visibility string `yaml:"visibility"`
}

type publishOrderingCase struct {
	Name     string                      `yaml:"name"`
	Stage    string                      `yaml:"stage"`
	Scenario string                      `yaml:"scenario"`
	Why      string                      `yaml:"why"`
	Expect   publishOrderingExpectation  `yaml:"expect"`
	Recheck  *publishOrderingExpectation `yaml:"recheck"`
}

var requiredPublishOrderingCases = []string{
	"publish-lock-held-before-predecessor-read",
	"publish-lock-serializes-concurrent-publish",
	"publish-lock-serializes-concurrent-rewrite",
	"publish-lock-released-after-cleanup",
	"publish-lock-unproven-unlock-evicts-connection",
	"republish-uncertain-before-side-effects",
	"republish-uncertain-copy-omits-identity",
	"publish-concurrent-writer-cannot-move-preimage",
	"publish-concurrent-delete-is-definite-not-uncertain",
	"republish-post-classification-upload-failure-restores-audience",
	"republish-post-classification-refusal-deletes-candidate",
	"republish-post-classification-uncertain-answers-503",
	"republish-refusal-audience-restore-unproven-reported",
	"publish-revision-compares-stored-identity",
	"publish-revision-stored-identity-identical-keeps",
	"publish-revision-stored-identity-changed-advances",
	"republish-object-third-value-refused",
	"republish-object-third-value-candidate-stored-refused",
	"republish-object-third-value-after-classification-refused",
	"republish-object-third-value-copy-omits-identity",
	"republish-object-third-value-never-installs-identity",
	"republish-preimage-moved-once-reauthenticates",
	"republish-preimage-moved-twice-refused",
	"republish-preimage-identity-wins",
	"republish-external-object-move-uncertain-answers-503",
	"republish-external-descriptor-move-refused",
	"republish-unavailable-refused-409",
	"republish-usable-identity-absent-refused",
}

func loadPublishOrderingCases(t *testing.T) []publishOrderingCase {
	t.Helper()
	cases, err := decodeFixtureRows[publishOrderingCase](publishOrderingYAML)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for _, c := range cases {
		if c.Name == "" || c.Scenario == "" || c.Why == "" || seen[c.Name] {
			t.Fatalf("ordering case must have a unique name, scenario and reason: %+v", c)
		}
		seen[c.Name] = true
		if c.Stage != "" && c.Stage != "n" && c.Stage != "n1" {
			t.Fatalf("unknown ordering stage %q", c.Stage)
		}
		if c.Recheck != nil && c.Scenario != "recorded-loss" {
			t.Fatalf("unexpected locked-state recheck on scenario %q", c.Scenario)
		}
	}
	for _, name := range requiredPublishOrderingCases {
		if !seen[name] {
			t.Fatalf("required ordering fixture %q is missing", name)
		}
	}
	return cases
}

type orderingGeneration struct {
	body     storage.BlobDescriptor
	metadata *storage.MetadataObject
}

// orderingStore preserves the real encryption and object store and only
// observes or injects faults at the storage boundary.
type orderingStore struct {
	storage.TranscriptBlobStore
	mu          sync.Mutex
	reads       int
	writes      int
	generations []orderingGeneration
	readHook    func(context.Context, int) error
	writeHook   func(context.Context, int) error
	afterWrite  func(context.Context, int) error
	deleteHook  func(context.Context, storage.BlobDescriptor) error
}

func (s *orderingStore) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads, s.writes = 0, 0
	s.readHook, s.writeHook, s.afterWrite, s.deleteHook = nil, nil, nil, nil
}

func (s *orderingStore) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads, s.writes
}

func (s *orderingStore) ReadBounded(ctx context.Context, id uuid.UUID, d storage.BlobDescriptor, loaded storage.LoadedContentIdentity, limit int64) ([]byte, storage.ContentIdentity, error) {
	s.mu.Lock()
	s.reads++
	n, hook := s.reads, s.readHook
	s.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, n); err != nil {
			return nil, storage.ContentIdentity{}, err
		}
	}
	return s.TranscriptBlobStore.ReadBounded(ctx, id, d, loaded, limit)
}

func (s *orderingStore) WriteGeneration(ctx context.Context, id uuid.UUID, body, metadata []byte) (storage.BlobDescriptor, storage.ContentIdentity, *storage.MetadataObject, error) {
	s.mu.Lock()
	s.writes++
	n, before, after := s.writes, s.writeHook, s.afterWrite
	s.mu.Unlock()
	if before != nil {
		if err := before(ctx, n); err != nil {
			return storage.BlobDescriptor{}, storage.ContentIdentity{}, nil, err
		}
	}
	d, identity, m, err := s.TranscriptBlobStore.WriteGeneration(ctx, id, body, metadata)
	if err != nil {
		return d, identity, m, err
	}
	s.mu.Lock()
	s.generations = append(s.generations, orderingGeneration{body: d, metadata: m})
	s.mu.Unlock()
	if after != nil {
		if err := after(ctx, n); err != nil {
			return d, identity, m, err
		}
	}
	return d, identity, m, nil
}

func (s *orderingStore) Delete(ctx context.Context, d storage.BlobDescriptor) error {
	s.mu.Lock()
	hook := s.deleteHook
	s.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, d); err != nil {
			return err
		}
	}
	return s.TranscriptBlobStore.Delete(ctx, d)
}

type orderingWorld struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	objects storage.ObjectStore
	ring    *config.TranscriptKeyring
	store   *orderingStore
	handler *Handler
	owner   *AuthUser
	id      pgtype.UUID
	localID string
	initial sqlc.Transcript
	body    []byte
	extra   []storage.ObjectKey
}

func newOrderingWorld(t *testing.T, pool *pgxpool.Pool, objects storage.ObjectStore, ordinal int, initial []byte) *orderingWorld {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	ownerID := pullInsertUser(t, ctx, pool, int64(998700+ordinal), "ordering-"+uuid.NewString()[:8])
	t.Cleanup(func() { cleanupOwners(t, context.Background(), pool, ownerID) })
	ring, err := config.ParseTranscriptKeyring(os.Getenv("TRANSCRIPT_KEK_ACTIVE_VERSION"), os.Getenv("TRANSCRIPT_KEK_KEYRING"))
	if err != nil {
		t.Fatal(err)
	}
	base, err := storage.NewEncryptedTranscriptStore(objects, ring)
	if err != nil {
		t.Fatal(err)
	}
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatal(err)
	}
	store := &orderingStore{TranscriptBlobStore: base}
	h := &Handler{cfg: minimalConfig(), pool: pool, queries: sqlc.New(pool), blobs: store, titles: titles}
	w := &orderingWorld{t: t, ctx: ctx, pool: pool, objects: objects, ring: ring, store: store,
		handler: h, owner: &AuthUser{ID: uuidFromPg(ownerID), Username: "ordering-owner"},
		localID: uuid.NewString(), body: initial}
	t.Cleanup(func() {
		store.mu.Lock()
		generations := append([]orderingGeneration(nil), store.generations...)
		store.mu.Unlock()
		for _, gen := range generations {
			_ = objects.Delete(context.Background(), gen.body.ObjectKey())
			if gen.metadata != nil {
				_ = objects.Delete(context.Background(), gen.metadata.Key())
			}
		}
		for _, key := range w.extra {
			_ = objects.Delete(context.Background(), key)
		}
	})
	response := w.request(initial, "initial")(ctx)
	if response.Code != http.StatusCreated {
		t.Fatalf("initial publish status=%d body=%s", response.Code, response.Body.String())
	}
	w.id, err = sqlc.New(pool).GetTranscriptIDByOwnerAndLocalID(ctx, sqlc.GetTranscriptIDByOwnerAndLocalIDParams{OwnerID: ownerID, LocalID: w.localID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { purgeAuditRows(t, context.Background(), pool, []pgtype.UUID{w.id}) })
	if response := transcriptVisibilityPatch(t, h, w.owner, w.id, dbVisibilityPublic); response.Code != http.StatusOK {
		t.Fatalf("set public audience: status=%d body=%s", response.Code, response.Body.String())
	}
	w.initial = w.row()
	if !w.initial.PublishMetadataKey.Valid {
		t.Fatal("initial authoritative publication has no metadata generation")
	}
	store.reset()
	return w
}

func (w *orderingWorld) request(body []byte, model string) func(context.Context) *httptest.ResponseRecorder {
	w.t.Helper()
	request := publishEvidenceRequest(w.localID, body, "")
	modelID, err := schema.NewModelID(model)
	if err != nil {
		w.t.Fatal(err)
	}
	request.Model.Model = modelID
	encoded, err := json.Marshal(request)
	if err != nil {
		w.t.Fatal(err)
	}
	multipart, boundary := multipartBody(w.t, map[string]string{"metadata": string(encoded)}, string(body))
	wire := append([]byte(nil), multipart.Bytes()...)
	return func(ctx context.Context) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, route.VillagePublishTranscript.Pattern(), bytes.NewReader(wire))
		r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
		r = r.WithContext(context.WithValue(ctx, UserContextKey, w.owner))
		recorder := httptest.NewRecorder()
		w.handler.PublishTranscript(recorder, r)
		return recorder
	}
}

func (w *orderingWorld) row() sqlc.Transcript {
	w.t.Helper()
	row, err := sqlc.New(w.pool).GetTranscriptByID(w.ctx, w.id)
	if err != nil {
		w.t.Fatal(err)
	}
	return row
}

func (w *orderingWorld) overwrite(ctx context.Context, body []byte) error {
	d, err := descriptorFromTranscript(w.initial)
	if err != nil {
		return err
	}
	id := uuidFromPg(w.id)
	dek, err := w.ring.Unwrap(ctx, d.WrappedDEK(), d.KeyVersion(), []byte("village:transcript-dek:v1:"+id.String()))
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(dek)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return err
	}
	return w.objects.Put(ctx, d.ObjectKey(), gcm.Seal(nil, nil, body, []byte("village:transcript-body:v1:"+id.String())), "application/octet-stream")
}

func (w *orderingWorld) assertLockState(ctx context.Context, rowLocked bool) error {
	conn, err := w.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	key := database.SessionPublishLockKey(w.owner.PgID(), w.localID)
	var acquired bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1,0))", key).Scan(&acquired); err != nil {
		return err
	}
	if acquired {
		_, _ = conn.Exec(ctx, "SELECT pg_advisory_unlock(hashtextextended($1,0))", key)
		return errors.New("publisher did not hold the advisory lock at the object-read boundary")
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var id pgtype.UUID
	err = tx.QueryRow(ctx, "SELECT id FROM transcripts WHERE id=$1 FOR UPDATE NOWAIT", w.id).Scan(&id)
	var pgErr *pgconn.PgError
	if rowLocked {
		if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
			return fmt.Errorf("row was not locked at the second read: %v", err)
		}
	} else if err != nil {
		return fmt.Errorf("row was locked at classification: %w", err)
	}
	return nil
}

func TestPublishOrdering(t *testing.T) {
	pool := publishLockPool(t, 12)
	t.Cleanup(pool.Close)
	objects := publishEvidenceObjects(t)
	for ordinal, c := range loadPublishOrderingCases(t) {
		if c.Stage == "n1" {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			initial := attachmentPublicationContent()
			if c.Scenario == "rewrite-skips" || c.Scenario == "competing-writers" {
				initial = legacyBarePayloadJSON("claude")
			}
			w := newOrderingWorld(t, pool, objects, ordinal, initial)
			runPublishOrderingScenario(t, w, c)
			if c.Recheck != nil {
				w2 := newOrderingWorld(t, pool, objects, 10000+ordinal, initial)
				recheck := c
				recheck.Scenario = "recorded-loss-after"
				recheck.Expect = *c.Recheck
				runPublishOrderingScenario(t, w2, recheck)
			}
		})
	}
}

// TestCanonicalRewritePreimageMove exercises the mounted read and real rewrite
// against PostgreSQL and encrypted object storage. The store hook is an
// out-of-band overwrite between the initial authenticated read and the
// rewrite's classification; an unchanged database descriptor cannot expose it.
func TestCanonicalRewritePreimageMove(t *testing.T) {
	cases, err := decodeFixtureRows[canonicalRewritePreimageCase](canonicalRewritePreimageYAML)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for _, c := range cases {
		if c.Name == "" || c.Why == "" || c.Before == "" || c.After == "" || c.Before == c.After || seen[c.Name] {
			t.Fatalf("invalid canonical rewrite pre-image case: %+v", c)
		}
		seen[c.Name] = true
	}
	for _, name := range requiredCanonicalRewritePreimageCases {
		if !seen[name] {
			t.Fatalf("required canonical rewrite pre-image fixture %q is missing", name)
		}
	}
	pool := publishLockPool(t, 12)
	t.Cleanup(pool.Close)
	objects := publishEvidenceObjects(t)
	for ordinal, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if c.Source != "legacy" {
				t.Fatalf("unknown canonical rewrite fixture source %q", c.Source)
			}
			body := legacyBarePayloadJSON("claude")
			moved := bytes.Replace(body, []byte(c.Before), []byte(c.After), 1)
			if bytes.Equal(body, moved) {
				t.Fatal("fixture did not change the legacy pre-image")
			}
			w := newOrderingWorld(t, pool, objects, 100+ordinal, body)
			// The legacy pre-image has no recorded hash. The transition guard refuses a
			// writer that clears a usable identity, so the fixture bypasses it.
			execFixtureIdentity(t, w.ctx, pool, "UPDATE transcripts SET content_hash=NULL WHERE id=$1", w.id)
			if t.Failed() {
				t.FailNow()
			}
			w.initial = w.row()
			w.store.readHook = func(ctx context.Context, n int) error {
				if n == 1 {
					return w.overwrite(ctx, moved)
				}
				return nil
			}
			id := uuidFromPg(w.id).String()
			path, err := route.VillageGetTranscriptContentPath(id)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request = withChiURLParam(request, "id", id)
			request = request.WithContext(withUserID(request.Context(), w.owner.ID))
			response := httptest.NewRecorder()
			w.handler.GetTranscriptContent(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("read after pre-image moved: status=%d body=%s", response.Code, response.Body.String())
			}
			if reads, writes := w.store.counts(); reads != 1 || writes != 0 {
				t.Fatalf("rewrite after pre-image moved: reads=%d writes=%d; %s", reads, writes, c.Why)
			}
			if got := w.row(); !reflect.DeepEqual(got, w.initial) {
				t.Fatal("rewrite of stale bytes changed the pending transcript row")
			}
			descriptor, err := descriptorFromTranscript(w.initial)
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := storage.NewLoadedContentIdentity(nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			stored, _, err := w.store.TranscriptBlobStore.Read(w.ctx, uuidFromPg(w.id), descriptor, loaded)
			if err != nil || !bytes.Equal(stored, moved) {
				t.Fatalf("moved pre-image was overwritten or unreadable: %v", err)
			}
		})
	}
}

func runPublishOrderingScenario(t *testing.T, w *orderingWorld, c publishOrderingCase) {
	t.Helper()
	switch c.Scenario {
	case "concurrent-publish", "rewrite-skips", "cleanup-blocks", "unlock-eviction", "competing-writers", "account-delete":
		runPublishOrderingInterleaving(t, w, c)
		return
	}
	candidate := republishAudienceRevisedContent()
	if c.Scenario == "identical" || c.Scenario == "third-before-identical" {
		candidate = w.body
	}
	third := bytes.Replace(w.body, []byte("please attach my prompts"), []byte("unattested third value"), 1)
	switch c.Scenario {
	case "lock-order":
		w.store.readHook = func(ctx context.Context, n int) error {
			return w.assertLockState(ctx, n == 2)
		}
	case "changed", "identical":
	case "object-is-candidate":
		if err := w.overwrite(w.ctx, candidate); err != nil {
			t.Fatal(err)
		}
	case "object-is-candidate-after":
		w.store.readHook = func(ctx context.Context, n int) error {
			if n == 2 {
				return w.overwrite(ctx, candidate)
			}
			return nil
		}
	case "third-before", "third-before-identical":
		if err := w.overwrite(w.ctx, third); err != nil {
			t.Fatal(err)
		}
	case "third-after":
		w.store.readHook = func(ctx context.Context, n int) error {
			if n == 2 {
				return w.overwrite(ctx, third)
			}
			return nil
		}
	case "uncertain-before", "known-absent":
		failure := storage.ErrObjectLookupUncertain
		if c.Scenario == "known-absent" {
			failure = storage.ErrObjectAbsentProvider
		}
		w.store.readHook = func(context.Context, int) error { return failure }
	case "recorded-loss":
		if _, err := w.pool.Exec(w.ctx, `UPDATE transcripts SET content_state='unavailable' WHERE id=$1`, w.id); err != nil {
			t.Fatal(err)
		}
		w.initial = w.row()
	case "recorded-loss-after":
		w.store.afterWrite = func(ctx context.Context, _ int) error {
			_, err := w.pool.Exec(ctx, `UPDATE transcripts SET content_state='unavailable' WHERE id=$1`, w.id)
			return err
		}
	case "uncertain-after":
		w.store.readHook = func(_ context.Context, n int) error {
			if n == 2 {
				return storage.ErrObjectLookupUncertain
			}
			return nil
		}
	case "corrupt-after":
		w.store.readHook = func(ctx context.Context, n int) error {
			if n == 2 {
				return w.objects.Put(ctx, storage.ObjectKey(w.initial.BlobKey), []byte("not authenticated ciphertext"), "application/octet-stream")
			}
			return nil
		}
	case "upload-failure", "restore-failure":
		if c.Scenario == "restore-failure" {
			installRepublishRestoreRefusal(t, w.ctx, w.pool, w.id)
		}
		w.store.writeHook = func(context.Context, int) error { return errors.New("synthetic object write failure") }
	case "update-refusal":
		installRepublishRollback(t, w.ctx, w.pool, w.id)
	case "descriptor-moved", "descriptor-moved-twice":
		moves := 1
		if c.Scenario == "descriptor-moved-twice" {
			moves = 2
		}
		w.store.afterWrite = func(ctx context.Context, _ int) error {
			return w.moveDescriptorOutsidePublisher(ctx, moves)
		}
	default:
		t.Fatalf("unhandled ordering scenario %q", c.Scenario)
	}

	response := w.request(candidate, "replacement")(w.ctx)
	expect := c.Expect
	if response.Code != expect.Status {
		t.Fatalf("status=%d want=%d (%s); response=%s", response.Code, expect.Status, c.Why, response.Body.String())
	}
	if response.Code == http.StatusServiceUnavailable && response.Header().Get("Retry-After") == "" {
		t.Fatal("retryable refusal omitted Retry-After")
	}
	if c.Scenario == "restore-failure" && !strings.Contains(response.Body.String(), "visibility") {
		t.Fatalf("unproven audience restore not reported: %s", response.Body.String())
	}
	reads, writes := w.store.counts()
	if reads != expect.Reads || writes != expect.Writes {
		t.Fatalf("read/write calls=%d/%d want %d/%d (%s)", reads, writes, expect.Reads, expect.Writes, c.Why)
	}
	row := w.row()
	if c.Scenario == "recorded-loss" {
		if !strings.Contains(response.Body.String(), "stored content is recorded as lost") || !reflect.DeepEqual(row, w.initial) {
			t.Fatalf("unavailable refusal must name the loss without modifying the row: status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if c.Scenario == "recorded-loss-after" && row.ContentState != contentstate.Unavailable.String() {
		t.Fatal("locked recheck did not preserve the declared unavailable state")
	}
	if row.ContentRevision != expect.Revision || row.Visibility != expect.Visibility {
		t.Fatalf("revision/visibility=%d/%s want %d/%s", row.ContentRevision, row.Visibility, expect.Revision, expect.Visibility)
	}
	if expect.Status == http.StatusOK {
		if row.ContentHash.String != schema.ComputeTranscriptHash(candidate) || row.BlobKey == w.initial.BlobKey {
			t.Fatal("committed replacement did not install the candidate under a new descriptor")
		}
		if objectExists(t, w.ctx, w.objects, w.initial.BlobKey) || objectExists(t, w.ctx, w.objects, w.initial.PublishMetadataKey.String) {
			t.Fatal("committed replacement retained a superseded object")
		}
	} else {
		if row.ContentHash != w.initial.ContentHash || row.SegmentedIdentityHash != w.initial.SegmentedIdentityHash ||
			row.SegmentedIdentitySize != w.initial.SegmentedIdentitySize || row.BlobSizeBytes != w.initial.BlobSizeBytes {
			t.Fatal("refusal changed a stored content identity")
		}
		moved := c.Scenario == "descriptor-moved" || c.Scenario == "descriptor-moved-twice"
		if (row.BlobKey != w.initial.BlobKey) != moved {
			t.Fatal("refusal changed the descriptor other than the explicitly injected move")
		}
		if !objectExists(t, w.ctx, w.objects, w.initial.BlobKey) || !objectExists(t, w.ctx, w.objects, w.initial.PublishMetadataKey.String) {
			t.Fatal("refusal deleted the predecessor body or metadata")
		}
		for _, secret := range []string{w.localID, uuidFromPg(w.id).String(), w.initial.BlobKey, w.initial.ContentHash.String, string(w.body), "ordering-owner"} {
			if secret != "" && strings.Contains(response.Body.String(), secret) {
				t.Fatalf("refusal disclosed synthetic identifying material %q", secret)
			}
		}
	}
	w.store.mu.Lock()
	generations := append([]orderingGeneration(nil), w.store.generations...)
	w.store.mu.Unlock()
	if len(generations) > 1 {
		candidateGeneration := generations[len(generations)-1]
		if candidateGeneration.metadata == nil {
			t.Fatal("authoritative candidate has no metadata object")
		}
		wantPresent := expect.Status == http.StatusOK
		if objectExists(t, w.ctx, w.objects, string(candidateGeneration.body.ObjectKey())) != wantPresent ||
			objectExists(t, w.ctx, w.objects, string(candidateGeneration.metadata.Key())) != wantPresent {
			t.Fatal("candidate body and metadata cleanup did not match the transaction outcome")
		}
	}
}

func (w *orderingWorld) moveDescriptorOutsidePublisher(ctx context.Context, moves int) error {
	ciphertext, contentType, err := w.objects.Get(ctx, storage.ObjectKey(w.initial.BlobKey))
	if err != nil {
		return err
	}
	for i := 0; i < moves; i++ {
		key := storage.ObjectKey("transcripts/" + uuid.NewString() + ".bin")
		if err := w.objects.Put(ctx, key, ciphertext, contentType); err != nil {
			return err
		}
		w.extra = append(w.extra, key)
		tx, err := w.pool.Begin(ctx)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)", w.owner.ID.String())
		if err == nil {
			_, err = tx.Exec(ctx, "UPDATE transcripts SET blob_key=$1 WHERE id=$2", string(key), w.id)
		}
		if err != nil {
			_ = tx.Rollback(context.Background())
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func receiveOrderingResponse(t *testing.T, ctx context.Context, result <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case response := <-result:
		return response
	case <-ctx.Done():
		t.Fatalf("timed out waiting for mounted publish: %v", ctx.Err())
		return nil
	}
}

func waitOrderingSignal(t *testing.T, ctx context.Context, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatalf("timed out waiting for storage boundary: %v", ctx.Err())
	}
}

// orderingAdvisoryLockPredicate restricts lock observations to this case's
// randomly named transcript, so another test's advisory lock cannot satisfy a
// wait or make the eviction case terminate an unrelated database session.
const orderingAdvisoryLockPredicate = `
	l.locktype = 'advisory'
	AND l.database = (SELECT oid FROM pg_database WHERE datname = current_database())
	AND l.classid = ((hashtextextended($1, 0) >> 32) & 4294967295)::oid
	AND l.objid = (hashtextextended($1, 0) & 4294967295)::oid
	AND l.objsubid = 1`

func awaitOrderingAdvisoryWait(ctx context.Context, pool *pgxpool.Pool, key string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_locks l WHERE `+orderingAdvisoryLockPredicate+` AND NOT l.granted
		)`, key).Scan(&waiting)
		if err != nil {
			return err
		}
		if waiting {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("transcript advisory-lock waiter was not observed: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func runPublishOrderingInterleaving(t *testing.T, w *orderingWorld, c publishOrderingCase) {
	t.Helper()
	candidate := republishAudienceRevisedContent()
	switch c.Scenario {
	case "account-delete":
		w.store.afterWrite = func(ctx context.Context, _ int) error {
			// DeleteAccount is exercised directly; this path is relative to
			// its router group because the current contract does not declare it.
			request := httptest.NewRequest(http.MethodDelete, "/auth/me", nil)
			request = request.WithContext(context.WithValue(ctx, UserContextKey, w.owner))
			response := httptest.NewRecorder()
			w.handler.DeleteAccount(response, request)
			if response.Code != http.StatusOK {
				return fmt.Errorf("account cascade status=%d", response.Code)
			}
			return nil
		}
		response := w.request(candidate, "replacement")(w.ctx)
		if response.Code != http.StatusConflict || response.Header().Get("Retry-After") != "" {
			t.Fatalf("account cascade returned status=%d response=%s", response.Code, response.Body.String())
		}
		_, err := sqlc.New(w.pool).GetTranscriptByID(w.ctx, w.id)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("account cascade left a transcript row: %v", err)
		}
		w.store.mu.Lock()
		generations := append([]orderingGeneration(nil), w.store.generations...)
		w.store.mu.Unlock()
		if len(generations) != 2 || generations[1].metadata == nil ||
			objectExists(t, w.ctx, w.objects, string(generations[1].body.ObjectKey())) ||
			objectExists(t, w.ctx, w.objects, string(generations[1].metadata.Key())) {
			t.Fatal("account cascade did not clean both candidate objects")
		}
		return
	case "unlock-eviction":
		var logs bytes.Buffer
		previous := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
		defer slog.SetDefault(previous)
		var holder int32
		w.store.readHook = func(ctx context.Context, n int) error {
			if n != 1 {
				return nil
			}
			key := database.SessionPublishLockKey(w.owner.PgID(), w.localID)
			err := w.pool.QueryRow(ctx, `
				SELECT l.pid FROM pg_locks l
				WHERE `+orderingAdvisoryLockPredicate+` AND l.granted`, key).Scan(&holder)
			if err != nil {
				return err
			}
			var terminated bool
			if err := w.pool.QueryRow(ctx, "SELECT pg_terminate_backend($1)", holder).Scan(&terminated); err != nil {
				return err
			}
			if !terminated {
				return errors.New("failed to terminate the pinned lock holder")
			}
			return nil
		}
		response := w.request(candidate, "replacement")(w.ctx)
		if response.Code < 500 || holder == 0 || !strings.Contains(logs.String(), "could not prove the physical session is unlocked") {
			t.Fatalf("dead lock holder did not force eviction: status=%d holder=%d logs=%s", response.Code, holder, logs.String())
		}
		conn, err := w.pool.Acquire(w.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		var nextPID int32
		var acquired bool
		key := database.SessionPublishLockKey(w.owner.PgID(), w.localID)
		if err := conn.QueryRow(w.ctx, "SELECT pg_backend_pid(),pg_try_advisory_lock(hashtextextended($1,0))", key).Scan(&nextPID, &acquired); err != nil {
			t.Fatal(err)
		}
		if !acquired || nextPID == holder {
			t.Fatalf("pool returned the dead or locked session: next=%d old=%d acquired=%t", nextPID, holder, acquired)
		}
		_, _ = conn.Exec(w.ctx, "SELECT pg_advisory_unlock(hashtextextended($1,0))", key)
		return
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	wait := func(ctx context.Context) error {
		enteredOnce.Do(func() { close(entered) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if c.Scenario == "cleanup-blocks" {
		w.store.deleteHook = func(ctx context.Context, descriptor storage.BlobDescriptor) error {
			if descriptor.ObjectKey() == storage.ObjectKey(w.initial.BlobKey) {
				return wait(ctx)
			}
			return nil
		}
	} else {
		w.store.writeHook = func(ctx context.Context, n int) error {
			if n == 1 {
				return wait(ctx)
			}
			return nil
		}
	}
	firstRequest := w.request(candidate, "first-replacement")
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstDone <- firstRequest(w.ctx) }()
	waitOrderingSignal(t, w.ctx, entered)

	if c.Scenario == "rewrite-skips" || c.Scenario == "competing-writers" {
		id := uuidFromPg(w.id).String()
		path, err := route.VillageGetTranscriptContentPath(id)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request = withChiURLParam(request, "id", id)
		request = request.WithContext(withUserID(request.Context(), w.owner.ID))
		response := httptest.NewRecorder()
		w.handler.GetTranscriptContent(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("read during held publish lock status=%d body=%s", response.Code, response.Body.String())
		}
		if _, writes := w.store.counts(); writes != 1 {
			t.Fatalf("read repair wrote while publish lock was held: writes=%d", writes)
		}
		if c.Scenario == "rewrite-skips" {
			unblock()
			response = receiveOrderingResponse(t, w.ctx, firstDone)
			if response.Code != http.StatusOK || w.row().ContentRevision != 2 {
				t.Fatalf("publisher did not complete after skipped rewrite: %d %s", response.Code, response.Body.String())
			}
			return
		}
	}

	secondBody := bytes.Replace(candidate, []byte("revised"), []byte("second"), 1)
	secondRequest := w.request(secondBody, "second-replacement")
	secondDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { secondDone <- secondRequest(w.ctx) }()

	var maintenanceDone chan error
	if c.Scenario == "competing-writers" {
		maintenanceDone = make(chan error, 1)
		go func() {
			maintenanceDone <- database.WithSessionPublishLock(w.ctx, w.pool, w.owner.PgID(), w.localID, func(conn *pgxpool.Conn) error {
				row, err := sqlc.New(conn).GetTranscriptByID(w.ctx, w.id)
				if err == nil && row.ContentHash == w.initial.ContentHash {
					return errors.New("maintenance acquired the lock before the replacement")
				}
				return err
			})
		}()
	}
	if err := awaitOrderingAdvisoryWait(w.ctx, w.pool, database.SessionPublishLockKey(w.owner.PgID(), w.localID)); err != nil {
		t.Fatal(err)
	}
	if reads, writes := w.store.counts(); reads != 2 && c.Scenario == "cleanup-blocks" || reads != 1 && c.Scenario != "cleanup-blocks" || writes != 1 {
		t.Fatalf("a waiting writer crossed the held lock: reads=%d writes=%d", reads, writes)
	}
	unblock()
	first := receiveOrderingResponse(t, w.ctx, firstDone)
	second := receiveOrderingResponse(t, w.ctx, secondDone)
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("serialized publications failed: first=%d %s second=%d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	if maintenanceDone != nil {
		select {
		case err := <-maintenanceDone:
			if err != nil {
				t.Fatalf("maintenance lock did not serialize: %v", err)
			}
		case <-w.ctx.Done():
			t.Fatal("maintenance lock did not finish")
		}
	}
	row := w.row()
	if row.ContentRevision != 3 || row.ContentHash.String != schema.ComputeTranscriptHash(secondBody) {
		t.Fatalf("second publisher did not classify the first committed generation: revision=%d", row.ContentRevision)
	}
	if reads, writes := w.store.counts(); reads != 4 || writes != 2 {
		t.Fatalf("serialized publish counts=%d/%d want 4/2", reads, writes)
	}
}
