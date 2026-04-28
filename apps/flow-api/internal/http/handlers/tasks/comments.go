package tasks

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	nflog "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/log"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/logutil"
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
		if _, err := deps.Queries.AddComment(ctx, generated.AddCommentParams{
			PublicID:    pub,
			WorkspaceID: ws.ID,
			TaskID:      handlerutil.NullInt32From(task.ID),
			AuthorID:    actorID,
			Body:        in.Body.Body,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		taskInternal := int64(task.ID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskCommentAdded,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":    task.PublicID.String(),
				"commentId": pub.String(),
			},
		}); err != nil {
			nflog.LoggerFromContext(ctx).ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "tasks.AddComment"),
				slog.String("event_type", string(eventbus.TaskCommentAdded)),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("task", task.PublicID),
				slog.String("comment_public_id", pub.String()),
			)
		}
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
		AuthorID:          r.AuthorPublicID.String(),
		AuthorDisplayName: r.AuthorDisplayName,
		AuthorAvatarURL:   nullStr(r.AuthorAvatarUrl),
		Body:              r.Body,
		EditedAt:          nullTimeUnix(r.EditedAt),
		UpdatedAt:         nullTimeUnix(r.UpdatedAt),
		CreatedAt:         r.CreatedAt.Unix(),
	}
}

// loadCommentAuthor returns the author internal id for a comment in this
// workspace. ErrNoRows when the comment does not exist or is disabled.
func loadCommentAuthor(ctx context.Context, db *sql.DB, wsID uint32, cid types.PublicID) (uint32, error) {
	const q = `SELECT author_id FROM comments
WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1`
	var uid uint32
	err := db.QueryRowContext(ctx, q, wsID, cid).Scan(&uid)
	return uid, err
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
		author, err := loadCommentAuthor(ctx, deps.DB, ws.ID, cid)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsTaskNotFound, apierrors.InternalUnexpected))
		}
		if author != actorID {
			return nil, httpErr(apierrors.WsTaskAccessDenied)
		}
		if err := deps.Queries.EditComment(ctx, generated.EditCommentParams{
			Body:        in.Body.Body,
			WorkspaceID: ws.ID,
			PublicID:    cid,
			AuthorID:    actorID,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		taskInternal := int64(task.ID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskCommentEdited,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":    task.PublicID.String(),
				"commentId": cid.String(),
			},
		}); err != nil {
			nflog.LoggerFromContext(ctx).ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "tasks.EditComment"),
				slog.String("event_type", string(eventbus.TaskCommentEdited)),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("task", task.PublicID),
				slog.String("comment_public_id", cid.String()),
			)
		}
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
		author, err := loadCommentAuthor(ctx, deps.DB, ws.ID, cid)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsTaskNotFound, apierrors.InternalUnexpected))
		}
		if author != actorID && !ws.Role.AtLeast(middleware.WorkspaceRoleAdmin) {
			return nil, httpErr(apierrors.WsTaskAccessDenied)
		}
		if err := deps.Queries.DeleteComment(ctx, generated.DeleteCommentParams{
			WorkspaceID: ws.ID,
			PublicID:    cid,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		taskInternal := int64(task.ID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskCommentRemoved,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":    task.PublicID.String(),
				"commentId": cid.String(),
			},
		}); err != nil {
			nflog.LoggerFromContext(ctx).ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "tasks.DeleteComment"),
				slog.String("event_type", string(eventbus.TaskCommentRemoved)),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("task", task.PublicID),
				slog.String("comment_public_id", cid.String()),
			)
		}
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
