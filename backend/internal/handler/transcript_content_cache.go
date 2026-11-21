package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// This file is the process-local transcript content cache. It holds the result
// of one decryption per stored generation, so that a main read and a history
// read of the same generation share one object read, one authentication and one
// structural pass, and so that two viewers who open the same transcript at the
// same moment cause one load.
//
// What it is not: it decides nothing about access. Every read applies the
// viewer's access rule against a freshly read row before it asks this cache for
// a byte, so a viewer who may not read a transcript never receives a cached
// byte, and a revision mismatch is compared against the row, not the cache.
// Nothing here is durable and nothing here is shared between processes: a
// second process holds its own entries, and an operator who restores a database
// flushes the caches by restarting the processes (see the runbook section
// "Transcript content cache" in docs/transcript-encryption-operations.md).

const (
	// transcriptContentCacheBudgetBytes is the strict ceiling on the plaintext
	// bytes the cache retains at once. It is a deployment constant recorded in
	// the runbook, not a contract value. Entries are weighted by the size of the
	// bytes they hold; the per-partition indexes built from those bytes are a
	// small multiple of one byte per turn and are not weighed separately.
	transcriptContentCacheBudgetBytes int64 = 256 << 20

	// transcriptContentCacheIdleLifetime is how long an entry may go without a
	// read before the sweep drops it.
	transcriptContentCacheIdleLifetime = 15 * time.Minute

	// transcriptContentCacheSweepInterval is how often the background ticker
	// looks for idle entries. A sweep never runs on a request path.
	transcriptContentCacheSweepInterval = time.Minute

	// transcriptContentCacheUnknownWeight is the weight reserved for a load
	// whose row records no size. It is conservative and is replaced by the
	// loaded bytes' real size when the load ends.
	transcriptContentCacheUnknownWeight int64 = transcriptContentCacheBudgetBytes / 8

	// transcriptHeavyWorkLimit bounds concurrent loads and index builds.
	transcriptHeavyWorkLimit = 2
)

// errTranscriptReadBusy is the bounded refusal for a read the cache cannot
// admit: the retained bytes plus the bytes already reserved by loads in flight
// leave no room for this generation. It is the whole answer; no partial entry
// is built and nothing is half-served. The read path answers 503 with a
// Retry-After header, and the same read succeeds once room frees.
var errTranscriptReadBusy = errors.New("transcript content could not be admitted in handler.transcriptContentCache.Acquire because the process-local content cache has no room for another load and no partial entry was built; nothing was read for this request; retry the read shortly")

type transcriptRefusalKind string

const (
	transcriptRefusalWeight transcriptRefusalKind = "weight"
	transcriptRefusalCount  transcriptRefusalKind = "count"
)

type transcriptAdmissionError struct{ kind transcriptRefusalKind }

func (e transcriptAdmissionError) Error() string { return errTranscriptReadBusy.Error() }
func (e transcriptAdmissionError) Unwrap() error { return errTranscriptReadBusy }

// transcriptCacheKey names one live generation. It holds the four facts that
// distinguish entries that can be live at the same time: the transcript, its
// content revision, its model provider and the harness whose cooking class
// numbered its rows. It holds no build component: the representation version,
// the cut version and the build are compile-time constants of the process, so
// none of them can tell two live entries apart. Withholding and validation are
// applied per response or are properties of the generation, not of the key.
type transcriptCacheKey struct {
	ID            uuid.UUID
	Revision      contentGeneration
	ModelProvider string
	Harness       schema.Harness
}

// transcriptCacheKeyForRow derives the key a read of this row looks up. The
// harness is the one the row's recorded provider names; the loaded entry is
// retained only when the generation it holds was cooked under that same class.
func transcriptCacheKeyForRow(row sqlc.Transcript) transcriptCacheKey {
	return transcriptCacheKey{
		ID:            uuid.UUID(row.ID.Bytes),
		Revision:      contentGeneration(row.ContentRevision),
		ModelProvider: row.ModelProvider,
		Harness:       canonicalHarness(row.ModelProvider),
	}
}

// transcriptCacheLoad is what admission needs to know about a load. Owner is
// not part of the key: it only lets one purge cover every entry an owner holds.
type transcriptCacheLoad struct {
	Owner uuid.UUID
	// Weight is the stored plaintext size, an estimate of the bytes the load
	// will hold. Zero or negative selects transcriptContentCacheUnknownWeight.
	Weight int64
}

