package engine

import (
	"context"
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

	if due, err := s.Queries.GetTaskDueOnForEngine(ctx, generated.GetTaskDueOnForEngineParams{
		ID:          taskID,
		WorkspaceID: s.WorkspaceID,
	}); err == nil && due.Valid {
		d := due.Time
		f.DueOn = &d
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
