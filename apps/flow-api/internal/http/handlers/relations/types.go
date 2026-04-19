// Package relations contains Huma operation handlers for the
// relation suggestion endpoints (list for workspace, list for task,
// resolve).
package relations

import (
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// Deps is the dependency bundle for handlers in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	Audit   *audit.Recorder
}

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// SuggestionDTO is the public DTO for a relation suggestion row.
type SuggestionDTO struct {
	ID              string  `json:"id" doc:"Suggestion public id (UUID v7)"`
	SuggestedKind   string  `json:"suggestedKind" doc:"Suggested relation kind (blocks, relates, duplicates)"`
	Confidence      float64 `json:"confidence" doc:"Cosine similarity score, e.g. 0.85"`
	Status          string  `json:"status" doc:"Resolution status (pending, accepted, dismissed)"`
	SourceTaskID    string  `json:"sourceTaskId" doc:"Source task public id (UUID v7)"`
	SourceTaskTitle string  `json:"sourceTaskTitle" doc:"Source task title"`
	TargetTaskID    string  `json:"targetTaskId" doc:"Target task public id (UUID v7)"`
	TargetTaskTitle string  `json:"targetTaskTitle" doc:"Target task title"`
	CreatedAt       int64   `json:"createdAt" doc:"Unix timestamp (seconds)"`
}

// --- ListForWorkspace ---

// ListForWorkspaceInput is the query for GET /workspaces/{wsId}/relation-suggestions.
type ListForWorkspaceInput struct {
	WsID   string `path:"wsId" doc:"Workspace public id (UUID v7)"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListForWorkspaceBody is the response body for GET /workspaces/{wsId}/relation-suggestions.
type ListForWorkspaceBody struct {
	Total       int64           `json:"total"`
	Suggestions []SuggestionDTO `json:"suggestions"`
	NextCursor  *string         `json:"nextCursor"`
}

// ListForWorkspaceOutput is the response for GET /workspaces/{wsId}/relation-suggestions.
type ListForWorkspaceOutput struct {
	Body ListForWorkspaceBody
}

// --- ListForTask ---

// ListForTaskInput is the query for GET /tasks/{id}/relation-suggestions.
type ListForTaskInput struct {
	ID    string `path:"id" doc:"Task public id (UUID v7)"`
	Limit int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

// ListForTaskBody is the response body for GET /tasks/{taskId}/relation-suggestions.
type ListForTaskBody struct {
	Suggestions []SuggestionDTO `json:"suggestions"`
}

// ListForTaskOutput is the response for GET /tasks/{taskId}/relation-suggestions.
type ListForTaskOutput struct {
	Body ListForTaskBody
}

// --- Resolve ---

// ResolveInput is the input for POST /relation-suggestions/{suggestionId}/resolve.
type ResolveInput struct {
	SuggestionID string `path:"suggestionId" doc:"Suggestion public id (UUID v7)"`
	Body         ResolveBody
}

// ResolveBody is the request body for POST /relation-suggestions/{suggestionId}/resolve.
type ResolveBody struct {
	Action string `json:"action" enum:"accept,dismiss" doc:"Action to take on the suggestion"`
}

// ResolveOutputBody is the response body for POST /relation-suggestions/{suggestionId}/resolve.
type ResolveOutputBody struct {
	Ok bool `json:"ok"`
}

// ResolveOutput is the response for POST /relation-suggestions/{suggestionId}/resolve.
type ResolveOutput struct {
	Body ResolveOutputBody
}
