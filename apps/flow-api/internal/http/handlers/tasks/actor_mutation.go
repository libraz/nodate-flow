package tasks

import "context"

// completeActorMutation refreshes task constraints after a committed actor
// change. actor.has_role facts depend on the task's current user and agent
// roles, so every actor mutation path must finish through this hook.
func completeActorMutation(ctx context.Context, deps Deps, workspaceID, taskID uint32) {
	autoEvaluateConstraints(ctx, deps, workspaceID, taskID)
}
