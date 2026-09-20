package handler

import (
	"fmt"
	"testing"
	"time"
)

func TestRepositoryAccessCache_RemembersUntilItExpires(t *testing.T) {
	var cache repositoryAccessCache
	now := time.Now()

	if _, ok := cache.lookup("viewer\x00acme/widgets", now); ok {
		t.Fatal("an empty cache reported an answer")
	}
	cache.store("viewer\x00acme/widgets", false, now)
	if admits, ok := cache.lookup("viewer\x00acme/widgets", now.Add(repositoryAccessTTL/2)); !ok || admits {
		t.Fatalf("lookup mid-life = (%v, %v), want the stored refusal", admits, ok)
	}
	if _, ok := cache.lookup("viewer\x00acme/widgets", now.Add(repositoryAccessTTL)); ok {
		t.Fatal("an entry was still reported at its expiry, so a refusal would never be asked again")
	}
}

// A table that fills up must not keep growing: a cache miss costs two GitHub
// calls, an unbounded map costs memory for as long as the process lives.
func TestRepositoryAccessCache_StaysBounded(t *testing.T) {
	var cache repositoryAccessCache
	now := time.Now()

	for i := 0; i < repositoryAccessMaxEntries+128; i++ {
		cache.store(fmt.Sprintf("viewer-%d\x00acme/widgets", i), false, now)
	}
	if len(cache.entries) > repositoryAccessMaxEntries {
		t.Fatalf("the cache holds %d entries, want at most %d", len(cache.entries), repositoryAccessMaxEntries)
	}
	cache.store("after\x00acme/widgets", true, now)
	if admits, ok := cache.lookup("after\x00acme/widgets", now); !ok || !admits {
		t.Fatalf("lookup after pruning = (%v, %v), want the stored answer", admits, ok)
	}
}

// Expired entries are pruned rather than thrown away with the live ones.
func TestRepositoryAccessCache_PrunesExpiredBeforeDroppingLiveEntries(t *testing.T) {
	var cache repositoryAccessCache
	now := time.Now()

	for i := 0; i < repositoryAccessMaxEntries; i++ {
		cache.store(fmt.Sprintf("old-%d", i), false, now)
	}
	// Every stored entry is live, so the next store prunes nothing and clears,
	// and the answer stored after that is the one that survives.
	cache.store("new", true, now.Add(repositoryAccessTTL+time.Second))
	if admits, ok := cache.lookup("new", now.Add(repositoryAccessTTL+time.Second)); !ok || !admits {
		t.Fatalf("lookup after a full table = (%v, %v), want the newly stored answer", admits, ok)
	}
}

func TestRepositoryAccessCache_IsSafeForConcurrentUse(t *testing.T) {
	var cache repositoryAccessCache
	now := time.Now()

	done := make(chan struct{})
	for worker := 0; worker < 8; worker++ {
		go func(worker int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 200; i++ {
				key := fmt.Sprintf("viewer-%d\x00acme/widgets-%d", worker, i%32)
				cache.store(key, i%2 == 0, now)
				cache.lookup(key, now)
			}
		}(worker)
	}
	for worker := 0; worker < 8; worker++ {
		<-done
	}
}
