package tasks

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/constraint/engine"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
)

// ReplayInput is the path input for GET /tasks/{id}/replay.
type ReplayInput struct {
	ID string `path:"id"`
}

// ReplayOutput is the response for GET /tasks/{id}/replay.
type ReplayOutput struct {
	Body struct {
		DerivedState string `json:"derivedState"`
		// Equivalent is true when the replayed state matches the
		// stored tasks.derived_state, false when drift is detected
		// (replay equivalence).
		Equivalent bool   `json:"equivalent"`
		Stored     string `json:"stored"`
	}
}

// Replay handles GET /tasks/{id}/replay. It recomputes the task's
// derived_state from its task.transition.* events and reports
// whether the replay agrees with the stored value.
func Replay(deps Deps) func(context.Context, *ReplayInput) (*ReplayOutput, error) {
	return func(ctx context.Context, _ *ReplayInput) (*ReplayOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		loader := &engine.ReplayLoader{WorkspaceID: ws.ID, Queries: deps.Queries}
		state, err := loader.ReplayTask(ctx, task.ID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		stored, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ReplayOutput{}
		out.Body.DerivedState = string(state)
		out.Body.Stored = string(stored.DerivedState)
		out.Body.Equivalent = string(state) == string(stored.DerivedState)
		return out, nil
	}
}
