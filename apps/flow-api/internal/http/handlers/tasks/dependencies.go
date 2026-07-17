package tasks

import (
	"context"
	"database/sql"
	stderrors "errors"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
)

// errDependencyCycle is the sentinel returned from the AddDependency tx
// callback when the locked cycle re-check detects that the new edge
// would close a cycle. It is not a transient MySQL error, so dbretry
// returns it verbatim without retrying and the handler maps it to
// WS.TASK.DEPENDENCY_CYCLE.
var errDependencyCycle = errDependencyCycleValue{}

type errDependencyCycleValue struct{}

func (errDependencyCycleValue) Error() string { return "tasks: dependency edge would close a cycle" }

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
				CreatedAt:             r.CreatedAt.Unix(),
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
		// Resolve target task internal id and owning project within the
		// same workspace. The project id feeds the row locks below.
		const q = `SELECT id, project_id FROM tasks WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1`
		var toID, toProjectID uint32
		if err := deps.DB.QueryRowContext(ctx, q, ws.ID, toPub).Scan(&toID, &toProjectID); err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsTaskNotFound, apierrors.InternalUnexpected))
		}
		prj, ok := middleware.ProjectFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		pub := types.New()
		taskInternal := int64(task.ID)
		// Insert the edge and append the event atomically inside one tx so
		// a crash between insert and a post-commit append cannot lose the
		// timeline row. The cycle check also runs INSIDE this tx, after
		// locking the endpoint project rows (LockProjectForDependency,
		// mirroring tasknumber.Allocate): without the lock two concurrent
		// POSTs (A->B and B->A) each read an edge set that misses the
		// other's insert, both pass the check, and both commit — forming a
		// cycle. The lock serializes the check-then-insert, so the loser
		// re-reads the winner's committed edge and is rejected with
		// WS.TASK.DEPENDENCY_CYCLE. Both endpoint projects are locked in
		// ascending id order so two cross-project writers cannot deadlock
		// on each other's lock. dbretry retries transient FK deadlocks:
		// task_dependencies has FKs into tasks/workspaces and races with
		// concurrent transition transactions on the same task rows.
		txErr := dbretry.InTx(ctx, deps.DB, "tasks.AddDependency", nil, func(ctx context.Context, tx *sql.Tx) error {
			qtx := deps.Queries.WithTx(tx)
			first, second := prj.ID, toProjectID
			if second < first {
				first, second = second, first
			}
			if _, e := qtx.LockProjectForDependency(ctx, generated.LockProjectForDependencyParams{
				WorkspaceID: ws.ID,
				ID:          first,
			}); e != nil {
				return e
			}
			if second != first {
				if _, e := qtx.LockProjectForDependency(ctx, generated.LockProjectForDependencyParams{
					WorkspaceID: ws.ID,
					ID:          second,
				}); e != nil {
					return e
				}
			}
			// Read the edge set only after the locks are granted: under
			// REPEATABLE READ the consistent snapshot is established at
			// the first plain SELECT, which here runs after any concurrent
			// dependency writer on these projects has committed, so the
			// walk sees that writer's edge.
			edges, e := qtx.ListDependencyEdgesForWorkspace(ctx, ws.ID)
			if e != nil {
				return e
			}
			// Reject any edge that would close a cycle so the dependency
			// graph stays a DAG (the documented contract). The new edge
			// is task.ID -> toID; it closes a cycle iff toID can already
			// reach task.ID along existing edges.
			if dependencyEdgeClosesCycle(edges, task.ID, toID) {
				return errDependencyCycle
			}
			if _, e := qtx.AddDependency(ctx, generated.AddDependencyParams{
				PublicID:    pub,
				WorkspaceID: ws.ID,
				FromTaskID:  task.ID,
				ToTaskID:    toID,
				Kind:        generated.TaskDependenciesKind(in.Body.Kind),
			}); e != nil {
				return e
			}
			return eventbus.Append(ctx, tx, eventbus.Event{
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
		})
		if txErr != nil {
			if stderrors.Is(txErr, errDependencyCycle) {
				return nil, httpErr(apierrors.WsTaskDependencyCycle)
			}
			// ErrNoRows from the project lock means an endpoint project
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
		txErr := dbretry.InTx(ctx, deps.DB, "tasks.RemoveDependency", nil, func(ctx context.Context, tx *sql.Tx) error {
			qtx := deps.Queries.WithTx(tx)
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

// dependencyEdgeClosesCycle reports whether adding a directed edge
// from -> to would close a cycle in the workspace dependency graph
// described by edges. The new edge closes a cycle iff `to` can already
// reach `from` along the existing edges (then from -> to -> ... -> from).
// A self-edge (from == to) is also a cycle. The walk is a bounded BFS
// over the in-memory adjacency list, so it terminates even on graphs
// that already contain cycles from legacy rows.
func dependencyEdgeClosesCycle(edges []generated.ListDependencyEdgesForWorkspaceRow, from, to uint32) bool {
	if from == to {
		return true
	}
	adj := make(map[uint32][]uint32, len(edges))
	for _, e := range edges {
		adj[e.FromTaskID] = append(adj[e.FromTaskID], e.ToTaskID)
	}
	visited := make(map[uint32]bool)
	stack := []uint32{to}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == from {
			return true
		}
		if visited[n] {
			continue
		}
		visited[n] = true
		stack = append(stack, adj[n]...)
	}
	return false
}
