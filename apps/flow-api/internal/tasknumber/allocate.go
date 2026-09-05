package tasknumber

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// Allocate returns the next per-project task number after locking the
// project row in the caller's transaction.
//
// It must be the transaction's first ordinary read. Under REPEATABLE READ
// the snapshot a non-locking read resolves against is fixed by the first
// ordinary read, and the locking read on the project row does not create
// one — so reaching here first is what puts the snapshot after the lock
// was granted, where the allocation sees what the previous holder
// committed. A transaction that reads before creating must call
// [taskcreate.LockProject] first; the SQL comment on AssignTaskNumber
// records why a locking read cannot lift that requirement.
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
