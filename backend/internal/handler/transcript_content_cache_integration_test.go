//go:build integration

package handler

import (
	"context"
	_ "embed"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

// This family drives the cache over real PostgreSQL, the encrypted store over
// RustFS and the mounted write paths. The production openCachedTranscript is
// the code under test; only a read counter, and for one case a gate, wrap the
// store.

//go:embed testdata/content_cache_encrypted.yaml
var contentCacheEncryptedYAML []byte

type contentCacheEncryptedCase struct {
	Name     string `yaml:"name"`
	Scenario string `yaml:"scenario"`
	Why      string `yaml:"why"`
}

var requiredContentCacheEncryptedCases = []string{
	"cache-single-decrypt-for-main-then-history",
	"cache-single-flight-two-viewers",
	"cache-partition-index-survives-the-hit",
	"cache-purge-on-publish",
	"cache-purge-on-visibility",
	"cache-purge-on-rewrite",
	"cache-purge-on-delete",
	"cache-purge-on-account-delete",
	"cache-purge-on-unshare",
	"cache-generation-move-observed-on-read",
}

// gatedReadStore counts authenticated reads and holds each at a gate.
type gatedReadStore struct {
	decryptionCountingStore
	gate chan struct{}
}

func (s *gatedReadStore) Read(ctx context.Context, id uuid.UUID, d storage.BlobDescriptor, loaded storage.LoadedContentIdentity) ([]byte, storage.ContentIdentity, error) {
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-ctx.Done():
			return nil, storage.ContentIdentity{}, ctx.Err()
		}
	}
	return s.decryptionCountingStore.Read(ctx, id, d, loaded)
}

