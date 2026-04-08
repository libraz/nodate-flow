// Package inbox contains Huma operation handlers for the /inbox endpoints
// (list, archive, snooze).
package inbox

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
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

// Item is the public DTO for an inbox row (a signal projected through v_inbox).
type Item struct {
	ID         string          `json:"id"`
	TaskID     string          `json:"taskId,omitempty"`
	TaskTitle  string          `json:"taskTitle,omitempty"`
	Source     string          `json:"source"`
	Kind       string          `json:"kind"`
	ExternalID string          `json:"externalId,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	ReceivedAt time.Time       `json:"receivedAt"`
	CreatedAt  time.Time       `json:"createdAt"`
}

// ListInput is the query for GET /inbox.
type ListInput struct {
	WorkspaceID string `query:"workspaceId" doc:"Workspace public id (UUID v7)"`
	Limit       int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset      int32  `query:"offset" minimum:"0" default:"0"`
}

// ListOutput is the response for GET /inbox.
type ListOutput struct {
	Body struct {
		Total      int64   `json:"total"`
		Items      []Item  `json:"items"`
		NextCursor *string `json:"nextCursor"`
	}
}

// ArchiveInput is the path for POST /inbox/{id}/archive.
type ArchiveInput struct {
	ID          string `path:"id"`
	WorkspaceID string `query:"workspaceId" doc:"Workspace public id (UUID v7)"`
}

// ArchiveOutput is the response for POST /inbox/{id}/archive.
type ArchiveOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// SnoozeInput is the body for POST /inbox/{id}/snooze.
type SnoozeInput struct {
	ID          string `path:"id"`
	WorkspaceID string `query:"workspaceId" doc:"Workspace public id (UUID v7)"`
	Body        struct {
		SnoozeUntil int64 `json:"snoozeUntil" doc:"Unix seconds at which to resurface the item"`
	}
}

// SnoozeOutput is the response for POST /inbox/{id}/snooze.
type SnoozeOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}
