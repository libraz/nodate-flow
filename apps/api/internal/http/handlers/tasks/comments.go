package tasks

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
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
			TaskID:      task.ID,
			AuthorID:    actorID,
			Body:        in.Body.Body,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		taskInternal := int64(task.ID)
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskCommentAdded,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":    task.PublicID.String(),
				"commentId": pub.String(),
			},
		})
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
			CreatedAt: time.Now(),
		}}, nil
	}
}

// ListComments handles GET /tasks/{id}/comments.
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
		rows, err := deps.Queries.ListCommentsForTask(ctx, generated.ListCommentsForTaskParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListTaskCommentsOutput{}
		out.Body.Comments = make([]TaskComment, 0, len(rows))
		for _, r := range rows {
			out.Body.Comments = append(out.Body.Comments, rowToComment(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
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
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsTaskNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
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
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskCommentEdited,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":    task.PublicID.String(),
				"commentId": cid.String(),
			},
		})
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
			EditedAt: timePtr(time.Now()),
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
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsTaskNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
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
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskCommentRemoved,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":    task.PublicID.String(),
				"commentId": cid.String(),
			},
		})
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
