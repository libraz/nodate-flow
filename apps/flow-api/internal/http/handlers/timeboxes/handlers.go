package timeboxes

import (
	"context"
	"database/sql"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

const dateLayout = "2006-01-02"

// timeboxActor is the [mutationlog.Actor] both halves of a record are
// stamped with, so the event row and the audit row cannot name different
// people for one change.
//
// The actor is read once, here, rather than at each half: a context
// carrying nobody must not decide that one table gets a row and the
// other does not. Zero is the recorder's "no authenticated user", and
// both rows then carry a NULL actor rather than one of them being
// skipped.
func timeboxActor(ctx context.Context, workspaceID uint32) mutationlog.Actor {
	actorID, _ := middleware.ActorFromContext(ctx)
	return mutationlog.Actor{UserID: actorID, WorkspaceID: workspaceID}
}

// publicIDOrEmpty delegates to handlerutil.PublicIDOrEmpty.
var publicIDOrEmpty = handlerutil.PublicIDOrEmpty

// isDuplicateEntry delegates to handlerutil.IsDuplicateEntry.
var isDuplicateEntry = handlerutil.IsDuplicateEntry

// resolveTaskInternal looks up the internal task id by workspace_id + public_id.
// This avoids pulling the full task detail view when only the numeric PK is needed.
func resolveTaskInternal(ctx context.Context, db *sql.DB, wsID uint32, pub types.PublicID) (uint32, error) {
	const q = `SELECT id FROM tasks WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1`
	var id uint32
	if err := db.QueryRowContext(ctx, q, wsID, pub).Scan(&id); err != nil {
		return 0, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsTaskNotFound, apierrors.InternalUnexpected))
	}
	return id, nil
}

// mapGetRow converts a GetTimeboxByPublicIdRow to a TimeboxDTO.
func mapGetRow(r generated.GetTimeboxByPublicIdRow) TimeboxDTO {
	return TimeboxDTO{
		ID:                 r.PublicID.String(),
		ProjectID:          publicIDOrEmpty(r.ProjectPublicID),
		ProjectName:        nullStr(r.ProjectName),
		CreatorID:          r.CreatorPublicID.String(),
		CreatorDisplayName: r.CreatorDisplayName,
		Name:               r.Name,
		Description:        nullStr(r.Description),
		StartsOn:           r.StartsOn.Format(dateLayout),
		EndsOn:             r.EndsOn.Format(dateLayout),
		Status:             string(r.Status),
		UpdatedAt:          nullTimeUnix(r.UpdatedAt),
		CreatedAt:          r.CreatedAt.Unix(),
	}
}

// mapListRow converts a ListTimeboxesForWorkspaceRow to a TimeboxDTO.
func mapListRow(r generated.ListTimeboxesForWorkspaceRow) TimeboxDTO {
	return TimeboxDTO{
		ID:                 r.PublicID.String(),
		ProjectID:          publicIDOrEmpty(r.ProjectPublicID),
		ProjectName:        nullStr(r.ProjectName),
		CreatorID:          r.CreatorPublicID.String(),
		CreatorDisplayName: r.CreatorDisplayName,
		Name:               r.Name,
		Description:        nullStr(r.Description),
		StartsOn:           r.StartsOn.Format(dateLayout),
		EndsOn:             r.EndsOn.Format(dateLayout),
		Status:             string(r.Status),
		UpdatedAt:          nullTimeUnix(r.UpdatedAt),
		CreatedAt:          r.CreatedAt.Unix(),
	}
}

// mapTaskRow converts a ListTasksForTimeboxRow to a TimeboxTaskDTO.
func mapTaskRow(r generated.ListTasksForTimeboxRow) TimeboxTaskDTO {
	return TimeboxTaskDTO{
		ID:           r.PublicID.String(),
		Title:        r.Title,
		DerivedState: string(r.DerivedState),
		Priority:     r.Priority,
		DueOn:        nullDateStr(r.DueOn),
		StartedOn:    nullDateStr(r.StartedOn),
		SortWeight:   r.SortWeight,
		UpdatedAt:    nullTimeUnix(r.UpdatedAt),
		CreatedAt:    r.CreatedAt.Unix(),
	}
}

// nullTimeUnix delegates to handlerutil.NullTimeUnixVal (returns int64, 0 for NULL).
var nullTimeUnix = handlerutil.NullTimeUnixVal

