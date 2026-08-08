package nlcommand

import (
	"fmt"
	"testing"
	"time"
)

// A prompt cache in a long-lived process needs a real ceiling. Purging
// only expired entries gave it none: entries arriving faster than the
// TTL kept the map over the limit, so every further Put walked the whole
// map, deleted nothing, and left it a little bigger.
func TestCacheStaysBounded(t *testing.T) {
	t.Parallel()

	c := NewCache(time.Hour)
	for i := range cacheMax * 3 {
		c.Put(fmt.Sprintf("prompt-%d", i), &ToolCall{Tool: "tasks.create"})
	}

	c.mu.Lock()
	size := len(c.m)
	c.mu.Unlock()
	if size > cacheMax {
		t.Fatalf("cache holds %d entries, want at most %d", size, cacheMax)
	}

	last := fmt.Sprintf("prompt-%d", cacheMax*3-1)
	if got := c.Get(last); got == nil {
		t.Errorf("the newest entry %q was evicted", last)
	}
}

// Eviction must not break the cache: what it keeps still answers.
func TestCacheReturnsWhatItKept(t *testing.T) {
	t.Parallel()

	c := NewCache(time.Hour)
	c.Put("show me today", &ToolCall{Tool: "tasks.list"})
	got := c.Get("show me today")
	if got == nil || got.Tool != "tasks.list" {
		t.Fatalf("Get = %+v, want the stored tool call", got)
	}
	if c.Get("never stored") != nil {
		t.Error("Get returned something for a key that was never stored")
	}
}
