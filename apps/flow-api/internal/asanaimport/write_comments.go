package asanaimport

import (
	"context"
	"database/sql"
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskservice"
)

// writeComments writes each comment in one transaction.
func (w *writer) writeComments(ctx context.Context) error {
	for _, cp := range w.p.Comments {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := &w.res.Projects[cp.Project].Comments
		switch cp.Action {
		case ActionSkip:
			c.Skipped++
		case ActionKeep:
			c.Kept++
		case ActionUnchanged:
			c.Duplicates++
		case ActionCreate, ActionUpdate:
			ref, ok := w.tasks[cp.TaskGID]
			if !ok {
				c.Skipped++ // task not imported
				continue
			}
			if cp.Action == ActionCreate {
				w.createComment(ctx, cp, ref, c)
			} else {
				w.editComment(ctx, cp, ref, c)
			}
		}
	}
	return nil
}

func (w *writer) createComment(ctx context.Context, cp *CommentPlan, ref taskRef, c *Counts) {
	what := "comment " + cp.GID
	author, err := w.userID(cp.Author)
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	err = w.inTx(ctx, "WriteComment", func(ctx context.Context, tx *dbretry.Tx) error {
		written, err := taskservice.WriteComment(ctx, tx, taskservice.WriteArgs{
			Actor: w.actor, TaskID: ref.id, TaskPublicID: ref.pub,
			AuthorUserID: sql.NullInt64{Int64: int64(author), Valid: true},
			Body:         cp.Body, CreatedAt: cp.CreatedAt, SourceUID: SourceUID(cp.GID),
		})
		if err != nil {
			return err
		}
		w.audit(ctx, tx, mutationlog.Mutation{
			AuditAction: "comment.create", ResourceType: "comment", ResourceID: written.PublicID.String(),
			Payload:  map[string]any{"taskId": ref.pub.String(), "commentId": written.PublicID.String()},
			CallSite: "asanaimport.Write (taskservice.WriteComment appended the event)",
		})
		return nil
	})
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	c.Imported++
}

func (w *writer) editComment(ctx context.Context, cp *CommentPlan, ref taskRef, c *Counts) {
	what := "comment " + cp.GID
	row := cp.Row
	if !row.AuthorID.Valid {
		w.fail(&c.Failed, what, errors.New("the stored comment has no human author"))
		return
	}
	err := w.inTx(ctx, "EditComment", func(ctx context.Context, tx *dbretry.Tx) error {
		if err := taskservice.EditComment(ctx, tx, taskservice.EditArgs{
			Actor: w.actor, CommentID: row.ID, CommentPublicID: row.PublicID,
			TaskID: ref.id, TaskPublicID: ref.pub,
			AuthorUserID: uint32(row.AuthorID.Int32), //#nosec G115 -- generated NullInt32 of an INT UNSIGNED user id
			Body:         cp.Body,
		}); err != nil {
			return err
		}
		w.audit(ctx, tx, mutationlog.Mutation{
			AuditAction: "comment.update", ResourceType: "comment", ResourceID: row.PublicID.String(),
			Payload:  map[string]any{"taskId": ref.pub.String(), "commentId": row.PublicID.String()},
			CallSite: "asanaimport.Write (taskservice.EditComment appended the event)",
		})
		return nil
	})
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	c.Updated++
}
