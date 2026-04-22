// Package itemkit is the sole writer for cross-table mutations that
// span tasks and calendar_events. Every function runs inside a caller-
// provided *sql.Tx so the two tables move in lockstep and the
// append-only events log contains exactly one item.* row per logical
// change.
//
// Design principle: a task with a date is a calendar_event. itemkit
// enforces that invariant in code because MySQL cross-table triggers
// are not expressive enough (CHECK constraints cover column-level
// invariants inside calendar_events only).
//
// Every itemkit function ALSO appends the legacy task.* / calendar.*
// kind alongside the new item.* kind for one release. Legacy kinds
// drop in the next release (see packages/go-shared/eventbus/kinds.go).
//
// itemkit does NOT enforce ACL — callers must authorize the actor
// before invoking. See docs/plan/release-5-unified-calendar.md
// "ACL model" for the policy.
package itemkit

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
)

// DateRole encodes how a calendar event relates to its linked task.
//   - RoleEvent     — event projects task.event_on (external milestone date).
//   - RoleDue       — event projects task.due_on (hard deadline).
//   - RoleScheduled — event is a time-block (multiple allowed per task).
type DateRole string

const (
	RoleEvent     DateRole = "event"
	RoleDue       DateRole = "due"
	RoleScheduled DateRole = "scheduled"
)

// IsValid reports whether the role is one of the three allowed values.
func (r DateRole) IsValid() bool {
	switch r {
	case RoleEvent, RoleDue, RoleScheduled:
		return true
	}
	return false
}

// TX is the minimal transaction surface itemkit needs. *sql.Tx
// satisfies it. Callers MUST pass a tx — passing *sql.DB would split
// the task write and the event write across connections and defeat
// the atomicity guarantee.
type TX interface {
	ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, q string, a ...any) *sql.Row
	QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error)
}

// taskRow is itemkit's minimal projection of a tasks row. Fields are
// named to mirror the tasks table columns.
type taskRow struct {
	id          uint32
	publicID    dbtype.PublicID
	workspaceID uint32
	title       string
	eventOn     sql.NullTime
	dueOn       sql.NullTime
	updatedAt   time.Time
}

// eventRow is itemkit's minimal projection of a calendar_events row.
type eventRow struct {
	id          uint32
	publicID    dbtype.PublicID
	workspaceID uint32
	calendarID  uint32
	taskID      sql.NullInt32
	taskRole    sql.NullString
	title       string
	startAt     sql.NullTime
	endAt       sql.NullTime
	timezone    string
	ownerUserID uint32
	allDay      bool
	updatedAt   time.Time
}

// findTaskByID reads the minimal task row under the given workspace.
// Returns sql.ErrNoRows when the task is missing or disabled.
func findTaskByID(ctx context.Context, tx TX, workspaceID, taskID uint32) (taskRow, error) {
	const q = `SELECT id, public_id, workspace_id, title, event_on, due_on, updated_at
	           FROM tasks
	           WHERE id = ? AND workspace_id = ? AND enabled = TRUE`
	var t taskRow
	err := tx.QueryRowContext(ctx, q, taskID, workspaceID).Scan(
		&t.id, &t.publicID, &t.workspaceID, &t.title, &t.eventOn, &t.dueOn, &t.updatedAt,
	)
	return t, err
}

// findEventByID reads the minimal calendar_events row under the given workspace.
func findEventByID(ctx context.Context, tx TX, workspaceID, eventID uint32) (eventRow, error) {
	const q = `SELECT id, public_id, workspace_id, calendar_id, task_id, task_role, title,
	                  start_at, end_at, timezone, owner_user_id, all_day, updated_at
	           FROM calendar_events
	           WHERE id = ? AND workspace_id = ? AND enabled = TRUE`
	var e eventRow
	err := tx.QueryRowContext(ctx, q, eventID, workspaceID).Scan(
		&e.id, &e.publicID, &e.workspaceID, &e.calendarID, &e.taskID, &e.taskRole, &e.title,
		&e.startAt, &e.endAt, &e.timezone, &e.ownerUserID, &e.allDay, &e.updatedAt,
	)
	return e, err
}

// findLinkedEvent returns the event (if any) currently linked to the
// given task with the given role. Returns sql.ErrNoRows when no link
// exists. For RoleScheduled the query returns the most recently-
// updated one (callers that need all of them should list separately).
func findLinkedEvent(ctx context.Context, tx TX, taskID uint32, role DateRole) (eventRow, error) {
	const q = `SELECT id, public_id, workspace_id, calendar_id, task_id, task_role, title,
	                  start_at, end_at, timezone, owner_user_id, all_day, updated_at
	           FROM calendar_events
	           WHERE task_id = ? AND task_role = ? AND enabled = TRUE
	           ORDER BY updated_at DESC
	           LIMIT 1`
	var e eventRow
	err := tx.QueryRowContext(ctx, q, taskID, string(role)).Scan(
		&e.id, &e.publicID, &e.workspaceID, &e.calendarID, &e.taskID, &e.taskRole, &e.title,
		&e.startAt, &e.endAt, &e.timezone, &e.ownerUserID, &e.allDay, &e.updatedAt,
	)
	return e, err
}

// dateOnly returns t truncated to local midnight in its own Location.
// Used when projecting event.start_at onto task.event_on / task.due_on.
func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// roleNullString wraps a DateRole as sql.NullString for nullable
// scanning / parameter binding.
func roleNullString(r DateRole) sql.NullString {
	return sql.NullString{String: string(r), Valid: true}
}

// wrapInvariant returns a formatted error carrying the itemkit
// invariant tag. Handlers translate this to HTTP 422 via the
// ITEM.ITEMKIT.INVARIANT_VIOLATION error code.
func wrapInvariant(name, detail string) error {
	return fmt.Errorf("itemkit invariant %q: %s", name, detail)
}
