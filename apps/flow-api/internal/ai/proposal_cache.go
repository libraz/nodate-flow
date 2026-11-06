// Package ai — proposal_cache.go provides a short-lived in-memory
// cache for LLM proposal results. It prevents redundant LLM calls when
// the same request is made multiple times within the TTL window (e.g.
// double-clicking a button, retrying after a transient UI error).
package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// proposalCacheTaskFingerprint records the current, actor-readable task data
// a proposal prompt was built from. The public id is the lookup key; the
// fingerprint includes its current text, state, and primary assignee fields.
type proposalCacheTaskFingerprint struct {
	PublicID    string `json:"publicId"`
	Fingerprint string `json:"fingerprint"`
}

// proposalCacheSnapshot contains the managed inputs that must still match
// before a cached proposal can be returned. RosterFingerprint is populated
// for smart-create proposals, whose prompt lists workspace members.
type proposalCacheSnapshot struct {
	TaskInputs        []proposalCacheTaskFingerprint `json:"taskInputs"`
	RosterFingerprint string                         `json:"rosterFingerprint,omitempty"`
}

type proposalCacheEnvelope struct {
	Payload  json.RawMessage
	Snapshot proposalCacheSnapshot
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

// getEnvelope decodes a fresh copy of a typed proposal payload. JSON is used
// as the copy boundary so callers cannot mutate a shared pointer or slice
// held by the cache after a hit.
func (c *ProposalCache) getEnvelope(key string, destination any) (proposalCacheSnapshot, bool) {
	var snapshot proposalCacheSnapshot
	value, ok := c.Get(key)
	if !ok {
		return snapshot, false
	}
	envelope, ok := value.(proposalCacheEnvelope)
	if !ok || len(envelope.Payload) == 0 {
		c.Delete(key)
		return snapshot, false
	}
	if err := json.Unmarshal(envelope.Payload, destination); err != nil {
		c.Delete(key)
		return snapshot, false
	}
	snapshot.TaskInputs = append([]proposalCacheTaskFingerprint(nil), envelope.Snapshot.TaskInputs...)
	snapshot.RosterFingerprint = envelope.Snapshot.RosterFingerprint
	return snapshot, true
}

// putEnvelope stores a typed proposal together with its managed-input
// snapshot. An unencodable value is simply left uncached; the proposal itself
// remains valid for the current request.
func (c *ProposalCache) putEnvelope(key string, value any, snapshot proposalCacheSnapshot) {
	payload, err := json.Marshal(value)
	if err != nil {
		return
	}
	snapshot.TaskInputs = append([]proposalCacheTaskFingerprint(nil), snapshot.TaskInputs...)
	c.Put(key, proposalCacheEnvelope{Payload: payload, Snapshot: snapshot})
}

// Delete removes a stale or unrecognized entry after validation fails.
func (c *ProposalCache) Delete(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.m, key)
	c.mu.Unlock()
}
