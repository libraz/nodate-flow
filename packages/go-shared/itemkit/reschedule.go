package itemkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/eventbus"
)

// RescheduleEventArgs moves a single calendar_events row in time.
// When the event is task-linked (task_id + task_role set) and the
// role is RoleDue, the change propagates to tasks.due_on.
type RescheduleEventArgs struct {
	WorkspaceID uint32
	EventID     uint32
	ActorUserID uint32

	// StartAt / EndAt are the new wall-clock times. Zero values are
	// treated as "clear" — the event becomes undated. Undated events
	// may not carry a projection role, so RescheduleEvent refuses to
	// set zero times on a task-linked event.
	StartAt time.Time
	EndAt   time.Time

	// Snap carries the actor's working-day preferences. Zero value
	// disables snap behavior.
	Snap SnapConfig
}

// RescheduleEvent updates a calendar_events row's start/end times
// and, when the event is a task projection, mirrors the date into
// tasks.due_on. Pure time-of-day changes (same date) do NOT touch
// the task — tasks have DATE precision, not DATETIME.
func RescheduleEvent(ctx context.Context, tx TX, args RescheduleEventArgs) error {
	if !args.StartAt.IsZero() && !args.EndAt.IsZero() && args.EndAt.Before(args.StartAt) {
		return wrapInvariant("chronology", "end_at before start_at")
	}
	evt, err := findEventByID(ctx, tx, args.WorkspaceID, args.EventID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("itemkit: event %d not found in ws %d", args.EventID, args.WorkspaceID)
		}
		return fmt.Errorf("itemkit: read event: %w", err)
	}
	// Undated flip is forbidden while a task-projection role is
	// attached (RoleDue). Scheduled role (time-block) may be cleared
	// but the plan keeps that out of MVP.
	if (args.StartAt.IsZero() || args.EndAt.IsZero()) && evt.taskRole.Valid {
		if DateRole(evt.taskRole.String) == RoleDue {
			return wrapInvariant("undated_projection",
				"cannot clear start/end on a task-projection event; unlink first")
		}
	}

	snap := applySnap(args.StartAt, args.EndAt, args.Snap)
	args.StartAt = snap.NewStart
	args.EndAt = snap.NewEnd

	const updateSQL = `UPDATE calendar_events
	                   SET start_at = ?, end_at = ?
	                   WHERE id = ? AND workspace_id = ?`
	startVal := sql.NullTime{Time: args.StartAt, Valid: !args.StartAt.IsZero()}
	endVal := sql.NullTime{Time: args.EndAt, Valid: !args.EndAt.IsZero()}
	if _, err := tx.ExecContext(ctx, updateSQL, startVal, endVal, evt.id, args.WorkspaceID); err != nil {
		return fmt.Errorf("itemkit: update event times: %w", err)
	}

	if err := applySnapFlags(ctx, tx, evt.id, snap); err != nil {
		return err
	}

	// Propagate to task only when linked and the DATE component changed.
	if evt.taskID.Valid && evt.taskRole.Valid {
		role := DateRole(evt.taskRole.String)
		if role == RoleDue {
			if !args.StartAt.IsZero() {
				if err := propagateTaskDateFromRole(ctx, tx, uint32(evt.taskID.Int32), role, args.StartAt); err != nil {
					return err
				}
			}
		}
	}

	payload := map[string]any{
		"eventPublicId": evt.publicID.String(),
	}
	if evt.taskID.Valid {
		payload["taskId"] = uint32(evt.taskID.Int32)
	}
	var taskPtr *uint32
	if evt.taskID.Valid {
		v := uint32(evt.taskID.Int32)
		taskPtr = &v
	}
	return appendItemEvents(ctx, tx, eventbus.ItemRescheduled, args.WorkspaceID, &args.ActorUserID, taskPtr, payload)
}

// RescheduleTaskArgs moves a task's due_on column and propagates to
// linked projection events.
type RescheduleTaskArgs struct {
	WorkspaceID uint32
	TaskID      uint32
	ActorUserID uint32

	// SetDueOn carries the new DATE value. When the flag is false the
	// column is untouched. Zero Time with SetDueOn=true clears the
	// column.
	SetDueOn bool
	DueOn    time.Time

	// Snap carries the actor's working-day preferences. Zero value
	// disables snap behavior. When SnapAuto adjusts the target DATE,
	// itemkit writes the snapped date into tasks.due_on as well so the
	// task and its projection event stay in lockstep.
	Snap SnapConfig
}

