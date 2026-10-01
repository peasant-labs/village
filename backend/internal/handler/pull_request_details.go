package handler

import (
	"context"
	"sync"
	"time"
)

// A pull request's title and head branch are GitHub's, not Village's: Village
// stores neither, and a stored copy would go stale the moment an author retitled
// the pull request. So they are read from GitHub, through the installation that
// links the repository, when an attachment is served. A read that cannot reach
// GitHub serves both as null, which the contract defines as "Village does not
// know them", rather than failing the page.
//
// Four things keep that read cheap and bounded, whoever is asking and however
// often. Views of the same pull request that arrive while it is being read share
// that one read. Every outcome is remembered in a short-lived, bounded,
// in-process cache - an answer for two minutes; a refusal, a failure, or a read
// that ran out of time as unknown for thirty seconds - so one pull request costs
// at most one GitHub read per window in this process however many callers ask
// (a full table is dropped, which costs a read again). A read never
// runs longer than its deadline, and a response never waits on GitHub longer
// than that either: what is not answered by then is served as unknown. And a
// list asks for a few pull requests at once rather than one after another.

// pullRequestDetail is what an attachment row shows beside its number.
type pullRequestDetail struct {
	title   *string
	headRef *string
}

const (
	// pullRequestDetailFetchers bounds how many pull requests one list reads from
	// GitHub at the same time.
	pullRequestDetailFetchers = 4
	// pullRequestDetailDeadline bounds one GitHub read, and how long one response
	// waits for the titles it serves.
	pullRequestDetailDeadline = 4 * time.Second
)

// detailDeadline is the deadline in force: the handler's own when a test set a
// shorter one, the shipped one otherwise.
func (h *Handler) detailDeadline() time.Duration {
	if h.pullDetailDeadline > 0 {
		return h.pullDetailDeadline
	}
	return pullRequestDetailDeadline
}

// pullRequestDetailsFor reads the title and head branch of each candidate's
// pull request, in the candidates' order. A pull request GitHub could not
// describe, or that the list did not reach before its deadline, gets a detail
// with both fields null.
func (h *Handler) pullRequestDetailsFor(ctx context.Context, candidates []pullRequestCandidate) []pullRequestDetail {
	details := make([]pullRequestDetail, len(candidates))
	if h.gh == nil {
		return details
	}
	deadline := time.Now().Add(h.detailDeadline())
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	slots := make(chan struct{}, pullRequestDetailFetchers)
	var wg sync.WaitGroup
	stopped := false
	for i, c := range candidates {
		// A remembered answer needs no slot and no read, so it is served even
		// after the list has stopped waiting on GitHub.
		if remembered, ok := h.pullDetails.peek(pullRequestKeyOf(c.owner, c.name, c.number), time.Now()); ok {
			details[i] = remembered.detail()
			continue
		}
		if stopped {
			continue
		}
		// No read starts once the response has stopped waiting: the pull requests
		// not reached by then are served as unknown, the list never has more than
		// its few reads in flight, and it starts none once its caller has gone.
		// A slot opens when a read settles or when the list's context ends, so
		// both the context and the clock are checked after taking one. The clock
		// check is what holds when a read times out on schedule: its timer can
		// fire, and free its slot, a moment before the list's own timer does.
		// Removing it lets a list start more reads than it has slots.
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			stopped = true
			continue
		}
		wg.Add(1)
		go func(i int, c pullRequestCandidate) {
			defer wg.Done()
			defer func() { <-slots }()
			details[i] = h.pullRequestDetail(ctx, c.installationID, c.owner, c.name, c.number)
		}(i, c)
	}
	wg.Wait()
	return details
}

// pullRequestDetail answers one pull request's title and head branch: from the
// cache, from a read already in flight, or from a read it starts. The caller
// waits no longer than its own context allows; the read itself runs to its own
// deadline regardless, so its outcome is remembered for the next caller even
// when this one has stopped waiting.
func (h *Handler) pullRequestDetail(ctx context.Context, installationID int64, owner, name string, number int) pullRequestDetail {
	if h.gh == nil {
		return pullRequestDetail{}
	}
	key := pullRequestKeyOf(owner, name, number)
	remembered, read, start := h.pullDetails.claim(key, time.Now())
	if read == nil {
		return remembered.detail()
	}
	if start {
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), h.detailDeadline())
		go func() {
			defer cancel()
			pr, err := h.gh.GetPullRequest(readCtx, installationID, owner, name, number)
			if err != nil || pr == nil {
				// A refusal, a failure, and a read that ran out of time are all
				// remembered as unknown, briefly, so a pull request GitHub will not
				// or cannot describe is not asked about on every view.
				h.pullDetails.settle(key, read, pullRequestDetailEntry{}, time.Now(), pullRequestDetailFailureTTL)
				return
			}
			h.pullDetails.settle(key, read, pullRequestDetailEntry{title: pr.Title, headRef: pr.HeadRef}, time.Now(), pullRequestDetailTTL)
		}()
	}
	select {
	case <-read.done:
		return read.entry.detail()
	case <-ctx.Done():
		return pullRequestDetail{}
	}
}

