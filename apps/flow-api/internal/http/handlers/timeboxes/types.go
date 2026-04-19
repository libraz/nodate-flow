// Package timeboxes contains Huma operation handlers for the
// /workspaces/{wsId}/timeboxes endpoints (timebox / sprint CRUD,
// task association, status transitions).
package timeboxes

import (
	"database/sql"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// Deps is the dependency bundle passed to each handler in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	Audit   *audit.Recorder
}

func httpErr(spec *apierrors.Spec) error {
	return huma.NewError(spec.Status, spec.Code+": "+spec.Message)
}

// nullStr delegates to handlerutil.NullStr.
var nullStr = handlerutil.NullStr

func nullDateStr(t sql.NullTime) *string {
	if !t.Valid {
		return nil
	}
	s := t.Time.Format("2006-01-02")
	return &s
}

// totalAsInt64 delegates to handlerutil.TotalAsInt64.
var totalAsInt64 = handlerutil.TotalAsInt64

// TimeboxDTO is the public DTO for a timebox row.
type TimeboxDTO struct {
	ID                 string `json:"id" doc:"Timebox public id (UUID v7)"`
	ProjectID          string `json:"projectId,omitempty"`
	ProjectName        string `json:"projectName,omitempty"`
	CreatorID          string `json:"creatorId"`
	CreatorDisplayName string `json:"creatorDisplayName"`
	Name               string `json:"name"`
	Description        string `json:"description,omitempty"`
	StartsOn           string `json:"startsOn" doc:"YYYY-MM-DD"`
	EndsOn             string `json:"endsOn" doc:"YYYY-MM-DD"`
	Status             string `json:"status"`
	UpdatedAt          int64  `json:"updatedAt"`
	CreatedAt          int64  `json:"createdAt"`
}

// TimeboxTaskDTO is the public DTO for a task within a timebox.
type TimeboxTaskDTO struct {
	ID           string  `json:"id" doc:"Task public id (UUID v7)"`
	Title        string  `json:"title"`
	DerivedState string  `json:"derivedState"`
	Priority     int32   `json:"priority"`
	DueOn        *string `json:"dueOn" doc:"YYYY-MM-DD or null"`
	StartedOn    *string `json:"startedOn" doc:"YYYY-MM-DD or null"`
	SortWeight   int32   `json:"sortWeight"`
	UpdatedAt    int64   `json:"updatedAt"`
	CreatedAt    int64   `json:"createdAt"`
}

// --- Create ---

// CreateTimeboxBody is the request body for POST /workspaces/{wsId}/timeboxes.
type CreateTimeboxBody struct {
	Name        string  `json:"name" minLength:"1" maxLength:"200"`
	Description string  `json:"description,omitempty" maxLength:"4000"`
	StartsOn    string  `json:"startsOn" doc:"YYYY-MM-DD"`
	EndsOn      string  `json:"endsOn" doc:"YYYY-MM-DD"`
	ProjectID   *string `json:"projectId,omitempty" doc:"Project public id; omit for workspace-wide"`
}

// CreateTimeboxInput is the input for POST /workspaces/{wsId}/timeboxes.
type CreateTimeboxInput struct {
	WsID string `path:"wsId"`
	Body CreateTimeboxBody
}

// CreateTimeboxOutput is the response for POST /workspaces/{wsId}/timeboxes.
type CreateTimeboxOutput struct {
	Body TimeboxDTO
}

// --- List ---