// Create handles POST /workspaces/{wsId}/timeboxes.
func Create(deps Deps) func(context.Context, *CreateTimeboxInput) (*CreateTimeboxOutput, error) {
	return func(ctx context.Context, in *CreateTimeboxInput) (*CreateTimeboxOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsMemberRoleDenied)
		}

		// Parse and validate dates.
		startsOn, err := time.Parse(dateLayout, in.Body.StartsOn)
		if err != nil {
			return nil, httpErr(apierrors.TimeboxTimeboxInvalidDates)
		}
		endsOn, err := time.Parse(dateLayout, in.Body.EndsOn)
		if err != nil {
			return nil, httpErr(apierrors.TimeboxTimeboxInvalidDates)
		}
		if !endsOn.After(startsOn) {
			return nil, httpErr(apierrors.TimeboxTimeboxInvalidDates)
		}

		// Resolve optional project public_id -> internal id.
		var projectID sql.NullInt32
		if in.Body.ProjectID != nil && *in.Body.ProjectID != "" {
			pid, err := types.Parse(*in.Body.ProjectID)
			if err != nil {
				return nil, httpErr(apierrors.ValidationPathParamInvalid)
			}
			row, err := deps.Queries.FindProjectByPublicId(ctx, generated.FindProjectByPublicIdParams{
				WorkspaceID: ws.ID,
				PublicID:    pid,
			})
			if err != nil {
				return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectNotFound, apierrors.InternalUnexpected))
			}
			projectID = sql.NullInt32{Int32: int32(row.ID), Valid: true} //#nosec G115 -- project_id is projects.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		}

		pub := types.New()
		_, err = deps.Queries.CreateTimebox(ctx, generated.CreateTimeboxParams{
			PublicID:    pub,
			WorkspaceID: ws.ID,
			ProjectID:   projectID,
			CreatorID:   actorID,
			Name:        in.Body.Name,
			Description: sql.NullString{String: in.Body.Description, Valid: in.Body.Description != ""},
			StartsOn:    startsOn,
			EndsOn:      endsOn,
		})
		if err != nil {
			if isDuplicateEntry(err) {
				return nil, httpErr(apierrors.TimeboxTimeboxNameTaken)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// One record for both halves, described by one payload: two
		// descriptions of one change drift, and a reader comparing the
		// tables then cannot tell which is stale. The timebox row is
		// committed on its own connection, so a lost record must not
		// answer with an error the client retries into a second timebox.
		deps.Mutations.Record(ctx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
			EventType:    eventbus.TimeboxCreated,
			AuditAction:  "timebox.create",
			ResourceType: "timebox",
			ResourceID:   pub.String(),
			Payload: map[string]any{
				"timeboxId": pub.String(),
				"name":      in.Body.Name,
			},
			CallSite: "timeboxes.Create",
		})

		// Re-read through the same query Get uses so the created timebox
		// carries the creator summary and project name. Hand-building the
		// response from the request body left those three fields blank on
		// exactly one code path, which a client rendering the new row
		// straight from the create response reads as "no creator".
		created, err := deps.Queries.GetTimeboxByPublicId(ctx, generated.GetTimeboxByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.TimeboxTimeboxNotFound, apierrors.InternalUnexpected))
		}

		return &CreateTimeboxOutput{Body: mapGetRow(created)}, nil
	}
}

