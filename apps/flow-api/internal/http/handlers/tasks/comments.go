package tasks

import (
	"context"
	"database/sql"
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/operation"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskservice"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// AddComment handles POST /tasks/{id}/comments.
func AddComment(deps Deps) func(context.Context, *AddTaskCommentInput) (*AddTaskCommentOutput, error) {
	return func(ctx context.Context, in *AddTaskCommentInput) (*AddTaskCommentOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskAccessDenied)
		}
		taskPub := types.FromUUID(task.PublicID)
		actor := mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}
		var (
			result  TaskComment
			written taskservice.WrittenComment
			wrote   bool
		)
		idempotent := in.IdempotencyKey != ""
		// One commit boundary for the body, the mentions it names and the
		// event describing it: a
		// mention row that lands without its comment, or a comment that
		// lands without its mentions, is a thread that says one thing and
		// notifies another.
		//
		// Retry on transient FK deadlocks: comments inherits FK locks
		// on tasks/workspaces/users via its FKs and races with the
		// task transition / fan-out paths under heavy parallel load.
		err := dbretry.InTx(ctx, deps.DB, "tasks.AddComment", taskMutationTxOptions(in.IdempotencyKey), func(ctx context.Context, tx *dbretry.Tx) error {
			result = TaskComment{}
			wrote = false
			qtx := deps.Queries.WithTx(tx.RawTx())
			var receipt operation.Receipt
			if idempotent {
				cached := TaskComment{}
				var replayed bool
				var err error
				receipt, replayed, err = lockTaskMutation(ctx, tx, ws, task, actorID,
					execution.CapabilityAddComment, "task.comment.add", in.IdempotencyKey, in.Body, &cached)
				if err != nil {
					return err
				}
				if replayed {
					commentID, err := types.Parse(cached.ID)
					if err != nil {
						return apierrors.New(apierrors.InternalUnexpected)
					}
					var enabled int
					err = tx.RawTx().QueryRowContext(ctx,
						`SELECT 1 FROM comments
WHERE workspace_id = ? AND task_id = ? AND public_id = ? AND enabled = TRUE
LIMIT 1`,
						ws.ID, task.ID, commentID,
					).Scan(&enabled)
					if errors.Is(err, sql.ErrNoRows) {
						return apierrors.New(apierrors.WsCommentNotFound)
					}
					if err != nil {
						return err
					}
					result = cached
					return nil
				}
			}
			var e error
			written, e = taskservice.WriteComment(ctx, tx, taskservice.WriteArgs{
				Actor:        actor,
				TaskID:       task.ID,
				TaskPublicID: taskPub,
				AuthorUserID: sql.NullInt64{Int64: int64(actorID), Valid: true},
				Body:         in.Body.Body,
			})
			if e != nil {
				return e
			}
			wrote = true
			if idempotent {
				createdAt, err := qtx.GetCommentCreatedAt(ctx, generated.GetCommentCreatedAtParams{
					WorkspaceID: ws.ID,
					PublicID:    written.PublicID,
				})
				if err != nil {
					return err
				}
				result = TaskComment{ID: written.PublicID.String(), Body: in.Body.Body, CreatedAt: createdAt.Unix()}
				return completeTaskMutation(ctx, tx, receipt, result)
			}
			return nil
		})
		if err != nil {
			if idempotent {
				return nil, taskMutationHTTPError(err)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if wrote {
			deps.Mutations.RecordTxAudit(ctx, actor, mutationlog.Mutation{
				AuditAction:  "comment.create",
				ResourceType: "comment",
				ResourceID:   written.PublicID.String(),
				Payload:      map[string]any{"taskId": taskPub.String(), "commentId": written.PublicID.String()},
				CallSite:     "tasks.AddComment (taskservice.WriteComment appended the event)",
			})
		}
		if !idempotent {
			result = TaskComment{
				ID:        written.PublicID.String(),
				Body:      in.Body.Body,
				CreatedAt: handlerutil.NowUnix(),
			}
		}
		return &AddTaskCommentOutput{Body: result}, nil
	}
}

