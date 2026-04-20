// Package labels contains Huma operation handlers for workspace-scoped
// label CRUD and task-label association.
package labels

import (
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// Deps is the dependency bundle passed to each handler in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	Audit   *audit.Recorder
}

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// isDuplicateEntry delegates to handlerutil.IsDuplicateEntry.
var isDuplicateEntry = handlerutil.IsDuplicateEntry

// actorPtr delegates to handlerutil.ActorPtr.
var actorPtr = handlerutil.ActorPtr

// Label is the public DTO for a label row.
type Label struct {
	ID            string `json:"id" doc:"Label public id (UUID v7)"`
	ProjectID     string `json:"projectId,omitempty"`
	ParentLabelID string `json:"parentLabelId,omitempty"`
	Name          string `json:"name"`
	Color         string `json:"color"`
	Description   string `json:"description,omitempty"`
	SortWeight    int32  `json:"sortWeight"`
	UpdatedAt     *int64 `json:"updatedAt,omitempty"`
	CreatedAt     int64  `json:"createdAt"`
}

// TaskLabel is the public DTO for a task-label association.
type TaskLabel struct {
	ID          string `json:"id" doc:"Label public id (UUID v7)"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description,omitempty"`
	SortWeight  int32  `json:"sortWeight"`
	CreatedAt   int64  `json:"createdAt"`
}

// ---- Label CRUD I/O ----

// CreateLabelBody is the JSON body for POST /workspaces/{wsId}/labels.
type CreateLabelBody struct {
	ProjectID     string `json:"projectId,omitempty" doc:"Project public id to scope label (omit for workspace-wide)"`
	ParentLabelID string `json:"parentLabelId,omitempty" doc:"Parent label public id for hierarchy"`
	Name          string `json:"name" minLength:"1" maxLength:"64"`
	Color         string `json:"color,omitempty" maxLength:"16" default:"#6b7280"`
	Description   string `json:"description,omitempty" maxLength:"255"`
}

// CreateLabelInput is the request for POST /workspaces/{wsId}/labels.
type CreateLabelInput struct {
	WsID string `path:"wsId"`
	Body CreateLabelBody
}

// CreateLabelOutput is the response for POST /workspaces/{wsId}/labels.
type CreateLabelOutput struct {
	Body Label
}

// ListLabelsInput is the query for GET /workspaces/{wsId}/labels.
type ListLabelsInput struct {
	WsID      string `path:"wsId"`
	ProjectID string `query:"projectId" doc:"Optional project public id to filter"`
	Limit     int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset    int32  `query:"offset" minimum:"0" default:"0"`
}

// ListLabelsBody is the response payload for GET /workspaces/{wsId}/labels.
type ListLabelsBody struct {
	Total  int64   `json:"total"`
	Labels []Label `json:"labels"`
}

// ListLabelsOutput is the response for GET /workspaces/{wsId}/labels.
type ListLabelsOutput struct {
	Body ListLabelsBody
}

// GetLabelInput is the path for GET /workspaces/{wsId}/labels/{id}.
type GetLabelInput struct {
	WsID string `path:"wsId"`
	ID   string `path:"id"`
}

// GetLabelOutput is the response for GET /workspaces/{wsId}/labels/{id}.
type GetLabelOutput struct {
	Body Label
}

// PatchLabelBody is the JSON body for PATCH /workspaces/{wsId}/labels/{id}.
type PatchLabelBody struct {
	Name          *string `json:"name,omitempty" minLength:"1" maxLength:"64"`
	Color         *string `json:"color,omitempty" maxLength:"16"`
	Description   *string `json:"description,omitempty" maxLength:"255"`
	ParentLabelID *string `json:"parentLabelId,omitempty"`
	SortWeight    *int32  `json:"sortWeight,omitempty"`
}

// PatchLabelInput is the request for PATCH /workspaces/{wsId}/labels/{id}.
type PatchLabelInput struct {
	WsID string `path:"wsId"`
	ID   string `path:"id"`
	Body PatchLabelBody
}

// PatchLabelOutput is the response for PATCH /workspaces/{wsId}/labels/{id}.
type PatchLabelOutput struct {
	Body Label
}

// DisableLabelInput is the path for DELETE /workspaces/{wsId}/labels/{id}.
type DisableLabelInput struct {
	WsID string `path:"wsId"`
	ID   string `path:"id"`
}

// DisableLabelOutput is the response for DELETE /workspaces/{wsId}/labels/{id}.
type DisableLabelOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// ---- Task-Label I/O ----

// AddTaskLabelBody is the JSON body for POST /tasks/{id}/labels.
type AddTaskLabelBody struct {
	LabelID string `json:"labelId" doc:"Label public id (UUID v7)"`
}

// AddTaskLabelInput is the request for POST /tasks/{id}/labels.
type AddTaskLabelInput struct {
	ID   string `path:"id"`
	Body AddTaskLabelBody
}

// AddTaskLabelOutput is the response for POST /tasks/{id}/labels.
type AddTaskLabelOutput struct {
	Body TaskLabel
}

// ListTaskLabelsInput is the path for GET /tasks/{id}/labels.
type ListTaskLabelsInput struct {
	ID string `path:"id"`
}

// ListTaskLabelsBody is the response payload for GET /tasks/{id}/labels.
type ListTaskLabelsBody struct {
	Labels []TaskLabel `json:"labels"`
}

// ListTaskLabelsOutput is the response for GET /tasks/{id}/labels.
type ListTaskLabelsOutput struct {
	Body ListTaskLabelsBody
}

// RemoveTaskLabelInput is the path for DELETE /tasks/{id}/labels/{labelId}.
type RemoveTaskLabelInput struct {
	ID      string `path:"id"`
	LabelID string `path:"labelId"`
}

// RemoveTaskLabelOutput is the response for DELETE /tasks/{id}/labels/{labelId}.
type RemoveTaskLabelOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}