// List handles GET /workspaces/{wsId}/timeboxes.
func List(deps Deps) func(context.Context, *ListTimeboxesInput) (*ListTimeboxesOutput, error) {
	return func(ctx context.Context, in *ListTimeboxesInput) (*ListTimeboxesOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		rows, err := deps.Queries.ListTimeboxesForWorkspace(ctx, generated.ListTimeboxesForWorkspaceParams{
			WorkspaceID: ws.ID,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListTimeboxesOutput{}
		out.Body.Timeboxes = make([]TimeboxDTO, 0, len(rows))
		for _, r := range rows {
			out.Body.Timeboxes = append(out.Body.Timeboxes, mapListRow(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// Get handles GET /workspaces/{wsId}/timeboxes/{timeboxId}.
func Get(deps Deps) func(context.Context, *GetTimeboxInput) (*GetTimeboxOutput, error) {
	return func(ctx context.Context, in *GetTimeboxInput) (*GetTimeboxOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		pub, err := types.Parse(in.TimeboxID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		row, err := deps.Queries.GetTimeboxByPublicId(ctx, generated.GetTimeboxByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.TimeboxTimeboxNotFound, apierrors.InternalUnexpected))
		}
		return &GetTimeboxOutput{Body: mapGetRow(row)}, nil
	}
}

// Update handles PATCH /workspaces/{wsId}/timeboxes/{timeboxId}.
func Update(deps Deps) func(context.Context, *UpdateTimeboxInput) (*UpdateTimeboxOutput, error) {
	return func(ctx context.Context, in *UpdateTimeboxInput) (*UpdateTimeboxOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		pub, err := types.Parse(in.TimeboxID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		// Fetch existing for merge.
		existing, err := deps.Queries.GetTimeboxByPublicId(ctx, generated.GetTimeboxByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.TimeboxTimeboxNotFound, apierrors.InternalUnexpected))
		}

		// Block updates on completed timeboxes.
		if existing.Status == generated.TimeboxesStatusCompleted {
			return nil, httpErr(apierrors.TimeboxTimeboxAlreadyCompleted)
		}

		// Merge fields.
		name := existing.Name
		if in.Body.Name != nil {
			name = *in.Body.Name
		}
		description := existing.Description
		if in.Body.Description != nil {
			description = sql.NullString{String: *in.Body.Description, Valid: *in.Body.Description != ""}
		}
		startsOn := existing.StartsOn
		if in.Body.StartsOn != nil {
			startsOn, err = time.Parse(dateLayout, *in.Body.StartsOn)
			if err != nil {
				return nil, httpErr(apierrors.TimeboxTimeboxInvalidDates)
			}
		}
		endsOn := existing.EndsOn
		if in.Body.EndsOn != nil {
			endsOn, err = time.Parse(dateLayout, *in.Body.EndsOn)
			if err != nil {
				return nil, httpErr(apierrors.TimeboxTimeboxInvalidDates)
			}
		}
		if !endsOn.After(startsOn) {
			return nil, httpErr(apierrors.TimeboxTimeboxInvalidDates)
		}

		// Not an existence check: a PATCH re-sending the timebox's current
		// name and dates changes nothing and MySQL counts zero. The re-read
		// below is what fails if the timebox is gone.
		if _, err := deps.Queries.UpdateTimebox(ctx, generated.UpdateTimeboxParams{
			Name:        name,
			Description: description,
			StartsOn:    startsOn,
			EndsOn:      endsOn,
			WorkspaceID: ws.ID,
			PublicID:    pub,
		}); err != nil {
			if isDuplicateEntry(err) {
				return nil, httpErr(apierrors.TimeboxTimeboxNameTaken)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// The UPDATE is committed on its own connection; see Create for
		// why one record covers both halves and why a lost one must not
		// fail the request.
		deps.Mutations.Record(ctx, timeboxActor(ctx, ws.ID), mutationlog.Mutation{
			EventType:    eventbus.TimeboxUpdated,
			AuditAction:  "timebox.update",
			ResourceType: "timebox",
			ResourceID:   pub.String(),
			Payload: map[string]any{
				"timeboxId": pub.String(),
				"name":      name,
			},
			CallSite: "timeboxes.Update",
		})

		// Re-fetch to return the updated row.
		updated, err := deps.Queries.GetTimeboxByPublicId(ctx, generated.GetTimeboxByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &UpdateTimeboxOutput{Body: mapGetRow(updated)}, nil
	}
}

// UpdateStatus handles POST /workspaces/{wsId}/timeboxes/{timeboxId}/status.
func UpdateStatus(deps Deps) func(context.Context, *UpdateTimeboxStatusInput) (*UpdateTimeboxStatusOutput, error) {
	return func(ctx context.Context, in *UpdateTimeboxStatusInput) (*UpdateTimeboxStatusOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		pub, err := types.Parse(in.TimeboxID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		existing, err := deps.Queries.GetTimeboxByPublicId(ctx, generated.GetTimeboxByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.TimeboxTimeboxNotFound, apierrors.InternalUnexpected))
		}

		if existing.Status == generated.TimeboxesStatusCompleted {
			return nil, httpErr(apierrors.TimeboxTimeboxAlreadyCompleted)
		}

		targetStatus := generated.TimeboxesStatus(in.Body.Status)
		// Not an existence check: setting the status the timebox already
		// holds changes nothing and MySQL counts zero. The re-read below is
		// what fails if the timebox is gone.
		if _, err := deps.Queries.UpdateTimeboxStatus(ctx, generated.UpdateTimeboxStatusParams{
			Status:      targetStatus,
			WorkspaceID: ws.ID,
			PublicID:    pub,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Emit the appropriate event.
		evtType := eventbus.TimeboxUpdated
		switch targetStatus {
		case generated.TimeboxesStatusActive:
			evtType = eventbus.TimeboxActivated
		case generated.TimeboxesStatusCompleted:
			evtType = eventbus.TimeboxCompleted
		}
		// The transition is on the timeline under the kind the target
		// status selects and in audit_logs under one action name, so an
		// administrator can query every transition without knowing which
		// kinds exist.
		deps.Mutations.Record(ctx, timeboxActor(ctx, ws.ID), mutationlog.Mutation{
			EventType:    evtType,
			AuditAction:  "timebox.status",
			ResourceType: "timebox",
			ResourceID:   pub.String(),
			Payload: map[string]any{
				"timeboxId": pub.String(),
				"status":    in.Body.Status,
			},
			CallSite: "timeboxes.UpdateStatus",
		})

		// Re-fetch to return the updated row.
		updated, err := deps.Queries.GetTimeboxByPublicId(ctx, generated.GetTimeboxByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &UpdateTimeboxStatusOutput{Body: mapGetRow(updated)}, nil
	}
}

// Delete handles DELETE /workspaces/{wsId}/timeboxes/{timeboxId}.
func Delete(deps Deps) func(context.Context, *DeleteTimeboxInput) (*DeleteTimeboxOutput, error) {
	return func(ctx context.Context, in *DeleteTimeboxInput) (*DeleteTimeboxOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		pub, err := types.Parse(in.TimeboxID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		// Scoped to the workspace and to timeboxes that are still live, so a
		// zero count means there is no such timebox to delete. Reporting
		// success wrote an audit entry for a delete that never happened.
		rows, err := deps.Queries.DisableTimebox(ctx, generated.DisableTimeboxParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if rows == 0 {
			return nil, httpErr(apierrors.TimeboxTimeboxNotFound)
		}

		// The statement clears `enabled`, so the timebox is archived
		// rather than erased and the kind says so. Recorded only past
		// the zero-row check: a second DELETE matches nothing and
		// answers not found, and a record written there would claim an
		// archival that already happened once.
		deps.Mutations.Record(ctx, timeboxActor(ctx, ws.ID), mutationlog.Mutation{
			EventType:    eventbus.TimeboxArchived,
			AuditAction:  "timebox.delete",
			ResourceType: "timebox",
			ResourceID:   pub.String(),
			Payload: map[string]any{
				"timeboxId": pub.String(),
			},
			CallSite: "timeboxes.Delete",
		})

		out := &DeleteTimeboxOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// AddTask handles POST /workspaces/{wsId}/timeboxes/{timeboxId}/tasks.
func AddTask(deps Deps) func(context.Context, *AddTaskInput) (*AddTaskOutput, error) {
	return func(ctx context.Context, in *AddTaskInput) (*AddTaskOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsMemberRoleDenied)
		}

		// Resolve timebox public_id -> internal id.
		tbPub, err := types.Parse(in.TimeboxID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		tb, err := deps.Queries.GetTimeboxByPublicId(ctx, generated.GetTimeboxByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    tbPub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.TimeboxTimeboxNotFound, apierrors.InternalUnexpected))
		}

		if tb.Status == generated.TimeboxesStatusCompleted {
			return nil, httpErr(apierrors.TimeboxTimeboxAlreadyCompleted)
		}

		// Resolve task public_id -> internal id.
		taskPub, err := types.Parse(in.Body.TaskID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		taskID, err := resolveTaskInternal(ctx, deps.DB, ws.ID, taskPub)
		if err != nil {
			return nil, err
		}

		// Adding a task to a timebox puts its title on the timebox list,
		// so it takes the same right as reading it. Without this a task
		// the actor cannot see could be pulled into a timebox they can,
		// which is a read of someone else's task through a write.
		if _, err := acl.AuthorizeTaskAccess(ctx, deps.DB, taskPub.UUID(), actorID, apierrors.WsTaskAccessDenied); err != nil {
			return nil, err
		}

		linkPub := types.New()
		if err := deps.Queries.AddTaskToTimebox(ctx, generated.AddTaskToTimeboxParams{
			PublicID:    linkPub,
			WorkspaceID: ws.ID,
			TimeboxID:   tb.ID,
			TaskID:      taskID,
		}); err != nil {
			if isDuplicateEntry(err) {
				return nil, httpErr(apierrors.TimeboxTaskAlreadyAdded)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// The link row is committed and carries a unique key, so a retry
		// is refused before it could record the change twice. The task
		// is named in its canonical form, which is the spelling every
		// other record of this task uses.
		//
		// TaskID carries the internal id because events.task_id is a
		// foreign key, and it is what the task's own timeline selects
		// on: without it the change is filed against the timebox alone
		// and never appears on the task it moved. The payload keeps the
		// public id, which is the only spelling that leaves the service.
		addedTaskID := int64(taskID)
		deps.Mutations.Record(ctx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
			EventType:    eventbus.TimeboxTaskAdded,
			AuditAction:  "timebox.task.add",
			ResourceType: "timebox",
			ResourceID:   tbPub.String(),
			TaskID:       &addedTaskID,
			Payload: map[string]any{
				"timeboxId": tbPub.String(),
				"taskId":    taskPub.String(),
			},
			CallSite: "timeboxes.AddTask",
		})

		out := &AddTaskOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// RemoveTask handles DELETE /workspaces/{wsId}/timeboxes/{timeboxId}/tasks/{taskId}.
func RemoveTask(deps Deps) func(context.Context, *RemoveTaskInput) (*RemoveTaskOutput, error) {
	return func(ctx context.Context, in *RemoveTaskInput) (*RemoveTaskOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		// Resolve timebox.
		tbPub, err := types.Parse(in.TimeboxID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		tb, err := deps.Queries.GetTimeboxByPublicId(ctx, generated.GetTimeboxByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    tbPub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.TimeboxTimeboxNotFound, apierrors.InternalUnexpected))
		}

		// Resolve task.
		taskPub, err := types.Parse(in.TaskID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		taskID, err := resolveTaskInternal(ctx, deps.DB, ws.ID, taskPub)
		if err != nil {
			return nil, err
		}

		// Nothing checks the membership row before this, so the count is
		// the only thing that knows the task was in the timebox at all:
		// removing a task that was never added answered ok and wrote an
		// audit entry saying it had been removed.
		rows, err := deps.Queries.RemoveTaskFromTimebox(ctx, generated.RemoveTaskFromTimeboxParams{
			TimeboxID: tb.ID,
			TaskID:    taskID,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if rows == 0 {
			return nil, httpErr(apierrors.TimeboxTaskNotFound)
		}

		// Recorded only past the zero-row check: removing a task that was
		// never in the timebox answers not found, and neither table may
		// carry a removal that did not happen. The task is named in its
		// canonical form, matching the record written when it was added,
		// and TaskID links the row the same way so the task's timeline
		// shows the removal beside the addition rather than only one of
		// the two.
		removedTaskID := int64(taskID)
		deps.Mutations.Record(ctx, timeboxActor(ctx, ws.ID), mutationlog.Mutation{
			EventType:    eventbus.TimeboxTaskRemoved,
			AuditAction:  "timebox.task.remove",
			ResourceType: "timebox",
			ResourceID:   tbPub.String(),
			TaskID:       &removedTaskID,
			Payload: map[string]any{
				"timeboxId": tbPub.String(),
				"taskId":    taskPub.String(),
			},
			CallSite: "timeboxes.RemoveTask",
		})

		out := &RemoveTaskOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// ListTasks handles GET /workspaces/{wsId}/timeboxes/{timeboxId}/tasks.
func ListTasks(deps Deps) func(context.Context, *ListTimeboxTasksInput) (*ListTimeboxTasksOutput, error) {
	return func(ctx context.Context, in *ListTimeboxTasksInput) (*ListTimeboxTasksOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsMemberRoleDenied)
		}

		// Resolve timebox.
		tbPub, err := types.Parse(in.TimeboxID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		tb, err := deps.Queries.GetTimeboxByPublicId(ctx, generated.GetTimeboxByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    tbPub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.TimeboxTimeboxNotFound, apierrors.InternalUnexpected))
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		// Belonging to a timebox says nothing about who may read the
		// task, so the list and the progress counts both run through the
		// visibility predicate. Counting the unfiltered set would answer
		// "how many are hidden from you", which is the same disclosure
		// by arithmetic.
		vis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole(ws.Role))
		rows, err := deps.Queries.ListTasksForTimebox(ctx, generated.ListTasksForTimeboxParams{
			TimeboxID:     tb.ID,
			IsElevated:    vis.IsElevated,
			ActorUserID:   vis.ActorUserID,
			ActorUserID_2: vis.ActorUserID,
			ActorUserID_3: vis.ActorUserID,
			Limit:         limit,
			Offset:        in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Fetch progress counts.
		progress, err := deps.Queries.CountTasksForTimebox(ctx, generated.CountTasksForTimeboxParams{
			TimeboxID:     tb.ID,
			IsElevated:    vis.IsElevated,
			ActorUserID:   vis.ActorUserID,
			ActorUserID_2: vis.ActorUserID,
			ActorUserID_3: vis.ActorUserID,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListTimeboxTasksOutput{}
		out.Body.Tasks = make([]TimeboxTaskDTO, 0, len(rows))
		for _, r := range rows {
			out.Body.Tasks = append(out.Body.Tasks, mapTaskRow(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		out.Body.TotalTasks = progress.TotalTasks
		out.Body.CompletedTasks = totalAsInt64(progress.CompletedTasks)
		return out, nil
	}
}
