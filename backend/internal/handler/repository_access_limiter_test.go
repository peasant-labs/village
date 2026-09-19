package handler

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// limiterViewer names a viewer for the limiter without a database.
func limiterViewer(name string) pgtype.UUID {
	sum := sha256.Sum256([]byte(name))
	var viewer pgtype.UUID
	copy(viewer.Bytes[:], sum[:16])
	viewer.Valid = true
	return viewer
}

// A burst is spent and then bounded: the refill is steady rather than a fresh
// burst, and one viewer's spending does not limit another's.
func TestRepositoryAccessLimiter_BoundsABurstAndRefills(t *testing.T) {
	var limiter repositoryAccessLimiter
	now := time.Now()
	viewer := limiterViewer("viewer")

	for i := 0; i < int(repositoryAccessBurst); i++ {
		if !limiter.allow(viewer, now) {
			t.Fatalf("question %d was refused inside the burst", i+1)
		}
	}
	if limiter.allow(viewer, now) {
		t.Fatal("a question past the burst was allowed")
	}
	if limiter.allow(viewer, now.Add(time.Second)) {
		t.Fatal("a token was handed out before the rate refilled one")
	}
	if !limiter.allow(viewer, now.Add(11*time.Second)) {
		t.Fatal("eleven seconds' refill did not buy one question at the shipped rate")
	}
	if !limiter.allow(limiterViewer("other"), now) {
		t.Fatal("one viewer's burst limited another viewer")
	}
}

// The peek answers the same question the spend does, without spending: it is what
// lets a refused read say it was throttled without the answer depending on the
// transcript asked about.
func TestRepositoryAccessLimiter_PeeksWithoutSpending(t *testing.T) {
	limiter := repositoryAccessLimiter{burst: 2, perSecond: 1}
	now := time.Now()
	viewer := limiterViewer("peek")

	if limiter.overBudget(viewer, now) {
		t.Fatal("a viewer who has never asked was reported over budget")
	}
	// Ten peeks must not spend the two questions that are there.
	for i := 0; i < 10; i++ {
		limiter.overBudget(viewer, now)
	}
	if !limiter.allow(viewer, now) || !limiter.allow(viewer, now) {
		t.Fatal("peeking spent a token")
	}
	if !limiter.overBudget(viewer, now) {
		t.Fatal("a spent burst was not reported over budget")
	}
	if limiter.allow(viewer, now) {
		t.Fatal("a spend past the burst was allowed")
	}
	// The peek follows the refill, so a viewer comes back without a fresh burst.
	if limiter.overBudget(viewer, now.Add(time.Second)) {
		t.Fatal("the peek did not follow the same refill the spend does")
	}
}

// The counter is per viewer: one exhausts their bucket and the next is untouched,
// and a viewer seen for the first time always starts full.
func TestRepositoryAccessLimiter_CountsEachViewerSeparately(t *testing.T) {
	limiter := repositoryAccessLimiter{burst: 2, perSecond: 0.001}
	now := time.Now()
	a, b := limiterViewer("a"), limiterViewer("b")

	if !limiter.allow(a, now) || !limiter.allow(a, now) {
		t.Fatal("a viewer could not spend a two-question burst")
	}
	if limiter.allow(a, now) {
		t.Fatal("a viewer spent past a two-question burst")
	}
	if !limiter.allow(b, now) || !limiter.allow(b, now) {
		t.Fatal("a second viewer did not have their own burst")
	}
	if limiter.allow(b, now) {
		t.Fatal("the second viewer spent past their own burst")
	}
}

// The table is bounded: a burst of viewers must not grow it without limit, and it
// still answers afterwards.
func TestRepositoryAccessLimiter_StaysBounded(t *testing.T) {
	limiter := repositoryAccessLimiter{burst: 1, perSecond: 0.001}
	now := time.Now()

	for i := 0; i < repositoryAccessMaxViewers+128; i++ {
		limiter.allow(limiterViewer(fmt.Sprintf("viewer-%d", i)), now)
	}
	if len(limiter.buckets) > repositoryAccessMaxViewers {
		t.Fatalf("the limiter holds %d buckets, want at most %d", len(limiter.buckets), repositoryAccessMaxViewers)
	}
	if !limiter.allow(limiterViewer("after"), now) {
		t.Fatal("the limiter refused a first question after pruning")
	}
}

func TestRepositoryAccessLimiter_IsSafeForConcurrentUse(t *testing.T) {
	limiter := repositoryAccessLimiter{burst: 4, perSecond: 0.001}
	now := time.Now()

	done := make(chan struct{})
	for worker := 0; worker < 8; worker++ {
		go func(worker int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 200; i++ {
				viewer := limiterViewer(fmt.Sprintf("viewer-%d", i%16))
				limiter.allow(viewer, now)
				limiter.overBudget(viewer, now)
			}
		}(worker)
	}
	for worker := 0; worker < 8; worker++ {
		<-done
	}
}
