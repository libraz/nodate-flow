// Package nlcommand cache.go provides a short-lived in-memory cache
// for resolved NL commands. Identical normalized prompts reuse the
// previous LLM result within the TTL window, avoiding redundant
// provider calls.
package nlcommand

import (
	"sync"
	"time"
)

// cacheEntry holds a cached resolution result with its expiry time.
type cacheEntry struct {
	result   *ToolCall
	expireAt time.Time
}

// Cache is a simple TTL-based cache for NL command resolutions.
// It is safe for concurrent use. Entries expire after TTL and are
// lazily evicted on access.
type Cache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]cacheEntry
}

// NewCache creates a Cache with the given TTL. A zero or negative TTL
// disables caching (Get always misses).
func NewCache(ttl time.Duration) *Cache {
	return &Cache{ttl: ttl, m: make(map[string]cacheEntry)}
}

// Get returns a cached ToolCall for the normalized key, or nil if
// absent or expired.
func (c *Cache) Get(key string) *ToolCall {
	if c == nil || c.ttl <= 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok {
		return nil
	}
	if time.Now().After(e.expireAt) {
		delete(c.m, key)
		return nil
	}
	return e.result
}

// Put stores a ToolCall under the normalized key with the configured TTL.
func (c *Cache) Put(key string, tc *ToolCall) {
	if c == nil || c.ttl <= 0 || tc == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = cacheEntry{result: tc, expireAt: time.Now().Add(c.ttl)}

	// Lazy eviction: if cache grows beyond 1000 entries, purge expired.
	if len(c.m) > 1000 {
		now := time.Now()
		for k, e := range c.m {
			if now.After(e.expireAt) {
				delete(c.m, k)
			}
		}
	}
}
