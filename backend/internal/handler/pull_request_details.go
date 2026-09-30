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
// Two things keep that read cheap. A short-lived, bounded, in-process cache
// answers repeat views of the same pull request without asking again, and a
// list asks for several pull requests at once rather than one after another.

// pullRequestDetail is what an attachment row shows beside its number.
type pullRequestDetail struct {
	title   *string
	headRef *string
}

// pullRequestDetailFetchers bounds how many pull requests one list reads from
// GitHub at the same time.
const pullRequestDetailFetchers = 4

// pullRequestDetailsFor reads the title and head branch of each candidate's
// pull request, in the candidates' order. A pull request GitHub could not
// describe gets a detail with both fields null.
func (h *Handler) pullRequestDetailsFor(ctx context.Context, candidates []pullRequestCandidate) []pullRequestDetail {
	details := make([]pullRequestDetail, len(candidates))
	if h.gh == nil {
		return details
	}
	slots := make(chan struct{}, pullRequestDetailFetchers)
	var wg sync.WaitGroup
	for i, c := range candidates {
		wg.Add(1)
		slots <- struct{}{}
		go func(i int, c pullRequestCandidate) {
			defer wg.Done()
			defer func() { <-slots }()
			details[i] = h.pullRequestDetail(ctx, c.installationID, c.attachment.RepoOwner, c.attachment.RepoName, int(c.attachment.Number))
		}(i, c)
	}
	wg.Wait()
	return details
}

// pullRequestDetail reads one pull request's title and head branch, from the
// cache when it holds a recent answer.
func (h *Handler) pullRequestDetail(ctx context.Context, installationID int64, owner, name string, number int) pullRequestDetail {
	if h.gh == nil {
		return pullRequestDetail{}
	}
	key := pullRequestKeyOf(owner, name, number)
	now := time.Now()
	if cached, ok := h.pullDetails.lookup(key, now); ok {
		return cached.detail()
	}
	pr, err := h.gh.GetPullRequest(ctx, installationID, owner, name, number)
	if err != nil || pr == nil {
		// Not remembered: the next read asks again rather than serving an
		// unknown title for the cache's lifetime.
		return pullRequestDetail{}
	}
	entry := pullRequestDetailEntry{title: pr.Title, headRef: pr.HeadRef}
	h.pullDetails.store(key, entry, now)
	return entry.detail()
}

// pullRequestDetailCache remembers, briefly and in this process alone, the
// title and head branch GitHub last reported for a pull request. It stores
// nothing durable and decides nothing: a miss costs one GitHub read.
//
// The zero value is ready to use and the table is bounded.
type pullRequestDetailCache struct {
	mu      sync.Mutex
	entries map[string]pullRequestDetailEntry
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

func (c *pullRequestDetailCache) lookup(key string, now time.Time) (pullRequestDetailEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || !now.Before(entry.expires) {
		return pullRequestDetailEntry{}, false
	}
	return entry, true
}

func (c *pullRequestDetailCache) store(key string, entry pullRequestDetailEntry, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
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
	entry.expires = now.Add(pullRequestDetailTTL)
	c.entries[key] = entry
}