type transcriptCacheLoader func(ctx context.Context) (*openedTranscript, error)

// cachePurgeReason names why entries were dropped. It only labels the purge
// for the log and for tests; every reason drops the same way.
type cachePurgeReason int

const (
	purgePublish cachePurgeReason = iota
	purgeRewrite
	purgeVisibility
	purgeUnshare
	purgeDelete
	purgeGenerationMoved
	purgeHarnessChanged
	purgeIdle
	purgeReasonCount
)

func (r cachePurgeReason) String() string {
	switch r {
	case purgePublish:
		return "publish"
	case purgeRewrite:
		return "rewrite"
	case purgeVisibility:
		return "visibility_change"
	case purgeUnshare:
		return "unshare"
	case purgeDelete:
		return "delete"
	case purgeGenerationMoved:
		return "generation_moved"
	case purgeHarnessChanged:
		return "harness_changed"
	case purgeIdle:
		return "idle"
	default:
		return fmt.Sprintf("cachePurgeReason(%d)", int(r))
	}
}

type transcriptCacheEntry struct {
	key      transcriptCacheKey
	owner    uuid.UUID
	opened   *openedTranscript
	weight   int64
	lastRead time.Time
}

// transcriptCacheFlight is one load in progress. Every reader of its key joins
// it, so the object is read and decrypted once however many viewers ask.
type transcriptCacheFlight struct {
	key       transcriptCacheKey
	owner     uuid.UUID
	weight    int64
	oversized bool

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	// The fields below are guarded by the cache's mutex until done is closed;
	// opened and err are read-only after it.
	waiters     int
	invalidated bool
	opened      *openedTranscript
	err         error
}

// transcriptContentCache is the cache. The zero value is not usable; build one
// with newTranscriptContentCache. Every method is safe on a nil receiver: a
// handler that composed no cache loads on every read and purges nothing.
type transcriptContentCache struct {
	budget int64
	idle   time.Duration
	every  time.Duration
	now    func() time.Time

	mu        sync.Mutex
	entries   map[transcriptCacheKey]*transcriptCacheEntry
	flights   map[transcriptCacheKey]*transcriptCacheFlight
	retained  int64 // bytes held by entries
	reserved  int64 // bytes reserved by in-budget loads in flight
	heavyWork int   // loads and builds currently in flight
	// oversized names the one active load whose weight exceeds the budget.
	oversized *transcriptCacheFlight
	purged    [purgeReasonCount]int

	sweepMu     sync.Mutex
	sweepCancel context.CancelFunc
	sweepWG     sync.WaitGroup
}

// transcriptContentCacheConfig overrides the shipped constants. Production
// leaves every field zero; a test injects a small budget, a short lifetime and
// a clock.
type transcriptContentCacheConfig struct {
	Budget        int64
	IdleLifetime  time.Duration
	SweepInterval time.Duration
	Now           func() time.Time
}

func newTranscriptContentCache(cfg transcriptContentCacheConfig) *transcriptContentCache {
	c := &transcriptContentCache{
		budget:  cfg.Budget,
		idle:    cfg.IdleLifetime,
		every:   cfg.SweepInterval,
		now:     cfg.Now,
		entries: make(map[transcriptCacheKey]*transcriptCacheEntry),
		flights: make(map[transcriptCacheKey]*transcriptCacheFlight),
	}
	if c.budget <= 0 {
		c.budget = transcriptContentCacheBudgetBytes
	}
	if c.idle <= 0 {
		c.idle = transcriptContentCacheIdleLifetime
	}
	if c.every <= 0 {
		c.every = transcriptContentCacheSweepInterval
	}
	if c.now == nil {
		c.now = time.Now
	}
	return c
}

