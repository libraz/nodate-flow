// Per-user debounce table for Discord PresenceUpdate events.
//
// Discord emits presence transitions in bursts — a client going from
// "online" to "idle" to "dnd" within seconds is common when a user
// closes their laptop or taps "do not disturb" on mobile, and the
// gateway streams every intermediate state. Flow-api only cares about
// the user's "settled" presence; the in-flight transitions add noise
// to the signals table without changing anything downstream.
//
// The strategy is "leading-edge emit + trailing-edge replay":
//
//  1. The first event for a user is emitted immediately so reactions
//     are crisp (judge agents can run as soon as the user changes
//     presence rather than waiting out the full window).
//  2. Any subsequent event for the same user within the window cancels
//     the existing trailing timer and schedules a new one that fires
//     `window` after the most recent event arrival. The latest event
//     payload is the one that fires.
//  3. Each event replaced by step (2) increments
//     DebounceDroppedTotal so dashboards see suppression activity.
//  4. Stop() drains pending timers without firing them — at shutdown
//     the next session will receive fresh PresenceUpdate snapshots so
//     replaying stale state would only ever mislead the judge.
//
// The table is intentionally in-memory: presence updates are ephemeral
// by nature and Discord re-sends a full PresenceUpdate burst when the
// gateway re-identifies, so a process restart loses nothing of value.
package gateway

import (
	"context"
	"sync"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/presence-discord/internal/obs"
)

// debounceEmitter is the narrow surface the debouncer needs to push a
// settled event downstream. Decoupled from the HTTP emitter so the
// table can be unit-tested without spinning up a fake flow-api.
type debounceEmitter interface {
	Emit(ctx context.Context, ev PresenceEvent)
}

// PresenceEvent is the minimal snapshot the debouncer holds per user.
// It carries enough payload to reconstruct the POST /signals body
// without retaining a pointer to the underlying discordgo struct
// (which the library may reuse for subsequent events).
type PresenceEvent struct {
	UserID           string
	Status           string
	GuildID          string
	GatewaySessionID string
	// Activities is captured as already-marshalable opaque values so the
	// debouncer never holds discordgo internals. The HTTP emitter
	// re-marshals before sending.
	Activities []any
	// ReceivedAt is when the gateway first saw the event, used for
	// observability and ordering tests.
	ReceivedAt time.Time
}

// pendingEntry tracks a user that has at least one event in flight.
// timer is non-nil only while a trailing emit is scheduled; nextEvent
// is the most recent payload to replay when the timer fires.
type pendingEntry struct {
	timer     *time.Timer
	nextEvent PresenceEvent
	// pending is true when a trailing emit is scheduled. False while
	// the entry exists only to record "last emit time" for window
	// gating.
	pending bool
}

// Debouncer collapses presence event bursts per user. Safe for
// concurrent use from multiple goroutines (the discordgo session
// dispatches handlers in its own goroutine; the trailing timers fire
// in time.AfterFunc goroutines).
type Debouncer struct {
	window  time.Duration
	emitter debounceEmitter

	mu      sync.Mutex
	entries map[string]*pendingEntry
	// stopped is true once Stop has been called. While stopped, Handle
	// drops events on the floor and refuses to schedule new timers so
	// graceful shutdown actually terminates.
	stopped bool
	// lastEmit records the wall-clock time of the most recent emitted
	// event per user. Used by Handle to gate the leading-edge fast
	// path against the trailing-window cooldown.
	lastEmit map[string]time.Time

	// now is overridable in tests so window arithmetic stays
	// deterministic without sleeping in CI.
	now func() time.Time
	// emitCtx is the context passed to emitter.Emit when the timer
	// fires. Captured from NewDebouncer so timers don't outlive the
	// gateway's lifetime even after Stop.
	emitCtx context.Context //nolint:containedctx // intentional — timer fires off-goroutine and must carry its own context.

	// sweepTicker drives the periodic GC of idle lastEmit records so the
	// table cannot grow without bound across reconnect snapshots.
	// Stopped by Stop.
	sweepTicker *time.Ticker
	// sweepStop is closed by Stop to wake the sweep goroutine even when
	// the ticker has been halted, so the goroutine never blocks
	// forever on a quiesced ticker channel.
	sweepStop chan struct{}
	// sweepDone is closed by the sweep goroutine once it observes Stop,
	// letting Stop block until the goroutine has exited.
	sweepDone chan struct{}
}

// NewDebouncer constructs a debouncer with the configured window and
// downstream emitter. The window must be > 0; a zero window degenerates
// to "emit everything", which defeats the whole point and is rejected
// at config.Validate.
//
// ctx is the long-lived context used when trailing timers fire. The
// gateway's Start passes its own ctx; cancellation halts in-flight
// emits via the HTTP client's own context propagation.
func NewDebouncer(ctx context.Context, window time.Duration, emitter debounceEmitter) *Debouncer {
	d := &Debouncer{
		window:    window,
		emitter:   emitter,
		entries:   map[string]*pendingEntry{},
		lastEmit:  map[string]time.Time{},
		now:       time.Now,
		emitCtx:   ctx,
		sweepStop: make(chan struct{}),
		sweepDone: make(chan struct{}),
	}
	// Sweep idle records on a cadence proportional to the window. A
	// distinct user that emits once leaves a lastEmit entry behind for
	// window gating; without this GC, every snowflake the gateway ever
	// observes — re-sent in full on each reconnect's READY/resume
	// snapshot — would accumulate forever and eventually OOM the
	// process. The sweep removes records that are already past their
	// gating window, so a churning population stays flat.
	interval := window
	if interval <= 0 {
		interval = time.Second
	}
	d.sweepTicker = time.NewTicker(interval)
	go d.sweepLoop()
	return d
}

