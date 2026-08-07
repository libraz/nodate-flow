package engine

import (
	"context"
	"database/sql"
	stderrors "errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/constraint"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// SqlcStore is the production [Store] implementation backed by sqlc
// queries. It populates a minimal [constraint.Facts] value — due_on,
// dependency states, and actor roles — and leaves the signal /
// approval / CI fact maps empty until the signal ingestion slice
// lands. Constraints that reference those builtins simply evaluate
// to false in the meantime, which is the conservative outcome for
// a constraint engine.
type SqlcStore struct {
	// WorkspaceID is the workspace this store is scoped to. The
	// caller MUST set it to the authenticated workspace before
	// invoking EvaluateTask; engine queries are keyed on the
	// internal task_id but revocation / soft-delete respects the
	// workspace column.
	WorkspaceID uint32
	Queries     *generated.Queries
}

// LoadTask implements Store.
func (s *SqlcStore) LoadTask(ctx context.Context, taskID uint32) (constraint.Facts, []Row, error) {
	f := constraint.Facts{Now: time.Now()}

	// A failed read is not the same fact as "this task has no due date",
	// and the difference is not visible downstream: constraint.Facts
	// encodes an absent due_on as a nil pointer, and every time.due_*
	// builtin answers false for a nil DueOn. Swallowing the error would
	// therefore turn a database outage into a definitive "the deadline
	// constraint is not met" — or, under `not(time.due_before(...))`,
	// into a definitive "it is met". Both are answers the engine has no
	// grounds to give, so the load fails instead and the caller decides
	// what to do with a task it could not evaluate.
	//
	// sql.ErrNoRows is the one exception: no row means the task is not
	// in this workspace, so there are no constraints to evaluate against
	// it either and the load stays empty rather than failing. That is
	// the tenant-scope contract the engine reads are built on.
	due, err := s.Queries.GetTaskDueOnForEngine(ctx, generated.GetTaskDueOnForEngineParams{
		ID:          taskID,
		WorkspaceID: s.WorkspaceID,
	})
	switch {
	case err == nil:
		if due.Valid {
			d := due.Time
			f.DueOn = &d
		}
	case !stderrors.Is(err, sql.ErrNoRows):
		return f, nil, err
	}

	deps, err := s.Queries.ListDependencyStatesForEngine(ctx, generated.ListDependencyStatesForEngineParams{
		FromTaskID:  taskID,
		WorkspaceID: s.WorkspaceID,
	})
	if err != nil {
		return f, nil, err
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

	roles, err := s.Queries.ListTaskActorRolesForEngine(ctx, generated.ListTaskActorRolesForEngineParams{
		TaskID:      taskID,
		WorkspaceID: s.WorkspaceID,
	})
	if err != nil {
		return f, nil, err
	}
	if len(roles) > 0 {
		f.ActorRoles = make(map[string]bool, len(roles))
		for _, r := range roles {
			f.ActorRoles[string(r)] = true
		}
	}

	rows, err := s.Queries.ListTaskConstraintsForEngine(ctx, generated.ListTaskConstraintsForEngineParams{
		TaskID:      taskID,
		WorkspaceID: s.WorkspaceID,
	})
	if err != nil {
		return f, nil, err
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, Row{
			PublicID:   r.PublicID.String(),
			Expression: []byte(r.Expression),
		})
	}
	return f, out, nil
}

// MarkSatisfied implements Store.
func (s *SqlcStore) MarkSatisfied(ctx context.Context, publicID string, _ time.Time) error {
	pid, err := types.Parse(publicID)
	if err != nil {
		return err
	}
	return s.Queries.SatisfyConstraint(ctx, generated.SatisfyConstraintParams{
		WorkspaceID: s.WorkspaceID,
		PublicID:    pid,
	})
}

// MarkFailed implements Store.
func (s *SqlcStore) MarkFailed(ctx context.Context, publicID string, _ time.Time) error {
	pid, err := types.Parse(publicID)
	if err != nil {
		return err
	}
	return s.Queries.FailConstraint(ctx, generated.FailConstraintParams{
		WorkspaceID: s.WorkspaceID,
		PublicID:    pid,
	})
}
