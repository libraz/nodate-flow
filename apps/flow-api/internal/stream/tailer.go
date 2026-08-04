// tailer.go — feeds the notifier from the `events` table so a change
// written by a different process on the same database reaches this
// process's subscribers.
//
// The in-process tap only sees appends made by this binary. Two
// products sharing a database each have their own fan-out, and neither
// reaches the other; the append-only log is the one thing both write,
// so tailing it closes the gap without a broker or any shared code.

package stream

import (
	"context"
	"database/sql"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// selfWriteLedger is the set of events.id values this process appended
// and already published through the tap, so the tailer can skip them
// rather than invalidate the same queries a second time.
type selfWriteLedger interface {
	// claim reports whether id was published by this process, removing
	// it from the ledger. A false answer means "publish it".
	claim(id uint64) bool
}

const (
	// defaultTailInterval is how often the log is polled. The query is
	// a primary-key range scan that usually returns nothing, so this
	// can be short without being expensive.
	defaultTailInterval = time.Second

	// defaultTailBatch caps rows read per poll. A poll that fills the
	// batch is immediately repeated, so a burst is drained within one
	// tick rather than one batch per tick.
	defaultTailBatch = 256

	// defaultTailGrace is how long an id may stay in the re-scan window.
	//
	// AUTO_INCREMENT ids are handed out when a statement runs but only
	// become visible when its transaction commits, so a transaction
	// that started earlier can appear *below* an id already read. A
	// cursor that advanced straight to the highest id seen would step
	// over it permanently. The tailer therefore keeps re-reading from
	// below the high-water mark until a row has had this long to
	// commit, and suppresses the repeats with an id set.
	//
	// The window bounds the correctness claim: an event whose
	// transaction stays open longer than this can still be missed.
	// Event appends are single INSERTs at the end of their transaction,
	// so five seconds is far more than they need.
	defaultTailGrace = 5 * time.Second

	// maxTailSeen bounds the suppression set. Reaching it means the
	// grace window filled with more events than expected; the oldest
	// half is dropped, which risks re-publishing an invalidation
	// (harmless) rather than growing without limit.
	maxTailSeen = 8192
)

// Tailer polls the append-only `events` table and publishes one
// invalidation per row to a [Notifier].
//
// It is the cross-process half of the fan-out: [EventbusTap] handles
// appends made by this binary, and the tailer handles everyone else's.
// Rows the tap already published are skipped via the ledger, so a
// single-product deployment behaves exactly as it did without a tailer.
type Tailer struct {
	db       *sql.DB
	notifier Notifier
	self     selfWriteLedger
	interval time.Duration
	batch    int
	grace    time.Duration

	// safeID is the highest id that will never be read again. It trails
	// the high-water mark by at least grace so late-committing rows are
	// still picked up. seen suppresses the duplicate reads that
	// trailing produces.
	safeID uint64
	seen   map[uint64]struct{}
	// pendingSafe is the high-water mark observed one grace period ago;
	// it becomes safeID at the next promotion.
	pendingSafe uint64
	highWater   uint64
	promoteAt   time.Time

	// now is time.Now except in tests.
	now func() time.Time

	published atomic.Uint64
	skipped   atomic.Uint64
	failures  atomic.Uint64
}

// NewTailer returns a tailer reading db and publishing to notifier.
//
// tap may be nil, in which case nothing is deduplicated and this
// process re-publishes its own appends. Passing the live tap switches
// on its self-write ledger, which is otherwise not recorded — an
// unread ledger would only grow.
func NewTailer(db *sql.DB, notifier Notifier, tap *EventbusTap) *Tailer {
	if notifier == nil {
		notifier = NopNotifier{}
	}
	t := &Tailer{
		db:       db,
		notifier: notifier,
		interval: defaultTailInterval,
		batch:    defaultTailBatch,
		grace:    defaultTailGrace,
		seen:     make(map[uint64]struct{}),
		now:      time.Now,
	}
	if tap != nil {
		tap.trackSelfWrites()
		t.self = tap
	}
	return t
}

// SetInterval overrides the poll period. Must be called before Run.
func (t *Tailer) SetInterval(d time.Duration) {
	if d > 0 {
		t.interval = d
	}
}

// SetGrace overrides how long a row may stay in the re-scan window.
// Must be called before Run.
func (t *Tailer) SetGrace(d time.Duration) {
	if d > 0 {
		t.grace = d
	}
}

// TailerMetrics is a point-in-time view of the tailer's counters.
type TailerMetrics struct {
	// EventsPublished counts rows forwarded to the notifier.
	EventsPublished uint64
	// EventsSkipped counts rows recognised as this process's own
	// appends, which the tap already published.
	EventsSkipped uint64
	// PollFailures counts polls that returned an error. A rising
	// value with a flat EventsPublished means the log is not being
	// read at all.
	PollFailures uint64
	// Cursor is the highest events.id observed.
	Cursor uint64
}

// Metrics returns the tailer's counters.
func (t *Tailer) Metrics() TailerMetrics {
	return TailerMetrics{
		EventsPublished: t.published.Load(),
		EventsSkipped:   t.skipped.Load(),
		PollFailures:    t.failures.Load(),
		Cursor:          atomic.LoadUint64(&t.highWater),
	}
}

// Run polls until ctx is cancelled, returning ctx.Err().
//
// It starts from the current end of the log rather than the beginning:
// a restarting process would otherwise replay the entire history as
// invalidations, and every subscriber gets a resync marker on connect
// anyway.
func (t *Tailer) Run(ctx context.Context) error {
	if err := t.seekToEnd(ctx); err != nil {
		// Starting from zero would replay the whole log. Refusing to
		// start is the quieter failure: realtime falls back to the
		// in-process tap, which is what a single-product deployment
		// has always used.
		return err
	}

	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			t.drain(ctx)
		}
	}
}