// pullRequestDetailCache remembers, briefly and in this process alone, the
// title and head branch GitHub last reported for a pull request. It stores
// nothing durable and decides nothing: a miss costs one GitHub read, shared by
// every caller that asks for the same pull request while it runs.
//
// The zero value is ready to use and the table is bounded.
type pullRequestDetailCache struct {
	mu       sync.Mutex
	entries  map[string]pullRequestDetailEntry
	inflight map[string]*pullRequestDetailRead
}

// pullRequestDetailRead is one GitHub read in flight. Every caller that asks for
// the same pull request meanwhile waits on it instead of reading again.
type pullRequestDetailRead struct {
	done  chan struct{}
	entry pullRequestDetailEntry
}

type pullRequestDetailEntry struct {
	title   string
	headRef string
	expires time.Time
}

const (
	// pullRequestDetailTTL bounds how stale a retitled pull request can look,
	// and how often one pull request viewed repeatedly costs a GitHub read.
	pullRequestDetailTTL = 2 * time.Minute
	// pullRequestDetailFailureTTL is how long a refusal or failure is served as
	// unknown before GitHub is asked again.
	pullRequestDetailFailureTTL = 30 * time.Second
	// pullRequestDetailMaxEntries bounds the table. Reaching it prunes expired
	// entries and then, if every one is live, drops them all.
	pullRequestDetailMaxEntries = 4096
)

// detail maps a remembered answer to the wire: an empty value is one GitHub did
// not give, so it is served as unknown rather than as an empty title.
func (e pullRequestDetailEntry) detail() pullRequestDetail {
	var out pullRequestDetail
	if e.title != "" {
		title := e.title
		out.title = &title
	}
	if e.headRef != "" {
		headRef := e.headRef
		out.headRef = &headRef
	}
	return out
}

// claim answers from the cache when it holds a live entry (read is nil).
// Otherwise it returns the read in flight for the key, and start reports whether
// this caller created it and so must perform it.
func (c *pullRequestDetailCache) claim(key string, now time.Time) (entry pullRequestDetailEntry, read *pullRequestDetailRead, start bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[key]; ok && now.Before(entry.expires) {
		return entry, nil, false
	}
	if read, ok := c.inflight[key]; ok {
		return pullRequestDetailEntry{}, read, false
	}
	if c.inflight == nil {
		c.inflight = make(map[string]*pullRequestDetailRead)
	}
	read = &pullRequestDetailRead{done: make(chan struct{})}
	c.inflight[key] = read
	return pullRequestDetailEntry{}, read, true
}

// peek answers from the cache alone, starting nothing.
func (c *pullRequestDetailCache) peek(key string, now time.Time) (pullRequestDetailEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || !now.Before(entry.expires) {
		return pullRequestDetailEntry{}, false
	}
	return entry, true
}

// settle remembers a finished read's outcome for ttl and releases everyone
// waiting on it.
func (c *pullRequestDetailCache) settle(key string, read *pullRequestDetailRead, entry pullRequestDetailEntry, now time.Time, ttl time.Duration) {
	c.mu.Lock()
	c.storeLocked(key, entry, now, ttl)
	read.entry = entry
	delete(c.inflight, key)
	c.mu.Unlock()
	close(read.done)
}

func (c *pullRequestDetailCache) storeLocked(key string, entry pullRequestDetailEntry, now time.Time, ttl time.Duration) {
	if c.entries == nil {
		c.entries = make(map[string]pullRequestDetailEntry)
	}
	if _, present := c.entries[key]; !present && len(c.entries) >= pullRequestDetailMaxEntries {
		for k, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= pullRequestDetailMaxEntries {
			clear(c.entries)
		}
	}
	entry.expires = now.Add(ttl)
	c.entries[key] = entry
}