// ListComments handles GET /tasks/{id}/comments.
//
// Pagination: when `cursor` is non-empty the keyset path runs
// (ListCommentsForTaskKeyset, ORDER BY created_at DESC) and emits
// `nextCursor`; otherwise the OFFSET path runs unchanged
// (ORDER BY created_at ASC). The two paths have opposite chronological
// direction by design — callers that want oldest-first must keep using
// OFFSET, callers that want newest-first should use the cursor path.
func ListComments(deps Deps) func(context.Context, *ListTaskCommentsInput) (*ListTaskCommentsOutput, error) {
	return func(ctx context.Context, in *ListTaskCommentsInput) (*ListTaskCommentsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		out := &ListTaskCommentsOutput{}

		if in.Cursor != "" {
			cursorAt, cursorPID, derr := handlerutil.DecodeCursor(in.Cursor)
			if derr != nil {
				return nil, httpErr(apierrors.ValidationQueryFieldInvalid)
			}
			pid := types.FromUUID(task.PublicID)
			rows, qerr := deps.Queries.ListCommentsForTaskKeyset(ctx, generated.ListCommentsForTaskKeysetParams{
				WorkspaceID:     ws.ID,
				TaskPublicID:    pid[:],
				CursorCreatedAt: sql.NullTime{Time: cursorAt, Valid: !cursorAt.IsZero()},
				CursorPublicID:  cursorPID,
				Limit:           limit + 1,
			})
			if qerr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			hasMore := int32(len(rows)) > limit //#nosec G115 -- rows length capped at limit+1 with limit validated to maximum:200
			if hasMore {
				rows = rows[:limit]
			}
			out.Body.Comments = make([]TaskComment, 0, len(rows))
			for _, r := range rows {
				out.Body.Comments = append(out.Body.Comments, rowToCommentKeyset(r))
			}
			if hasMore {
				last := rows[len(rows)-1]
				nc := handlerutil.EncodeCursor(last.CreatedAt, last.PublicID)
				out.Body.NextCursor = &nc
			}
			return out, nil
		}

		pid := types.FromUUID(task.PublicID)
		rows, err := deps.Queries.ListCommentsForTask(ctx, generated.ListCommentsForTaskParams{
			WorkspaceID:  ws.ID,
			TaskPublicID: pid[:],
			Limit:        limit,
			Offset:       in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out.Body.Comments = make([]TaskComment, 0, len(rows))
		for _, r := range rows {
			out.Body.Comments = append(out.Body.Comments, rowToComment(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
			if int64(in.Offset+limit) < out.Body.Total {
				// NOTE: the OFFSET path orders comments ASC; the keyset
				// path orders DESC. Bridging from OFFSET → keyset
				// inverts direction, so callers that genuinely want
				// chronological order must stay on OFFSET. The cursor
				// is still emitted so newest-first consumers can opt in.
				last := rows[len(rows)-1]
				nc := handlerutil.EncodeCursor(last.CreatedAt, last.PublicID)
				out.Body.NextCursor = &nc
			}
		}
		return out, nil
	}
}

// rowToCommentKeyset is the keyset twin of rowToComment — same DTO, no
// Total column on the source row.
func rowToCommentKeyset(r generated.ListCommentsForTaskKeysetRow) TaskComment {
	return TaskComment{
		ID:                r.PublicID.String(),
		AuthorID:          publicIDOrEmpty(r.AuthorPublicID),
		AuthorKind:        string(r.AuthorKind),
		AuthorAgentID:     commentAuthorAgentID(r.AuthorAgentPublicID),
		AuthorDisplayName: r.AuthorDisplayName,
		AuthorAvatarURL:   handlerutil.AvatarURL(r.AuthorAvatarUrl, r.AuthorAvatarStorageObjectID, r.AuthorPublicID),
		Body:              r.Body,
		EditedAt:          nullTimeUnix(r.EditedAt),
		UpdatedAt:         nullTimeUnix(r.UpdatedAt),
		CreatedAt:         r.CreatedAt.Unix(),
	}
}

// loadCommentForWrite returns the internal id and nullable human-author
// internal id for a comment in this workspace. Agent comments have no human
// author id. ErrNoRows when the comment does not exist or is disabled.
//
// The internal id is read here rather than at each caller because both
// writers need it for the same thing: the mention sync addresses a comment
// by its internal id, and the public id the request carries is the only
// name the transport has.
func loadCommentForWrite(ctx context.Context, db *sql.DB, wsID uint32, cid types.PublicID) (commentID uint32, authorID sql.NullInt32, err error) {
	const q = `SELECT id, author_id FROM comments
WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1`
	err = db.QueryRowContext(ctx, q, wsID, cid).Scan(&commentID, &authorID)
	return commentID, authorID, err
}

// EditComment handles PATCH /tasks/{id}/comments/{cid}. Author only.
func EditComment(deps Deps) func(context.Context, *EditTaskCommentInput) (*EditTaskCommentOutput, error) {
	return func(ctx context.Context, in *EditTaskCommentInput) (*EditTaskCommentOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskAccessDenied)
		}
		cid, err := types.Parse(in.CID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		commentID, author, err := loadCommentForWrite(ctx, deps.DB, ws.ID, cid)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsTaskNotFound, apierrors.InternalUnexpected))
		}
		if !author.Valid || uint32(author.Int32) != actorID { //#nosec G115 -- users.id is INT UNSIGNED and fits uint32 within realistic deployments
			return nil, httpErr(apierrors.WsTaskAccessDenied)
		}
		// The body, the mentions it names and the event share a commit
		// boundary: a clear that committed without its re-insert would drop
		// every mention the edited comment still makes.
		actor := mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}
		taskPub := types.FromUUID(task.PublicID)
		if err := dbretry.InTx(ctx, deps.DB, "tasks.EditComment", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			return taskservice.EditComment(ctx, tx, taskservice.EditArgs{
				Actor:           actor,
				CommentID:       commentID,
				CommentPublicID: cid,
				TaskID:          task.ID,
				TaskPublicID:    taskPub,
				AuthorUserID:    actorID,
				Body:            in.Body.Body,
			})
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		deps.Mutations.RecordTxAudit(ctx, actor, mutationlog.Mutation{
			AuditAction:  "comment.update",
			ResourceType: "comment",
			ResourceID:   cid.String(),
			Payload:      map[string]any{"taskId": taskPub.String(), "commentId": cid.String()},
			CallSite:     "tasks.EditComment (taskservice.EditComment appended the event)",
		})
		return &EditTaskCommentOutput{Body: TaskComment{
			ID:       cid.String(),
			Body:     in.Body.Body,
			EditedAt: int64Ptr(handlerutil.NowUnix()),
		}}, nil
	}
}

