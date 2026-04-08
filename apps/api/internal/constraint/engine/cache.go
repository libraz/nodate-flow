package engine

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
	"sync"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/constraint"
)

// Cache is a memoization layer for [constraint.Evaluate]. It keys
// results on a SHA-256 digest of (expression bytes, facts
// snapshot), which preserves determinism: any run with identical
// inputs — including replay — hits the same cache line.
//
// The cache is safe for concurrent use. It has no eviction policy
// because the keyspace is bounded by active task_constraints rows
// and short-lived Facts; callers that want size bounds should
// instantiate per-request caches instead.
type Cache struct {
	mu sync.RWMutex
	m  map[string]bool
}

// NewCache returns an empty [Cache].
func NewCache() *Cache { return &Cache{m: map[string]bool{}} }

// Evaluate returns the cached outcome for (expression, facts) or
// computes and stores it. Parse errors are surfaced directly (not
// cached) because their source is operator-visible input and fixes
// should take effect immediately.
func (c *Cache) Evaluate(expression []byte, facts constraint.Facts) (bool, error) {
	key := hashKey(expression, facts)
	c.mu.RLock()
	if v, ok := c.m[key]; ok {
		c.mu.RUnlock()
		return v, nil
	}
	c.mu.RUnlock()

	parsed, err := constraint.Parse(expression)
	if err != nil {
		return false, err
	}
	v, err := constraint.Evaluate(parsed, facts)
	if err != nil {
		return false, err
	}
	c.mu.Lock()
	c.m[key] = v
	c.mu.Unlock()
	return v, nil
}

// Size returns the current number of cached entries (test-only).
func (c *Cache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.m)
}

// hashKey produces a deterministic digest over the expression bytes
// and every Facts field that Evaluate reads. Maps are walked in
// sorted key order so two equivalent fact sets always produce the
// same hash regardless of insertion order.
func hashKey(expr []byte, f constraint.Facts) string {
	h := sha256.New()
	h.Write(expr)
	h.Write([]byte{0})
	if f.DueOn != nil {
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], uint64(f.DueOn.UnixNano()))
		h.Write(buf[:])
	}
	h.Write([]byte{0})
	writeStringMap(h, f.DependencyStates)
	h.Write([]byte{0})
	writeBoolMap(h, f.ActorRoles)
	h.Write([]byte{0})
	writeBoolMap(h, f.SignalsReceived)
	h.Write([]byte{0})
	writeBoolMap(h, f.Approvals)
	h.Write([]byte{0})
	h.Write([]byte(f.CIStatus))
	return hex.EncodeToString(h.Sum(nil))
}

func writeStringMap(h interface{ Write([]byte) (int, error) }, m map[string]string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{'='})
		_, _ = h.Write([]byte(m[k]))
		_, _ = h.Write([]byte{';'})
	}
}

func writeBoolMap(h interface{ Write([]byte) (int, error) }, m map[string]bool) {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{';'})
	}
}
