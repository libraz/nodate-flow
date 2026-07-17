package reactions

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/logutil"
)

// Create handles POST /tasks/{id}/reactions.
func Create(deps Deps) func(context.Context, *CreateReactionInput) (*CreateReactionOutput, error) {
	return func(ctx context.Context, in *CreateReactionInput) (*CreateReactionOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}

		taskID := sql.NullInt32{Int32: int32(task.ID), Valid: true} //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments

		// Check for duplicate reaction (same user, same task, same emoji).
		_, dupErr := deps.Queries.FindExistingReaction(ctx, generated.FindExistingReactionParams{
			UserID: actorID,
			TaskID: taskID,
			Emoji:  in.Body.Emoji,
		})
		if dupErr == nil {
			return nil, httpErr(apierrors.WsReactionAlreadyExists)
		}
		if !errors.Is(dupErr, sql.ErrNoRows) {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		pub := types.New()
		if _, err := deps.Queries.CreateReaction(ctx, generated.CreateReactionParams{
			PublicID:    pub,
			WorkspaceID: ws.ID,
			UserID:      actorID,
			TaskID:      taskID,
			CommentID:   sql.NullInt32{},
			Emoji:       in.Body.Emoji,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		taskIDInt64 := int64(task.ID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.ReactionAdded,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskIDInt64,
			Payload: map[string]any{
				"reactionId": pub.String(),
				"emoji":      in.Body.Emoji,
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "reactions.Create"),
				slog.String("event_type", string(eventbus.ReactionAdded)),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("task", task.PublicID),
				slog.String("reaction_public_id", pub.String()),
			)
		}

		if deps.Audit != nil {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "reaction.create",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "reaction",
				ResourceID:   pub.String(),
				Metadata:     map[string]any{"emoji": in.Body.Emoji, "taskId": in.ID},
			})
		}

		// Fetch the actor's display name for the response.
		row, err := deps.Queries.FindReactionByPublicId(ctx, pub)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// To return the user display name we look up the actor user row.
		// Since FindReactionByPublicId does not JOIN users, we use the
		// list query filtered to just this task and find the matching
		// row. A generous bound caps the scan while still covering any
		// realistic per-task reaction count.
		reactions, err := deps.Queries.ListReactionsForTask(ctx, generated.ListReactionsForTaskParams{
			TaskID:      taskID,
			WorkspaceID: ws.ID,
			Limit:       handlerutil.MaxListLimit,
			Offset:      0,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		for _, r := range reactions {
			if r.PublicID == row.PublicID {
				return &CreateReactionOutput{Body: mapTaskReactionRow(r)}, nil
			}
		}

		// Fallback: return without display name.
		return &CreateReactionOutput{Body: Reaction{
			ID:        pub.String(),
			Emoji:     in.Body.Emoji,
			UserID:    "", // cannot resolve without JOIN
			CreatedAt: row.CreatedAt.Unix(),
		}}, nil
	}
}

// ListForTask handles GET /tasks/{id}/reactions.
func ListForTask(deps Deps) func(context.Context, *ListReactionsInput) (*ListReactionsOutput, error) {
	return func(ctx context.Context, in *ListReactionsInput) (*ListReactionsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		page := handlerutil.Bind(in.Limit, in.Offset, handlerutil.DefaultListLimit, handlerutil.MaxListLimit)
		taskID := sql.NullInt32{Int32: int32(task.ID), Valid: true} //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		rows, err := deps.Queries.ListReactionsForTask(ctx, generated.ListReactionsForTaskParams{
			TaskID:      taskID,
			WorkspaceID: ws.ID,
			Limit:       page.Limit,
			Offset:      page.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListReactionsOutput{}
		out.Body.Reactions = make([]Reaction, 0, len(rows))
		for _, r := range rows {
			out.Body.Reactions = append(out.Body.Reactions, mapTaskReactionRow(r))
		}
		if len(rows) > 0 {
			out.Body.Total = handlerutil.TotalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// Delete handles DELETE /tasks/{id}/reactions/{reactionId}.
func Delete(deps Deps) func(context.Context, *DeleteReactionInput) (*DeleteReactionOutput, error) {
	return func(ctx context.Context, in *DeleteReactionInput) (*DeleteReactionOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}

		pub, err := types.Parse(in.ReactionID)
		if err != nil {
			return nil, httpErr(apierrors.WsReactionNotFound)
		}

		// Verify the reaction exists and belongs to this user.
		row, err := deps.Queries.FindReactionByPublicId(ctx, pub)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsReactionNotFound, apierrors.InternalUnexpected))
		}
		if row.UserID != actorID {
			return nil, httpErr(apierrors.WsReactionNotFound)
		}

		if err := deps.Queries.DisableReaction(ctx, generated.DisableReactionParams{
			PublicID: pub,
			UserID:   actorID,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		taskIDInt64 := int64(row.TaskID.Int32)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.ReactionRemoved,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskIDInt64,
			Payload: map[string]any{
				"reactionId": pub.String(),
				"emoji":      row.Emoji,
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "reactions.Delete"),
				slog.String("event_type", string(eventbus.ReactionRemoved)),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("task", task.PublicID),
				slog.String("reaction_public_id", pub.String()),
			)
		}

		if deps.Audit != nil {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "reaction.delete",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "reaction",
				ResourceID:   pub.String(),
			})
		}

		out := &DeleteReactionOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