// Acquire returns the opened generation for key: the retained entry when one
// is live, else the result of the one load every concurrent reader of the key
// shares.
//
// Admission is by weight against the strict budget. A load whose weight fits
// reserves it while it runs; one that does not fit is refused with
// errTranscriptReadBusy before anything is read. A generation heavier than the
// whole budget is its own single-flight case: one such load runs at a time,
// concurrent viewers of that same transcript share it, its result is served and
// not retained, and a second oversized transcript is refused while one is
// active. That keeps a near-limit transcript readable without letting it, or
// many of its kind, be an unconditional refusal or an unbounded footprint.
//
// A load that fails is not remembered. A load every one of whose readers has
// gone is canceled.
func (c *transcriptContentCache) Acquire(ctx context.Context, key transcriptCacheKey, load transcriptCacheLoad, loader transcriptCacheLoader) (*openedTranscript, error) {
	if c == nil {
		return loader(ctx)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	weight := load.Weight
	if weight <= 0 {
		weight = transcriptContentCacheUnknownWeight
	}

	c.mu.Lock()
	if entry, ok := c.entries[key]; ok {
		entry.lastRead = c.now()
		opened := entry.opened
		c.mu.Unlock()
		if opened.Reloaded {
			copy := *opened
			copy.Reloaded = false
			return &copy, nil
		}
		return opened, nil
	}
	if flight, ok := c.flights[key]; ok {
		flight.waiters++
		c.mu.Unlock()
		return c.wait(ctx, flight)
	}
	flight := &transcriptCacheFlight{key: key, owner: load.Owner, weight: weight, done: make(chan struct{}), waiters: 1}
	if err := c.admitLocked(weight); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	if weight > c.budget {
		flight.oversized = true
		c.oversized = flight
	} else {
		c.reserved += weight
	}
	// The load outlives the reader that started it, so a second reader that
	// joined is not failed by the first one's cancellation; it is canceled only
	// when every reader has left.
	flight.ctx, flight.cancel = context.WithCancel(context.WithoutCancel(ctx))
	c.flights[key] = flight
	c.mu.Unlock()

	go c.run(flight, loader)
	return c.wait(ctx, flight)
}

// admitLocked checks both limits before changing any accounting. A zero
// weight represents a build against an already-open generation.
func (c *transcriptContentCache) admitLocked(weight int64) error {
	if weight > c.budget {
		if c.oversized != nil {
			return transcriptAdmissionError{kind: transcriptRefusalWeight}
		}
	} else if c.retained+c.reserved+weight > c.budget {
		return transcriptAdmissionError{kind: transcriptRefusalWeight}
	}
	if c.heavyWork >= transcriptHeavyWorkLimit {
		return transcriptAdmissionError{kind: transcriptRefusalCount}
	}
	c.heavyWork++
	return nil
}

// AcquireBuild reserves a heavy-work slot until its caller finishes building.
// The returned release frees the slot once; a second call is a no-op, so a
// caller that releases on both a deferred path and an early one cannot free a
// slot another build holds.
func (c *transcriptContentCache) AcquireBuild(ctx context.Context) (func(), error) {
	if c == nil {
		return func() {}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	err := c.admitLocked(0)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			c.heavyWork--
			c.mu.Unlock()
		})
	}, nil
}

func (c *transcriptContentCache) wait(ctx context.Context, flight *transcriptCacheFlight) (*openedTranscript, error) {
	select {
	case <-flight.done:
		return flight.opened, flight.err
	case <-ctx.Done():
	}
	select {
	case <-flight.done:
		return flight.opened, flight.err
	default:
	}
	c.mu.Lock()
	flight.waiters--
	abandoned := flight.waiters == 0
	c.mu.Unlock()
	if abandoned {
		flight.cancel()
	}
	return nil, ctx.Err()
}

func (c *transcriptContentCache) run(flight *transcriptCacheFlight, loader transcriptCacheLoader) {
	var opened *openedTranscript
	var err error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				opened, err = nil, fmt.Errorf("transcript content load panicked in handler.transcriptContentCache.run; nothing was cached; report this as a defect: %v", recovered)
			}
		}()
		opened, err = loader(flight.ctx)
	}()
	if err == nil && (opened == nil || opened.Index == nil) {
		err = errors.New("transcript content load returned no generation in handler.transcriptContentCache.run; nothing was cached; report this as a defect")
	}

	c.mu.Lock()
	c.heavyWork--
	if c.flights[flight.key] == flight {
		delete(c.flights, flight.key)
	}
	if flight.oversized {
		if c.oversized == flight {
			c.oversized = nil
		}
	} else {
		c.reserved -= flight.weight
	}
	if err == nil {
		c.retainLocked(flight, opened)
	}
	flight.opened, flight.err = opened, err
	c.mu.Unlock()
	close(flight.done)
	flight.cancel()
}

