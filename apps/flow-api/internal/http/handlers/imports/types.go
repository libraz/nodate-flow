// Package imports contains Huma operation handlers for workspace-scoped
// import job CRUD (/workspaces/{wsId}/imports). Import jobs track bulk
// imports from external systems (GitHub, Jira, Linear, CSV).
package imports

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

// actorPtr delegates to handlerutil.ActorPtr.
var actorPtr = handlerutil.ActorPtr

// totalAsInt64 delegates to handlerutil.TotalAsInt64.
var totalAsInt64 = handlerutil.TotalAsInt64

// nullStrPtr delegates to handlerutil.NullStrPtr.
var nullStrPtr = handlerutil.NullStrPtr

// nullTimeUnix delegates to handlerutil.NullTimeUnix.
var nullTimeUnix = handlerutil.NullTimeUnix

// ImportJobBody is the public DTO for an import job row.
type ImportJobBody struct {
	ID             string  `json:"id" doc:"Import job public id (UUID v7)"`
	Source         string  `json:"source"`
	Status         string  `json:"status"`
	TotalItems     int     `json:"totalItems"`
	ProcessedItems int     `json:"processedItems"`
	FailedItems    int     `json:"failedItems"`
	ErrorLog       *string `json:"errorLog,omitempty"`
	StartedAt      *int64  `json:"startedAt,omitempty"`
	CompletedAt    *int64  `json:"completedAt,omitempty"`
	CreatedAt      int64   `json:"createdAt"`
}

// ---- Create ----

// CreateImportBody is the JSON body for POST /workspaces/{wsId}/imports.
type CreateImportBody struct {
	Source     string          `json:"source" enum:"github,jira,linear,csv"`
	ProjectID *string         `json:"projectId,omitempty" doc:"Target project public id (UUID v7)"`
	ConfigJSON map[string]any `json:"configJson,omitempty" doc:"Source-specific configuration"`
}

// CreateImportInput is the request for POST /workspaces/{wsId}/imports.
type CreateImportInput struct {
	WsID string `path:"wsId"`
	Body CreateImportBody
}

// CreateImportOutput is the response for POST /workspaces/{wsId}/imports.
type CreateImportOutput struct {
	Body ImportJobBody
}

// ---- List ----

// ListImportsInput is the query for GET /workspaces/{wsId}/imports.
type ListImportsInput struct {
	WsID   string `path:"wsId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListImportsBody is the response payload for GET /workspaces/{wsId}/imports.
type ListImportsBody struct {
	Total      int64           `json:"total"`
	Items      []ImportJobBody `json:"items"`
	NextCursor *string         `json:"nextCursor"`
}

// ListImportsOutput is the response for GET /workspaces/{wsId}/imports.
type ListImportsOutput struct {
	Body ListImportsBody
}

// ---- Get ----

// GetImportInput is the path for GET /workspaces/{wsId}/imports/{importId}.
type GetImportInput struct {
	WsID     string `path:"wsId"`
	ImportID string `path:"importId"`
}

// GetImportOutput is the response for GET /workspaces/{wsId}/imports/{importId}.
type GetImportOutput struct {
	Body ImportJobBody
}

// ---- Cancel ----

// CancelImportInput is the path for POST /workspaces/{wsId}/imports/{importId}/cancel.
type CancelImportInput struct {
	WsID     string `path:"wsId"`
	ImportID string `path:"importId"`
}

// CancelImportOutput is the response for POST /workspaces/{wsId}/imports/{importId}/cancel.
type CancelImportOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}
