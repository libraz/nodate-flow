// Package timeline contains Huma operation handlers for the
// /tasks/{id}/timeline and /workspaces/{wsId}/timeline endpoints.
package timeline

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

// totalAsInt64 normalises the COUNT(*) OVER() return type into int64.
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

func nullStr(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}

// Event is the public DTO for an events row joined through v_task_timeline.
type Event struct {
	ID                string          `json:"id"`
	TaskID            string          `json:"taskId,omitempty"`
	ActorUserID       string          `json:"actorUserId,omitempty"`
	ActorDisplayName  string          `json:"actorDisplayName,omitempty"`
	Type              string          `json:"type"`
	Payload           json.RawMessage `json:"payload,omitempty"`
	OccurredAt        time.Time       `json:"occurredAt"`
}

// ListForTaskInput is the request shape for GET /tasks/{id}/timeline.
type ListForTaskInput struct {
	ID     string `path:"id"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListForWorkspaceInput is the request shape for GET /workspaces/{wsId}/timeline.
type ListForWorkspaceInput struct {
	WsID   string `path:"wsId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListOutput is the response for both timeline endpoints.
type ListOutput struct {
	Body struct {
		Total      int64   `json:"total"`
		Events     []Event `json:"events"`
		NextCursor *string `json:"nextCursor"`
	}
}
