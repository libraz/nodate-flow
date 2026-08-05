// Package reconciler contains background drift-healing loops that keep
// tasks and calendar_events consistent when a writer bypassed itemkit
// (e.g. a direct SQL patch during a migration, or a crashed transaction
// that rolled back one table but not the other).
//
// The reconciler is the safety net below itemkit, not a substitute for
// it: every scan that finds drift is also a bug somewhere upstream. The
// Prometheus counter item_inconsistency_total therefore doubles as an
// alert source — sustained non-zero rate means a writer is skipping
// itemkit.
package reconciler

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// DefaultInterval is the gap between reconciler runs when Start is
// called without an explicit interval override. Five minutes keeps the
// steady-state load trivial while bounding drift visibility.
const DefaultInterval = 5 * time.Minute

// maxDriftRowsPerPass caps how many linked events one due-date scan
// inspects. The scan resumes from its cursor on the next pass, so the
// cap trades a longer time-to-detect on a large table for a bounded
// query — the alternative, an unpaged scan, grows without limit under
// exactly the deployments that can least afford it.
const maxDriftRowsPerPass = 5000

// MetricsSink is the minimal counter surface the reconciler needs. The
// flow-api wires this to the Prometheus counters in internal/obs;
// tests pass a map-backed recorder.
type MetricsSink interface {
	IncInconsistency(kind string)
	IncHeal(kind string)
	IncRun()
	IncError()
}

// Reconciler scans tasks / calendar_events for drift and heals what
// it can, logging and metering the rest.
type Reconciler struct {
	DB       *sql.DB
	Logger   *slog.Logger
	Metrics  MetricsSink
	Interval time.Duration

	// dueDriftCursor is the last calendar_events.id the due-date scan
	// inspected. Atomic because RunOnce is exported for tests and
	// nothing stops a caller from driving a pass off another goroutine.
	dueDriftCursor atomic.Uint32
}

// Start runs the reconciler loop until ctx is cancelled. It blocks
// until one scan has completed, then ticks on r.Interval (or
// DefaultInterval when unset). Each tick starts a scan only if the
// previous one has finished — overlap would have no value.
func (r *Reconciler) Start(ctx context.Context) {
	interval := r.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	// First run happens immediately so startup-time drift (from a
	// crash or manual SQL) is caught without waiting a full interval.
	r.runOnce(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.runOnce(ctx)
		}
	}
}

// RunOnce performs a single scan pass. Exposed for tests.
func (r *Reconciler) RunOnce(ctx context.Context) {
	r.runOnce(ctx)
}

func (r *Reconciler) runOnce(ctx context.Context) {
	if r.Metrics != nil {
		r.Metrics.IncRun()
	}
	// Each scan is a short, read-only query followed by a narrow
	// UPDATE per row. Errors are logged and metered; they do not
	// abort the rest of the pass because the drift kinds are
	// independent.
	r.scanDueDateDrift(ctx)
	r.scanOrphanRole(ctx)
	r.scanEnabledMismatch(ctx)
}

