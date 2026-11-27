// Package taskupdate owns the one way a task's editable fields are
// changed: the REST PATCH, the description-version restore and the MCP
// update tool all call [Apply], so the merge, the rules, the description
// history, the mention rows, the calendar mirror and the timeline event
// stay one sequence rather than one per transport.
//
// The shape is the one the other write kits have: [Apply] runs on the
// caller's transaction, writes the rows and the event inside it, and
// writes no audit row. The caller records that with RecordTxAudit after
// the commit, using [Result.Payload] as the metadata so the event and the
// audit row describe the change with one value. Authorization, ETags,
// idempotency receipts, error translation and the embedding refresh stay
// with the caller.
package taskupdate

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mentions"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskdesc"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
)

// ErrTaskNotFound is returned when the task row is gone by the time the
// transaction locks it.
var ErrTaskNotFound = errors.New("taskupdate: task not found")

// ErrDueOnConflict is returned when [Patch.ExpectedDueOn] is set and the
// stored due date differs from it.
var ErrDueOnConflict = errors.New("taskupdate: stored due date differs from the expected one")

// ErrParentCycle is returned when the proposed parent is the task itself,
// one of its descendants, or sits in a chain deeper than [MaxParentDepth].
var ErrParentCycle = errors.New("taskupdate: parent would make the task its own ancestor")

// ErrParentProjectMismatch is returned when the proposed parent belongs to
// another project than the task.
var ErrParentProjectMismatch = errors.New("taskupdate: parent is in a different project")

// ErrParentNotFound is returned when the proposed parent does not exist in
// the task's workspace.
var ErrParentNotFound = errors.New("taskupdate: parent not found")

// MaxParentDepth caps the ancestor walk of the cycle check.
const MaxParentDepth = 32

// errRestoreShape is returned when a restore carries anything but the
// description it restores.
var errRestoreShape = errors.New("taskupdate: a restore patches the description only")

// ItemkitError carries a refusal from the calendar mirror (itemkit) as
// itemkit returned it, so each transport can classify it with its own
// translator. It unwraps to the original error.
type ItemkitError struct{ Err error }

func (e *ItemkitError) Error() string { return e.Err.Error() }

// Unwrap returns the itemkit error.
func (e *ItemkitError) Unwrap() error { return e.Err }

// Patch is the set of fields to change. A nil pointer leaves the stored
// value; a non-nil one replaces it. For the dates, a non-nil pointer to
// an invalid [sql.NullTime] clears the column.
type Patch struct {
	Title       *string
	Description *string
	Priority    *int32
	DueOn       *sql.NullTime
	StartOn     *sql.NullTime
	SortWeight  *int32
	Visibility  *generated.TasksVisibility
	// ExpectedDueOn, when non-nil, makes the update conditional on the
	// stored due date equalling it (invalid = no due date).
	ExpectedDueOn *sql.NullTime
	// Parent, when non-nil, re-parents the task; a nil ParentID clears the
	// link. The caller has already authorized the parent for the actor.
	Parent *ParentChange
}

// ParentChange names the new parent of a task.
type ParentChange struct {
	// ID is the parent's internal id; invalid clears the parent.
	ID sql.NullInt32
	// PublicID is the parent's public id, recorded in the event payload.
	PublicID types.PublicID
}

// parentLink is the slice of a task row the ancestor walk reads.
type parentLink func(id uint32) (parent sql.NullInt32, err error)

// checkParentChain refuses a parent that is the task itself or whose
// ancestor chain reaches the task, within [MaxParentDepth] hops.
func checkParentChain(self, proposed uint32, link parentLink) error {
	cur := proposed
	for i := 0; i <= MaxParentDepth; i++ {
		if cur == self {
			return ErrParentCycle
		}
		next, err := link(cur)
		if err != nil {
			return err
		}
		if !next.Valid {
			return nil
		}
		cur = uint32(next.Int32) //#nosec G115 -- parent_task_id is tasks.id, fits uint32
	}
	return ErrParentCycle
}

// Args identifies the task, the change and who makes it.
type Args struct {
	WorkspaceID uint32
	TaskID      uint32
	PublicID    types.PublicID
	Patch       Patch
	// Actor is the user or the agent making the change; WorkspaceID on it
	// is ignored in favour of Args.WorkspaceID.
	Actor mutationlog.Actor
	// Via, when set, is recorded in the task.updated payload ("mcp").
	Via string
	// RestoredFrom, when set, makes this the restore of that description
	// version: the description is snapshotted and its mentions re-derived
	// even when unchanged, the dates are written back unchecked, and the
	// event is description.version.restored instead of task.updated.
	RestoredFrom types.PublicID
}

