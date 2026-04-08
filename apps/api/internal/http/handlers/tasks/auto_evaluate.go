package tasks

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/constraint/engine"
)

// autoEvaluateConstraints is the best-effort hook mutating handlers
// call after a write that might change a constraint outcome
// (constraints add/remove, dependency add/remove, state transition).
// It swallows engine errors because the user-visible write already
// succeeded and a re-evaluation on the next request will recover.
func autoEvaluateConstraints(ctx context.Context, deps Deps, workspaceID, taskID uint32) {
	eng := &engine.Engine{Store: &engine.SqlcStore{WorkspaceID: workspaceID, Queries: deps.Queries}}
	_, _ = eng.EvaluateTask(ctx, taskID)
}
