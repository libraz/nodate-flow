package engine

import (
	"context"
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
)

// ReplayLoader is the sqlc-backed loader for Replay. It fetches
// task.transition.* events ordered by occurred_at and converts
// them to the pure [TransitionEvent] slice the replay engine
// consumes.
type ReplayLoader struct {
	WorkspaceID uint32
	Queries     *generated.Queries
}

// Load returns the transition events for the given internal task
// id in evaluation order.
func (l *ReplayLoader) Load(ctx context.Context, taskID uint32) ([]TransitionEvent, error) {
	rows, err := l.Queries.ListTransitionEventsForReplay(ctx, generated.ListTransitionEventsForReplayParams{
		WorkspaceID: l.WorkspaceID,
		TaskID:      sql.NullInt32{Int32: int32(taskID), Valid: true}, //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	})
	if err != nil {
		return nil, err
	}
	out := make([]TransitionEvent, 0, len(rows))
	for _, r := range rows {
		name, ok := ParseTransitionName(r.Type)
		if !ok {
			continue
		}
		var reverses *int64
		if r.ReversesEventID.Valid {
			v := r.ReversesEventID.Int64
			reverses = &v
		}
		out = append(out, TransitionEvent{ID: r.ID, Name: name, ReversesEventID: reverses})
	}
	return out, nil
}

// ReplayTask loads transition events from the database and runs
// [Replay], returning the recomputed derived state.
func (l *ReplayLoader) ReplayTask(ctx context.Context, taskID uint32) (DerivedState, error) {
	evs, err := l.Load(ctx, taskID)
	if err != nil {
		return "", err
	}
	return Replay(evs)
}
