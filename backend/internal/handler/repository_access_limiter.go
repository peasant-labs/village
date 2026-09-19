package handler

import (
	"sync"
	"time"
)

// repositoryAccessLimiter bounds how often one signed-in viewer can make Village
// ask GitHub whether they may read a private repository.
//
// The question costs two GitHub calls, the URL it hangs on is guessable, and the
// installation's quota is shared with every other GitHub-backed feature — checks,
// sticky comments, prompt matching, refreshes. A viewer gets a burst that a real
// page load fits inside and a refill far below what a loop needs, so a scripted
// caller is bounded to one question per refill interval while someone reading a
// pull request never notices.
//
// It counts questions, not reads: a viewer who passes the collected checks never
// touches it, and the refusal cache is consulted first, so a repeated refusal
// within its minute consumes no token at all.
//
// The zero value is ready to use, it lives in this process only, and its table is
// bounded. A limiter that cannot answer denies.
type repositoryAccessLimiter struct {
	mu      sync.Mutex
	buckets map[string]repositoryAccessBucket

	// burst and perSecond override the defaults below. They exist so a caller
	// with a reason — a test standing in for a loop — can set a limit it can
	// exhaust, and the zero value keeps the shipped one.
	burst     float64
	perSecond float64
}

type repositoryAccessBucket struct {
	tokens float64
	refill time.Time
}

const (
	// repositoryAccessBurst is what one viewer may spend before a refill: a
	// digest page plus its transcript links, each asking once, fits inside it.
	repositoryAccessBurst = 40.0
	// repositoryAccessPerSecond is the steady rate. A loop settles at one
	// question every ten seconds per viewer; a person reading never reaches it.
	repositoryAccessPerSecond = 0.1
	// repositoryAccessMaxViewers bounds the table. Reaching it drops expired
	// buckets and then, if they are all live, drops them all: a forgotten viewer
	// costs another question, an unbounded map costs memory for the life of the
	// process.
	repositoryAccessMaxViewers = 4096
	// repositoryAccessIdle is how long an untouched bucket is kept. Long enough
	// that a reader's burst survives a slow page load, short enough that an idle
	// viewer's bucket does not hold a slot forever.
	repositoryAccessIdle = 10 * time.Minute
)

// allow reports whether this viewer may spend a question now, and takes one when
// it may.
func (l *repositoryAccessLimiter) allow(viewer string, now time.Time) bool {
	key := viewer
	burst, perSecond := l.limits()

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = make(map[string]repositoryAccessBucket)
	}

	bucket, seen := l.buckets[key]
	if !seen {
		if len(l.buckets) >= repositoryAccessMaxViewers {
			l.pruneLocked(now)
		}
		// A viewer's first question starts with a full burst less this one.
		l.buckets[key] = repositoryAccessBucket{tokens: burst - 1, refill: now}
		return true
	}

	elapsed := now.Sub(bucket.refill).Seconds()
	if elapsed > 0 {
		bucket.tokens += elapsed * perSecond
		if bucket.tokens > burst {
			bucket.tokens = burst
		}
		bucket.refill = now
	}
	if bucket.tokens < 1 {
		// Still recorded, so the refill continues from here rather than the
		// viewer's next request starting a fresh burst.
		l.buckets[key] = bucket
		return false
	}
	bucket.tokens--
	l.buckets[key] = bucket
	return true
}

// limits reports the burst and rate in force: the injected ones when set, and the
// shipped ones otherwise.
func (l *repositoryAccessLimiter) limits() (burst float64, perSecond float64) {
	burst, perSecond = l.burst, l.perSecond
	if burst <= 0 {
		burst = repositoryAccessBurst
	}
	if perSecond <= 0 {
		perSecond = repositoryAccessPerSecond
	}
	return burst, perSecond
}

// pruneLocked drops buckets nobody has touched recently, and then all of them if
// every one is still live. It is called only when the table is full.
func (l *repositoryAccessLimiter) pruneLocked(now time.Time) {
	for key, bucket := range l.buckets {
		if now.Sub(bucket.refill) > repositoryAccessIdle {
			delete(l.buckets, key)
		}
	}
	if len(l.buckets) >= repositoryAccessMaxViewers {
		clear(l.buckets)
	}
}
