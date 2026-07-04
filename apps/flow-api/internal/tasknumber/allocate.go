package tasknumber

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
)

// Allocate returns the next per-project task number after locking the
// project row in the caller's transaction.
func Allocate(ctx context.Context, q *generated.Queries, workspaceID, projectID uint32) (int32, error) {
	if _, err := q.LockProjectForTaskNumber(ctx, generated.LockProjectForTaskNumberParams{
		WorkspaceID: workspaceID,
		ID:          projectID,
	}); err != nil {
		return 0, err
	}
	return q.AssignTaskNumber(ctx, generated.AssignTaskNumberParams{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
	})
}
