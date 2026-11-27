package asanaimport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskactors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskstate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskupdate"
)

// writeTasks writes each task in one transaction, parents first.
func (w *writer) writeTasks(ctx context.Context) error {
	for _, tp := range w.p.Tasks {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := &w.res.Projects[tp.Project].Tasks
		if tp.Row != nil && tp.Action != ActionSkip {
			w.tasks[tp.GID] = taskRef{tp.Row.ID, tp.Row.PublicID}
		}
		switch tp.Action {
		case ActionSkip:
			c.Skipped++
		case ActionKeep:
			c.Kept++
		case ActionUnchanged:
			c.Duplicates++
		case ActionCreate:
			w.createTask(ctx, tp, c)
		case ActionUpdate:
			w.updateTask(ctx, tp, c)
		}
	}
	for i := range w.p.Projects {
		w.res.Projects[i].Tasks.Kept += w.p.TaskSync(i).Orphaned
	}
	return nil
}

func (w *writer) createTask(ctx context.Context, tp *TaskPlan, c *Counts) {
	what := "task " + tp.GID
	projectID := w.projectIDs[tp.Project]
	if projectID == 0 {
		w.fail(&c.Failed, what, nil)
		return
	}
	var parent sql.NullInt32
	if tp.ParentGID != "" {
		ref, ok := w.tasks[tp.ParentGID]
		if !ok {
			c.Skipped++ // parent not imported
			return
		}
		parent = nullInt32(ref.id)
	}
	v := tp.Value
	title, err := taskrules.NewTitle(v.Title)
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	creator, err := w.userID(v.CreatedBy)
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	var actors []taskcreate.Actor
	want := make([]ActorRef, 0, 1+len(v.Watchers))
	if v.Assignee != "" {
		want = append(want, ActorRef{v.Assignee, generated.TaskActorsRoleAssignee})
	}
	for _, e := range v.Watchers {
		want = append(want, ActorRef{e, generated.TaskActorsRoleWatcher})
	}
	for _, a := range want {
		uid, err := w.userID(a.Email)
		if err != nil {
			w.fail(&c.Failed, what, err)
			return
		}
		actors = append(actors, taskcreate.Actor{UserID: uid, Role: a.Role})
	}
	labels := make([]labelEntry, 0, len(v.Labels))
	for _, ref := range v.Labels {
		l, err := w.label(ref)
		if err != nil {
			w.fail(&c.Failed, what, err)
			return
		}
		labels = append(labels, l)
	}

	var (
		ref  taskRef
		gate int
	)
	err = w.inTx(ctx, "CreateTask", func(ctx context.Context, tx *dbretry.Tx) error {
		gate = 0
		created, err := taskcreate.New(ctx, tx, taskcreate.AuthoredBy(creator).WithActors(actors...), taskcreate.Args{
			WorkspaceID: w.p.Workspace.ID, ProjectID: projectID, ParentTaskID: parent, Title: title,
			Description: sql.NullString{String: v.Description, Valid: v.Description != ""},
			DueOn:       v.DueOn, StartedOn: v.StartOn, CreatedAt: v.CreatedAt, SourceUID: SourceUID(tp.GID),
		})
		if err != nil {
			return err
		}
		taskID := created.ID
		ref = taskRef{uint32(created.ID), created.PublicID} //#nosec G115 -- tasks.id fits uint32 within realistic deployments
		if err := w.rec.RecordInTx(ctx, tx, w.actor, mutationlog.Mutation{
			EventType: eventbus.TaskCreated, AuditAction: "task.create",
			ResourceType: "task", ResourceID: created.PublicID.String(), TaskID: &taskID,
			Payload:  map[string]any{"taskId": created.PublicID.String(), "title": title.String(), "source": via},
			CallSite: "asanaimport.CreateTask",
		}); err != nil {
			return err
		}
		for _, l := range labels {
			if err := w.attachLabel(ctx, tx, ref, l); err != nil {
				return err
			}
		}
		if v.Completed {
			gated, err := w.transition(ctx, tx, ref, taskstate.TransitionComplete)
			if err != nil {
				return err
			}
			if gated {
				gate++
			}
		}
		return nil
	})
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	w.tasks[tp.GID] = ref
	w.p.Projects[tp.Project].Notes.CompletionGate += gate
	c.Imported++
}