// sweepLoop runs the periodic GC until Stop fires. It exits promptly
// once stopped and closes sweepDone so Stop can join it.
func (d *Debouncer) sweepLoop() {
	defer close(d.sweepDone)
	for {
		select {
		case <-d.sweepStop:
			return
		case <-d.sweepTicker.C:
			if d.sweep() {
				return
			}
		}
	}
}

// sweep removes idle lastEmit records whose gating window has elapsed
// and any leftover non-pending entries. It returns true once the
// debouncer has been stopped so the caller exits the loop. Entries with
// a pending trailing emit are preserved — fire() owns their cleanup.
func (d *Debouncer) sweep() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return true
	}
	cutoff := d.now().Add(-d.window)
	for userID, last := range d.lastEmit {
		if entry, ok := d.entries[userID]; ok && entry.pending {
			continue
		}
		if !last.After(cutoff) {
			delete(d.lastEmit, userID)
			delete(d.entries, userID)
		}
	}
	return false
}

// Handle ingests a presence event. The leading-edge fires
// synchronously through the emitter (so the caller observes the same
// goroutine-ordering guarantees as discordgo's handler dispatch); the
// trailing-edge fires on a time.AfterFunc goroutine.
func (d *Debouncer) Handle(ev PresenceEvent) {
	d.mu.Lock()

	if d.stopped {
		d.mu.Unlock()
		return
	}

	now := d.now()
	last, hasLast := d.lastEmit[ev.UserID]
	entry, hasEntry := d.entries[ev.UserID]

	// Leading-edge fast path: no recent emit for this user, so push
	// through immediately. The window starts at this emit.
	if !hasLast || now.Sub(last) >= d.window {
		d.lastEmit[ev.UserID] = now
		d.mu.Unlock()
		d.emitter.Emit(d.emitCtx, ev)
		return
	}

	// Within the window. Schedule (or reschedule) a trailing emit so
	// the latest snapshot wins. Bumping DebounceDroppedTotal on each
	// replacement lets the dashboard show the storm rate without
	// needing per-user series.
	if hasEntry && entry.pending {
		// Cancel the prior trailing timer; the old payload is being
		// replaced by ev.
		if entry.timer != nil {
			entry.timer.Stop()
		}
		obs.DebounceDroppedTotal.Inc()
	} else {
		entry = &pendingEntry{}
		d.entries[ev.UserID] = entry
	}

	entry.pending = true
	entry.nextEvent = ev

	// Fire `window` after the most recent event arrival, not `window`
	// after the leading emit. This matches the plan's "if within N
	// seconds we receive multiple events for the same user, only send
	// the LAST one" semantics — the burst tail decides when settling
	// is over.
	userID := ev.UserID
	entry.timer = time.AfterFunc(d.window, func() {
		d.fire(userID)
	})

	d.mu.Unlock()
}

// fire is the trailing-timer callback. It clears the pending entry,
// records the emit timestamp, and pushes the captured event through
// the emitter on the caller's goroutine (i.e. AfterFunc's goroutine).
func (d *Debouncer) fire(userID string) {
	d.mu.Lock()
	entry, ok := d.entries[userID]
	if !ok || !entry.pending {
		d.mu.Unlock()
		return
	}
	ev := entry.nextEvent
	entry.pending = false
	entry.timer = nil
	// The entry no longer holds a scheduled timer; drop it from the
	// table so a user that has settled stops consuming memory. lastEmit
	// is retained for window gating and is reclaimed by the periodic
	// sweep once its window elapses.
	delete(d.entries, userID)
	d.lastEmit[userID] = d.now()
	if d.stopped {
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()
	d.emitter.Emit(d.emitCtx, ev)
}

// Stop cancels every pending trailing timer and prevents Handle from
// scheduling new ones. Stop is idempotent. Calling it from a deferred
// path in Gateway.Stop guarantees the binary exits even if a burst
// arrives during the shutdown window.
//
// Stop does NOT flush pending payloads — at gateway-restart Discord
// re-emits PresenceUpdate snapshots for every guild member, so the
// next session will repopulate the picture. Flushing stale state at
// shutdown would only ever mislead the judge.
func (d *Debouncer) Stop() {
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return
	}
	d.stopped = true
	for _, entry := range d.entries {
		if entry.timer != nil {
			entry.timer.Stop()
		}
		entry.pending = false
	}
	ticker := d.sweepTicker
	d.mu.Unlock()

	// Stop the ticker and wake the sweep goroutine outside the lock so
	// it can take the mutex, observe stopped, and exit without
	// deadlocking against Stop. sweepStop guarantees the goroutine wakes
	// even though Stop halts the ticker channel.
	if ticker != nil {
		ticker.Stop()
	}
	close(d.sweepStop)
	<-d.sweepDone
}
