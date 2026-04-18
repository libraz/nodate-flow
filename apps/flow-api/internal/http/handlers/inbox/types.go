// Package inbox contains Huma operation handlers for the /inbox endpoints
// (list, archive, snooze).
package inbox

import (
	"database/sql"
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
)

// Deps is the dependency bundle for handlers in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
}

func httpErr(spec *apierrors.Spec) error {
	return huma.NewError(spec.Status, spec.Code+": "+spec.Message)
}

func nullStr(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}

func totalAsInt64(v interface{}) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case uint64:
		return int64(x)
	case []byte:
		var n int64
		for _, c := range x {
			if c < '0' || c > '9' {
				return n
			}
			n = n*10 + int64(c-'0')
		}
		return n
	}
	return 0
}

// InboxItem is the public DTO for an inbox row (a signal projected through v_inbox).
type InboxItem struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspaceId"`
	TaskID     string          `json:"taskId,omitempty"`
	TaskTitle  string          `json:"taskTitle,omitempty"`
	Source     string          `json:"source"`
	Kind       string          `json:"kind"`
	ExternalID string          `json:"externalId,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	ReceivedAt int64           `json:"receivedAt"`
	CreatedAt  int64           `json:"createdAt"`
}

// ListInboxInput is the query for GET /inbox.
type ListInboxInput struct {
	WorkspaceID string `query:"workspaceId" doc:"Workspace public id (UUID v7)"`
	Limit       int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset      int32  `query:"offset" minimum:"0" default:"0"`
}

// ListInboxOutputBody is the response body for GET /inbox.
type ListInboxOutputBody struct {
	Total      int64       `json:"total"`
	Items      []InboxItem `json:"items"`
	NextCursor *string     `json:"nextCursor"`
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