// DeleteComment handles DELETE /tasks/{id}/comments/{cid}.
// Allowed for the comment author or any workspace admin/owner.
func DeleteComment(deps Deps) func(context.Context, *DeleteTaskCommentInput) (*DeleteTaskCommentOutput, error) {
	return func(ctx context.Context, in *DeleteTaskCommentInput) (*DeleteTaskCommentOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskAccessDenied)
		}
		cid, err := types.Parse(in.CID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		commentID, author, err := loadCommentForWrite(ctx, deps.DB, ws.ID, cid)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsTaskNotFound, apierrors.InternalUnexpected))
		}
		isAuthor := author.Valid && uint32(author.Int32) == actorID //#nosec G115 -- users.id is INT UNSIGNED and fits uint32 within realistic deployments
		if !isAuthor && !ws.Role.AtLeast(middleware.WorkspaceRoleAdmin) {
			return nil, httpErr(apierrors.WsTaskAccessDenied)
		}
		actor := mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}
		taskPub := types.FromUUID(task.PublicID)
		var gone bool
		if err := dbretry.InTx(ctx, deps.DB, "tasks.DeleteComment", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			gone = false
			// Only a still-live comment matches, so a miss means a concurrent
			// delete won and this request removed nothing.
			err := taskservice.DisableComment(ctx, tx, taskservice.DisableArgs{
				Actor:           actor,
				CommentID:       commentID,
				CommentPublicID: cid,
				TaskID:          task.ID,
				TaskPublicID:    taskPub,
			})
			if errors.Is(err, taskservice.ErrCommentNotFound) {
				gone = true
				return nil
			}
			return err
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if gone {
			return nil, httpErr(apierrors.WsCommentNotFound)
		}
		deps.Mutations.RecordTxAudit(ctx, actor, mutationlog.Mutation{
			AuditAction:  "comment.delete",
			ResourceType: "comment",
			ResourceID:   cid.String(),
			Payload:      map[string]any{"taskId": taskPub.String(), "commentId": cid.String()},
			CallSite:     "tasks.DeleteComment (taskservice.DisableComment appended the event)",
		})
		out := &DeleteTaskCommentOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
