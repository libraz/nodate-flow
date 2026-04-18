package timeboxes

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

const dateLayout = "2006-01-02"

// actorPtr returns a pointer to the actor's internal user id for
// eventbus.Event, or nil if not available.
func actorPtr(ctx context.Context) *int64 {
	uid, ok := middleware.ActorFromContext(ctx)
	if !ok {
		return nil
	}
	v := int64(uid)
	return &v
}

// publicIDOrEmpty returns the UUID string of a types.PublicID, or ""
// when it is the zero value (i.e. a LEFT JOIN returned NULL).
func publicIDOrEmpty(p types.PublicID) string {
	var zero types.PublicID
	if p == zero {
		return ""
	}
	return p.String()
}

// isDuplicateEntry detects MySQL error 1062 without taking a hard
// dependency on the mysql driver package.
func isDuplicateEntry(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "Error 1062") || strings.Contains(s, "Duplicate entry")
}

// resolveTaskInternal looks up the internal task id by workspace_id + public_id.
// This avoids pulling the full task detail view when only the numeric PK is needed.
func resolveTaskInternal(ctx context.Context, db *sql.DB, wsID uint32, pub types.PublicID) (uint32, error) {
	const q = `SELECT id FROM tasks WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1`
	var id uint32
	if err := db.QueryRowContext(ctx, q, wsID, pub).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, httpErr(apierrors.WsTaskNotFound)
		}
		return 0, httpErr(apierrors.InternalUnexpected)
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

// nullTimeUnix converts a sql.NullTime to a unix-seconds int64.
// For timeboxes the UpdatedAt is always set after any mutation, so
// we fall back to 0 only for the initial NULL case.
func nullTimeUnix(t sql.NullTime) int64 {
	if !t.Valid {
		return 0
	}
	return t.Time.Unix()
}

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
				if errors.Is(err, sql.ErrNoRows) {
					return nil, httpErr(apierrors.WsProjectNotFound)
				}
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			projectID = sql.NullInt32{Int32: int32(row.ID), Valid: true}
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

		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TimeboxCreated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload: map[string]any{
				"timeboxId": pub.String(),
				"name":      in.Body.Name,
			},
		})

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "timebox.create",
			ActorID:      actorID,
			WorkspaceID:  ws.ID,
			ResourceType: "timebox",
			ResourceID:   pub.String(),
			Metadata:     map[string]any{"name": in.Body.Name},
		})

		return &CreateTimeboxOutput{Body: TimeboxDTO{
			ID:                 pub.String(),
			ProjectID:          ptrStr(in.Body.ProjectID),
			CreatorID:          "", // not resolved here; get via Get if needed
			CreatorDisplayName: "",
			Name:               in.Body.Name,
			Description:        in.Body.Description,
			StartsOn:           in.Body.StartsOn,
			EndsOn:             in.Body.EndsOn,
			Status:             string(generated.TimeboxesStatusPlanned),
			UpdatedAt:          0,
			CreatedAt:          time.Now().Unix(),
		}}, nil
	}
}

// ptrStr safely dereferences a *string, returning "" if nil.
func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.TimeboxTimeboxNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
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
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.TimeboxTimeboxNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
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

		if err := deps.Queries.UpdateTimebox(ctx, generated.UpdateTimeboxParams{
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

		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TimeboxUpdated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload: map[string]any{
				"timeboxId": pub.String(),
				"name":      name,
			},
		})

		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "timebox.update",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "timebox",
				ResourceID:   pub.String(),
			})
		}

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
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.TimeboxTimeboxNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if existing.Status == generated.TimeboxesStatusCompleted {
			return nil, httpErr(apierrors.TimeboxTimeboxAlreadyCompleted)
		}

		targetStatus := generated.TimeboxesStatus(in.Body.Status)
		if err := deps.Queries.UpdateTimeboxStatus(ctx, generated.UpdateTimeboxStatusParams{
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
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        evtType,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload: map[string]any{
				"timeboxId": pub.String(),
				"status":    in.Body.Status,
			},
		})

		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "timebox.status",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "timebox",
				ResourceID:   pub.String(),
				Metadata:     map[string]any{"status": in.Body.Status},
			})
		}

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
		if err := deps.Queries.DisableTimebox(ctx, generated.DisableTimeboxParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "timebox.delete",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "timebox",
				ResourceID:   pub.String(),
			})
		}

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
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.TimeboxTimeboxNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
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

		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TimeboxTaskAdded,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload: map[string]any{
				"timeboxId": tbPub.String(),
				"taskId":    taskPub.String(),
			},
		})

		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "timebox.task.add",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "timebox",
				ResourceID:   tbPub.String(),
				Metadata:     map[string]any{"taskId": in.Body.TaskID},
			})
		}

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
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.TimeboxTimeboxNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
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

		if err := deps.Queries.RemoveTaskFromTimebox(ctx, generated.RemoveTaskFromTimeboxParams{
			TimeboxID: tb.ID,
			TaskID:    taskID,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TimeboxTaskRemoved,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload: map[string]any{
				"timeboxId": tbPub.String(),
				"taskId":    taskPub.String(),
			},
		})

		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "timebox.task.remove",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "timebox",
				ResourceID:   tbPub.String(),
				Metadata:     map[string]any{"taskId": in.TaskID},
			})
		}

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
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.TimeboxTimeboxNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		rows, err := deps.Queries.ListTasksForTimebox(ctx, generated.ListTasksForTimeboxParams{
			TimeboxID: tb.ID,
			Limit:     limit,
			Offset:    in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Fetch progress counts.
		progress, err := deps.Queries.CountTasksForTimebox(ctx, tb.ID)
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