// Result is what the write left behind, for the caller's embedding
// refresh, audit row and response.
type Result struct {
	TitleChanged bool
	DescChanged  bool
	NewTitle     taskrules.Title
	NewDesc      sql.NullString
	// Version is the description version written, zero when none was.
	Version taskdesc.Version
	// Payload is the event payload; pass it as the audit metadata.
	Payload map[string]any
}

// Apply locks the task, merges the patch over the stored row, checks the
// rules, writes the row, the description history, the mentions and the
// calendar mirror, and appends the event, all on tx.
//
// Rule violations come back as [taskrules] errors, itemkit refusals as
// [*ItemkitError], and a vanished row or a failed due-date precondition as
// [ErrTaskNotFound] / [ErrDueOnConflict]. SQL errors are returned as they
// are so dbretry can retry the transient ones.
func Apply(ctx context.Context, tx *dbretry.Tx, args Args) (Result, error) {
	restoring := args.RestoredFrom != (types.PublicID{})
	p := args.Patch
	if restoring && (p.Description == nil || p.Title != nil || p.Priority != nil || p.DueOn != nil ||
		p.StartOn != nil || p.SortWeight != nil || p.Visibility != nil || p.ExpectedDueOn != nil || p.Parent != nil) {
		return Result{}, errRestoreShape
	}
	if args.Actor.UserID != 0 && args.Actor.AgentID != 0 {
		return Result{}, mutationlog.ErrActorConflict
	}

	q := generated.New(tx)
	// Locked before it is read, so the merge starts from what the previous
	// writer committed rather than overwriting a concurrent edit.
	if _, err := q.LockTaskForTransition(ctx, generated.LockTaskForTransitionParams{
		ID:          args.TaskID,
		WorkspaceID: args.WorkspaceID,
	}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Result{}, ErrTaskNotFound
		}
		return Result{}, err
	}
	current, err := q.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
		WorkspaceID: args.WorkspaceID,
		PublicID:    args.PublicID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Result{}, ErrTaskNotFound
		}
		return Result{}, err
	}
	if p.ExpectedDueOn != nil && !sameDate(current.DueOn, *p.ExpectedDueOn) {
		return Result{}, ErrDueOnConflict
	}

	newTitle, err := taskrules.PatchTitle(current.Title, p.Title)
	if err != nil {
		return Result{}, err
	}
	newDesc := current.Description
	if p.Description != nil {
		newDesc = sql.NullString{String: *p.Description, Valid: *p.Description != ""}
	}
	newPriority := current.Priority
	if p.Priority != nil {
		newPriority = *p.Priority
	}
	newDue := current.DueOn
	if p.DueOn != nil {
		newDue = *p.DueOn
	}
	newStart := current.StartedOn
	if p.StartOn != nil {
		newStart = *p.StartOn
	}
	// The merged pair, not the patch: a patch that moves only one side
	// still faces the other side's stored value. A restore writes both
	// back as stored, and a row predating the rule may hold an inverted
	// pair a description restore must not be refused over.
	if !restoring {
		if err := taskrules.DateOrder(newDue, newStart); err != nil {
			return Result{}, err
		}
	}
	newSortWeight := current.SortWeight
	if p.SortWeight != nil {
		newSortWeight = *p.SortWeight
	}
	newVisibility := current.Visibility
	if p.Visibility != nil {
		newVisibility = *p.Visibility
	}

	titleChanged := p.Title != nil && newTitle.String() != current.Title
	dueOnChanged := p.DueOn != nil && newDue != current.DueOn
	// Against the stored value: a client round-tripping the whole task
	// re-sends a description it never edited.
	descChanged := p.Description != nil && newDesc != current.Description

	needsItemkit := false
	if titleChanged || dueOnChanged {
		linked, err := q.CountActiveCalendarEventsByTaskId(ctx,
			sql.NullInt32{Int32: int32(args.TaskID), Valid: true}, //#nosec G115 -- tasks.id fits int32 within realistic deployments
		)
		if err != nil {
			return Result{}, err
		}
		needsItemkit = linked > 0
	}

	parentChanged := false
	if p.Parent != nil {
		self, err := q.GetTaskParentLink(ctx, generated.GetTaskParentLinkParams{WorkspaceID: args.WorkspaceID, ID: args.TaskID})
		if err != nil {
			return Result{}, err
		}
		if p.Parent.ID.Valid {
			proposed := uint32(p.Parent.ID.Int32) //#nosec G115 -- tasks.id fits uint32
			target, err := q.GetTaskParentLink(ctx, generated.GetTaskParentLinkParams{WorkspaceID: args.WorkspaceID, ID: proposed})
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return Result{}, ErrParentNotFound
				}
				return Result{}, err
			}
			if target.ProjectID != self.ProjectID {
				return Result{}, ErrParentProjectMismatch
			}
			if err := checkParentChain(args.TaskID, proposed, func(id uint32) (sql.NullInt32, error) {
				l, err := q.GetTaskParentLink(ctx, generated.GetTaskParentLinkParams{WorkspaceID: args.WorkspaceID, ID: id})
				return l.ParentTaskID, err
			}); err != nil {
				return Result{}, err
			}
		}
		parentChanged = p.Parent.ID != self.ParentTaskID
		if parentChanged {
			updatedBy := sql.NullInt32{Int32: int32(args.Actor.UserID), Valid: args.Actor.UserID != 0} //#nosec G115 -- users.id fits int32 within realistic deployments
			if _, err := q.SetTaskParent(ctx, generated.SetTaskParentParams{
				ParentTaskID:    p.Parent.ID,
				UpdatedByUserID: updatedBy,
				WorkspaceID:     args.WorkspaceID,
				ID:              args.TaskID,
			}); err != nil {
				return Result{}, err
			}
		}
	}

	updatedBy := sql.NullInt32{Int32: int32(args.Actor.UserID), Valid: args.Actor.UserID != 0} //#nosec G115 -- users.id fits int32 within realistic deployments
	// Not an existence check: re-sending the current values changes
	// nothing and MySQL counts zero. The row lock established existence.
	if _, err := q.UpdateTask(ctx, generated.UpdateTaskParams{
		Title:           newTitle.String(),
		Description:     newDesc,
		Priority:        newPriority,
		DueOn:           newDue,
		StartedOn:       newStart,
		SortWeight:      newSortWeight,
		Visibility:      newVisibility,
		UpdatedByUserID: updatedBy,
		WorkspaceID:     args.WorkspaceID,
		PublicID:        args.PublicID,
	}); err != nil {
		return Result{}, err
	}

	res := Result{TitleChanged: titleChanged, DescChanged: descChanged, NewTitle: newTitle, NewDesc: newDesc}
	userID, agentID := actorIDs(args.Actor)
	if descChanged || restoring {
		author, err := descriptionAuthor(ctx, q, args.WorkspaceID, args.Actor)
		if err != nil {
			return Result{}, err
		}
		// In the transaction that writes the body: a body committed
		// without its snapshot is one no restore can return to.
		res.Version, err = taskdesc.SnapshotWithActor(ctx, q, args.WorkspaceID, args.TaskID, author, newDesc.String)
		if err != nil {
			return Result{}, err
		}
		// A restore's own event names the version it created.
		if !restoring {
			if err := taskdesc.AnnounceWithActor(ctx, tx, taskdesc.Announcement{
				WorkspaceID:  args.WorkspaceID,
				TaskID:       args.TaskID,
				TaskPublicID: args.PublicID,
				Author:       userID,
				Version:      res.Version,
			}, author); err != nil {
				return Result{}, err
			}
		}
		// Beside the snapshot: a body edited down to nothing writes no
		// version and still has to stop naming the people it used to.
		if err := mentions.SyncTaskDescription(ctx, tx, mentions.TaskDescriptionArgs{
			WorkspaceID:  args.WorkspaceID,
			TaskID:       args.TaskID,
			TaskPublicID: args.PublicID,
			ActorUserID:  userID,
			ActorAgentID: agentID,
			Body:         newDesc.String,
		}); err != nil {
			return Result{}, err
		}
	}

	if needsItemkit && titleChanged {
		rename := itemkit.RenameItemArgs{
			WorkspaceID: args.WorkspaceID,
			ActorUserID: args.Actor.UserID,
			TaskID:      args.TaskID,
			NewTitle:    newTitle,
		}
		var err error
		if args.Actor.AgentID != 0 {
			_, err = itemkit.RenameItemAsAgent(ctx, tx, rename, args.Actor.AgentID)
		} else {
			_, err = itemkit.RenameItem(ctx, tx, rename)
		}
		if err != nil {
			return Result{}, &ItemkitError{Err: err}
		}
	}
	if needsItemkit && dueOnChanged {
		// An agent has no working week of its own, so its reschedule
		// resolves to no snapping.
		snap, err := itemkit.ResolveSnapConfig(ctx, tx, args.WorkspaceID, args.Actor.UserID)
		if err != nil {
			return Result{}, err
		}
		var due time.Time
		if newDue.Valid {
			due = newDue.Time
		}
		reschedule := itemkit.RescheduleTaskArgs{
			WorkspaceID: args.WorkspaceID,
			TaskID:      args.TaskID,
			ActorUserID: args.Actor.UserID,
			SetDueOn:    true,
			DueOn:       due,
			Snap:        snap,
		}
		if args.Actor.AgentID != 0 {
			err = itemkit.RescheduleTaskAsAgent(ctx, tx, reschedule, args.Actor.AgentID)
		} else {
			err = itemkit.RescheduleTask(ctx, tx, reschedule)
		}
		if err != nil {
			return Result{}, &ItemkitError{Err: err}
		}
	}

	kind := eventbus.TaskUpdated
	res.Payload = map[string]any{"taskId": args.PublicID.String()}
	if restoring {
		kind = eventbus.DescriptionVersionRestored
		res.Payload["restoredFrom"] = args.RestoredFrom.String()
		res.Payload["restoredVersionId"] = args.RestoredFrom.String()
		res.Payload["newVersionId"] = res.Version.PublicID.String()
		res.Payload["versionNumber"] = res.Version.Number
	}
	if parentChanged {
		if p.Parent.ID.Valid {
			res.Payload["parentTaskId"] = p.Parent.PublicID.String()
		} else {
			res.Payload["parentTaskId"] = nil
		}
	}
	if args.Via != "" {
		res.Payload["via"] = args.Via
	}
	taskID := int64(args.TaskID)
	if err := eventbus.Append(ctx, tx, eventbus.Event{
		Type:         kind,
		WorkspaceID:  args.WorkspaceID,
		ActorUserID:  userID,
		ActorAgentID: agentID,
		TaskID:       &taskID,
		Payload:      res.Payload,
	}); err != nil {
		return Result{}, err
	}
	return res, nil
}