// ListTimeboxesInput is the query for GET /workspaces/{wsId}/timeboxes.
type ListTimeboxesInput struct {
	WsID   string `path:"wsId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListTimeboxesBody is the response body for GET /workspaces/{wsId}/timeboxes.
type ListTimeboxesBody struct {
	Total     int64        `json:"total"`
	Timeboxes []TimeboxDTO `json:"timeboxes"`
}

// ListTimeboxesOutput is the response for GET /workspaces/{wsId}/timeboxes.
type ListTimeboxesOutput struct {
	Body ListTimeboxesBody
}

// --- Get ---

// GetTimeboxInput is the path for GET /workspaces/{wsId}/timeboxes/{timeboxId}.
type GetTimeboxInput struct {
	WsID      string `path:"wsId"`
	TimeboxID string `path:"timeboxId"`
}

// GetTimeboxOutput is the response for GET /workspaces/{wsId}/timeboxes/{timeboxId}.
type GetTimeboxOutput struct {
	Body TimeboxDTO
}

// --- Update ---

// UpdateTimeboxBody is the request body for PATCH /workspaces/{wsId}/timeboxes/{timeboxId}.
type UpdateTimeboxBody struct {
	Name        *string `json:"name,omitempty" minLength:"1" maxLength:"200"`
	Description *string `json:"description,omitempty" maxLength:"4000"`
	StartsOn    *string `json:"startsOn,omitempty" doc:"YYYY-MM-DD"`
	EndsOn      *string `json:"endsOn,omitempty" doc:"YYYY-MM-DD"`
}

// UpdateTimeboxInput is the input for PATCH /workspaces/{wsId}/timeboxes/{timeboxId}.
type UpdateTimeboxInput struct {
	WsID      string `path:"wsId"`
	TimeboxID string `path:"timeboxId"`
	Body      UpdateTimeboxBody
}

// UpdateTimeboxOutput is the response for PATCH /workspaces/{wsId}/timeboxes/{timeboxId}.
type UpdateTimeboxOutput struct {
	Body TimeboxDTO
}

// --- UpdateStatus ---

// UpdateTimeboxStatusBody is the request body for POST /workspaces/{wsId}/timeboxes/{timeboxId}/status.
type UpdateTimeboxStatusBody struct {
	Status string `json:"status" doc:"Target status: planned, active, completed, cancelled"`
}

// UpdateTimeboxStatusInput is the input for POST /workspaces/{wsId}/timeboxes/{timeboxId}/status.
type UpdateTimeboxStatusInput struct {
	WsID      string `path:"wsId"`
	TimeboxID string `path:"timeboxId"`
	Body      UpdateTimeboxStatusBody
}

// UpdateTimeboxStatusOutput is the response for POST /workspaces/{wsId}/timeboxes/{timeboxId}/status.
type UpdateTimeboxStatusOutput struct {
	Body TimeboxDTO
}

// --- Delete ---

// DeleteTimeboxInput is the path for DELETE /workspaces/{wsId}/timeboxes/{timeboxId}.
type DeleteTimeboxInput struct {
	WsID      string `path:"wsId"`
	TimeboxID string `path:"timeboxId"`
}

// DeleteTimeboxBody is the response body for DELETE /workspaces/{wsId}/timeboxes/{timeboxId}.
type DeleteTimeboxBody struct {
	Ok bool `json:"ok"`
}

// DeleteTimeboxOutput is the response for DELETE /workspaces/{wsId}/timeboxes/{timeboxId}.
type DeleteTimeboxOutput struct {
	Body DeleteTimeboxBody
}

// --- AddTask ---

// AddTaskBody is the request body for POST /workspaces/{wsId}/timeboxes/{timeboxId}/tasks.
type AddTaskBody struct {
	TaskID string `json:"taskId" doc:"Task public id (UUID v7)"`
}

// AddTaskInput is the input for POST /workspaces/{wsId}/timeboxes/{timeboxId}/tasks.
type AddTaskInput struct {
	WsID      string `path:"wsId"`
	TimeboxID string `path:"timeboxId"`
	Body      AddTaskBody
}

// AddTaskOutputBody is the response body for POST /workspaces/{wsId}/timeboxes/{timeboxId}/tasks.
type AddTaskOutputBody struct {
	Ok bool `json:"ok"`
}

// AddTaskOutput is the response for POST /workspaces/{wsId}/timeboxes/{timeboxId}/tasks.
type AddTaskOutput struct {
	Body AddTaskOutputBody
}

// --- RemoveTask ---

// RemoveTaskInput is the path for DELETE /workspaces/{wsId}/timeboxes/{timeboxId}/tasks/{taskId}.
type RemoveTaskInput struct {
	WsID      string `path:"wsId"`
	TimeboxID string `path:"timeboxId"`
	TaskID    string `path:"taskId"`
}

// RemoveTaskOutputBody is the response body for DELETE /workspaces/{wsId}/timeboxes/{timeboxId}/tasks/{taskId}.
type RemoveTaskOutputBody struct {
	Ok bool `json:"ok"`
}

// RemoveTaskOutput is the response for DELETE /workspaces/{wsId}/timeboxes/{timeboxId}/tasks/{taskId}.
type RemoveTaskOutput struct {
	Body RemoveTaskOutputBody
}

// --- ListTasks ---

// ListTimeboxTasksInput is the query for GET /workspaces/{wsId}/timeboxes/{timeboxId}/tasks.
type ListTimeboxTasksInput struct {
	WsID      string `path:"wsId"`
	TimeboxID string `path:"timeboxId"`
	Limit     int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset    int32  `query:"offset" minimum:"0" default:"0"`
}

// ListTimeboxTasksBody is the response body for GET /workspaces/{wsId}/timeboxes/{timeboxId}/tasks.
type ListTimeboxTasksBody struct {
	Total          int64            `json:"total"`
	Tasks          []TimeboxTaskDTO `json:"tasks"`
	TotalTasks     int64            `json:"totalTasks"`
	CompletedTasks int64            `json:"completedTasks"`
}

// ListTimeboxTasksOutput is the response for GET /workspaces/{wsId}/timeboxes/{timeboxId}/tasks.
type ListTimeboxTasksOutput struct {
	Body ListTimeboxTasksBody
}
