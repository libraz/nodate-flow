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
		if _, err := tx.ExecContext(ctx,
			`UPDATE calendar_events SET enabled = FALSE
			 WHERE task_id = ? AND task_role = 'scheduled' AND enabled = TRUE`, task.id); err != nil {
			return fmt.Errorf("itemkit: disable scheduled events: %w", err)
		}
	}

	return appendItemEvents(ctx, tx, eventbus.ItemUnscheduled, args.WorkspaceID, &args.ActorUserID, &task.id,
		map[string]any{
			"taskPublicId": task.publicID.String(),
			"role":         string(args.Role),
		})
}

// unlinkEventRow soft-disables one event row. Used by UnscheduleTask,
// DeleteEvent, and propagateEventFromTaskDate when the task date is
// cleared.
func unlinkEventRow(ctx context.Context, tx TX, evt eventRow, actorID uint32, reason string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE calendar_events SET enabled = FALSE WHERE id = ?`, evt.id); err != nil {
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
	if _, err := tx.ExecContext(ctx,
		`UPDATE calendar_events SET enabled = FALSE WHERE task_id = ? AND enabled = TRUE`, task.id); err != nil {
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
	if _, err := tx.ExecContext(ctx, `UPDATE calendar_events SET enabled = FALSE WHERE id = ?`, evt.id); err != nil {
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
	if _, err := calendar.New(tx.RawTx()).DisableCalendarEventOverridesByParent(ctx,
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
