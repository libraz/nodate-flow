package signals

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/constraint/engine"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// reevaluateSignalTask re-evaluates the constraints of the task a newly
// stored signal is attached to, and of its dependents, because
// signal.received reads the kinds of a task's signals. It is best effort
// like the task handlers' hook: the signal row is already written, and
// the next evaluation of the task recovers from a failure here.
func reevaluateSignalTask(ctx context.Context, deps Deps, wsID uint32, taskFK sql.NullInt32, subjectType generated.SignalsSubjectType, subjectID sql.NullInt32) {
	taskID := taskFK
	if !taskID.Valid && subjectType == generated.SignalsSubjectTypeTask {
		taskID = subjectID
	}
	if !taskID.Valid {
		return
	}
	eng := &engine.Engine{Store: &engine.SqlcStore{WorkspaceID: wsID, Queries: deps.Queries}}
	_ = eng.EvaluateTaskAndDependents(ctx, uint32(taskID.Int32)) //#nosec G115 -- tasks.id is INT UNSIGNED
}
