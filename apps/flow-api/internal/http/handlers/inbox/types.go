// Package inbox contains Huma operation handlers for the /inbox endpoints
// (list, archive, snooze).
package inbox

import (
	"database/sql"
	"encoding/json"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
)

// Deps is the dependency bundle for handlers in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	// Mutations records a change in both the event log the suggestion
	// list is replayed from and the audit log an administrator queries by
	// action name, so a reaction cannot land in one and be missing from
	// the other.
	Mutations *mutationlog.Recorder
}

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// nullStr delegates to handlerutil.NullStr.
var nullStr = handlerutil.NullStr

// totalAsInt64 delegates to handlerutil.TotalAsInt64.
var totalAsInt64 = handlerutil.TotalAsInt64

// Item is the public DTO for an inbox row (a signal projected through v_inbox).
type Item struct {
	ID          string          `json:"id" doc:"Inbox item public id (UUID v7)"`
	WorkspaceID string          `json:"workspaceId"`
	TaskID      string          `json:"taskId,omitempty"`
	TaskTitle   string          `json:"taskTitle,omitempty"`
	Source      string          `json:"source"`
	Kind        string          `json:"kind"`
	ExternalID  string          `json:"externalId,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	ReceivedAt  int64           `json:"receivedAt"`
	CreatedAt   int64           `json:"createdAt"`
}

// ListInboxInput is the query for GET /inbox.
type ListInboxInput struct {
	WorkspaceID string `query:"workspaceId" doc:"Workspace public id (UUID v7)"`
	Limit       int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset      int32  `query:"offset" minimum:"0" default:"0"`
}

// ListInboxOutputBody is the response body for GET /inbox.
type ListInboxOutputBody struct {
	Total      int64   `json:"total"`
	Items      []Item  `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

// ListInboxOutput is the response for GET /inbox.
type ListInboxOutput struct {
	Body ListInboxOutputBody
}

// ArchiveInboxInput is the path for POST /inbox/{id}/archive.
type ArchiveInboxInput struct {
	ID          string `path:"id"`
	WorkspaceID string `query:"workspaceId" doc:"Workspace public id (UUID v7)"`
}

// ArchiveInboxOutputBody is the response body for POST /inbox/{id}/archive.
type ArchiveInboxOutputBody struct {
	Ok bool `json:"ok"`
}

// ArchiveInboxOutput is the response for POST /inbox/{id}/archive.
type ArchiveInboxOutput struct {
	Body ArchiveInboxOutputBody
}

// SnoozeInboxInputBody is the JSON body for POST /inbox/{id}/snooze.
type SnoozeInboxInputBody struct {
	SnoozeUntil int64 `json:"snoozeUntil" doc:"Unix seconds at which to resurface the item"`
}

// SnoozeInboxInput is the request for POST /inbox/{id}/snooze.
type SnoozeInboxInput struct {
	ID          string `path:"id"`
	WorkspaceID string `query:"workspaceId" doc:"Workspace public id (UUID v7)"`
	Body        SnoozeInboxInputBody
}

// SnoozeInboxOutputBody is the response body for POST /inbox/{id}/snooze.
type SnoozeInboxOutputBody struct {
	Ok bool `json:"ok"`
}

// SnoozeInboxOutput is the response for POST /inbox/{id}/snooze.
type SnoozeInboxOutput struct {
	Body SnoozeInboxOutputBody
}
