package ai

import (
	"fmt"
	"testing"
	"time"
)

type agingEntry struct{ expireAt time.Time }

func agingExpiry(e agingEntry) time.Time { return e.expireAt }

// Purging only expired entries is not a bound. A cache filling faster
// than its TTL stays over the ceiling forever, which is both memory the
// process never gives back and a full-map walk on every subsequent
// write.
func TestEvictOldestBoundsACacheOfLiveEntries(t *testing.T) {
	t.Parallel()

	m := make(map[string]agingEntry)
	future := time.Now().Add(time.Hour)
	for i := range 1000 {
		m[fmt.Sprintf("k-%d", i)] = agingEntry{expireAt: future.Add(time.Duration(i) * time.Second)}
	}

	EvictOldest(m, 500, agingExpiry)

	if len(m) > 500 {
		t.Fatalf("cache holds %d entries with none expired, want at most 500", len(m))
	}
	// A sweep must also make real room, or the next write sweeps again.
	if len(m) > 500-500/evictKeepFraction {
		t.Errorf("cache left at %d entries; a sweep should free a quarter of the ceiling", len(m))
	}
}

// What survives has to be the entries with the most life left: dropping
// the freshest would make the cache useless exactly when it is busiest.
func TestEvictOldestKeepsTheEntriesFurthestFromExpiry(t *testing.T) {
	t.Parallel()

	m := make(map[string]agingEntry)
	now := time.Now()
	for i := range 100 {
		m[fmt.Sprintf("k-%02d", i)] = agingEntry{expireAt: now.Add(time.Duration(i+1) * time.Minute)}
	}

	EvictOldest(m, 20, agingExpiry)

	for key := range m {
		var i int
		if _, err := fmt.Sscanf(key, "k-%d", &i); err != nil {
			t.Fatalf("unexpected key %q", key)
		}
		if i < 80 {
			t.Errorf("kept %q, which expires before entries that were dropped", key)
		}
	}
}

// Expired entries go first, and if that is enough nothing live is
// touched.
func TestEvictOldestPrefersExpiredEntries(t *testing.T) {
	t.Parallel()

	m := make(map[string]agingEntry)
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	for i := range 90 {
		m[fmt.Sprintf("dead-%d", i)] = agingEntry{expireAt: past}
	}
	for i := range 10 {
		m[fmt.Sprintf("live-%d", i)] = agingEntry{expireAt: future}
	}

	EvictOldest(m, 50, agingExpiry)

	if len(m) != 10 {
		t.Fatalf("cache holds %d entries, want the 10 live ones", len(m))
	}
	for key := range m {
		if key[:4] != "live" {
			t.Errorf("kept expired entry %q", key)
		}
	}
}

// BenchmarkProposalCachePut measures a write into a cache that is past
// its ceiling with nothing expired — the state a busy process sits in,
// and the one where purging only expired entries walked the whole map
// on every write and deleted nothing.
func BenchmarkProposalCachePut(b *testing.B) {
	c := NewProposalCache(time.Hour)
	for i := range proposalCacheMax {
		c.Put(fmt.Sprintf("warm-%d", i), i)
	}
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		c.Put(fmt.Sprintf("key-%d", i), i)
	}
}

// The proposal cache is the caller that matters: it must stay bounded
// while still answering for what it kept.
func TestProposalCacheStaysBounded(t *testing.T) {
	t.Parallel()

	c := NewProposalCache(time.Hour)
	for i := range proposalCacheMax * 3 {
		c.Put(fmt.Sprintf("key-%d", i), i)
	}

	c.mu.Lock()
	size := len(c.m)
	c.mu.Unlock()
	if size > proposalCacheMax {
		t.Fatalf("proposal cache holds %d entries, want at most %d", size, proposalCacheMax)
	}

	// The most recent write is still served — eviction takes the oldest,
	// not everything.
	last := fmt.Sprintf("key-%d", proposalCacheMax*3-1)
	if _, ok := c.Get(last); !ok {
		t.Errorf("the newest entry %q was evicted", last)
	}
}
