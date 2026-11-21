package handler

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/peasant-labs/schema"
)

//go:embed testdata/content_cache.yaml
var contentCacheYAML []byte

type contentCacheCase struct {
	Name     string   `yaml:"name"`
	Scenario string   `yaml:"scenario"`
	Why      string   `yaml:"why"`
	Budget   int64    `yaml:"budget"`
	Sizes    []int    `yaml:"sizes"`
	Readers  int      `yaml:"readers"`
	Turns    int      `yaml:"turns"`
	Vary     []string `yaml:"vary"`
	Fields   []string `yaml:"fields"`
}

// requiredContentCacheCases is the exact set of cases the fixture holds: a
// deleted case and an unlisted case both fail.
var requiredContentCacheCases = []string{
	"cache-single-flight-two-viewers",
	"cache-single-flight-joiner-survives-starter-cancel",
	"cache-abandoned-load-is-canceled-and-releases-its-weight",
	"cache-full-of-cold-entries-evicts-least-recent",
	"cache-all-in-flight-exhaustion-refuses-and-evicts-nothing",
	"cache-admission-counts-reserved-bytes",
	"cache-oversized-single-flight",
	"cache-retention-is-strict-after-load",
	"cache-lifetime-expiry",
	"cache-sweeper-ticker-drops-idle-entries",
	"cache-key-varies-by-revision-model-provider-and-harness",
	"cache-key-holds-no-build-component",
	"cache-partition-index-keyed-by-partition-and-section",
	"cache-single-decrypt-for-main-then-history",
	"cache-load-failure-is-not-remembered",
	"cache-purge-on-write-drops-transcript-and-owner",
	"cache-purge-during-load-is-not-retained",
	"cache-purge-on-generation-move",
	"cache-purge-on-harness-change",
	"cache-entry-cooked-under-another-harness-is-not-retained",
	"cache-absent-cache-loads-and-purges-nothing",
	"cache-index-memory-counts-against-budget",
	"cache-index-memory-charged-through-the-read",
}

func loadContentCacheCases(t *testing.T) []contentCacheCase {
	t.Helper()
	cases, err := decodeFixtureRows[contentCacheCase](contentCacheYAML)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || c.Scenario == "" || c.Why == "" || seen[c.Name] {
			t.Fatalf("content cache case must have a unique name, a scenario and a reason: %+v", c.Name)
		}
		seen[c.Name] = true
	}
	for _, name := range requiredContentCacheCases {
		if !seen[name] {
			t.Fatalf("required content cache case %q is missing from the fixture", name)
		}
		delete(seen, name)
	}
	for name := range seen {
		t.Fatalf("content cache case %q is not in the required-name manifest; add it", name)
	}
	return cases
}

// cacheClock is a settable clock.
type cacheClock struct {
	mu sync.Mutex
	at time.Time
}

func newCacheClock() *cacheClock { return &cacheClock{at: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }

func (c *cacheClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *cacheClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// cacheTestKey is one transcript's generation. Distinct ordinals are distinct
// transcripts.
func cacheTestKey(ordinal int) transcriptCacheKey {
	return transcriptCacheKey{
		ID:            uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("content-cache-%d", ordinal))),
		Revision:      1,
		ModelProvider: "claude-code",
		Harness:       schema.HarnessClaudeCode,
	}
}

// cacheTestDocument is a canonical envelope of exactly size bytes whose turn
// content is padded to reach it, with one earlier-history section per entry of
// history.
func cacheTestDocument(t *testing.T, harness schema.Harness, size int, history int) []byte {
	t.Helper()
	build := func(pad string) string {
		var b strings.Builder
		b.WriteString(`{"contractVersion":"0.1.0","kind":"session_detail","sessionDetail":{"id":"cache-fixture","harness":"` + string(harness) + `","turns":[{"index":0,"role":"user","content":"` + pad + `","timestamp":"2026-01-01T00:00:01Z","depth":0}]`)
		if history > 0 {
			b.WriteString(`,"earlierHistory":[`)
			for i := 0; i < history; i++ {
				if i > 0 {
					b.WriteByte(',')
				}
				b.WriteString(`{"state":"uncertain_migrated","turns":[{"index":0,"role":"user","content":"older","timestamp":"2025-12-31T00:00:01Z","depth":0}]}`)
			}
			b.WriteByte(']')
		}
		b.WriteString(`}}`)
		return b.String()
	}
	base := len(build(""))
	if size < base {
		t.Fatalf("a %d-byte cache document cannot hold the envelope, which needs %d bytes", size, base)
	}
	return []byte(build(strings.Repeat("x", size-base)))
}

