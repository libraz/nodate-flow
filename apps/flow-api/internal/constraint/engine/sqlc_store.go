package engine

import (
	"context"
	"database/sql"
	stderrors "errors"
	"slices"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/constraint"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// SqlcStore is the production [Store] implementation backed by sqlc
// queries. It populates every [constraint.Facts] field a DSL op reads:
// due_on, dependency states and kinds, actor roles, and the kinds of the
// signals attached to the task.
//
// Queries is bound to a transaction by passing generated.New(tx); the
// task row read takes FOR UPDATE, so a store outside a transaction holds
// that lock only for the statement.
type SqlcStore struct {
	// WorkspaceID is the workspace this store is scoped to. Engine
	// queries are keyed on the internal task_id but every read also
	// filters on the workspace column.
	WorkspaceID uint32
	Queries     *generated.Queries
	// Locking reads the facts with FOR SHARE. A caller whose transaction
	// may hold a snapshot older than the facts sets it; a READ COMMITTED
	// transaction reads current values without it.
	Locking bool
}

// LoadTask implements Store.
func (s *SqlcStore) LoadTask(ctx context.Context, taskID uint32) (Task, error) {
	f := constraint.Facts{Now: time.Now()}

	// A failed read is not the same fact as "this task has no due date":
	// Facts encodes an absent due_on as a nil pointer, which every
	// time.due_* builtin answers false for. The load fails instead of
	// turning an outage into a verdict. sql.ErrNoRows means the task is
	// disabled or not in this workspace, so there is nothing to evaluate.
	head, err := s.Queries.GetTaskForEngine(ctx, generated.GetTaskForEngineParams{
		ID:          taskID,
		WorkspaceID: s.WorkspaceID,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return Task{}, nil
		}
		return Task{}, err
	}
	if head.DueOn.Valid {
		d := head.DueOn.Time
		f.DueOn = &d
	}

	deps, err := s.loadDependencies(ctx, taskID)
	if err != nil {
		return Task{}, err
	}
	if len(deps) > 0 {
		f.DependencyStates = make(map[string]string, len(deps))
		f.DependencyKinds = make(map[string]string, len(deps))
		for _, d := range deps {
			pid := d.ToPublicID.String()
			f.DependencyStates[pid] = string(d.DerivedState)
			f.DependencyKinds[pid] = string(d.DependencyKind)
		}
	}

	roles, err := s.loadRoles(ctx, taskID)
	if err != nil {
		return Task{}, err
	}
	if len(roles) > 0 {
		f.ActorRoles = make(map[string]bool, len(roles))
		for _, r := range roles {
			f.ActorRoles[string(r)] = true
		}
	}

	kinds, err := s.loadSignalKinds(ctx, taskID)
	if err != nil {
		return Task{}, err
	}
	if len(kinds) > 0 {
		f.SignalsReceived = make(map[string]bool, len(kinds))
		for _, k := range kinds {
			f.SignalsReceived[k] = true
		}
	}

	rows, err := s.loadConstraints(ctx, taskID)
	if err != nil {
		return Task{}, err
	}
	return Task{PublicID: head.PublicID.String(), Facts: f, Rows: rows, Found: true}, nil
}

func (s *SqlcStore) loadDependencies(ctx context.Context, taskID uint32) ([]generated.ListDependencyStatesForEngineRow, error) {
	if !s.Locking {
		return s.Queries.ListDependencyStatesForEngine(ctx, generated.ListDependencyStatesForEngineParams{
			FromTaskID:  taskID,
			WorkspaceID: s.WorkspaceID,
		})
	}
	locked, err := s.Queries.ListDependencyStatesForEngineLocked(ctx, generated.ListDependencyStatesForEngineLockedParams{
		FromTaskID:  taskID,
		WorkspaceID: s.WorkspaceID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]generated.ListDependencyStatesForEngineRow, 0, len(locked))
	for _, d := range locked {
		out = append(out, generated.ListDependencyStatesForEngineRow(d))
	}
	return out, nil
}

func (s *SqlcStore) loadRoles(ctx context.Context, taskID uint32) ([]generated.TaskActorsRole, error) {
	if !s.Locking {
		return s.Queries.ListTaskActorRolesForEngine(ctx, generated.ListTaskActorRolesForEngineParams{
			TaskID:      taskID,
			WorkspaceID: s.WorkspaceID,
		})
	}
	return s.Queries.ListTaskActorRolesForEngineLocked(ctx, generated.ListTaskActorRolesForEngineLockedParams{
		TaskID:      taskID,
		WorkspaceID: s.WorkspaceID,
	})
}

func (s *SqlcStore) loadSignalKinds(ctx context.Context, taskID uint32) ([]string, error) {
	tid := sql.NullInt32{Int32: int32(taskID), Valid: true} //#nosec G115 -- tasks.id is INT UNSIGNED, fits int32 within realistic deployments
	if !s.Locking {
		return s.Queries.ListSignalKindsForTaskForEngine(ctx, generated.ListSignalKindsForTaskForEngineParams{
			WorkspaceID: s.WorkspaceID,
			TaskID:      tid,
		})
	}
	byTask, err := s.Queries.ListSignalKindsByTaskForEngineLocked(ctx, generated.ListSignalKindsByTaskForEngineLockedParams{
		WorkspaceID: s.WorkspaceID,
		TaskID:      tid,
	})
	if err != nil {
		return nil, err
	}
	bySubject, err := s.Queries.ListSignalKindsBySubjectForEngineLocked(ctx, generated.ListSignalKindsBySubjectForEngineLockedParams{
		WorkspaceID: s.WorkspaceID,
		SubjectID:   tid,
	})
	if err != nil {
		return nil, err
	}
	// The two arms overlap when a signal carries both links.
	kinds := append(byTask, bySubject...)
	slices.Sort(kinds)
	return slices.Compact(kinds), nil
}

func (s *SqlcStore) loadConstraints(ctx context.Context, taskID uint32) ([]Row, error) {
	if !s.Locking {
		rows, err := s.Queries.ListTaskConstraintsForEngine(ctx, generated.ListTaskConstraintsForEngineParams{
			TaskID:      taskID,
			WorkspaceID: s.WorkspaceID,
		})
		if err != nil {
			return nil, err
		}
		return toRows(rows), nil
	}
	locked, err := s.Queries.ListTaskConstraintsForEngineLocked(ctx, generated.ListTaskConstraintsForEngineLockedParams{
		TaskID:      taskID,
		WorkspaceID: s.WorkspaceID,
	})
	if err != nil {
		return nil, err
	}
	rows := make([]generated.ListTaskConstraintsForEngineRow, 0, len(locked))
	for _, r := range locked {
		rows = append(rows, generated.ListTaskConstraintsForEngineRow(r))
	}
	return toRows(rows), nil
}

func toRows(rows []generated.ListTaskConstraintsForEngineRow) []Row {
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, Row{PublicID: r.PublicID.String(), Kind: string(r.Kind), Expression: []byte(r.Expression)})
	}
	return out
}

// MarkSatisfied implements Store. The update matches only a row that is
// not already satisfied, so the affected-row count is the change.
func (s *SqlcStore) MarkSatisfied(ctx context.Context, publicID string, _ time.Time) (bool, error) {
	pid, err := types.Parse(publicID)
	if err != nil {
		return false, err
	}
	n, err := s.Queries.SatisfyConstraint(ctx, generated.SatisfyConstraintParams{
		WorkspaceID: s.WorkspaceID,
		PublicID:    pid,
	})
	return n > 0, err
}

// MarkFailed implements Store; see MarkSatisfied.
func (s *SqlcStore) MarkFailed(ctx context.Context, publicID string, _ time.Time) (bool, error) {
	pid, err := types.Parse(publicID)
	if err != nil {
		return false, err
	}
	n, err := s.Queries.FailConstraint(ctx, generated.FailConstraintParams{
		WorkspaceID: s.WorkspaceID,
		PublicID:    pid,
	})
	return n > 0, err
}