func (w *writer) updateTask(ctx context.Context, tp *TaskPlan, c *Counts) {
	what := "task " + tp.GID
	ch := tp.Change
	ref := taskRef{tp.Row.ID, tp.Row.PublicID}
	adds := make([]Account, len(ch.AddActors))
	for i, a := range ch.AddActors {
		acc, ok := w.accounts[a.Email]
		if !ok {
			w.fail(&c.Failed, what, fmt.Errorf("account %q is not available", a.Email))
			return
		}
		adds[i] = acc
	}
	attach := make([]labelEntry, len(ch.AttachLabels))
	for i, lr := range ch.AttachLabels {
		l, err := w.label(lr)
		if err != nil {
			w.fail(&c.Failed, what, err)
			return
		}
		attach[i] = l
	}
	patch := taskupdate.Patch{Title: ch.Title, Description: ch.Description, DueOn: ch.DueOn, StartOn: ch.StartOn}
	patched := patch.Title != nil || patch.Description != nil || patch.DueOn != nil || patch.StartOn != nil

	var gate int
	err := w.inTx(ctx, "UpdateTask", func(ctx context.Context, tx *dbretry.Tx) error {
		gate = 0
		if patched {
			r, err := taskupdate.Apply(ctx, tx, taskupdate.Args{
				WorkspaceID: w.p.Workspace.ID, TaskID: ref.id, PublicID: ref.pub,
				Patch: patch, Actor: w.actor, Via: via,
			})
			if err != nil {
				return err
			}
			w.audit(ctx, tx, mutationlog.Mutation{
				AuditAction: "task.update", ResourceType: "task", ResourceID: ref.pub.String(), Payload: r.Payload,
				CallSite: "asanaimport.Write (taskupdate.Apply appended the event)",
			})
		}
		// Removals first: a changed assignee frees the role before it is given.
		for _, rm := range ch.RemoveActors {
			if err := w.removeActor(ctx, tx, ref, rm); err != nil {
				return err
			}
		}
		for i, a := range adds {
			if err := w.addActor(ctx, tx, ref, a, ch.AddActors[i].Role); err != nil {
				return err
			}
		}
		for _, id := range ch.DetachLabels {
			if err := w.detachLabel(ctx, tx, ref, id); err != nil {
				return err
			}
		}
		for _, l := range attach {
			if err := w.attachLabel(ctx, tx, ref, l); err != nil {
				return err
			}
		}
		if ch.Transition != "" {
			gated, err := w.transition(ctx, tx, ref, ch.Transition)
			if err != nil {
				return err
			}
			if gated {
				gate++
			}
		}
		return nil
	})
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	w.p.Projects[tp.Project].Notes.CompletionGate += gate
	// A gated completion that was the only difference changed nothing.
	if gate > 0 && !patched && len(ch.RemoveActors) == 0 && len(adds) == 0 && len(ch.DetachLabels) == 0 && len(attach) == 0 {
		c.Duplicates++
		return
	}
	c.Updated++
}

// transition applies a completion change; gated reports the D8 refusal,
// which leaves tx usable.
func (w *writer) transition(ctx context.Context, tx *dbretry.Tx, ref taskRef, verb string) (gated bool, err error) {
	r, spec, err := taskstate.ApplyTransitionTx(ctx, tx, taskstate.ApplyParams{
		WorkspaceID: w.p.Workspace.ID, TaskID: ref.id, PublicID: ref.pub,
		Transition: verb, ActorUserID: w.actorID, Via: via,
	})
	if err != nil {
		return false, err
	}
	if spec == apierrors.WsTaskCompletionNeedsApproval {
		return true, nil
	}
	if spec != nil {
		return false, fmt.Errorf("transition %s refused: %s", verb, spec.Code)
	}
	w.audit(ctx, tx, mutationlog.Mutation{
		AuditAction: "task.transition", ResourceType: "task", ResourceID: ref.pub.String(),
		Payload:  map[string]any{"transition": verb, "fromState": string(r.FromState), "toState": string(r.ToState)},
		CallSite: "asanaimport.Write (taskstate.ApplyTransitionTx appended the event)",
	})
	return false, nil
}

func (w *writer) addActor(ctx context.Context, tx *dbretry.Tx, ref taskRef, a Account, role generated.TaskActorsRole) error {
	pub := types.New()
	err := taskactors.Add(ctx, tx, taskactors.Args{
		Actor: w.actor, TaskID: ref.id, TaskPublicID: ref.pub.String(),
		UserID: a.ID, UserPublicID: a.PublicID.String(), Role: role, PublicID: pub,
	})
	if errors.Is(err, taskactors.ErrAlreadyActor) {
		return nil
	}
	if err != nil {
		return err
	}
	w.audit(ctx, tx, mutationlog.Mutation{
		AuditAction: "task.actor.add", ResourceType: "task_actor", ResourceID: pub.String(),
		Payload: map[string]any{
			"taskId": ref.pub.String(), "actorId": pub.String(), "userId": a.PublicID.String(), "role": string(role),
		},
		CallSite: "asanaimport.Write (taskactors.Add appended the event)",
	})
	return nil
}

func (w *writer) removeActor(ctx context.Context, tx *dbretry.Tx, ref taskRef, rm ActorRow) error {
	pub, err := generated.New(tx).FindActiveTaskActor(ctx, generated.FindActiveTaskActorParams{
		WorkspaceID: w.p.Workspace.ID, TaskID: ref.id, UserID: nullInt32(rm.UserID), Role: rm.Role,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	err = taskactors.Remove(ctx, tx, taskactors.Args{
		Actor: w.actor, TaskID: ref.id, TaskPublicID: ref.pub.String(), ActorPublicID: pub, Mutations: w.rec,
	})
	if errors.Is(err, taskactors.ErrNotActor) {
		return nil
	}
	if err != nil {
		return err
	}
	w.audit(ctx, tx, mutationlog.Mutation{
		AuditAction: "task.actor.remove", ResourceType: "task_actor", ResourceID: pub.String(),
		Payload:  map[string]any{"taskId": ref.pub.String(), "actorId": pub.String()},
		CallSite: "asanaimport.Write (taskactors.Remove appended the event)",
	})
	return nil
}