// cacheTestOpened builds an opened generation that holds key's revision and
// provider and a document of the given size cooked under key's harness.
func cacheTestOpened(t *testing.T, key transcriptCacheKey, size int, history int) *openedTranscript {
	t.Helper()
	index, err := newTranscriptIndex(transcriptIndexInput{
		Revision:      key.Revision,
		ModelProvider: key.ModelProvider,
		Bytes:         cacheTestDocument(t, key.Harness, size, history),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &openedTranscript{Index: index, Bytes: index}
}

// cacheTestWeight is the weight the cache retains a fresh generation of this
// size at, before any partition is built: its bytes plus its header and history
// summaries.
func cacheTestWeight(t *testing.T, key transcriptCacheKey, size int, history int) int64 {
	t.Helper()
	return cacheTestOpened(t, key, size, history).Index.retainedWeight()
}

// cacheTestShortTurnsDocument is a canonical envelope of turns one-word turns
// in the main partition and in each of history sections: little text, many
// rows, so its indexes outweigh its bytes.
func cacheTestShortTurnsDocument(turns int, history int) []byte {
	array := func(stamp string) string {
		var b strings.Builder
		b.WriteByte('[')
		for i := 0; i < turns; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			fmt.Fprintf(&b, `{"index":%d,"role":"%s","content":"ok","timestamp":"%s","depth":0}`, i, role, stamp)
		}
		b.WriteByte(']')
		return b.String()
	}
	var b strings.Builder
	b.WriteString(`{"contractVersion":"0.1.0","kind":"session_detail","sessionDetail":{"id":"short-turns","harness":"claude-code","turns":` + array("2026-01-01T00:00:01Z"))
	if history > 0 {
		b.WriteString(`,"earlierHistory":[`)
		for i := 0; i < history; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`{"state":"uncertain_migrated","turns":` + array("2025-12-31T00:00:01Z") + `}`)
		}
		b.WriteByte(']')
	}
	b.WriteString(`}}`)
	return []byte(b.String())
}

// chargingReserve is the reservation the read path hands a build: a heavy-work
// slot whose release charges the cache for the index the build added.
func chargingReserve(c *transcriptContentCache, id uuid.UUID, x *transcriptIndex) transcriptBuildReserve {
	return func() (func(), error) {
		release, err := c.AcquireBuild(context.Background())
		if err != nil {
			return nil, err
		}
		return func() {
			release()
			c.Charge(id, x)
		}, nil
	}
}

// cacheTestLoader counts loads and can hold them at a gate.
type cacheTestLoader struct {
	t     *testing.T
	key   transcriptCacheKey
	size  int
	hist  int
	gate  chan struct{} // nil: do not wait
	loads atomic.Int32
	// canceled is closed when a load saw its context canceled.
	canceled chan struct{}
	failWith error
	// as, when set, is the generation the load reports instead of key.
	as *transcriptCacheKey
}

func (l *cacheTestLoader) load(ctx context.Context) (*openedTranscript, error) {
	l.loads.Add(1)
	if l.gate != nil {
		select {
		case <-l.gate:
		case <-ctx.Done():
			if l.canceled != nil {
				close(l.canceled)
			}
			return nil, ctx.Err()
		}
	}
	if l.failWith != nil {
		return nil, l.failWith
	}
	key := l.key
	if l.as != nil {
		key = *l.as
	}
	return cacheTestOpened(l.t, key, l.size, l.hist), nil
}

func cacheEventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// cacheWaiters is how many readers wait on the load in flight for key.
func cacheWaiters(c *transcriptContentCache, key transcriptCacheKey) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if flight, ok := c.flights[key]; ok {
		return flight.waiters
	}
	return 0
}

type cacheResult struct {
	opened *openedTranscript
	err    error
}

func cacheAcquireAsync(c *transcriptContentCache, ctx context.Context, key transcriptCacheKey, weight int64, loader *cacheTestLoader) <-chan cacheResult {
	out := make(chan cacheResult, 1)
	go func() {
		opened, err := c.Acquire(ctx, key, transcriptCacheLoad{Owner: uuid.Nil, Weight: weight}, loader.load)
		out <- cacheResult{opened, err}
	}()
	return out
}

func cacheAcquire(t *testing.T, c *transcriptContentCache, key transcriptCacheKey, loader *cacheTestLoader) *openedTranscript {
	t.Helper()
	opened, err := c.Acquire(context.Background(), key, transcriptCacheLoad{Weight: int64(loader.size)}, loader.load)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	return opened
}

func (c contentCacheCase) newCache(clock *cacheClock) *transcriptContentCache {
	cfg := transcriptContentCacheConfig{Budget: c.Budget}
	if clock != nil {
		cfg.Now = clock.Now
	}
	return newTranscriptContentCache(cfg)
}

func (c contentCacheCase) loader(t *testing.T, key transcriptCacheKey, size int) *cacheTestLoader {
	return &cacheTestLoader{t: t, key: key, size: size}
}

