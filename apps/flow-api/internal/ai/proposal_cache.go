// Package ai — proposal_cache.go provides a short-lived in-memory
// cache for LLM proposal results. It prevents redundant LLM calls when
// the same request is made multiple times within the TTL window (e.g.
// double-clicking a button, retrying after a transient UI error).
package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// ProposalCache is a TTL-based in-memory cache for LLM proposal
// results keyed by a content hash. It is safe for concurrent use.
// A nil *ProposalCache is valid and behaves as a no-op (always misses).
type ProposalCache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]proposalEntry
}

type proposalEntry struct {
	value    any
	expireAt time.Time
}

// NewProposalCache creates a cache with the given TTL. A zero or
// negative TTL disables caching.
func NewProposalCache(ttl time.Duration) *ProposalCache {
	return &ProposalCache{ttl: ttl, m: make(map[string]proposalEntry)}
}

// ProposalCacheKey builds a deterministic cache key from workspace ID
// and content strings (e.g. title + description).
func ProposalCacheKey(workspaceID uint32, parts ...string) string {
	h := sha256.New()
	fmt.Fprintf(h, "ws:%d", workspaceID)
	for _, p := range parts {
		h.Write([]byte{0}) // separator
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Get returns a cached value and true if found and not expired,
// otherwise nil and false.
func (c *ProposalCache) Get(key string) (any, bool) {
	if c == nil || c.ttl <= 0 {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expireAt) {
		delete(c.m, key)
		return nil, false
	}
	return e.value, true
}

// proposalCacheMax is the ceiling on live entries. Reaching it triggers
// one sweep, which is guaranteed to make room.
const proposalCacheMax = 500

// Put stores a value under the given key with the configured TTL.
func (c *ProposalCache) Put(key string, value any) {
	if c == nil || c.ttl <= 0 || value == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = proposalEntry{value: value, expireAt: time.Now().Add(c.ttl)}
	if len(c.m) > proposalCacheMax {
		EvictOldest(c.m, proposalCacheMax, func(e proposalEntry) time.Time { return e.expireAt })
	}
}