// retainLocked decides whether a finished load is kept. It is kept when no
// purge touched its transcript while it ran, it was cooked under the harness
// its key names, and the bytes it really holds fit in the budget now. A load
// that reports a different revision than the one it was asked for found the
// generation moved: the entry is stored under the revision it holds, and every
// other live entry of that transcript is dropped.
func (c *transcriptContentCache) retainLocked(flight *transcriptCacheFlight, opened *openedTranscript) {
	if flight.invalidated {
		return
	}
	key := flight.key
	index := opened.Index
	if index.Revision != key.Revision || index.ModelProvider != key.ModelProvider {
		key.Revision = index.Revision
		key.ModelProvider = index.ModelProvider
		c.observeLocked(key)
	}
	if index.Harness != key.Harness {
		return
	}
	size := int64(len(index.Bytes))
	if size > c.budget || c.retained+c.reserved+size > c.budget {
		return
	}
	if _, exists := c.entries[key]; exists {
		return
	}
	c.entries[key] = &transcriptCacheEntry{key: key, owner: flight.owner, opened: opened, weight: size, lastRead: c.now()}
	c.retained += size
}

// Observe records the generation a read has just seen for key.ID and drops
// every other live entry of that transcript: one whose revision differs is a
// generation that moved, one that differs only in provider or harness is a
// harness change. A load in flight for another key is marked so its result is
// served to its readers and not retained. It returns the number of entries
// dropped.
func (c *transcriptContentCache) Observe(key transcriptCacheKey) int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.observeLocked(key)
}

func (c *transcriptContentCache) observeLocked(key transcriptCacheKey) int {
	dropped := 0
	for other, entry := range c.entries {
		if other.ID != key.ID || other == key {
			continue
		}
		reason := purgeHarnessChanged
		if other.Revision != key.Revision {
			reason = purgeGenerationMoved
		}
		c.dropLocked(entry, reason)
		dropped++
	}
	for other, flight := range c.flights {
		if other.ID == key.ID && other != key {
			flight.invalidated = true
		}
	}
	return dropped
}

// PurgeTranscript drops every entry of one transcript and keeps any load in
// flight for it from being retained. A write path calls it after the write
// commits. It returns the number of entries dropped.
func (c *transcriptContentCache) PurgeTranscript(id uuid.UUID, reason cachePurgeReason) int {
	return c.purge(reason, func(key transcriptCacheKey, _ uuid.UUID) bool { return key.ID == id })
}

// PurgeOwner is PurgeTranscript for every transcript one owner holds, for the
// writes that move many of an owner's transcripts at once.
func (c *transcriptContentCache) PurgeOwner(owner uuid.UUID, reason cachePurgeReason) int {
	return c.purge(reason, func(_ transcriptCacheKey, entryOwner uuid.UUID) bool { return entryOwner == owner })
}

func (c *transcriptContentCache) purge(reason cachePurgeReason, matches func(transcriptCacheKey, uuid.UUID) bool) int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	dropped := 0
	for key, entry := range c.entries {
		if matches(key, entry.owner) {
			c.dropLocked(entry, reason)
			dropped++
		}
	}
	for key, flight := range c.flights {
		if matches(key, flight.owner) {
			flight.invalidated = true
		}
	}
	return dropped
}

func (c *transcriptContentCache) dropLocked(entry *transcriptCacheEntry, reason cachePurgeReason) {
	delete(c.entries, entry.key)
	c.retained -= entry.weight
	c.purged[reason]++
}

// Sweep drops every entry that has gone the idle lifetime without a read and
// returns how many it dropped. Only the background ticker calls it.
func (c *transcriptContentCache) Sweep() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	dropped := 0
	for _, entry := range c.entries {
		if now.Sub(entry.lastRead) >= c.idle {
			c.dropLocked(entry, purgeIdle)
			dropped++
		}
	}
	return dropped
}