func TestTranscriptContentCache(t *testing.T) {
	for _, c := range loadContentCacheCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			ctx := context.Background()
			switch c.Scenario {
			case "single-flight":
				cache := c.newCache(nil)
				key := cacheTestKey(1)
				loader := c.loader(t, key, c.Sizes[0])
				loader.gate = make(chan struct{})
				results := make([]<-chan cacheResult, c.Readers)
				for i := range results {
					results[i] = cacheAcquireAsync(cache, ctx, key, int64(c.Sizes[0]), loader)
				}
				cacheEventually(t, "every reader to join the one load", func() bool { return cacheWaiters(cache, key) == c.Readers })
				close(loader.gate)
				var first *openedTranscript
				for i, ch := range results {
					res := <-ch
					if res.err != nil {
						t.Fatalf("reader %d: %v", i, res.err)
					}
					if first == nil {
						first = res.opened
					} else if res.opened != first {
						t.Fatalf("reader %d was served a different generation than reader 0", i)
					}
				}
				if got := loader.loads.Load(); got != 1 {
					t.Fatalf("%d readers caused %d loads, want 1", c.Readers, got)
				}
				if got := cacheAcquire(t, cache, key, loader); got != first || loader.loads.Load() != 1 {
					t.Fatalf("a later read did not hit the retained entry (loads=%d)", loader.loads.Load())
				}

			case "joiner-survives-cancel":
				cache := c.newCache(nil)
				key := cacheTestKey(1)
				loader := c.loader(t, key, c.Sizes[0])
				loader.gate = make(chan struct{})
				loader.canceled = make(chan struct{})
				starterCtx, cancelStarter := context.WithCancel(ctx)
				starter := cacheAcquireAsync(cache, starterCtx, key, int64(c.Sizes[0]), loader)
				cacheEventually(t, "the starter's load to begin", func() bool { return cacheWaiters(cache, key) == 1 })
				joiner := cacheAcquireAsync(cache, ctx, key, int64(c.Sizes[0]), loader)
				cacheEventually(t, "the second reader to join", func() bool { return cacheWaiters(cache, key) == 2 })
				cancelStarter()
				if res := <-starter; !errors.Is(res.err, context.Canceled) {
					t.Fatalf("the starter that left got %v, want context.Canceled", res.err)
				}
				select {
				case <-loader.canceled:
					t.Fatal("the load was canceled while a reader still waited on it")
				case <-time.After(20 * time.Millisecond):
				}
				close(loader.gate)
				if res := <-joiner; res.err != nil || res.opened == nil {
					t.Fatalf("the joiner got (%v, %v), want the generation", res.opened, res.err)
				}
				if got := loader.loads.Load(); got != 1 {
					t.Fatalf("loads = %d, want 1", got)
				}

			case "abandoned-load":
				cache := c.newCache(nil)
				key := cacheTestKey(1)
				loader := c.loader(t, key, c.Sizes[0])
				loader.gate = make(chan struct{})
				loader.canceled = make(chan struct{})
				readerCtx, cancelReader := context.WithCancel(ctx)
				reader := cacheAcquireAsync(cache, readerCtx, key, int64(c.Sizes[0]), loader)
				cacheEventually(t, "the load to begin", func() bool { return cacheWaiters(cache, key) == 1 })
				cancelReader()
				if res := <-reader; !errors.Is(res.err, context.Canceled) {
					t.Fatalf("the reader that left got %v", res.err)
				}
				select {
				case <-loader.canceled:
				case <-time.After(5 * time.Second):
					t.Fatal("the load was not canceled after its last reader left")
				}
				cacheEventually(t, "the abandoned flight to clear", func() bool { return cache.stats().InFlight == 0 })
				// The whole budget is free again: a load as heavy as the budget is admitted.
				other := cacheTestKey(2)
				if _, err := cache.Acquire(ctx, other, transcriptCacheLoad{Weight: c.Budget}, c.loader(t, other, c.Sizes[0]).load); err != nil {
					t.Fatalf("the abandoned load kept its weight reserved: %v", err)
				}

			case "eviction-least-recent":
				cache := c.newCache(nil)
				keys := []transcriptCacheKey{cacheTestKey(1), cacheTestKey(2), cacheTestKey(3)}
				loaders := make([]*cacheTestLoader, len(keys))
				for i := range keys {
					loaders[i] = c.loader(t, keys[i], c.Sizes[i])
				}
				cacheAcquire(t, cache, keys[0], loaders[0])
				cacheAcquire(t, cache, keys[1], loaders[1])
				// The first entry is read again, so the second is the least recent.
				cacheAcquire(t, cache, keys[0], loaders[0])
				// With every heavy-work slot taken the read is refused on count,
				// and a read refused for any reason evicts nothing.
				a, _ := cache.AcquireBuild(ctx)
				b, _ := cache.AcquireBuild(ctx)
				if _, err := cache.Acquire(ctx, keys[2], transcriptCacheLoad{Weight: int64(c.Sizes[2])}, loaders[2].load); !errors.Is(err, errTranscriptReadBusy) {
					t.Fatalf("a read with every slot taken = %v, want the bounded refusal", err)
				}
				if !cache.has(keys[1]) || cache.stats().Purged[purgeEvicted] != 0 || loaders[2].loads.Load() != 0 {
					t.Fatalf("a read refused on count evicted an entry or loaded: %+v", cache.stats())
				}
				a()
				b()
				opened, err := cache.Acquire(ctx, keys[2], transcriptCacheLoad{Weight: int64(c.Sizes[2])}, loaders[2].load)
				if err != nil || opened == nil {
					t.Fatalf("a read beside cold retained entries = (%v, %v), want it admitted by eviction", opened, err)
				}
				if !cache.has(keys[0]) || cache.has(keys[1]) || !cache.has(keys[2]) {
					t.Fatalf("eviction took the wrong entry: first %v second %v third %v", cache.has(keys[0]), cache.has(keys[1]), cache.has(keys[2]))
				}
				stats := cache.stats()
				want := cacheTestWeight(t, keys[0], c.Sizes[0], 0) + cacheTestWeight(t, keys[2], c.Sizes[2], 0)
				if stats.Purged[purgeEvicted] != 1 || stats.RetainedBytes != want || stats.RetainedBytes > c.Budget || stats.InFlight != 0 {
					t.Fatalf("eviction accounting: %+v, want one eviction and %d bytes retained", stats, want)
				}
				if loaders[0].loads.Load() != 1 || loaders[2].loads.Load() != 1 {
					t.Fatal("the hot entry was reloaded or the new generation loaded twice")
				}

			case "in-flight-exhaustion":
				cache := c.newCache(nil)
				kept, running, refusedKey := cacheTestKey(1), cacheTestKey(2), cacheTestKey(3)
				cacheAcquire(t, cache, kept, c.loader(t, kept, c.Sizes[0]))
				holder := c.loader(t, running, c.Sizes[1])
				holder.gate = make(chan struct{})
				inFlight := cacheAcquireAsync(cache, ctx, running, int64(c.Sizes[1]), holder)
				cacheEventually(t, "the load to start", func() bool { return cacheWaiters(cache, running) == 1 })
				refused := c.loader(t, refusedKey, c.Sizes[2])
				opened, err := cache.Acquire(ctx, refusedKey, transcriptCacheLoad{Weight: int64(c.Sizes[2])}, refused.load)
				if !errors.Is(err, errTranscriptReadBusy) || opened != nil || refused.loads.Load() != 0 {
					t.Fatalf("a read with the budget reserved by loads in flight = (%v, %v, loads %d), want the bounded refusal and no read", opened, err, refused.loads.Load())
				}
				if !cache.has(kept) || cache.stats().Purged[purgeEvicted] != 0 || cache.has(refusedKey) {
					t.Fatalf("a refused read evicted a retained entry or left one: %+v", cache.stats())
				}
				close(holder.gate)
				if res := <-inFlight; res.err != nil {
					t.Fatal(res.err)
				}
				cacheAcquire(t, cache, refusedKey, refused)
				if !cache.has(refusedKey) || cache.stats().RetainedBytes > c.Budget {
					t.Fatalf("once the load ended the read was not admitted within the budget: %+v", cache.stats())
				}

			case "admission-refusal-reserved":
				cache := c.newCache(nil)
				first, second := cacheTestKey(1), cacheTestKey(2)
				holder := c.loader(t, first, c.Sizes[0])
				holder.gate = make(chan struct{})
				running := cacheAcquireAsync(cache, ctx, first, int64(c.Sizes[0]), holder)
				cacheEventually(t, "the first load to start", func() bool { return cacheWaiters(cache, first) == 1 })
				refused := c.loader(t, second, c.Sizes[1])
				if _, err := cache.Acquire(ctx, second, transcriptCacheLoad{Weight: int64(c.Sizes[1])}, refused.load); !errors.Is(err, errTranscriptReadBusy) || refused.loads.Load() != 0 {
					t.Fatalf("a load beside a reserving load = %v (loads %d), want the bounded refusal and no read", err, refused.loads.Load())
				}
				close(holder.gate)
				if res := <-running; res.err != nil {
					t.Fatal(res.err)
				}
				cache.PurgeTranscript(first.ID, purgePublish)
				cacheAcquire(t, cache, second, refused)

			case "oversized-single-flight":
				cache := c.newCache(nil)
				bigX, bigY, small := cacheTestKey(1), cacheTestKey(2), cacheTestKey(3)
				holder := c.loader(t, bigX, c.Sizes[0])
				holder.gate = make(chan struct{})
				viewers := make([]<-chan cacheResult, c.Readers)
				for i := range viewers {
					viewers[i] = cacheAcquireAsync(cache, ctx, bigX, int64(c.Sizes[0]), holder)
				}
				cacheEventually(t, "both viewers to share the oversized load", func() bool { return cacheWaiters(cache, bigX) == c.Readers })
				other := c.loader(t, bigY, c.Sizes[1])
				if _, err := cache.Acquire(ctx, bigY, transcriptCacheLoad{Weight: int64(c.Sizes[1])}, other.load); !errors.Is(err, errTranscriptReadBusy) || other.loads.Load() != 0 {
					t.Fatalf("a second oversized transcript beside an active one = %v (loads %d), want the bounded refusal", err, other.loads.Load())
				}
				cacheAcquire(t, cache, small, c.loader(t, small, c.Sizes[2]))
				close(holder.gate)
				var first *openedTranscript
				for i, ch := range viewers {
					res := <-ch
					if res.err != nil {
						t.Fatalf("viewer %d: %v", i, res.err)
					}
					if first == nil {
						first = res.opened
					} else if res.opened != first {
						t.Fatalf("viewer %d did not share the one oversized load", i)
					}
				}
				if got := holder.loads.Load(); got != 1 {
					t.Fatalf("%d viewers of one oversized transcript caused %d loads, want 1", c.Readers, got)
				}
				if cache.has(bigX) {
					t.Fatal("an oversized generation was retained")
				}
				if stats := cache.stats(); stats.Entries != 1 || stats.RetainedBytes != cacheTestWeight(t, small, c.Sizes[2], 0) {
					t.Fatalf("the small load beside it was not the only retained entry: %+v", stats)
				}
				cacheAcquire(t, cache, bigY, other)
				if holder.loads.Load() != 1 || other.loads.Load() != 1 {
					t.Fatalf("loads after the slot freed: first %d second %d", holder.loads.Load(), other.loads.Load())
				}

			case "strict-retention":
				cache := c.newCache(nil)
				kept, oversized, heavy := cacheTestKey(1), cacheTestKey(2), cacheTestKey(3)
				cacheAcquire(t, cache, kept, c.loader(t, kept, c.Sizes[0]))
				// Each row recorded a small size; the bytes it holds are large.
				opened, err := cache.Acquire(ctx, oversized, transcriptCacheLoad{Weight: 100}, c.loader(t, oversized, c.Sizes[1]).load)
				if err != nil || opened == nil {
					t.Fatalf("a load admitted on its estimate must be served: (%v, %v)", opened, err)
				}
				if cache.has(oversized) || !cache.has(kept) || cache.stats().RetainedBytes != cacheTestWeight(t, kept, c.Sizes[0], 0) {
					t.Fatalf("an entry heavier than the budget was retained or evicted another: %+v", cache.stats())
				}
				opened, err = cache.Acquire(ctx, heavy, transcriptCacheLoad{Weight: 100}, c.loader(t, heavy, c.Sizes[2]).load)
				if err != nil || opened == nil {
					t.Fatalf("a load admitted on its estimate must be served: (%v, %v)", opened, err)
				}
				stats := cache.stats()
				if !cache.has(heavy) || cache.has(kept) || stats.RetainedBytes != cacheTestWeight(t, heavy, c.Sizes[2], 0) || stats.RetainedBytes > c.Budget {
					t.Fatalf("an entry that fits only alone did not evict the older one within the budget: %+v", stats)
				}

			case "lifetime-expiry":
				clock := newCacheClock()
				cache := c.newCache(clock)
				first, second := cacheTestKey(1), cacheTestKey(2)
				loader := c.loader(t, first, c.Sizes[0])
				cacheAcquire(t, cache, first, loader)
				clock.Advance(transcriptContentCacheIdleLifetime - time.Second)
				if dropped := cache.Sweep(); dropped != 0 {
					t.Fatalf("a sweep a second before the lifetime dropped %d entries", dropped)
				}
				cacheAcquire(t, cache, first, loader) // a read refreshes the entry
				clock.Advance(transcriptContentCacheIdleLifetime - time.Second)
				if dropped := cache.Sweep(); dropped != 0 {
					t.Fatalf("a read did not refresh the lifetime: the sweep dropped %d", dropped)
				}
				clock.Advance(2 * time.Second)
				cacheAcquire(t, cache, second, c.loader(t, second, c.Sizes[1]))
				if got := cache.stats().Entries; got != 2 {
					t.Fatalf("a request swept: %d entries remain, want 2 until the ticker runs", got)
				}
				if dropped := cache.Sweep(); dropped != 1 || cache.has(first) || !cache.has(second) {
					t.Fatalf("the sweep dropped %d entries; want exactly the idle one", dropped)
				}
				if got := cache.stats(); got.Purged[purgeIdle] != 1 || got.RetainedBytes != cacheTestWeight(t, second, c.Sizes[1], 0) {
					t.Fatalf("idle accounting: %+v", got)
				}

			case "sweeper-ticker":
				clock := newCacheClock()
				cache := newTranscriptContentCache(transcriptContentCacheConfig{Budget: c.Budget, Now: clock.Now, SweepInterval: 2 * time.Millisecond})
				key := cacheTestKey(1)
				cacheAcquire(t, cache, key, c.loader(t, key, c.Sizes[0]))
				clock.Advance(transcriptContentCacheIdleLifetime + time.Second)
				cache.StartSweeper()
				cache.StartSweeper() // idempotent
				cacheEventually(t, "the ticker to drop the idle entry", func() bool { return cache.stats().Entries == 0 })
				drain, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				if err := cache.DrainSweeper(drain); err != nil {
					t.Fatal(err)
				}
				if err := cache.DrainSweeper(drain); err != nil {
					t.Fatalf("a second drain: %v", err)
				}

			case "key-varies":
				cache := c.newCache(nil)
				base := cacheTestKey(1)
				baseLoader := c.loader(t, base, c.Sizes[0])
				baseOpened := cacheAcquire(t, cache, base, baseLoader)
				varied := map[string]transcriptCacheKey{}
				for _, dimension := range c.Vary {
					key := base
					switch dimension {
					case "revision":
						key.Revision = base.Revision + 1
					case "model_provider":
						key.ModelProvider = "codex"
					case "cooking_class":
						key.Harness = schema.HarnessPi
					default:
						t.Fatalf("fixture varies unknown key component %q", dimension)
					}
					varied[dimension] = key
				}
				for dimension, key := range varied {
					loader := c.loader(t, key, c.Sizes[0])
					opened := cacheAcquire(t, cache, key, loader)
					if opened == baseOpened || loader.loads.Load() != 1 {
						t.Fatalf("a different %s was served the base generation (loads %d)", dimension, loader.loads.Load())
					}
				}
				if got := cache.stats().Entries; got != 1+len(varied) {
					t.Fatalf("%d entries, want one per distinct key (%d)", got, 1+len(varied))
				}
				if again := cacheAcquire(t, cache, base, baseLoader); again != baseOpened || baseLoader.loads.Load() != 1 {
					t.Fatal("the base key no longer hits after other keys were added")
				}

			case "key-fields":
				var got []string
				typ := reflect.TypeOf(transcriptCacheKey{})
				for i := 0; i < typ.NumField(); i++ {
					got = append(got, typ.Field(i).Name)
				}
				want := append([]string(nil), c.Fields...)
				sort.Strings(got)
				sort.Strings(want)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("cache key fields = %v, want exactly %v", got, want)
				}

			case "partition-keyed":
				cache := c.newCache(nil)
				key := cacheTestKey(1)
				loader := c.loader(t, key, c.Sizes[0])
				loader.hist = 2
				opened := cacheAcquire(t, cache, key, loader)
				main, err := opened.Index.Partition(mainPartitionKey(), noTranscriptBuildReserve)
				if err != nil {
					t.Fatal(err)
				}
				first, err := opened.Index.Partition(historyPartitionKey(0), noTranscriptBuildReserve)
				if err != nil {
					t.Fatal(err)
				}
				second, err := opened.Index.Partition(historyPartitionKey(1), noTranscriptBuildReserve)
				if err != nil {
					t.Fatal(err)
				}
				if main == first || first == second || main == second {
					t.Fatal("partitions of one entry share an index")
				}
				if _, err := opened.Index.Partition(partitionKey{Kind: schema.TranscriptPartitionMain, Section: 0}, noTranscriptBuildReserve); !errors.Is(err, errPartitionAbsent) {
					t.Fatalf("a main key that names a section resolved: %v", err)
				}
				again := cacheAcquire(t, cache, key, loader)
				gotMain, _ := again.Index.Partition(mainPartitionKey(), noTranscriptBuildReserve)
				gotSecond, _ := again.Index.Partition(historyPartitionKey(1), noTranscriptBuildReserve)
				if again != opened || gotMain != main || gotSecond != second {
					t.Fatal("a later read of the entry did not find the indexes it already held")
				}
				wantKeys := []partitionKey{mainPartitionKey(), historyPartitionKey(0), historyPartitionKey(1)}
				if len(opened.Index.Partitions) != len(wantKeys) {
					t.Fatalf("entry holds %d indexes, want %d", len(opened.Index.Partitions), len(wantKeys))
				}
				for _, want := range wantKeys {
					if _, ok := opened.Index.Partitions[want]; !ok {
						t.Fatalf("entry holds no index for %+v", want)
					}
				}

			case "main-then-history":
				cache := c.newCache(nil)
				key := cacheTestKey(1)
				loader := c.loader(t, key, c.Sizes[0])
				loader.hist = 1
				mainRead := cacheAcquire(t, cache, key, loader)
				if _, err := mainRead.Index.Partition(mainPartitionKey(), noTranscriptBuildReserve); err != nil {
					t.Fatal(err)
				}
				historyRead := cacheAcquire(t, cache, key, loader)
				if _, err := historyRead.Index.Partition(historyPartitionKey(0), noTranscriptBuildReserve); err != nil {
					t.Fatal(err)
				}
				if historyRead != mainRead || loader.loads.Load() != 1 {
					t.Fatalf("a main read then a history read loaded %d times, want 1", loader.loads.Load())
				}

			case "failed-load":
				cache := c.newCache(nil)
				key := cacheTestKey(1)
				boom := errors.New("object store down")
				loader := c.loader(t, key, c.Sizes[0])
				loader.gate = make(chan struct{})
				loader.failWith = boom
				results := make([]<-chan cacheResult, c.Readers)
				for i := range results {
					results[i] = cacheAcquireAsync(cache, ctx, key, int64(c.Sizes[0]), loader)
				}
				cacheEventually(t, "every reader to join", func() bool { return cacheWaiters(cache, key) == c.Readers })
				close(loader.gate)
				for i, ch := range results {
					if res := <-ch; !errors.Is(res.err, boom) || res.opened != nil {
						t.Fatalf("reader %d got (%v, %v), want the load's error", i, res.opened, res.err)
					}
				}
				if stats := cache.stats(); stats.Entries != 0 || stats.RetainedBytes != 0 || stats.InFlight != 0 {
					t.Fatalf("a failed load left state behind: %+v", stats)
				}
				loader.failWith = nil
				cacheAcquire(t, cache, key, loader)
				if got := loader.loads.Load(); got != 2 {
					t.Fatalf("loads = %d, want a fresh load after the failure", got)
				}

			case "purge-scopes":
				cache := c.newCache(nil)
				owner, other := uuid.New(), uuid.New()
				aRev1, aRev2, b, d := cacheTestKey(1), cacheTestKey(1), cacheTestKey(2), cacheTestKey(3)
				aRev2.Revision = 2
				load := func(key transcriptCacheKey, owner uuid.UUID) {
					loader := c.loader(t, key, c.Sizes[0])
					if _, err := cache.Acquire(ctx, key, transcriptCacheLoad{Owner: owner, Weight: int64(c.Sizes[0])}, loader.load); err != nil {
						t.Fatal(err)
					}
				}
				load(aRev1, owner)
				load(aRev2, owner)
				load(b, owner)
				load(d, other)
				if dropped := cache.PurgeTranscript(aRev1.ID, purgePublish); dropped != 2 {
					t.Fatalf("purging one transcript dropped %d entries, want both of its revisions", dropped)
				}
				if cache.has(aRev1) || cache.has(aRev2) || !cache.has(b) || !cache.has(d) {
					t.Fatal("the transcript purge touched another transcript")
				}
				if dropped := cache.PurgeOwner(owner, purgeDelete); dropped != 1 || cache.has(b) || !cache.has(d) {
					t.Fatalf("the owner purge dropped %d entries and must leave another owner's", dropped)
				}
				stats := cache.stats()
				if stats.Purged[purgePublish] != 2 || stats.Purged[purgeDelete] != 1 || stats.Entries != 1 || stats.RetainedBytes != cacheTestWeight(t, d, c.Sizes[0], 0) {
					t.Fatalf("purge accounting: %+v", stats)
				}

			case "purge-during-load":
				cache := c.newCache(nil)
				key := cacheTestKey(1)
				loader := c.loader(t, key, c.Sizes[0])
				loader.gate = make(chan struct{})
				reading := cacheAcquireAsync(cache, ctx, key, int64(c.Sizes[0]), loader)
				cacheEventually(t, "the load to begin", func() bool { return cacheWaiters(cache, key) == 1 })
				cache.PurgeTranscript(key.ID, purgeRewrite)
				close(loader.gate)
				if res := <-reading; res.err != nil || res.opened == nil {
					t.Fatalf("the reader of an invalidated load got (%v, %v), want it served", res.opened, res.err)
				}
				if cache.has(key) || cache.stats().RetainedBytes != 0 {
					t.Fatal("a load that a purge overtook was retained")
				}
				loader.gate = nil
				cacheAcquire(t, cache, key, loader)
				if !cache.has(key) || loader.loads.Load() != 2 {
					t.Fatalf("the next read did not load and retain afresh (loads %d)", loader.loads.Load())
				}

			case "generation-moved":
				cache := c.newCache(nil)
				old := cacheTestKey(1)
				cacheAcquire(t, cache, old, c.loader(t, old, c.Sizes[0]))
				moved := old
				moved.Revision = 2
				if dropped := cache.Observe(moved); dropped != 1 || cache.has(old) {
					t.Fatalf("observing a newer revision dropped %d entries; the older generation must go", dropped)
				}
				if got := cache.stats().Purged[purgeGenerationMoved]; got != 1 {
					t.Fatalf("generation-moved purges = %d, want 1", got)
				}
				// A load that reports a newer revision than it was asked for.
				asked := moved
				asked.Revision = 3
				reported := asked
				reported.Revision = 4
				cacheAcquire(t, cache, moved, c.loader(t, moved, c.Sizes[0]))
				loader := c.loader(t, asked, c.Sizes[0])
				loader.as = &reported
				opened := cacheAcquire(t, cache, asked, loader)
				if opened.Index.Revision != 4 || cache.has(asked) || !cache.has(reported) || cache.has(moved) {
					t.Fatalf("the entry was not kept under the revision it holds (asked %v, held %v)", asked.Revision, opened.Index.Revision)
				}

			case "harness-changed":
				cache := c.newCache(nil)
				old := cacheTestKey(1)
				cacheAcquire(t, cache, old, c.loader(t, old, c.Sizes[0]))
				changed := old
				changed.Harness = schema.HarnessPi
				changed.ModelProvider = "pi"
				if dropped := cache.Observe(changed); dropped != 1 || cache.has(old) {
					t.Fatalf("observing another harness dropped %d entries", dropped)
				}
				stats := cache.stats()
				if stats.Purged[purgeHarnessChanged] != 1 || stats.Purged[purgeGenerationMoved] != 0 {
					t.Fatalf("a harness change was counted as %+v", stats.Purged)
				}

			case "harness-mismatch":
				cache := c.newCache(nil)
				key := cacheTestKey(1)
				loader := c.loader(t, key, c.Sizes[0])
				cooked := key
				cooked.Harness = schema.HarnessPi
				loader.as = &cooked
				opened := cacheAcquire(t, cache, key, loader)
				if opened == nil || cache.has(key) || cache.stats().Entries != 0 {
					t.Fatalf("an entry cooked under another class was retained: %+v", cache.stats())
				}

			case "nil-cache":
				var cache *transcriptContentCache
				key := cacheTestKey(1)
				loader := c.loader(t, key, 300)
				if _, err := cache.Acquire(ctx, key, transcriptCacheLoad{}, loader.load); err != nil || loader.loads.Load() != 1 {
					t.Fatalf("a nil cache must load directly: loads=%d err=%v", loader.loads.Load(), err)
				}
				if cache.PurgeTranscript(key.ID, purgePublish) != 0 || cache.PurgeOwner(uuid.New(), purgeDelete) != 0 || cache.Observe(key) != 0 || cache.Sweep() != 0 {
					t.Fatal("a nil cache dropped something")
				}
				var h Handler
				h.purgeTranscriptContent(toPgUUID(key.ID), purgePublish)
				h.purgeOwnerContent(toPgUUID(key.ID), purgeDelete)
				h.StartContentCacheSweeper()
				if err := h.DrainContentCache(ctx); err != nil {
					t.Fatal(err)
				}

			case "index-memory":
				testCacheIndexMemory(t, c)

			case "index-memory-read":
				h, mux, base, x := admissionHandler(t, true)
				before := h.contentCache.stats().RetainedBytes
				if before != x.retainedWeight() || len(x.Partitions) != 0 {
					t.Fatalf("the cold entry is weighed at %d, want %d with no index built", before, x.retainedWeight())
				}
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+"/segments?v=2", nil))
				if w.Code != http.StatusOK {
					t.Fatalf("directory read: %d %s", w.Code, w.Body.String())
				}
				main := x.Partitions[mainPartitionKey()]
				if main == nil || main.memory <= 0 {
					t.Fatal("the read built no main index")
				}
				if got := h.contentCache.stats().RetainedBytes; got != before+main.memory || got != x.retainedWeight() {
					t.Fatalf("after the read the entry is weighed at %d, want %d (bytes, summaries and the main index)", got, before+main.memory)
				}

			default:
				t.Fatalf("fixture scenario %q is unknown", c.Scenario)
			}
		})
	}
}

