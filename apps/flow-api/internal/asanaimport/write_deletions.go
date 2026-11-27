package asanaimport

import (
	"context"
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskservice"
)

// writeDeletions applies a sync's task and comment removals.
func (w *writer) writeDeletions(ctx context.Context) error {
	for _, d := range w.p.TaskDeletions {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := &w.res.Projects[d.Project].Tasks
		switch {
		case d.Moved:
			// The old row goes only once the new placement holds a row.
			if ref, ok := w.tasks[d.Row.GID]; !ok || ref.id == d.Row.ID {
				c.Kept++
				continue
			}
			if w.deleteTask(ctx, d.Row, c) {
				c.Moved++
			}
		case d.Delete:
			if w.deleteTask(ctx, d.Row, c) {
				c.Deleted++
			}
		case d.Probed:
			c.Kept++
		}
	}
	for _, d := range w.p.CommentDeletions {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := &w.res.Projects[d.Project].Comments
		switch {
		case d.Delete:
			w.deleteComment(ctx, d, c)
		case d.Probed:
			c.Kept++
		}
	}
	return nil
}

func (w *writer) deleteTask(ctx context.Context, row *ExistingTask, c *Counts) bool {
	err := w.inTx(ctx, "DeleteTask", func(ctx context.Context, tx *dbretry.Tx) error {
		deleted, err := itemkit.DeleteTask(ctx, tx, w.p.Workspace.ID, row.ID, w.p.Importer.ID)
		if err != nil {
			return err
		}
		// A row already gone changed nothing and records nothing.
		if deleted {
			w.audit(ctx, tx, mutationlog.Mutation{
				AuditAction: "task.delete", ResourceType: "task", ResourceID: row.PublicID.String(),
				CallSite: "asanaimport.Write (itemkit.DeleteTask appended the event)",
			})
		}
		return nil
	})
	if err != nil {
		w.fail(&c.Failed, "task "+row.GID, err)
		return false
	}
	return true
}

func (w *writer) deleteComment(ctx context.Context, d *CommentDeletion, c *Counts) {
	ref := w.tasks[d.TaskGID]
	row := d.Row
	err := w.inTx(ctx, "DeleteComment", func(ctx context.Context, tx *dbretry.Tx) error {
		if err := taskservice.DisableComment(ctx, tx, taskservice.DisableArgs{
			Actor: w.actor, CommentID: row.ID, CommentPublicID: row.PublicID, TaskID: ref.id, TaskPublicID: ref.pub,
		}); err != nil {
			return err
		}
		w.audit(ctx, tx, mutationlog.Mutation{
			AuditAction: "comment.delete", ResourceType: "comment", ResourceID: row.PublicID.String(),
			Payload:  map[string]any{"taskId": ref.pub.String(), "commentId": row.PublicID.String()},
			CallSite: "asanaimport.Write (taskservice.DisableComment appended the event)",
		})
		return nil
	})
	if err != nil && !errors.Is(err, taskservice.ErrCommentNotFound) {
		w.fail(&c.Failed, "comment "+row.GID, err)
		return
	}
	c.Deleted++
}