// scanDueDateDrift finds linked calendar_events (task_role = 'due')
// whose start date disagrees with tasks.due_on, then heals by copying
// from the event (richer source — it carries the time-of-day too).
//
// The comparison happens in Go rather than as `DATE(ce.start_at) <>
// t.due_on`, because the SQL form asks the question in UTC. A Tokyo
// 08:00 meeting is stored as 23:00 UTC the previous day, so the SQL date
// is a day early — which made this loop re-assert the wrong deadline
// every five minutes, silently undoing both itemkit's correct write and
// any manual correction. A reconciler that heals toward a wrong value is
// worse than no reconciler: it makes the bug survive being fixed.
//
// Doing it in Go means the scan can no longer be narrowed by a WHERE
// clause — the local date has to be computed before it is known whether
// a row drifted. So the scan is paged instead: each pass reads at most
// maxDriftRowsPerPass rows and resumes from where it stopped, which
// bounds the work per pass while still covering the whole table over
// successive passes. Drift healing was always eventual; this makes the
// cost of that eventuality explicit.
func (r *Reconciler) scanDueDateDrift(ctx context.Context) {
	const role = "due"
	// The JOIN uses the (task_id, task_role, enabled) index; the keyset
	// on ce.id keeps each page a bounded forward scan.
	const q = `SELECT t.id, ce.id, t.due_on, ce.start_at, ce.timezone
	      FROM tasks t
	      JOIN calendar_events ce ON ce.task_id = t.id AND ce.enabled
	      WHERE t.enabled
	        AND ce.task_role = ?
	        AND ce.start_at IS NOT NULL
	        AND ce.id > ?
	      ORDER BY ce.id
	      LIMIT ?`
	cursor := r.dueDriftCursor.Load()
	rows, err := r.DB.QueryContext(ctx, q, role, cursor, maxDriftRowsPerPass)
	if err != nil {
		r.logError("scan drift failed", err, "role", role)
		return
	}
	defer rows.Close()

	const kind = "date_drift_due"
	type drift struct {
		taskID    uint32
		eventID   uint32
		taskDate  sql.NullTime
		eventDate sql.NullTime
	}
	var drifts []drift
	var scanned int
	var lastID uint32
	for rows.Next() {
		var d drift
		var startAt sql.NullTime
		var tz string
		if err := rows.Scan(&d.taskID, &d.eventID, &d.taskDate, &startAt, &tz); err != nil {
			r.logError("scan row failed", err, "role", role)
			continue
		}
		scanned++
		lastID = d.eventID
		if !startAt.Valid {
			continue
		}
		local, err := region.LocalDate(startAt.Time, region.EffectiveTimezone(tz))
		if err != nil {
			// The row names a zone that does not exist, so there is no
			// date to heal toward. Report it rather than falling back to
			// UTC, which would write a plausible-looking wrong date.
			if r.Metrics != nil {
				r.Metrics.IncInconsistency("event_timezone_invalid")
			}
			r.Logger.Warn("item consistency drift detected",
				"kind", "event_timezone_invalid", "task_id", d.taskID,
				"event_id", d.eventID, "timezone", tz)
			continue
		}
		if d.taskDate.Valid && sameDate(d.taskDate.Time, local) {
			continue
		}
		d.eventDate = sql.NullTime{Time: local, Valid: true}
		drifts = append(drifts, d)
	}
	if err := rows.Err(); err != nil {
		r.logError("scan iteration failed", err, "role", role)
		return
	}
	// A short page means the end of the table; start the next pass from
	// the beginning so rows before the cursor are revisited.
	if scanned < maxDriftRowsPerPass {
		r.dueDriftCursor.Store(0)
	} else {
		r.dueDriftCursor.Store(lastID)
	}

	for _, d := range drifts {
		if r.Metrics != nil {
			r.Metrics.IncInconsistency(kind)
		}
		r.Logger.Warn("item consistency drift detected",
			"kind", kind, "task_id", d.taskID, "event_id", d.eventID,
			"task_date", nullTimeString(d.taskDate),
			"event_date", nullTimeString(d.eventDate))

		// Heal: copy event.start_at's date onto the task. This is
		// the direction itemkit uses for linked writes — the event
		// is the richer source.
		const upd = `UPDATE tasks SET due_on = ? WHERE id = ? AND enabled`
		if err := r.heal(ctx, "reconciler.dateDrift", func(ctx context.Context) error {
			_, err := r.DB.ExecContext(ctx, upd, d.eventDate, d.taskID)
			return err
		}); err != nil {
			r.logError("heal drift failed", err,
				"kind", kind, "task_id", d.taskID, "event_id", d.eventID)
			continue
		}
		if r.Metrics != nil {
			r.Metrics.IncHeal(kind)
		}
	}
}

