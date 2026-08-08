package calendar_event_day

import (
	"context"
	"sync"
	"time"
)

// CursorStore records how far the event-day scan has got for each
// workspace. The stored instant is the upper bound of the last span
// that completed, so the next tick resumes exactly where the previous
// one stopped.
//
// The zero time means "never scanned", which resolves to a full
// catch-up-width scan. That is the correct answer for a workspace the
// job has not seen and a safe one for a workspace whose cursor was
// lost: re-scanning is idempotent, because flow-api collapses repeats
// on the day-scoped external_id.
//
// The interface exists because the in-process implementation cannot
// answer the question across a restart, and the whole point of a cursor
// is to survive one. A worker that is redeployed while a workspace is
// several days behind starts over from the catch-up allowance and drops
// the rest of the backlog — the same days, lost the same way, just for
// a different reason than the one this seam was introduced to fix.
// Closing that needs a row per (job, workspace) holding the scanned-
// through instant, which is a schema change and therefore not in this
// package's hands.
type CursorStore interface {
	// Load returns the instant this workspace has been scanned through,
	// or the zero time when there is no cursor for it.
	Load(ctx context.Context, workspaceID uint32) (time.Time, error)
	// Save records the instant this workspace has been scanned through.
	Save(ctx context.Context, workspaceID uint32, scannedThrough time.Time) error
}

// MemoryCursorStore keeps cursors in the worker process.
//
// It is the default, and it is what the job has always done. Its limit
// is stated on CursorStore: cursors do not survive a restart, so a
// backlog wider than the catch-up allowance is truncated by a deploy
// even though a running process would have walked through it.
type MemoryCursorStore struct {
	mu sync.Mutex
	by map[uint32]time.Time
}

// NewMemoryCursorStore returns an empty in-process cursor store.
func NewMemoryCursorStore() *MemoryCursorStore {
	return &MemoryCursorStore{by: make(map[uint32]time.Time)}
}

// Load implements [CursorStore].
func (s *MemoryCursorStore) Load(_ context.Context, workspaceID uint32) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.by[workspaceID], nil
}

// Save implements [CursorStore].
func (s *MemoryCursorStore) Save(_ context.Context, workspaceID uint32, scannedThrough time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.by == nil {
		s.by = make(map[uint32]time.Time)
	}
	s.by[workspaceID] = scannedThrough
	return nil
}