// drain polls until the log is caught up or an error stops it.
func (t *Tailer) drain(ctx context.Context) {
	for {
		n, err := t.poll(ctx)
		if err != nil {
			t.failures.Add(1)
			slog.WarnContext(ctx, "stream: event tail poll failed", "err", err)
			return
		}
		t.promote()
		if n < t.batch {
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// seekToEnd points the cursor at the current end of the log.
func (t *Tailer) seekToEnd(ctx context.Context) error {
	var maxID sql.NullInt64
	if err := t.db.QueryRowContext(ctx, `SELECT MAX(id) FROM events`).Scan(&maxID); err != nil {
		return err
	}
	if maxID.Valid && maxID.Int64 > 0 {
		//#nosec G115 -- MAX of an AUTO_INCREMENT column is non-negative
		t.safeID = uint64(maxID.Int64)
	}
	t.pendingSafe = t.safeID
	atomic.StoreUint64(&t.highWater, t.safeID)
	t.promoteAt = t.now().Add(t.grace)
	return nil
}

// tailQuery reads the next batch. The workspace public id is resolved
// in the same statement because the tailer has no workspace cache to
// consult — the rows it cares about were written by a process whose
// subscriptions this one never saw.
const tailQuery = `
	SELECT e.id, e.type, BIN_TO_UUID(w.public_id, 0)
	  FROM events e
	  JOIN workspaces w ON w.id = e.workspace_id
	 WHERE e.id > ?
	 ORDER BY e.id
	 LIMIT ?`

// poll reads one batch and publishes what it has not published before.
// It returns the number of rows read.
func (t *Tailer) poll(ctx context.Context) (int, error) {
	rows, err := t.db.QueryContext(ctx, tailQuery, t.safeID, t.batch)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()

	count := 0
	at := t.now().Unix()
	for rows.Next() {
		var (
			id          uint64
			eventType   string
			workspaceID string
		)
		if err := rows.Scan(&id, &eventType, &workspaceID); err != nil {
			return count, err
		}
		count++
		if id > atomic.LoadUint64(&t.highWater) {
			atomic.StoreUint64(&t.highWater, id)
		}
		if _, dup := t.seen[id]; dup {
			continue
		}
		t.seen[id] = struct{}{}

		if t.self != nil && t.self.claim(id) {
			t.skipped.Add(1)
			continue
		}
		kind, ok := KindForEventType(eventType)
		if !ok {
			continue
		}
		t.notifier.Publish(ctx, Event{Kind: kind, WorkspaceID: workspaceID, At: at})
		t.published.Add(1)
	}
	if err := rows.Err(); err != nil {
		return count, err
	}
	t.trimSeen()
	return count, nil
}

// promote moves the floor up once a grace period has passed, so rows
// that had time to commit stop being re-read.
func (t *Tailer) promote() {
	now := t.now()
	if now.Before(t.promoteAt) {
		return
	}
	t.safeID = t.pendingSafe
	t.pendingSafe = atomic.LoadUint64(&t.highWater)
	t.promoteAt = now.Add(t.grace)
	for id := range t.seen {
		if id <= t.safeID {
			delete(t.seen, id)
		}
	}
}

// trimSeen enforces the suppression set's ceiling, dropping the oldest
// half. Dropped ids may be published twice; an invalidation is
// idempotent, so that is the safe direction to fail in.
func (t *Tailer) trimSeen() {
	if len(t.seen) <= maxTailSeen {
		return
	}
	ids := make([]uint64, 0, len(t.seen))
	for id := range t.seen {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids[:len(ids)/2] {
		delete(t.seen, id)
	}
}

// selfWrites is the ledger [EventbusTap] keeps for a tailer.
type selfWrites struct {
	mu      sync.Mutex
	on      bool
	ids     map[uint64]struct{}
	highest uint64
}

// enable switches recording on. Until a tailer asks for it nothing is
// recorded, because an unread ledger has no bound.
func (s *selfWrites) enable() {
	s.mu.Lock()
	s.on = true
	if s.ids == nil {
		s.ids = make(map[uint64]struct{})
	}
	s.mu.Unlock()
}

// record notes that this process published the event with this id.
func (s *selfWrites) record(id uint64) {
	if id == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.on {
		return
	}
	s.ids[id] = struct{}{}
	if id > s.highest {
		s.highest = id
	}
	if len(s.ids) > maxTailSeen {
		// The tailer is not draining. Keep the newest ids, which are
		// the ones it is about to reach.
		cutoff := s.highest - uint64(maxTailSeen/2)
		for existing := range s.ids {
			if existing <= cutoff {
				delete(s.ids, existing)
			}
		}
	}
}

// claim implements [selfWriteLedger].
func (s *selfWrites) claim(id uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ids[id]; !ok {
		return false
	}
	delete(s.ids, id)
	return true
}
