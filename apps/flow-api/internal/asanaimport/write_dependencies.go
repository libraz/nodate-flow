package asanaimport

import (
	"context"
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskdeps"
)

// writeDependencies removes the edges Asana dropped, then adds the new
// ones: the plan's cycle check assumed the removals.
func (w *writer) writeDependencies(ctx context.Context) error {
	pubs := map[uint32]types.PublicID{}
	for _, t := range w.p.existing.Tasks {
		pubs[t.ID] = t.PublicID
	}
	for _, dd := range w.p.DependencyDeletions {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := &w.res.Projects[dd.Project].Dependencies
		e := dd.Edge
		err := w.inTx(ctx, "RemoveDependency", func(ctx context.Context, tx *dbretry.Tx) error {
			if err := taskdeps.Remove(ctx, tx, taskdeps.RemoveArgs{
				WorkspaceID: w.p.Workspace.ID, FromTaskID: e.FromTaskID, DependencyID: e.PublicID,
				ActorUserID: w.actorID, FromTaskPublicID: pubs[e.FromTaskID].String(),
			}); err != nil {
				return err
			}
			w.audit(ctx, tx, mutationlog.Mutation{
				AuditAction: "task.dependency.remove", ResourceType: "task_dependency", ResourceID: e.PublicID.String(),
				CallSite: "asanaimport.Write (taskdeps.Remove appended the event)",
			})
			return nil
		})
		if err != nil && !errors.Is(err, taskdeps.ErrNotFound) {
			w.fail(&c.Failed, "dependency "+e.PublicID.String(), err)
			continue
		}
		c.Deleted++
	}
	for _, dp := range w.p.Dependencies {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := &w.res.Projects[dp.Project].Dependencies
		switch dp.Action {
		case ActionSkip:
			c.Skipped++
		case ActionUnchanged:
			c.Duplicates++
		case ActionCreate:
			w.addDependency(ctx, dp, c)
		}
	}
	return nil
}

func (w *writer) addDependency(ctx context.Context, dp *DependencyPlan, c *Counts) {
	from, ok1 := w.tasks[dp.BlockerGID]
	to, ok2 := w.tasks[dp.BlockedGID]
	if !ok1 || !ok2 {
		c.Skipped++ // an end was not imported
		return
	}
	err := w.inTx(ctx, "AddDependency", func(ctx context.Context, tx *dbretry.Tx) error {
		pub, err := taskdeps.Add(ctx, tx, taskdeps.Args{
			WorkspaceID: w.p.Workspace.ID, FromTaskID: from.id, ToTaskID: to.id,
			Kind: generated.TaskDependenciesKindBlocks, ActorUserID: w.actorID,
			FromTaskPublicID: from.pub.String(), ToTaskPublicID: to.pub.String(), Via: via,
		})
		if err != nil {
			return err
		}
		w.audit(ctx, tx, mutationlog.Mutation{
			AuditAction: "task.dependency.add", ResourceType: "task_dependency", ResourceID: pub.String(),
			CallSite: "asanaimport.Write (taskdeps.Add appended the event)",
		})
		return nil
	})
	switch {
	case errors.Is(err, taskdeps.ErrDuplicate):
		c.Duplicates++
	case errors.Is(err, taskdeps.ErrCycle):
		c.Skipped++ // cycle
	case err != nil:
		w.fail(&c.Failed, "dependency "+dp.BlockerGID+" -> "+dp.BlockedGID, err)
	default:
		c.Imported++
	}
}
