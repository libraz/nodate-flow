package itemkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
)

// UnscheduleTaskArgs removes a projection link without deleting the
// task. For RoleDue the linked event is soft-disabled and
// tasks.due_on is cleared.
type UnscheduleTaskArgs struct {
	WorkspaceID uint32
	TaskID      uint32
	ActorUserID uint32
	Role        DateRole
}

// UnscheduleTask removes a projection event for a task. When Role is
// RoleDue, the single linked event is disabled and the task's due_on
// column is cleared. When Role is RoleScheduled, every time-block
// event on the task is disabled (there is no single "scheduled" link
// by design).
func UnscheduleTask(ctx context.Context, tx TX, args UnscheduleTaskArgs) error {
	if !args.Role.IsValid() {
		return wrapInvariant("role_valid", fmt.Sprintf("unknown role %q", args.Role))
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

	switch args.Role {
	case RoleDue:
		existing, err := findLinkedEvent(ctx, tx, task.id, args.Role)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("itemkit: find linked event: %w", err)
		}
		if err := unlinkEventRow(ctx, tx, existing, args.ActorUserID, "unschedule"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE tasks SET due_on = NULL WHERE id = ?", task.id); err != nil {
			return fmt.Errorf("itemkit: clear task due_on: %w", err)
		}
	case RoleScheduled:
		// The role is part of the named statement rather than a parameter,
		// so this cannot be pointed at the due projection, which has to
		// clear tasks.due_on in the same breath and goes through the branch
		// above instead.
		if err := calendar.New(tx.RawTx()).DisableCalendarEventTimeBlocksByTask(ctx,
			calendar.DisableCalendarEventTimeBlocksByTaskParams{
				WorkspaceID: args.WorkspaceID,
				TaskID:      taskIDParam(task.id),
			}); err != nil {
			return fmt.Errorf("itemkit: disable scheduled events: %w", err)
		}
	}

	return appendItemEvents(ctx, tx, eventbus.ItemUnscheduled, args.WorkspaceID, &args.ActorUserID, &task.id,
		map[string]any{
			"taskPublicId": task.publicID.String(),
			"role":         string(args.Role),
		})
}

// unlinkEventRow soft-disables one event row. Used by UnscheduleTask for
// the due role, and by propagateEventFromTaskDate when the task date is
// cleared. Both reach it with an event they resolved by task link.
func unlinkEventRow(ctx context.Context, tx TX, evt eventRow, actorID uint32, reason string) error {
	// Same named statement every other disable in this package goes
	// through, so the column keeps one write path. Its key is (public_id,
	// calendar_id, workspace_id); each caller resolves evt with
	// findLinkedEvent inside this transaction, so all three are read off
	// the row being withdrawn and identify it and nothing else.
	//
	// affected-rows: not-applicable — this helper is reached only after a
	// caller has found a live link, and each of them already answers a
	// missing one by returning nil. A zero therefore means another writer
	// withdrew the row between that read and this statement, which leaves
	// the state the caller asked for.
	if _, err := calendar.New(tx.RawTx()).DisableCalendarEvent(ctx, calendar.DisableCalendarEventParams{
		PublicID:    evt.publicID,
		CalendarID:  evt.calendarID,
		WorkspaceID: evt.workspaceID,
	}); err != nil {
		return fmt.Errorf("itemkit: soft-disable event: %w", err)
	}
	var taskPtr *uint32
	if evt.taskID.Valid {
		tid := uint32(evt.taskID.Int32) //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
		taskPtr = &tid
	}
	return appendItemEvents(ctx, tx, eventbus.ItemUnscheduled, evt.workspaceID, &actorID, taskPtr,
		map[string]any{
			"eventPublicId": evt.publicID.String(),
			"reason":        reason,
		})
}

// DeleteTask soft-disables a task and every calendar_events row
// linked to it. Symmetric to a cascade: the task goes, its
// projection events go, but unrelated events stay.
func DeleteTask(ctx context.Context, tx TX, workspaceID, taskID, actorID uint32) error {
	disarm, err := armProjectionGuard(ctx, tx)
	if err != nil {
		return err
	}
	defer disarm()

	task, err := findTaskByID(ctx, tx, workspaceID, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // already gone; idempotent
		}
		return fmt.Errorf("itemkit: read task: %w", err)
	}
	if err := calendar.New(tx.RawTx()).DisableCalendarEventsByTask(ctx,
		calendar.DisableCalendarEventsByTaskParams{
			WorkspaceID: workspaceID,
			TaskID:      taskIDParam(task.id),
		}); err != nil {
		return fmt.Errorf("itemkit: soft-disable linked events: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET enabled = FALSE WHERE id = ?`, task.id); err != nil {
		return fmt.Errorf("itemkit: soft-disable task: %w", err)
	}
	return appendItemEvents(ctx, tx, eventbus.ItemDeleted, workspaceID, &actorID, &task.id,
		map[string]any{
			"taskPublicId": task.publicID.String(),
		})
}

