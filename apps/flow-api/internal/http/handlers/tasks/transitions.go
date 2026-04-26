package tasks

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/taskstate"
)

// Transition handles POST /tasks/{id}/transitions. It applies a single
// state machine step, persists the new derived_state, and appends a
// `task.transition.<name>` event in the same transaction.
//
// All of that work is delegated to [taskstate.ApplyTransitionTx], the
// single canonical write path for tasks.derived_state. The MCP tool and
// the auto-action executor go through the same helper.
func Transition(deps Deps) func(context.Context, *TransitionTaskInput) (*TransitionTaskOutput, error) {
	return func(ctx context.Context, in *TransitionTaskInput) (*TransitionTaskOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		// Start the transaction first; the helper acquires the row lock
		// inside it so concurrent transition requests serialize.
		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer func() { _ = tx.Rollback() }()

		extra := map[string]any{}
		if in.Body.OccurredAt != 0 {
			extra["occurredAt"] = in.Body.OccurredAt
		}

		result, spec, _ := taskstate.ApplyTransitionTx(ctx, tx, taskstate.ApplyParams{
			WorkspaceID:  ws.ID,
			TaskID:       task.ID,
			PublicID:     types.FromUUID(task.PublicID),
			Transition:   in.Body.Transition,
			ActorUserID:  actorPtr(ctx),
			Reason:       in.Body.Reason,
			Via:          "api",
			ExtraPayload: extra,
		})
		if spec != nil {
			return nil, httpErr(spec)
		}

		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if aID, aOk := middleware.ActorFromContext(ctx); aOk {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "task.transition",
				ActorID:      aID,
				WorkspaceID:  ws.ID,
				ResourceType: "task",
				ResourceID:   task.PublicID.String(),
				Metadata: map[string]any{
					"transition": in.Body.Transition,
					"fromState":  string(result.FromState),
					"toState":    string(result.ToState),
				},
			})
		}

		autoEvaluateConstraints(ctx, deps, ws.ID, task.ID)
		row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &TransitionTaskOutput{Body: rowToTaskFromFind(row)}, nil
	}
}
