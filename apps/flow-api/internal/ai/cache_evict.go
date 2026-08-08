package ai

import (
	"sort"
	"time"
)

// evictKeepFraction is how much of the ceiling a sweep leaves behind.
// Trimming to three quarters means each sweep frees a quarter of the
// cache, so the O(n) walk happens once per limit/4 insertions rather
// than on every insertion past the ceiling.
const evictKeepFraction = 4

// EvictOldest bounds a TTL cache: it drops expired entries, and if that
// left the map still over limit, drops the entries closest to expiring
// until a quarter of the ceiling is free.
//
// The second half is what makes the ceiling real. Purging only expired
// entries is no bound at all — a cache filling faster than its TTL stays
// over the limit, which means every subsequent write walks the whole map
// and deletes nothing. That is both the unbounded memory and the
// slowdown: the more entries there are, the more each write costs.
//
// expiresAt reads the entry's deadline. The map is modified in place.
func EvictOldest[V any](m map[string]V, limit int, expiresAt func(V) time.Time) {
	now := time.Now()
	for k, v := range m {
		if now.After(expiresAt(v)) {
			delete(m, k)
		}
	}
	if limit <= 0 || len(m) <= limit {
		return
	}

	target := limit - limit/evictKeepFraction
	type aging struct {
		key string
		at  time.Time
	}
	entries := make([]aging, 0, len(m))
	for k, v := range m {
		entries = append(entries, aging{key: k, at: expiresAt(v)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
	for _, e := range entries[:len(entries)-target] {
		delete(m, e.key)
	}
}
