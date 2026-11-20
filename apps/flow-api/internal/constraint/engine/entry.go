package engine

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// verdictSource is the system actor recorded on verdict events.
const verdictSource = "engine.constraint"

// EvaluateTaskTx evaluates the task's own constraints inside the caller's
// transaction. The task row is taken FOR UPDATE and the facts are read
// FOR SHARE, so a snapshot the caller took earlier does not leak into the
// verdict. Changed verdicts are appended as events on tx and commit or
// roll back with it.
func EvaluateTaskTx(ctx context.Context, tx *dbretry.Tx, workspaceID, taskID uint32) ([]Outcome, error) {
	eng := &Engine{
		Store: &SqlcStore{WorkspaceID: workspaceID, Queries: generated.New(tx), Locking: true},
		Emit:  appendVerdict(tx, workspaceID),
	}
	return eng.evaluate(ctx, taskID)
}

// Evaluate evaluates the task's own constraints in a transaction of its
// own at READ COMMITTED: every fact read sees the latest committed value,
// so the task row lock is the only lock taken. Changed verdicts are
// appended as events in the same transaction.
func Evaluate(ctx context.Context, db dbretry.TxBeginner, workspaceID, taskID uint32) ([]Outcome, error) {
	var outcomes []Outcome
	err := dbretry.InTx(ctx, db, "engine.evaluate", &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx *dbretry.Tx) error {
		eng := &Engine{
			Store: &SqlcStore{WorkspaceID: workspaceID, Queries: generated.New(tx)},
			Emit:  appendVerdict(tx, workspaceID),
		}
		var err error
		outcomes, err = eng.evaluate(ctx, taskID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return outcomes, nil
}

// ListDependents returns the internal ids of the enabled tasks holding an
// enabled outgoing dependency edge to taskID: the tasks whose dependency
// facts read taskID's state.
func ListDependents(ctx context.Context, db generated.DBTX, workspaceID, taskID uint32) ([]uint32, error) {
	return generated.New(db).ListDependentTaskIDsForEngine(ctx, generated.ListDependentTaskIDsForEngineParams{
		ToTaskID:    taskID,
		WorkspaceID: workspaceID,
	})
}

// appendVerdict is the only producer of verdict events: kind, payload and
// actor are written here and nowhere else.
func appendVerdict(tx *dbretry.Tx, workspaceID uint32) EmitFunc {
	return func(ctx context.Context, taskID uint32, taskPublicID string, row Row, satisfied bool) error {
		kind := eventbus.TaskConstraintFailed
		if satisfied {
			kind = eventbus.TaskConstraintSatisfied
		}
		taskInternal := int64(taskID)
		return eventbus.Append(ctx, tx, eventbus.Event{
			Type:              kind,
			WorkspaceID:       workspaceID,
			ActorSystemSource: verdictSource,
			TaskID:            &taskInternal,
			Payload: map[string]any{
				"taskId":       taskPublicID,
				"constraintId": row.PublicID,
				"kind":         row.Kind,
			},
		})
	}
}