func TestTranscriptContentCacheEncrypted(t *testing.T) {
	fixture, err := decodeFixtureDocument[struct {
		Content        string                      `yaml:"content"`
		HistoryContent string                      `yaml:"history_content"`
		Cases          []contentCacheEncryptedCase `yaml:"cases"`
	}](contentCacheEncryptedYAML)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" || c.Scenario == "" || c.Why == "" || seen[c.Name] {
			t.Fatalf("encrypted content cache case must have a unique name, scenario and reason: %+v", c)
		}
		seen[c.Name] = true
	}
	for _, name := range requiredContentCacheEncryptedCases {
		if !seen[name] {
			t.Fatalf("required encrypted content cache case %q is missing", name)
		}
		delete(seen, name)
	}
	for name := range seen {
		t.Fatalf("encrypted content cache case %q is not in the required-name manifest", name)
	}

	pool := publishLockPool(t, 8)
	t.Cleanup(pool.Close)
	objects := publishEvidenceObjects(t)
	allow := func(sqlc.Transcript) bool { return true }

	for ordinal, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if c.Scenario == "purge-unshare" {
				testCachePurgeOnUnshare(t, pool)
				return
			}
			w := newOrderingWorld(t, pool, objects, 31000+ordinal, []byte(fixture.Content))
			cache := newTranscriptContentCache(transcriptContentCacheConfig{})
			w.handler.contentCache = cache
			gated := &gatedReadStore{decryptionCountingStore: decryptionCountingStore{TranscriptBlobStore: w.handler.blobs}}
			w.handler.blobs = gated
			history := strings.ReplaceAll(fixture.HistoryContent, "__SESSION_ID__", w.localID)
			republish := func() {
				t.Helper()
				if replaced := w.request([]byte(history), "history")(w.ctx); replaced.Code != http.StatusOK {
					t.Fatalf("republish status=%d body=%s", replaced.Code, replaced.Body.String())
				}
			}
			open := func(row sqlc.Transcript) *openedTranscript {
				t.Helper()
				opened, err := w.handler.openCachedTranscript(w.ctx, row, allow)
				if err != nil {
					t.Fatalf("open the stored generation: %v", err)
				}
				return opened
			}
			row := w.row()
			key := transcriptCacheKeyForRow(row)
			if key.Harness != "claude-code" || key.ModelProvider != row.ModelProvider {
				t.Fatalf("the published row names provider %q, so the key's harness %q does not match the fixture's cooking class", row.ModelProvider, key.Harness)
			}

			switch c.Scenario {
			case "main-then-history":
				republish()
				row = w.row()
				gated.reads = 0
				mainRead := open(row)
				if _, err := mainRead.Index.Partition(mainPartitionKey(), noTranscriptBuildReserve); err != nil {
					t.Fatal(err)
				}
				historyRead := open(row)
				if _, err := historyRead.Index.Partition(historyPartitionKey(0), noTranscriptBuildReserve); err != nil {
					t.Fatal(err)
				}
				if got := gated.count(); got != 1 || historyRead != mainRead {
					t.Fatalf("a main read then a history read decrypted %d times (same entry: %v), want 1 and the same entry", got, historyRead == mainRead)
				}

			case "two-viewers":
				gated.gate = make(chan struct{})
				results := make([]<-chan cacheResult, 2)
				for i := range results {
					out := make(chan cacheResult, 1)
					results[i] = out
					go func() {
						opened, err := w.handler.openCachedTranscript(w.ctx, row, allow)
						out <- cacheResult{opened, err}
					}()
				}
				cacheEventually(t, "both viewers to join one load", func() bool { return cacheWaiters(cache, key) == 2 })
				close(gated.gate)
				var first *openedTranscript
				for i, ch := range results {
					res := <-ch
					if res.err != nil {
						t.Fatalf("viewer %d: %v", i, res.err)
					}
					if first == nil {
						first = res.opened
					} else if res.opened != first {
						t.Fatalf("viewer %d was served another generation", i)
					}
				}
				if got := gated.count(); got != 1 {
					t.Fatalf("two viewers of one generation caused %d authenticated reads, want 1", got)
				}

			case "partition-survives":
				republish()
				row = w.row()
				gated.reads = 0
				mainRead := open(row)
				built, err := mainRead.Index.Partition(mainPartitionKey(), noTranscriptBuildReserve)
				if err != nil {
					t.Fatal(err)
				}
				again := open(row)
				gotMain, err := again.Index.Partition(mainPartitionKey(), noTranscriptBuildReserve)
				if err != nil || gotMain != built {
					t.Fatalf("the retained entry did not hold the index it built (%v)", err)
				}
				history, err := again.Index.Partition(historyPartitionKey(0), noTranscriptBuildReserve)
				if err != nil || history == built || len(history.Rows) == 0 {
					t.Fatalf("history section index: %+v (%v)", history, err)
				}
				if got := gated.count(); got != 1 {
					t.Fatalf("authenticated reads = %d, want 1", got)
				}

			case "purge-publish":
				open(row)
				if !cache.has(key) {
					t.Fatal("the read did not warm an entry")
				}
				republish()
				if cache.has(key) {
					t.Fatal("a republish left the replaced generation in the cache")
				}
				fresh := w.row()
				if fresh.ContentRevision == row.ContentRevision {
					t.Fatalf("the republish kept revision %d; this case needs changed content", row.ContentRevision)
				}
				gated.reads = 0
				if opened := open(fresh); opened.Index.Revision != contentGeneration(fresh.ContentRevision) || gated.count() != 1 {
					t.Fatalf("the next read did not load the new generation (revision %s, reads %d)", opened.Index.Revision, gated.count())
				}

			case "purge-visibility":
				open(row)
				if !cache.has(key) {
					t.Fatal("the read did not warm an entry")
				}
				if response := transcriptVisibilityPatch(t, w.handler, w.owner, w.id, dbVisibilityPrivate); response.Code != http.StatusOK {
					t.Fatalf("visibility change status=%d body=%s", response.Code, response.Body.String())
				}
				if cache.has(key) {
					t.Fatal("a visibility change left the entry in the cache")
				}
				if got := cache.stats().Purged[purgeVisibility]; got != 1 {
					t.Fatalf("visibility purges = %d, want 1", got)
				}

			case "purge-rewrite":
				open(row)
				if !cache.has(key) {
					t.Fatal("the read did not warm an entry")
				}
				source := rewriteFixtureSourceIdentity(t, w.ctx, gated.TranscriptBlobStore, row)
				installed, err := w.handler.rewriteCanonicalTranscript(w.ctx, row, source, []byte(fixture.Content), ShapeUnknown)
				if err != nil || installed == nil {
					t.Fatalf("rewrite: installed=%v err=%v", installed, err)
				}
				after := w.row()
				if got := transcriptCacheKeyForRow(after); got != key {
					t.Fatalf("the rewrite moved the key (%+v to %+v); this case needs the same key", key, got)
				}
				if cache.has(key) {
					t.Fatal("a rewrite left the replaced generation under the same key")
				}

			case "purge-delete":
				open(row)
				if !cache.has(key) {
					t.Fatal("the read did not warm an entry")
				}
				if response := w.deleteTranscript(); response.Code != http.StatusOK {
					t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
				}
				if cache.has(key) {
					t.Fatal("a delete left the entry in the cache")
				}
				if _, err := w.handler.openCachedTranscript(w.ctx, row, allow); err == nil {
					t.Fatal("a deleted transcript's generation still opened")
				}

			case "purge-account":
				open(row)
				if !cache.has(key) {
					t.Fatal("the read did not warm an entry")
				}
				// unstring-allow: go-endpoint-literal — account deletion is not declared by the contract
				r := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/me", nil)
				r = r.WithContext(context.WithValue(w.ctx, UserContextKey, w.owner))
				recorder := httptest.NewRecorder()
				w.handler.DeleteAccount(recorder, r)
				if recorder.Code != http.StatusOK {
					t.Fatalf("account delete status=%d body=%s", recorder.Code, recorder.Body.String())
				}
				if cache.has(key) {
					t.Fatal("an account deletion left a cascaded transcript's entry in the cache")
				}

			case "observe-generation":
				open(row)
				if !cache.has(key) {
					t.Fatal("the read did not warm an entry")
				}
				// Another process writes: this process's purge is not reached.
				w.handler.contentCache = nil
				republish()
				w.handler.contentCache = cache
				if !cache.has(key) {
					t.Fatal("the simulated foreign write reached this process's cache")
				}
				fresh := w.row()
				gated.reads = 0
				opened := open(fresh)
				if cache.has(key) || opened.Index.Revision != contentGeneration(fresh.ContentRevision) || gated.count() != 1 {
					t.Fatalf("the read did not drop the older generation and load the new one (old retained %v, revision %s, reads %d)", cache.has(key), opened.Index.Revision, gated.count())
				}
				if got := cache.stats().Purged[purgeGenerationMoved]; got != 1 {
					t.Fatalf("generation-moved purges = %d, want 1", got)
				}

			default:
				t.Fatalf("fixture scenario %q is unknown", c.Scenario)
			}
		})
	}
}

