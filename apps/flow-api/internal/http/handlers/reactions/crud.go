package reactions

import (
	"context"
	"database/sql"
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
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
		taskIDInt64 := int64(task.ID)
		if err := dbretry.InTx(ctx, deps.DB, "reactions.Create", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			if _, err := generated.New(tx).CreateReaction(ctx, generated.CreateReactionParams{
				PublicID:    pub,
				WorkspaceID: ws.ID,
				UserID:      actorID,
				TaskID:      taskID,
				CommentID:   sql.NullInt32{},
				Emoji:       in.Body.Emoji,
			}); err != nil {
				return err
			}
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
				EventType:    eventbus.ReactionAdded,
				AuditAction:  "reaction.create",
				ResourceType: "reaction",
				ResourceID:   pub.String(),
				TaskID:       &taskIDInt64,
				Payload:      map[string]any{"reactionId": pub.String(), "emoji": in.Body.Emoji, "taskId": in.ID},
				CallSite:     "reactions.Create",
			})
		}); err != nil {
			// A concurrent identical add loses on uniq_reactions_user_task_emoji
			// and gets the same answer as the pre-check above.
			if handlerutil.IsDuplicateEntry(err) {
				return nil, httpErr(apierrors.WsReactionAlreadyExists)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
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
		// The task itself is not needed here — the reaction is located by
		// its own public id — but its presence is the route's precondition.
		if _, ok := middleware.TaskFromContext(ctx); !ok {
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

		// Scoped to this user's live reactions, so a zero count means there
		// is no reaction here to remove.
		taskIDInt64 := int64(row.TaskID.Int32)
		var missing bool
		if err := dbretry.InTx(ctx, deps.DB, "reactions.Delete", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			rows, err := generated.New(tx).DisableReaction(ctx, generated.DisableReactionParams{
				PublicID: pub,
				UserID:   actorID,
			})
			if err != nil {
				return err
			}
			if missing = rows == 0; missing {
				return nil
			}
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
				EventType:    eventbus.ReactionRemoved,
				AuditAction:  "reaction.delete",
				ResourceType: "reaction",
				ResourceID:   pub.String(),
				TaskID:       &taskIDInt64,
				Payload:      map[string]any{"reactionId": pub.String(), "emoji": row.Emoji},
				CallSite:     "reactions.Delete",
			})
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if missing {
			return nil, httpErr(apierrors.WsReactionNotFound)
		}

		out := &DeleteReactionOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
