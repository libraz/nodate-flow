package itemkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarrules"
	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
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
	// The window is ordered by the rule both transports answer with, so a
	// browser and an agent sending the same inverted pair are refused for
	// the same reason. What each of them is told is still theirs to decide:
	// the refusal leaves here as an itemkit invariant, which the REST and
	// MCP translators already map onto their own vocabularies.
	//
	// A zero instant is "no such end of the window" rather than year one,
	// which is what UnixSeconds encodes; the rule then has nothing to
	// order, and clearing a window is refused further down instead.
	if calendarrules.RequireEventChronology(
		calendarrules.UnixSeconds(args.StartAt), calendarrules.UnixSeconds(args.EndAt)) != nil {
		return wrapInvariant("chronology", "end_at before start_at")
	}
	disarm, err := armProjectionGuard(ctx, tx)
	if err != nil {
		return err
	}
	defer disarm()

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
	// The row's own all-day flag decides the shape of the window it can
	// hold: this call moves an event but never converts one, so an all-day
	// row stays a date and the instants that stand for it stay canonical
	// whichever transport asked for the move.
	args.StartAt, args.EndAt = allDayWindow(evt.allDay, args.StartAt, args.EndAt)

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
				if err := propagateTaskDateFromRole(ctx, tx, uint32(evt.taskID.Int32), role, args.StartAt, evt.timezone, evt.allDay); err != nil { //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
					return err
				}
			}
		}
	}

	payload := map[string]any{
		"eventPublicId": evt.publicID.String(),
	}
	var taskPtr *uint32
	if evt.taskID.Valid {
		v := uint32(evt.taskID.Int32) //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
		taskPtr = &v
		// The events row carries the internal task id in its own column;
		// the payload is read by clients, so it names the task by the id
		// they can resolve.
		task, err := findTaskByID(ctx, tx, args.WorkspaceID, v)
		if err != nil {
			return fmt.Errorf("itemkit: resolve task public id: %w", err)
		}
		payload["taskId"] = task.publicID.String()
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
	disarm, err := armProjectionGuard(ctx, tx)
	if err != nil {
		return err
	}
	defer disarm()

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
		val = sql.NullTime{Time: dateOnly(d).DateColumn(), Valid: true}
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
	// Rebuild the instant in the event's own zone. Composing it from the
	// stored UTC clock instead keeps the wrong time-of-day: a Tokyo 08:00
	// meeting reads as 23:00 in UTC, so moving its task to the 8th put
	// the event at 23:00 on the 8th — 08:00 on the 9th in Tokyo, a day
	// past the deadline that asked for it. The two then disagree
	// permanently, and the drift reconciler resolves that disagreement in
	// the event's favour, silently undoing the date the user typed.
	z, err := region.Resolve(existing.timezone)
	if err != nil {
		return wrapInvariant("event_timezone_valid",
			fmt.Sprintf("event timezone %q is not a known IANA zone", existing.timezone))
	}
	dur := existing.endAt.Time.Sub(existing.startAt.Time)
	localStart := existing.startAt.Time.In(z.Location())
	newStart := dateOnly(newDate).At(z,
		localStart.Hour(), localStart.Minute(), localStart.Second())
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
	return dateOnly(t).String()
}