// sameDate compares two nullable dates by calendar day.
func sameDate(a, b sql.NullTime) bool {
	if a.Valid != b.Valid {
		return false
	}
	return !a.Valid || a.Time.Format(time.DateOnly) == b.Time.Format(time.DateOnly)
}

// actorIDs widens the actor onto the optional user/agent pair the event
// row and the mention sync take; at most one is non-nil.
func actorIDs(a mutationlog.Actor) (userID, agentID *int64) {
	if a.AgentID != 0 {
		v := int64(a.AgentID)
		return nil, &v
	}
	if a.UserID != 0 {
		v := int64(a.UserID)
		return &v, nil
	}
	return nil, nil
}

// descriptionAuthor is the byline a description version carries. An
// agent's public id and name are copied so the history keeps naming it
// after the agent row is deleted.
func descriptionAuthor(ctx context.Context, q *generated.Queries, workspaceID uint32, a mutationlog.Actor) (taskdesc.Actor, error) {
	switch {
	case a.AgentID != 0:
		byline, err := q.GetMentionActorAgentByline(ctx, generated.GetMentionActorAgentBylineParams{
			WorkspaceID: workspaceID,
			ID:          a.AgentID,
		})
		if err != nil {
			return taskdesc.Actor{}, err
		}
		return taskdesc.Actor{
			AgentID:       sql.NullInt32{Int32: int32(a.AgentID), Valid: true}, //#nosec G115 -- ai_agents.id fits int32 within realistic deployments
			AgentPublicID: byline.PublicID,
			AgentName:     sql.NullString{String: byline.Name, Valid: true},
		}, nil
	case a.UserID != 0:
		return taskdesc.Actor{UserID: sql.NullInt32{Int32: int32(a.UserID), Valid: true}}, nil //#nosec G115 -- users.id fits int32 within realistic deployments
	}
	return taskdesc.Actor{}, nil
}