// testCachePurgeOnUnshare shares a transcript to a real collective and
// withdraws it through the mounted handlers. The transcript row has no stored
// object, so the entry is warmed with a loader, keyed by the real row; what is
// under test is that the mounted unshare path reaches the purge.
func testCachePurgeOnUnshare(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	h := New(&config.Config{}, pool, nil)
	world := newShareWorldAt(t, ctx, pool, "cache-unshare", "open", dbVisibilityPrivate)
	defer world.cleanup(t, ctx)
	if status := world.submitTo(t, h, world.group); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("share status=%d", status)
	}
	row, err := sqlc.New(pool).GetTranscriptByID(ctx, world.transcript)
	if err != nil {
		t.Fatal(err)
	}
	key := transcriptCacheKeyForRow(row)
	loader := &cacheTestLoader{t: t, key: key, size: 300}
	if _, err := h.contentCache.Acquire(ctx, key, transcriptCacheLoad{Owner: uuid.UUID(row.OwnerID.Bytes), Weight: 300}, loader.load); err != nil {
		t.Fatal(err)
	}
	if !h.contentCache.has(key) {
		t.Fatal("the warm-up did not retain an entry")
	}
	if status := world.unshareFrom(t, h, world.group); status != http.StatusOK {
		t.Fatalf("unshare status=%d", status)
	}
	if h.contentCache.has(key) {
		t.Fatal("an unshare left the entry in the cache")
	}
	if got := h.contentCache.stats().Purged[purgeUnshare]; got != 1 {
		t.Fatalf("unshare purges = %d, want 1", got)
	}
}
