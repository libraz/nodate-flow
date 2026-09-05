// Package intake contains Huma operation handlers for workspace-scoped
// intake triage queue CRUD (/workspaces/{wsId}/intake). Intake items
// represent inbound work that has not yet been triaged into a task.
package intake

import (
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// Deps is the dependency bundle passed to each handler in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	// Embedder upserts the embedding of the task a conversion creates.
	// A converted item is a task like any other, so it is searchable
	// under its own text rather than only after the reindex cron runs.
	// Optional: nil disables write-time embedding.
	Embedder *embed.Client
	Audit    *audit.Recorder
}

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// actorPtr delegates to handlerutil.ActorPtr.
var actorPtr = handlerutil.ActorPtr

// totalAsInt64 delegates to handlerutil.TotalAsInt64.
var totalAsInt64 = handlerutil.TotalAsInt64

// nullStr delegates to handlerutil.NullStr.
var nullStr = handlerutil.NullStr

// Record is the public DTO for an intake item row. Named `Record` rather
// than `Item` so the merged OpenAPI spec keeps a unique component name
// (inbox.Item already claims the `Item` schema name).
//
// It carries no link to the task a converted item became. intake_items
// stores that as an internal row id, which must never leave the API, and
// none of the intake queries joins tasks to pick up its public id. The
// field this DTO used to declare for it was therefore empty on every
// response — a promise the contract could not keep, and one that
// generated clients typed as if it could. Restoring the link means
// joining tasks in the three intake queries first.
type Record struct {
	ID                   string   `json:"id" doc:"Intake item public id (UUID v7)"`
	Title                string   `json:"title"`
	Body                 string   `json:"body,omitempty"`
	TriageStatus         string   `json:"triageStatus"`
	SnoozeUntil          *int64   `json:"snoozeUntil,omitempty"`
	AIScore              *float64 `json:"aiScore,omitempty"`
	AIReasoning          string   `json:"aiReasoning,omitempty"`
	TriagedByUserID      string   `json:"triagedByUserId,omitempty"`
	TriagedByDisplayName string   `json:"triagedByDisplayName,omitempty"`
	CreatedAt            int64    `json:"createdAt"`
}

// ---- Create ----

// CreateIntakeItemBody is the JSON body for POST /workspaces/{wsId}/intake.
type CreateIntakeItemBody struct {
	Title string `json:"title" minLength:"1" maxLength:"500"`
	Body  string `json:"body,omitempty" maxLength:"50000"`
}

// CreateIntakeItemInput is the request for POST /workspaces/{wsId}/intake.
type CreateIntakeItemInput struct {
	WsID string `path:"wsId"`
	Body CreateIntakeItemBody
}

// CreateIntakeItemOutput is the response for POST /workspaces/{wsId}/intake.
type CreateIntakeItemOutput struct {
	Body Record
}

// ---- List ----

// ListIntakeItemsInput is the query for GET /workspaces/{wsId}/intake.
type ListIntakeItemsInput struct {
	WsID   string `path:"wsId"`
	Status string `query:"status" doc:"Filter by triage status: pending, accepted, rejected, snoozed, duplicate"`
	Cursor string `query:"cursor" doc:"Opaque cursor returned by previous page; pass to fetch next page. Empty when at end."`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListIntakeItemsBody is the response payload for GET /workspaces/{wsId}/intake.
type ListIntakeItemsBody struct {
	Total      int64    `json:"total"`
	Items      []Record `json:"items"`
	NextCursor *string  `json:"nextCursor"`
}

// ListIntakeItemsOutput is the response for GET /workspaces/{wsId}/intake.
type ListIntakeItemsOutput struct {
	Body ListIntakeItemsBody
}

// ---- Get ----

// GetIntakeItemInput is the path for GET /workspaces/{wsId}/intake/{id}.
type GetIntakeItemInput struct {
	WsID string `path:"wsId"`
	ID   string `path:"id"`
}

// GetIntakeItemOutput is the response for GET /workspaces/{wsId}/intake/{id}.
type GetIntakeItemOutput struct {
	Body Record
}

// ---- Triage ----

// TriageIntakeItemBody is the JSON body for PATCH /workspaces/{wsId}/intake/{id}.
type TriageIntakeItemBody struct {
	Status      string `json:"status" enum:"accepted,rejected,snoozed,duplicate"`
	SnoozeUntil *int64 `json:"snoozeUntil,omitempty" doc:"Unix seconds timestamp for snooze expiry"`
}

// TriageIntakeItemInput is the request for PATCH /workspaces/{wsId}/intake/{id}.
type TriageIntakeItemInput struct {
	WsID string `path:"wsId"`
	ID   string `path:"id"`
	Body TriageIntakeItemBody
}

// TriageIntakeItemOutput is the response for PATCH /workspaces/{wsId}/intake/{id}.
type TriageIntakeItemOutput struct {
	Body Record
}

// ---- Convert ----

// ConvertIntakeItemBody is the JSON body for POST /workspaces/{wsId}/intake/{id}/convert.
type ConvertIntakeItemBody struct {
	ProjectID string `json:"projectId" doc:"Project public id (UUID v7) to create the task in"`
}

// ConvertIntakeItemInput is the request for POST /workspaces/{wsId}/intake/{id}/convert.
type ConvertIntakeItemInput struct {
	WsID string `path:"wsId"`
	ID   string `path:"id"`
	Body ConvertIntakeItemBody
}

// ConvertIntakeItemOutput is the response for POST /workspaces/{wsId}/intake/{id}/convert.
type ConvertIntakeItemOutput struct {
	Body struct {
		Ok     bool   `json:"ok"`
		TaskID string `json:"taskId" doc:"Public id of the created task"`
	}
}