// scanOrphanRole finds calendar_events violating the invariant
// (task_id IS NULL) = (task_role IS NULL). The projection guard trigger
// now rejects such a row outright, so this scan covers rows written
// before the trigger existed, or by a database the trigger was never
// applied to. It only logs, because the correct heal direction is
// ambiguous (which did the writer mean to set?); the counter signals
// that some writer is bypassing itemkit.
func (r *Reconciler) scanOrphanRole(ctx context.Context) {
	const q = `SELECT id, public_id, task_id, task_role
	           FROM calendar_events
	           WHERE enabled
	             AND ((task_id IS NULL) <> (task_role IS NULL))`
	rows, err := r.DB.QueryContext(ctx, q)
	if err != nil {
		r.logError("scan orphan role failed", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id uint32
		var publicID []byte
		var taskID sql.NullInt32
		var taskRole sql.NullString
		if err := rows.Scan(&id, &publicID, &taskID, &taskRole); err != nil {
			r.logError("scan orphan row failed", err)
			continue
		}
		if r.Metrics != nil {
			r.Metrics.IncInconsistency("orphan_role")
		}
		r.Logger.Warn("item consistency drift detected",
			"kind", "orphan_role", "event_id", id,
			"task_id_null", !taskID.Valid, "task_role_null", !taskRole.Valid)
	}
	if err := rows.Err(); err != nil {
		r.logError("scan orphan iteration failed", err)
	}
}

// scanEnabledMismatch finds linked events still enabled after their
// task was soft-disabled. Heal: disable the event (task is the
// lifecycle anchor).
func (r *Reconciler) scanEnabledMismatch(ctx context.Context) {
	const q = `SELECT t.id, ce.id
	           FROM tasks t
	           JOIN calendar_events ce ON ce.task_id = t.id
	           WHERE t.enabled = FALSE AND ce.enabled = TRUE`
	rows, err := r.DB.QueryContext(ctx, q)
	if err != nil {
		r.logError("scan enabled mismatch failed", err)
		return
	}
	defer rows.Close()
	type pair struct{ taskID, eventID uint32 }
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.taskID, &p.eventID); err != nil {
			r.logError("scan enabled row failed", err)
			continue
		}
		pairs = append(pairs, p)
	}
	if err := rows.Err(); err != nil {
		r.logError("scan enabled iteration failed", err)
		return
	}
	if len(pairs) == 0 {
		return
	}

	// Healing here means soft-disabling a task-projected event, which
	// trg_calendar_events_projection_guard_upd refuses unless the writer
	// declares itself part of the projection engine. The reconciler is:
	// it restores the task↔event invariant itemkit failed to hold.
	//
	// The opt-in is a session variable, so the heals run on one pinned
	// connection rather than through the pool — otherwise the UPDATE
	// could land on a connection that never saw the SET, and worse, the
	// SET could stay behind on a connection that goes on to serve an
	// ordinary request with the guard down.
	conn, err := r.DB.Conn(ctx)
	if err != nil {
		r.logError("acquire connection for enabled mismatch heal failed", err)
		return
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "SET @nf_item_projection_engine = 1"); err != nil {
		r.logError("arm projection guard failed", err)
		return
	}
	defer func() { _, _ = conn.ExecContext(ctx, "SET @nf_item_projection_engine = NULL") }()

	for _, p := range pairs {
		if r.Metrics != nil {
			r.Metrics.IncInconsistency("enabled_mismatch")
		}
		r.Logger.Warn("item consistency drift detected",
			"kind", "enabled_mismatch", "task_id", p.taskID, "event_id", p.eventID)
		const upd = `UPDATE calendar_events SET enabled = FALSE WHERE id = ? AND enabled`
		if err := r.heal(ctx, "reconciler.enabledMismatch", func(ctx context.Context) error {
			_, err := conn.ExecContext(ctx, upd, p.eventID)
			return err
		}); err != nil {
			r.logError("heal enabled mismatch failed", err,
				"task_id", p.taskID, "event_id", p.eventID)
			continue
		}
		if r.Metrics != nil {
			r.Metrics.IncHeal("enabled_mismatch")
		}
	}
}

func (r *Reconciler) logError(msg string, err error, args ...any) {
	if r.Metrics != nil {
		r.Metrics.IncError()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	args = append([]any{"err", err}, args...)
	r.Logger.Error(msg, args...)
}

// sameDate reports whether two date-carrying times name the same
// calendar day. Both sides are midnight-carried values (a DATE column
// read back, and region.LocalDate's output), so comparing the Y/M/D
// triple avoids depending on either one's location.
func sameDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func nullTimeString(t sql.NullTime) string {
	if !t.Valid {
		return ""
	}
	return t.Time.Format("2006-01-02")
}

// heal runs one healing statement, retrying the transient MySQL errors
// (deadlock, lock-wait timeout) that a contended table hands back.
//
// Without the retry a heal that lost a lock race is logged and dropped,
// and the pair stays broken until some later run happens to catch it.
// The background loop does eventually come round again, but that makes
// the recovery a property of the schedule rather than of the reconciler
// — and the one-shot callers, RunOnce and anything driving a single
// pass, never get a second round at all. Contention is exactly what a
// safety net that sweeps live tables should expect to meet.
//
// Each statement stands alone, so retrying the statement is enough;
// there is no transaction whose earlier work a retry would have to
// redo.
func (r *Reconciler) heal(ctx context.Context, label string, exec func(context.Context) error) error {
	return dbretry.Do(ctx, label, exec)
}
