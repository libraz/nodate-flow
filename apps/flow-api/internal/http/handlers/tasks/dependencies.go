package tasks

import (
	"context"
	"database/sql"
	"errors"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// ListDependencies handles GET /tasks/{id}/dependencies. Returns both
// outgoing edges (this task → other) and incoming edges (other → this).
func ListDependencies(deps Deps) func(context.Context, *ListTaskDependenciesInput) (*ListTaskDependenciesOutput, error) {
	return func(ctx context.Context, _ *ListTaskDependenciesInput) (*ListTaskDependenciesOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		out := &ListTaskDependenciesOutput{}
		out.Body.Outgoing = []TaskDependencyEdge{}
		out.Body.Incoming = []TaskDependencyEdge{}

		outRows, err := deps.Queries.ListDependenciesForTask(ctx, generated.ListDependenciesForTaskParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
			Limit:       200,
			Offset:      0,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		for _, r := range outRows {
			out.Body.Outgoing = append(out.Body.Outgoing, TaskDependencyEdge{
				ID:                    r.PublicID.String(),
				Kind:                  string(r.Kind),
				OtherTaskID:           r.ToTaskPublicID.String(),
				OtherTaskTitle:        r.ToTaskTitle,
				OtherTaskDerivedState: string(r.ToTaskDerivedState),
				CreatedAt:             r.CreatedAt,
			})
		}

		inRows, err := deps.Queries.ListIncomingDependenciesForTask(ctx, generated.ListIncomingDependenciesForTaskParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
			Limit:       200,
			Offset:      0,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		for _, r := range inRows {
			out.Body.Incoming = append(out.Body.Incoming, TaskDependencyEdge{
				ID:                    r.PublicID.String(),
				Kind:                  string(r.Kind),
				OtherTaskID:           r.FromTaskPublicID.String(),
				OtherTaskTitle:        r.FromTaskTitle,
				OtherTaskDerivedState: string(r.FromTaskDerivedState),
				CreatedAt:             r.CreatedAt,
			})
		}
		return out, nil
	}
}

// AddDependency handles POST /tasks/{id}/dependencies.
func AddDependency(deps Deps) func(context.Context, *AddTaskDependencyInput) (*AddTaskDependencyOutput, error) {
	return func(ctx context.Context, in *AddTaskDependencyInput) (*AddTaskDependencyOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		toPub, err := types.Parse(in.Body.ToTaskID)
		if err != nil {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		// Resolve target task internal id within the same workspace.
		const q = `SELECT id FROM tasks WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1`
		var toID uint32
		if err := deps.DB.QueryRowContext(ctx, q, ws.ID, toPub).Scan(&toID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsTaskNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		pub := types.New()
		if _, err := deps.Queries.AddDependency(ctx, generated.AddDependencyParams{
			PublicID:    pub,
			WorkspaceID: ws.ID,
			FromTaskID:  task.ID,
			ToTaskID:    toID,
			Kind:        generated.TaskDependenciesKind(in.Body.Kind),
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		taskInternal := int64(task.ID)
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskDependencyAdded,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":       task.PublicID.String(),
				"dependencyId": pub.String(),
				"toTaskId":     toPub.String(),
				"kind":         in.Body.Kind,
			},
		})
		if aID, aOk := middleware.ActorFromContext(ctx); aOk {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "task.dependency.add",
				ActorID:      aID,
				WorkspaceID:  ws.ID,
				ResourceType: "task_dependency",
				ResourceID:   pub.String(),
			})
		}
		autoEvaluateConstraints(ctx, deps, ws.ID, task.ID)
		return &AddTaskDependencyOutput{Body: TaskDependency{
			ID:         pub.String(),
			Kind:       in.Body.Kind,
			FromTaskID: task.PublicID.String(),
			ToTaskID:   toPub.String(),
		}}, nil
	}
}

// RemoveDependency handles DELETE /tasks/{id}/dependencies/{depId}.
func RemoveDependency(deps Deps) func(context.Context, *RemoveTaskDependencyInput) (*RemoveTaskDependencyOutput, error) {
	return func(ctx context.Context, in *RemoveTaskDependencyInput) (*RemoveTaskDependencyOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		depID, err := types.Parse(in.DepID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		if err := deps.Queries.DeleteDependency(ctx, generated.DeleteDependencyParams{
			WorkspaceID: ws.ID,
			PublicID:    depID,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		taskInternal := int64(task.ID)
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskDependencyRemoved,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":       task.PublicID.String(),
				"dependencyId": depID.String(),
			},
		})
		if aID, aOk := middleware.ActorFromContext(ctx); aOk {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "task.dependency.remove",
				ActorID:      aID,
				WorkspaceID:  ws.ID,
				ResourceType: "task_dependency",
				ResourceID:   depID.String(),
			})
		}
		autoEvaluateConstraints(ctx, deps, ws.ID, task.ID)
		out := &RemoveTaskDependencyOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