// DeleteEvent soft-disables a single calendar_events row, along with
// the occurrence overrides that name it as their series. When the
// event is task-linked with RoleDue, the task's due_on column is
// cleared but the task itself survives.
func DeleteEvent(ctx context.Context, tx TX, workspaceID, eventID, actorID uint32) error {
	disarm, err := armProjectionGuard(ctx, tx)
	if err != nil {
		return err
	}
	defer disarm()

	evt, err := findEventByID(ctx, tx, workspaceID, eventID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("itemkit: read event: %w", err)
	}
	cq := calendar.New(tx.RawTx())
	// The disable goes through the named query rather than an inline
	// statement so that this path and the calendar handlers keep writing
	// the column the same way: a predicate or a column added to
	// DisableCalendarEvent reaches every caller, which an inline copy
	// would silently miss. Its key is (public_id, calendar_id,
	// workspace_id) and it is confined to enabled rows; findEventByID
	// resolved all three off the row it read as live in this same
	// transaction, so the statement lands on exactly that row.
	//
	// affected-rows: not-applicable — the count answers whether this
	// writer's disable won, not whether the event exists, and a delete of
	// something already deleted is the state the caller asked for. The
	// missing-event case is already answered above by returning nil, so a
	// zero here (another writer disabled the row between the read and this
	// statement) must reach the same outcome rather than a not-found.
	if _, err := cq.DisableCalendarEvent(ctx, calendar.DisableCalendarEventParams{
		PublicID:    evt.publicID,
		CalendarID:  evt.calendarID,
		WorkspaceID: workspaceID,
	}); err != nil {
		return fmt.Errorf("itemkit: soft-disable event: %w", err)
	}
	// A series' per-occurrence overrides are separate rows naming this
	// one in recurrence_parent_id. fk_calendar_events_recurrence_parent
	// cascades on DELETE, but the product never deletes a row — a delete
	// is a cleared enabled flag — so the cascade never fires and reaching
	// the children is the writer's job. An override left enabled carries
	// no recurrence rule of its own, which is exactly what the
	// non-recurring range queries select, so the cancelled series comes
	// back as a scatter of standalone events.
	//
	// Rows that are already disabled match nothing, and an event with no
	// overrides answers zero, so this costs one statement on every
	// delete and changes nothing outside a series.
	//
	// affected-rows: not-applicable — the removal being reported is the
	// event's; findEventByID resolved it and the disable above is the write
	// that carries it. This reaches whatever overrides name that event as
	// their parent, and an event that is not a series has none, so zero is
	// the ordinary count rather than something nobody found.
	parentID := sql.NullInt32{Int32: int32(evt.id), Valid: true} //#nosec G115 -- recurrence_parent_id references calendar_events.id (INT UNSIGNED) and holds the same width
	if _, err := cq.DisableCalendarEventOverridesByParent(ctx,
		calendar.DisableCalendarEventOverridesByParentParams{
			WorkspaceID:        workspaceID,
			RecurrenceParentID: parentID,
		}); err != nil {
		return fmt.Errorf("itemkit: disable occurrence overrides: %w", err)
	}
	var taskPtr *uint32
	if evt.taskID.Valid && evt.taskRole.Valid {
		tid := uint32(evt.taskID.Int32) //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
		taskPtr = &tid
		if DateRole(evt.taskRole.String) == RoleDue {
			if _, err := tx.ExecContext(ctx, `UPDATE tasks SET due_on = NULL WHERE id = ?`, tid); err != nil {
				return fmt.Errorf("itemkit: clear task due_on: %w", err)
			}
		}
	}
	return appendItemEvents(ctx, tx, eventbus.ItemUnscheduled, workspaceID, &actorID, taskPtr,
		map[string]any{
			"eventPublicId": evt.publicID.String(),
			"reason":        "event_deleted",
		})
}