// StartSweeper starts the background ticker that runs Sweep. It is idempotent.
func (c *transcriptContentCache) StartSweeper() {
	if c == nil {
		return
	}
	c.sweepMu.Lock()
	defer c.sweepMu.Unlock()
	if c.sweepCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.sweepCancel = cancel
	c.sweepWG.Add(1)
	go func() {
		defer c.sweepWG.Done()
		ticker := time.NewTicker(c.every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if dropped := c.Sweep(); dropped > 0 {
					slog.Debug("transcript content cache dropped idle entries", "operation", "content_cache_sweep", "entries", dropped)
				}
			}
		}
	}()
}

// DrainSweeper stops the ticker and waits, until ctx ends, for a sweep in
// flight to return.
func (c *transcriptContentCache) DrainSweeper(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.sweepMu.Lock()
	cancel := c.sweepCancel
	c.sweepCancel = nil
	c.sweepMu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		c.sweepWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("transcript content cache sweep still running at shutdown: %w", ctx.Err())
	}
}

// transcriptCacheStats is a point-in-time view for tests and diagnostics.
type transcriptCacheStats struct {
	Entries       int
	RetainedBytes int64
	InFlight      int
	Purged        [purgeReasonCount]int
}

func (c *transcriptContentCache) stats() transcriptCacheStats {
	if c == nil {
		return transcriptCacheStats{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return transcriptCacheStats{Entries: len(c.entries), RetainedBytes: c.retained, InFlight: len(c.flights), Purged: c.purged}
}

// has reports whether a live entry exists for key, without counting as a read.
func (c *transcriptContentCache) has(key transcriptCacheKey) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.entries[key]
	return ok
}

// openCachedTranscript opens the generation row currently stores, through the
// cache. Callers apply the viewer's access rule to the fresh row first; the
// cache is consulted only for a viewer who already passed it. A read that finds
// the row at another generation than a live entry drops that entry.
//
// The loader's reload authorization belongs only to its starting reader.
// Every waiter must independently reauthorize when Reloaded is set. A cache
// hit also requires that check when its source descriptor differs from the
// caller's freshly authorized row.
func (h *Handler) openCachedTranscript(ctx context.Context, row sqlc.Transcript, authorize func(sqlc.Transcript) bool) (*openedTranscript, error) {
	load := func(ctx context.Context) (*openedTranscript, error) {
		return h.openTranscriptContent(ctx, row, authorize)
	}
	cache := h.contentCache
	if cache == nil {
		return load(ctx)
	}
	key := transcriptCacheKeyForRow(row)
	cache.Observe(key)
	var weight int64
	if row.BlobSizeBytes.Valid {
		weight = row.BlobSizeBytes.Int64
	}
	opened, err := cache.Acquire(ctx, key, transcriptCacheLoad{Owner: uuid.UUID(row.OwnerID.Bytes), Weight: weight}, load)
	if err != nil {
		return nil, err
	}
	initial, initialErr := descriptorFromTranscript(row)
	source, sourceErr := descriptorFromTranscript(opened.SourceRow)
	if initialErr != nil || sourceErr != nil || !descriptorsEqual(initial, source) {
		copy := *opened
		copy.Reloaded = true
		opened = &copy
	}
	return opened, nil
}

// StartContentCacheSweeper starts the cache's idle-lifetime ticker.
func (h *Handler) StartContentCacheSweeper() { h.contentCache.StartSweeper() }

// DrainContentCache stops the ticker and waits for a sweep in flight.
func (h *Handler) DrainContentCache(ctx context.Context) error {
	return h.contentCache.DrainSweeper(ctx)
}

// purgeTranscriptContent drops the cached content of one transcript after a
// write to it commits. It is a no-op for a handler with no cache.
func (h *Handler) purgeTranscriptContent(id pgtype.UUID, reason cachePurgeReason) {
	if h.contentCache == nil || !id.Valid {
		return
	}
	h.contentCache.PurgeTranscript(uuid.UUID(id.Bytes), reason)
}

// purgeOwnerContent drops the cached content of every transcript one owner
// holds, for the writes that move many of them at once.
func (h *Handler) purgeOwnerContent(owner pgtype.UUID, reason cachePurgeReason) {
	if h.contentCache == nil || !owner.Valid {
		return
	}
	h.contentCache.PurgeOwner(uuid.UUID(owner.Bytes), reason)
}