// RescheduleTask updates tasks.due_on per the args and mirrors the
// change onto any linked projection event, preserving the event's
// time-of-day portion.
func RescheduleTask(ctx context.Context, tx TX, args RescheduleTaskArgs) error {
	if !args.SetDueOn {
		return nil
	}
	task, err := findTaskByID(ctx, tx, args.WorkspaceID, args.TaskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("itemkit: task %d not found in ws %d", args.TaskID, args.WorkspaceID)
		}
		return fmt.Errorf("itemkit: read task: %w", err)
	}

	if args.SetDueOn {
		snappedDueOn := args.DueOn
		if !snappedDueOn.IsZero() {
			out := applySnap(snappedDueOn, snappedDueOn, args.Snap)
			snappedDueOn = out.NewStart
		}
		if err := updateTaskDateColumn(ctx, tx, task.id, "due_on", snappedDueOn); err != nil {
			return err
		}
		if err := propagateEventFromTaskDate(ctx, tx, task, RoleDue, snappedDueOn, args.ActorUserID, args.Snap); err != nil {
			return err
		}
		args.DueOn = snappedDueOn
	}

	payload := map[string]any{
		"taskPublicId": task.publicID.String(),
	}
	if args.SetDueOn {
		payload["dueOn"] = dateOrNull(args.DueOn)
	}
	return appendItemEvents(ctx, tx, eventbus.ItemRescheduled, args.WorkspaceID, &args.ActorUserID, &task.id, payload)
}

// updateTaskDateColumn writes the due_on DATE column.
// A zero Time is stored as SQL NULL.
func updateTaskDateColumn(ctx context.Context, tx TX, taskID uint32, col string, d time.Time) error {
	var val sql.NullTime
	if !d.IsZero() {
		val = sql.NullTime{Time: dateOnly(d), Valid: true}
	}
	q := "UPDATE tasks SET " + col + " = ? WHERE id = ?"
	if _, err := tx.ExecContext(ctx, q, val, taskID); err != nil {
		return fmt.Errorf("itemkit: update task %s: %w", col, err)
	}
	return nil
}

// propagateEventFromTaskDate updates the start/end of the linked
// event for the given role to reflect a new task date. Preserves the
// event's original time-of-day and duration. Clears the link when
// the task date is zero (unscheduled task). snap carries the actor's
// snap-to-working-day preference so the mirrored event gets the same
// badge treatment as a direct RescheduleEvent call.
func propagateEventFromTaskDate(ctx context.Context, tx TX, task taskRow, role DateRole, newDate time.Time, actorID uint32, snap SnapConfig) error {
	existing, err := findLinkedEvent(ctx, tx, task.id, role)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("itemkit: find linked event: %w", err)
	}
	if newDate.IsZero() {
		// Unschedule: disable the event, leave the task in place.
		return unlinkEventRow(ctx, tx, existing, actorID, "task_date_cleared")
	}
	if !existing.startAt.Valid || !existing.endAt.Valid {
		return wrapInvariant("event_undated_but_projection",
			"linked projection event has null start/end; reconciler must heal")
	}
	dur := existing.endAt.Time.Sub(existing.startAt.Time)
	newStart := time.Date(newDate.Year(), newDate.Month(), newDate.Day(),
		existing.startAt.Time.Hour(), existing.startAt.Time.Minute(), existing.startAt.Time.Second(), 0,
		existing.startAt.Time.Location())
	newEnd := newStart.Add(dur)

	// Task date has already been snapped upstream; re-run snap here only
	// to badge the mirrored event's flags consistently.
	out := applySnap(newStart, newEnd, snap)

	const upd = `UPDATE calendar_events SET start_at = ?, end_at = ? WHERE id = ?`
	if _, err := tx.ExecContext(ctx, upd,
		sql.NullTime{Time: out.NewStart, Valid: true},
		sql.NullTime{Time: out.NewEnd, Valid: true},
		existing.id); err != nil {
		return fmt.Errorf("itemkit: mirror event times from task date: %w", err)
	}
	if err := applySnapFlags(ctx, tx, existing.id, out); err != nil {
		return err
	}
	return nil
}

// dateOrNull renders a date for event payload, using empty string for
// zero times.
func dateOrNull(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return dateOnly(t).Format("2006-01-02")
}
