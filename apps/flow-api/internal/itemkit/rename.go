package itemkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/eventbus"
)

// RenameItemArgs describes a title change on either side of the
// task ↔ event link. The caller sets exactly one of TaskID / EventID
// as the origin of the edit; the counterpart side is propagated.
type RenameItemArgs struct {
	WorkspaceID uint32
	ActorUserID uint32
	NewTitle    string

	// Exactly one of these is non-zero.
	TaskID  uint32
	EventID uint32
}

// RenameItem propagates a title change across the linked task and
// all of its linked calendar_events (or, when the origin is an
// event, to the task's linked calendar_events plus the task row).
// Titles stay in lockstep; if that later feels too aggressive, a
// "linked-origin title" shadow column can be introduced without
// altering this API.
func RenameItem(ctx context.Context, tx TX, args RenameItemArgs) error {
	if args.NewTitle == "" {
		return wrapInvariant("title_required", "new title is empty")
	}
	if (args.TaskID == 0) == (args.EventID == 0) {
		return wrapInvariant("rename_origin", "exactly one of TaskID/EventID must be set")
	}

	if args.TaskID != 0 {
		return renameFromTask(ctx, tx, args)
	}
	return renameFromEvent(ctx, tx, args)
}

func renameFromTask(ctx context.Context, tx TX, args RenameItemArgs) error {
	task, err := findTaskByID(ctx, tx, args.WorkspaceID, args.TaskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("itemkit: task %d not found in ws %d", args.TaskID, args.WorkspaceID)
		}
		return fmt.Errorf("itemkit: read task: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET title = ? WHERE id = ?`, args.NewTitle, task.id); err != nil {
		return fmt.Errorf("itemkit: update task title: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE calendar_events SET title = ? WHERE task_id = ? AND enabled = TRUE`,
		args.NewTitle, task.id); err != nil {
		return fmt.Errorf("itemkit: update linked event titles: %w", err)
	}
	return appendItemEvents(ctx, tx, eventbus.ItemRenamed, args.WorkspaceID, &args.ActorUserID, &task.id,
		map[string]any{
			"taskPublicId": task.publicID.String(),
			"newTitle":     args.NewTitle,
			"origin":       "task",
		})
}

func renameFromEvent(ctx context.Context, tx TX, args RenameItemArgs) error {
	evt, err := findEventByID(ctx, tx, args.WorkspaceID, args.EventID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("itemkit: event %d not found in ws %d", args.EventID, args.WorkspaceID)
		}
		return fmt.Errorf("itemkit: read event: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE calendar_events SET title = ? WHERE id = ?`, args.NewTitle, evt.id); err != nil {
		return fmt.Errorf("itemkit: update event title: %w", err)
	}
	var taskPtr *uint32
	if evt.taskID.Valid {
		tid := uint32(evt.taskID.Int32) //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
		taskPtr = &tid
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET title = ? WHERE id = ?`, args.NewTitle, tid); err != nil {
			return fmt.Errorf("itemkit: propagate event rename to task: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE calendar_events SET title = ? WHERE task_id = ? AND enabled = TRUE AND id <> ?`,
			args.NewTitle, tid, evt.id); err != nil {
			return fmt.Errorf("itemkit: fan-out event rename to siblings: %w", err)
		}
	}
	return appendItemEvents(ctx, tx, eventbus.ItemRenamed, args.WorkspaceID, &args.ActorUserID, taskPtr,
		map[string]any{
			"eventPublicId": evt.publicID.String(),
			"newTitle":      args.NewTitle,
			"origin":        "event",
		})
}
