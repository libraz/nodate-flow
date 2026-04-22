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
	"time"
)

// DefaultInterval is the gap between reconciler runs when Start is
// called without an explicit interval override. Five minutes keeps the
// steady-state load trivial while bounding drift visibility.
const DefaultInterval = 5 * time.Minute

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
// whose DATE(start_at) disagrees with tasks.due_on, then heals by
// copying from the event (richer source — it carries the time-of-day
// too).
func (r *Reconciler) scanDueDateDrift(ctx context.Context) {
	const role = "due"
	// The JOIN uses the (task_id, task_role, enabled) index. DATE() is
	// a row-level filter applied after the join.
	const q = `SELECT t.id, ce.id, t.due_on, DATE(ce.start_at)
	      FROM tasks t
	      JOIN calendar_events ce ON ce.task_id = t.id AND ce.enabled
	      WHERE t.enabled
	        AND ce.task_role = ?
	        AND ce.start_at IS NOT NULL
	        AND (t.due_on IS NULL OR DATE(ce.start_at) <> t.due_on)`
	rows, err := r.DB.QueryContext(ctx, q, role)
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
	for rows.Next() {
		var d drift
		if err := rows.Scan(&d.taskID, &d.eventID, &d.taskDate, &d.eventDate); err != nil {
			r.logError("scan row failed", err, "role", role)
			continue
		}
		drifts = append(drifts, d)
	}
	if err := rows.Err(); err != nil {
		r.logError("scan iteration failed", err, "role", role)
		return
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
		if _, err := r.DB.ExecContext(ctx, upd, d.eventDate, d.taskID); err != nil {
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
// (task_id IS NULL) = (task_role IS NULL). itemkit enforces this on
// every write; the reconciler only logs because the correct heal
// direction is ambiguous (which did the writer mean to set?). The
// counter signals that some writer is bypassing itemkit.
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
	for _, p := range pairs {
		if r.Metrics != nil {
			r.Metrics.IncInconsistency("enabled_mismatch")
		}
		r.Logger.Warn("item consistency drift detected",
			"kind", "enabled_mismatch", "task_id", p.taskID, "event_id", p.eventID)
		const upd = `UPDATE calendar_events SET enabled = FALSE WHERE id = ? AND enabled`
		if _, err := r.DB.ExecContext(ctx, upd, p.eventID); err != nil {
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

func nullTimeString(t sql.NullTime) string {
	if !t.Valid {
		return ""
	}
	return t.Time.Format("2006-01-02")
}
