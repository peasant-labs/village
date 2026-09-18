package handler

import (
	"sync"
	"time"
)

// repositoryAccessCache remembers, for a short time and in this process alone,
// whether one GitHub account may read one repository.
//
// It stores nothing durable, and it remembers only REFUSALS. An admission is
// asked live on every read, because an owner narrowing a transcript or
// retracting its share must take effect at once rather than up to a minute
// later, and the cache exists for the case that is both abusive and
// staleness-free: a stranger probing a pull request URL they cannot read. What
// the asymmetry costs is that a reader newly granted access waits at most the
// TTL, which is the safe direction — a refusal that clears itself, never access
// that outlives its reason.
//
// The zero value is ready to use. A miss, an expiry, or a full table that cannot
// be pruned costs the two calls again; nothing here decides access, it only
// remembers what GitHub was just asked. It answers traffic, not authorization:
// the throttle that would bound a determined caller is a separate change.
type repositoryAccessCache struct {
	mu      sync.Mutex
	entries map[string]repositoryAccessEntry
}

type repositoryAccessEntry struct {
	admits  bool
	expires time.Time
}

const (
	// repositoryAccessTTL bounds both the saving and the staleness of a refusal.
	// Long enough to collapse a stranger's repeated probes of one pull request,
	// short enough that a reader newly granted access is not locked out long.
	repositoryAccessTTL = 60 * time.Second
	// repositoryAccessMaxEntries bounds the table. Reaching it prunes expired
	// entries and then, if they are all live, drops them: a cache miss costs two
	// calls, an unbounded map costs memory for as long as the process lives.
	repositoryAccessMaxEntries = 4096
)

func (c *repositoryAccessCache) lookup(key string, now time.Time) (bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || !now.Before(entry.expires) {
		return false, false
	}
	return entry.admits, true
}

func (c *repositoryAccessCache) store(key string, admits bool, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]repositoryAccessEntry)
	}
	if len(c.entries) >= repositoryAccessMaxEntries {
		for existing, entry := range c.entries {
			if !now.Before(entry.expires) {
				delete(c.entries, existing)
			}
		}
		if len(c.entries) >= repositoryAccessMaxEntries {
			clear(c.entries)
		}
	}
	c.entries[key] = repositoryAccessEntry{admits: admits, expires: now.Add(repositoryAccessTTL)}
}
