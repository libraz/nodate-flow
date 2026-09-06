package tasks

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
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
		pub := types.New()
		// One commit boundary for the body and the mentions it names: a
		// mention row that lands without its comment, or a comment that
		// lands without its mentions, is a thread that says one thing and
		// notifies another.
		//
		// Retry on transient FK deadlocks: comments inherits FK locks
		// on tasks/workspaces/users via its FKs and races with the
		// task transition / fan-out paths under heavy parallel load.
		if err := dbretry.InTx(ctx, deps.DB, "tasks.AddComment", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			qtx := deps.Queries.WithTx(tx.RawTx())
			id, e := qtx.AddComment(ctx, generated.AddCommentParams{
				PublicID:    pub,
				WorkspaceID: ws.ID,
				TaskID:      handlerutil.NullInt32From(task.ID),
				AuthorID:    actorID,
				Body:        in.Body.Body,
			})
			if e != nil {
				return e
			}
			return syncCommentMentions(ctx, tx, ws.ID, task, uint32(id), pub, actorPtr(ctx), in.Body.Body) //#nosec G115 -- LastInsertId for comments.id (INT UNSIGNED), fits uint32 within realistic deployments
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		taskInternal := int64(task.ID)
		eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(deps.DB), eventbus.Event{
			Type:        eventbus.TaskCommentAdded,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":    task.PublicID.String(),
				"commentId": pub.String(),
			},
		}, "tasks.AddComment")
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "comment.create",
			ActorID:      actorID,
			WorkspaceID:  ws.ID,
			ResourceType: "comment",
			ResourceID:   pub.String(),
		})
		return &AddTaskCommentOutput{Body: TaskComment{
			ID:        pub.String(),
			Body:      in.Body.Body,
			CreatedAt: handlerutil.NowUnix(),
		}}, nil
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
		AuthorDisplayName: bylineDisplayName(r.AuthorDisplayName),
		AuthorAvatarURL:   nullStr(r.AuthorAvatarUrl),
		Body:              r.Body,
		EditedAt:          nullTimeUnix(r.EditedAt),
		UpdatedAt:         nullTimeUnix(r.UpdatedAt),
		CreatedAt:         r.CreatedAt.Unix(),
	}
}

// loadCommentForWrite returns the internal id and the author internal id
// for a comment in this workspace. ErrNoRows when the comment does not
// exist or is disabled.
//
// The internal id is read here rather than at each caller because both
// writers need it for the same thing: the mention sync addresses a comment
// by its internal id, and the public id the request carries is the only
// name the transport has.
func loadCommentForWrite(ctx context.Context, db *sql.DB, wsID uint32, cid types.PublicID) (commentID, authorID uint32, err error) {
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
		if author != actorID {
			return nil, httpErr(apierrors.WsTaskAccessDenied)
		}
		// The body and the mentions it names share a commit boundary: a
		// clear that committed without its re-insert would drop every
		// mention the edited comment still makes.
		if err := dbretry.InTx(ctx, deps.DB, "tasks.EditComment", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			qtx := deps.Queries.WithTx(tx.RawTx())
			// Not an existence check: re-submitting the current body changes
			// nothing and MySQL counts zero. Authorship, and with it the
			// comment's existence, is established just above.
			if _, e := qtx.EditComment(ctx, generated.EditCommentParams{
				Body:        in.Body.Body,
				WorkspaceID: ws.ID,
				PublicID:    cid,
				AuthorID:    actorID,
			}); e != nil {
				return e
			}
			return syncCommentMentions(ctx, tx, ws.ID, task, commentID, cid, actorPtr(ctx), in.Body.Body)
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		taskInternal := int64(task.ID)
		eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(deps.DB), eventbus.Event{
			Type:        eventbus.TaskCommentEdited,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":    task.PublicID.String(),
				"commentId": cid.String(),
			},
		}, "tasks.EditComment")
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "comment.update",
			ActorID:      actorID,
			WorkspaceID:  ws.ID,
			ResourceType: "comment",
			ResourceID:   cid.String(),
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
		if author != actorID && !ws.Role.AtLeast(middleware.WorkspaceRoleAdmin) {
			return nil, httpErr(apierrors.WsTaskAccessDenied)
		}
		var gone bool
		// The delete is a soft one, so no foreign key cascade reaches the
		// mention rows. Clearing them here, on the same commit boundary, is
		// what stops a comment nobody can read from going on naming people.
		if err := dbretry.InTx(ctx, deps.DB, "tasks.DeleteComment", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			gone = false
			qtx := deps.Queries.WithTx(tx.RawTx())
			// Only matches a comment that is still live, so zero rows means a
			// concurrent delete won and this request removed nothing.
			rows, e := qtx.DeleteComment(ctx, generated.DeleteCommentParams{
				WorkspaceID: ws.ID,
				PublicID:    cid,
			})
			if e != nil {
				return e
			}
			if rows == 0 {
				gone = true
				return nil
			}
			return syncCommentMentions(ctx, tx, ws.ID, task, commentID, cid, actorPtr(ctx), "")
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if gone {
			return nil, httpErr(apierrors.WsCommentNotFound)
		}
		taskInternal := int64(task.ID)
		eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(deps.DB), eventbus.Event{
			Type:        eventbus.TaskCommentRemoved,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":    task.PublicID.String(),
				"commentId": cid.String(),
			},
		}, "tasks.DeleteComment")
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "comment.delete",
			ActorID:      actorID,
			WorkspaceID:  ws.ID,
			ResourceType: "comment",
			ResourceID:   cid.String(),
		})
		out := &DeleteTaskCommentOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