// testCacheIndexMemory drives builds of a many-short-turn generation with
// several history sections through the cache, with the reservation the read
// path uses, and checks every build is charged and the budget holds.
func testCacheIndexMemory(t *testing.T, c contentCacheCase) {
	const sections = 3
	turns := c.Turns
	key := cacheTestKey(1)
	open := func() *openedTranscript {
		index, err := newTranscriptIndex(transcriptIndexInput{Revision: key.Revision, ModelProvider: key.ModelProvider, Bytes: cacheTestShortTurnsDocument(turns, sections)})
		if err != nil {
			t.Fatal(err)
		}
		return &openedTranscript{Index: index, Bytes: index}
	}
	load := func(opened *openedTranscript) transcriptCacheLoader {
		return func(context.Context) (*openedTranscript, error) { return opened, nil }
	}

	// Each build raises the entry's weight by the index it built.
	cache := c.newCache(nil)
	opened := open()
	x := opened.Index
	if _, err := cache.Acquire(context.Background(), key, transcriptCacheLoad{Weight: int64(len(x.Bytes))}, load(opened)); err != nil {
		t.Fatal(err)
	}
	reserve := chargingReserve(cache, key.ID, x)
	weight := cache.stats().RetainedBytes
	if weight != x.retainedWeight() {
		t.Fatalf("the cold entry is weighed at %d, want %d", weight, x.retainedWeight())
	}
	parts := []partitionKey{mainPartitionKey()}
	for i := 0; i < sections; i++ {
		parts = append(parts, historyPartitionKey(i))
	}
	for _, part := range parts {
		built, err := x.Partition(part, reserve)
		if err != nil {
			t.Fatal(err)
		}
		// A row costs at least its served-row record and its verdict.
		if floor := int64(turns) * (indexedTurnBytes + cookedTurnBytes); built.memory < floor {
			t.Fatalf("partition %+v is estimated at %d bytes, below the %d its %d rows hold", part, built.memory, floor, turns)
		}
		got := cache.stats().RetainedBytes
		if got != weight+built.memory {
			t.Fatalf("after building %+v the entry is weighed at %d, want %d", part, got, weight+built.memory)
		}
		weight = got
		// A second read of a built partition charges nothing more.
		if _, err := x.Partition(part, reserve); err != nil || cache.stats().RetainedBytes != weight {
			t.Fatalf("a repeated read of %+v changed the weight: %d -> %d (%v)", part, weight, cache.stats().RetainedBytes, err)
		}
	}
	if index := weight - int64(len(x.Bytes)); index <= int64(len(x.Bytes)) {
		t.Fatalf("the fixture's indexes weigh %d bytes against %d bytes of text; the case needs index-heavy content", index, len(x.Bytes))
	}

	// A grown entry evicts a colder one rather than break the budget.
	full := weight
	baseWeight := open().Index.retainedWeight()
	cold := cacheTestKey(2)
	coldSize := int(full-baseWeight) / 2
	budgeted := newTranscriptContentCache(transcriptContentCacheConfig{Budget: full + int64(coldSize)/2})
	if _, err := budgeted.Acquire(context.Background(), cold, transcriptCacheLoad{Weight: int64(coldSize)}, c.loader(t, cold, coldSize).load); err != nil {
		t.Fatal(err)
	}
	grown := open()
	if _, err := budgeted.Acquire(context.Background(), key, transcriptCacheLoad{Weight: int64(len(grown.Index.Bytes))}, load(grown)); err != nil {
		t.Fatal(err)
	}
	if !budgeted.has(cold) || !budgeted.has(key) {
		t.Fatal("both cold generations should fit before any index is built")
	}
	for _, part := range parts {
		if _, err := grown.Index.Partition(part, chargingReserve(budgeted, key.ID, grown.Index)); err != nil {
			t.Fatal(err)
		}
		if got := budgeted.stats().RetainedBytes; got > budgeted.budget {
			t.Fatalf("building %+v left %d bytes retained over the %d budget", part, got, budgeted.budget)
		}
	}
	if budgeted.has(cold) || !budgeted.has(key) || budgeted.stats().Purged[purgeEvicted] != 1 {
		t.Fatalf("the grown entry did not evict the colder one: %+v", budgeted.stats())
	}

	// An entry whose indexes outgrow the whole budget leaves the cache, and
	// its reader keeps the index it holds.
	alone := newTranscriptContentCache(transcriptContentCacheConfig{Budget: baseWeight + (full-baseWeight)/2})
	solo := open()
	if _, err := alone.Acquire(context.Background(), key, transcriptCacheLoad{Weight: int64(len(solo.Index.Bytes))}, load(solo)); err != nil {
		t.Fatal(err)
	}
	for _, part := range parts {
		if _, err := solo.Index.Partition(part, chargingReserve(alone, key.ID, solo.Index)); err != nil {
			t.Fatalf("a build that outgrows the cache must still serve its reader: %v", err)
		}
		if got := alone.stats().RetainedBytes; got > alone.budget {
			t.Fatalf("building %+v left %d bytes retained over the %d budget", part, got, alone.budget)
		}
	}
	if alone.has(key) || alone.stats().RetainedBytes != 0 || alone.stats().Purged[purgeEvicted] != 1 {
		t.Fatalf("an entry heavier than the budget stayed retained: %+v", alone.stats())
	}
}
