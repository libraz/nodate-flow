package itemkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
)

// RenameItemArgs describes a title change on either side of the
// task ↔ event link. The caller sets exactly one of TaskID / EventID
// as the origin of the edit; the counterpart side is propagated.
type RenameItemArgs struct {
	WorkspaceID uint32
	ActorUserID uint32
	// NewTitle lands in tasks.title on both origins — an event rename
	// propagates to the linked task — so it carries the task title rule
	// even when the edit started on the calendar side. The writes below
	// are raw ExecContext, invisible to anything that derives its write
	// sinks from the generated queries; the type is what reaches them.
	NewTitle taskrules.Title

	// Exactly one of these is non-zero.
	TaskID  uint32
	EventID uint32
}

// RenamedTask is the task text a rename leaves behind, returned so the
// caller can refresh the task's search embedding once its transaction has
// committed. Search is served from stored embeddings, so a title that
// lands without one leaves the task findable only under text it no longer
// has — and the refresh cannot run inside the transaction, which may
// still roll back.
//
// TaskID is zero when the rename touched no task, which is an event that
// projects nothing; there is then nothing to refresh. Description is the
// body the task already carried: a rename does not write it, and the
// embedding is composed from the title and the description together.
type RenamedTask struct {
	TaskID      uint32
	Title       string
	Description string
}

// RenameItem propagates a title change across the linked task and
// all of its linked calendar_events (or, when the origin is an
// event, to the task's linked calendar_events plus the task row).
// Titles stay in lockstep; if that later feels too aggressive, a
// "linked-origin title" shadow column can be introduced without
// altering this API.
//
// The returned [RenamedTask] is what the caller passes to
// embed.RefreshTaskAfterCommit after committing. It is returned rather
// than refreshed here because this function only ever runs inside a
// transaction the caller owns.
func RenameItem(ctx context.Context, tx TX, args RenameItemArgs) (RenamedTask, error) {
	// The zero Title is the one value the type cannot rule out, so the
	// invariant stays: a caller that omits the field entirely still
	// compiles.
	if args.NewTitle.String() == "" {
		return RenamedTask{}, wrapInvariant("title_required", "new title is empty")
	}
	if (args.TaskID == 0) == (args.EventID == 0) {
		return RenamedTask{}, wrapInvariant("rename_origin", "exactly one of TaskID/EventID must be set")
	}

	disarm, err := armProjectionGuard(ctx, tx)
	if err != nil {
		return RenamedTask{}, err
	}
	defer disarm()

	if args.TaskID != 0 {
		return renameFromTask(ctx, tx, args)
	}
	return renameFromEvent(ctx, tx, args)
}

func renameFromTask(ctx context.Context, tx TX, args RenameItemArgs) (RenamedTask, error) {
	task, err := findTaskByID(ctx, tx, args.WorkspaceID, args.TaskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RenamedTask{}, fmt.Errorf("itemkit: task %d not found in ws %d", args.TaskID, args.WorkspaceID)
		}
		return RenamedTask{}, fmt.Errorf("itemkit: read task: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET title = ? WHERE id = ?`, args.NewTitle.String(), task.id); err != nil {
		return RenamedTask{}, fmt.Errorf("itemkit: update task title: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE calendar_events SET title = ? WHERE task_id = ? AND enabled = TRUE`,
		args.NewTitle.String(), task.id); err != nil {
		return RenamedTask{}, fmt.Errorf("itemkit: update linked event titles: %w", err)
	}
	if err := appendItemEvents(ctx, tx, eventbus.ItemRenamed, args.WorkspaceID, &args.ActorUserID, &task.id,
		map[string]any{
			"taskPublicId": task.publicID.String(),
			"newTitle":     args.NewTitle.String(),
			"origin":       "task",
		}); err != nil {
		return RenamedTask{}, err
	}
	return RenamedTask{
		TaskID:      task.id,
		Title:       args.NewTitle.String(),
		Description: task.description.String,
	}, nil
}

func renameFromEvent(ctx context.Context, tx TX, args RenameItemArgs) (RenamedTask, error) {
	evt, err := findEventByID(ctx, tx, args.WorkspaceID, args.EventID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RenamedTask{}, fmt.Errorf("itemkit: event %d not found in ws %d", args.EventID, args.WorkspaceID)
		}
		return RenamedTask{}, fmt.Errorf("itemkit: read event: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE calendar_events SET title = ? WHERE id = ?`, args.NewTitle.String(), evt.id); err != nil {
		return RenamedTask{}, fmt.Errorf("itemkit: update event title: %w", err)
	}
	var taskPtr *uint32
	var renamed RenamedTask
	if evt.taskID.Valid {
		tid := uint32(evt.taskID.Int32) //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
		taskPtr = &tid
		// Read before the propagation writes over the title: what is
		// wanted from the row is the description, which the rename does
		// not touch, and reading it here is the only chance to — the
		// caller refreshes the embedding after its transaction has
		// committed and has no transaction left to read it in.
		//
		// A link pointing at a row this workspace has no enabled task
		// for leaves the refresh with nothing to name. The title write
		// below still runs, matching what it did before, but a task that
		// is disabled or foreign is not in anybody's search results.
		task, terr := findTaskByID(ctx, tx, args.WorkspaceID, tid)
		switch {
		case terr == nil:
			renamed = RenamedTask{
				TaskID:      tid,
				Title:       args.NewTitle.String(),
				Description: task.description.String,
			}
		case !errors.Is(terr, sql.ErrNoRows):
			return RenamedTask{}, fmt.Errorf("itemkit: read linked task: %w", terr)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET title = ? WHERE id = ?`, args.NewTitle.String(), tid); err != nil {
			return RenamedTask{}, fmt.Errorf("itemkit: propagate event rename to task: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE calendar_events SET title = ? WHERE task_id = ? AND enabled = TRUE AND id <> ?`,
			args.NewTitle.String(), tid, evt.id); err != nil {
			return RenamedTask{}, fmt.Errorf("itemkit: fan-out event rename to siblings: %w", err)
		}
	}
	if err := appendItemEvents(ctx, tx, eventbus.ItemRenamed, args.WorkspaceID, &args.ActorUserID, taskPtr,
		map[string]any{
			"eventPublicId": evt.publicID.String(),
			"newTitle":      args.NewTitle.String(),
			"origin":        "event",
		}); err != nil {
		return RenamedTask{}, err
	}
	return renamed, nil
}
