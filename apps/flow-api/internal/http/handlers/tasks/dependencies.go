package tasks

import (
	"context"
	stderrors "errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskdeps"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// dependencyEdgeClosesCycle is the walk [taskdeps.Add] applies before
// inserting an edge. Kept as a package-local alias so the reachability
// cases stay covered by this package's table test while the
// implementation has a single home.
var dependencyEdgeClosesCycle = taskdeps.ClosesCycle

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
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		out := &ListTaskDependenciesOutput{}
		out.Body.Outgoing = []TaskDependencyEdge{}
		out.Body.Incoming = []TaskDependencyEdge{}

		// Reaching this route means the actor may see the task in the
		// path. Each edge names a second task and carries its title, so
		// the far end is filtered on its own visibility; without it an
		// edge into a private task discloses that task's title to anyone
		// who can read the task it points at.
		vis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole(ws.Role))
		outRows, err := deps.Queries.ListDependenciesForTask(ctx, generated.ListDependenciesForTaskParams{
			WorkspaceID:      ws.ID,
			FromTaskPublicID: types.FromUUID(task.PublicID),
			IsElevated:       vis.IsElevated,
			ActorUserID:      vis.ActorUserID,
			ActorUserID_2:    vis.ActorUserID,
			ActorUserID_3:    vis.ActorUserID,
			Limit:            200,
			Offset:           0,
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
				CreatedAt:             r.CreatedAt.Unix(),
			})
		}

		inRows, err := deps.Queries.ListIncomingDependenciesForTask(ctx, generated.ListIncomingDependenciesForTaskParams{
			WorkspaceID:    ws.ID,
			ToTaskPublicID: types.FromUUID(task.PublicID),
			IsElevated:     vis.IsElevated,
			ActorUserID:    vis.ActorUserID,
			ActorUserID_2:  vis.ActorUserID,
			ActorUserID_3:  vis.ActorUserID,
			Limit:          200,
			Offset:         0,
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
				CreatedAt:             r.CreatedAt.Unix(),
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
		// Resolve the target task's internal id within the same
		// workspace. This is also the existence check: a public id from
		// another tenant finds nothing and answers 404.
		const q = `SELECT id FROM tasks WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1`
		var toID uint32
		if err := deps.DB.QueryRowContext(ctx, q, ws.ID, toPub).Scan(&toID); err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsTaskNotFound, apierrors.InternalUnexpected))
		}
		// The lock / cycle-check / insert / append sequence lives in
		// taskdeps so every writer of an edge gets it; see that package
		// for why the lock has to cover the whole workspace edge set.
		// dbretry retries transient FK deadlocks: task_dependencies has
		// FKs into tasks/workspaces and races with concurrent transition
		// transactions on the same task rows.
		var pub types.PublicID
		txErr := dbretry.InTx(ctx, deps.DB, "tasks.AddDependency", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			id, e := taskdeps.Add(ctx, tx, taskdeps.Args{
				WorkspaceID:      ws.ID,
				FromTaskID:       task.ID,
				ToTaskID:         toID,
				Kind:             generated.TaskDependenciesKind(in.Body.Kind),
				ActorUserID:      actorPtr(ctx),
				FromTaskPublicID: task.PublicID.String(),
				ToTaskPublicID:   toPub.String(),
				Via:              "api",
			})
			if e != nil {
				return e
			}
			pub = id
			return nil
		})
		if txErr != nil {
			if stderrors.Is(txErr, taskdeps.ErrCycle) {
				return nil, httpErr(apierrors.WsTaskDependencyCycle)
			}
			// ErrNoRows from inside the transaction means an endpoint
			// vanished (disabled) between the resolve and the tx.
			return nil, httpErr(apierr.SpecForErrNoRows(txErr, apierrors.WsTaskNotFound, apierrors.InternalUnexpected))
		}
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
		taskInternal := int64(task.ID)
		// Delete the edge and append the event atomically inside one tx so
		// the timeline row cannot be lost between the UPDATE and a
		// post-commit append (the AddDependency path already commits both
		// in one tx). dbretry retries transient FK deadlocks with
		// concurrent transition transactions on the same task rows.
		var affected int64
		txErr := dbretry.InTx(ctx, deps.DB, "tasks.RemoveDependency", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			qtx := deps.Queries.WithTx(tx.RawTx())
			n, e := qtx.DeleteDependency(ctx, generated.DeleteDependencyParams{
				WorkspaceID: ws.ID,
				PublicID:    depID,
				FromTaskID:  task.ID,
			})
			if e != nil {
				return e
			}
			affected = n
			if n == 0 {
				// Nothing was deleted, so no event must be appended; the
				// NOT_FOUND mapping happens outside the tx.
				return nil
			}
			return eventbus.Append(ctx, tx, eventbus.Event{
				Type:        eventbus.TaskDependencyRemoved,
				WorkspaceID: ws.ID,
				ActorUserID: actorPtr(ctx),
				TaskID:      &taskInternal,
				Payload: map[string]any{
					"taskId":       task.PublicID.String(),
					"dependencyId": depID.String(),
				},
			})
		})
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		// 0 rows means the edge does not exist on this task's path (it may
		// belong to a sibling task). Return NOT_FOUND so a no-op is
		// distinguishable from a real delete and one task cannot delete
		// another task's edge.
		if affected == 0 {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
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
