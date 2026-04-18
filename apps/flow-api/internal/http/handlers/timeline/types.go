// Package timeline contains Huma operation handlers for the
// /tasks/{id}/timeline, /projects/{prjId}/timeline and
// /workspaces/{wsId}/timeline endpoints. All three return rows from
// v_task_timeline filtered by scope and the optional kind / actor query
// parameters.
package timeline

import (
	"database/sql"
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

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

// totalAsInt64 normalises the COUNT(*) OVER() return type into int64.
// MySQL drivers may surface the value as int64, int, uint64 or as a
// decimal byte slice depending on the underlying column type, so we
// accept all four shapes here.
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

// uuidFromBytes converts a raw BINARY(16) public_id into its canonical
// UUID string form. Empty or malformed slices yield an empty string so
// the field is omitted via the ",omitempty" JSON tag.
func uuidFromBytes(b []byte) string {
	if len(b) != 16 {
		return ""
	}
	u, err := uuid.FromBytes(b)
	if err != nil {
		return ""
	}
	return u.String()
}

// TimelineEvent is the public DTO for a row in v_task_timeline. Time
// fields use the project-wide convention: *At = int64 unix seconds.
type TimelineEvent struct {
	// ID is the event public_id (UUID v7 string).
	ID string `json:"id"`
	// Type is the canonical dotted event kind, e.g. "task.created".
	Type string `json:"type"`
	// TaskID is the owning task's public_id, omitted when nil.
	TaskID string `json:"taskId,omitempty"`
	// ActorUserID is the actor user's public_id, omitted when nil.
	ActorUserID string `json:"actorUserId,omitempty"`
	// ActorDisplayName is a denormalised convenience for UIs that need
	// the user's display name without a separate fetch.
	ActorDisplayName string `json:"actorDisplayName,omitempty"`
	// Payload is the raw JSON payload column passed through unmodified.
	Payload json.RawMessage `json:"payload,omitempty"`
	// OccurredAt is the unix timestamp in seconds (UTC).
	OccurredAt int64 `json:"occurredAt"`
}

// ListTimelineForTaskInput is the request shape for GET
// /tasks/{id}/timeline. The Kind and Actor filters mirror the project /
// workspace shapes so SDK consumers see one consistent filter API.
type ListTimelineForTaskInput struct {
	ID     string   `path:"id"`
	Limit  int32    `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32    `query:"offset" minimum:"0" default:"0"`
	Kind   []string `query:"kind" doc:"Filter by event kind. Repeat to OR multiple kinds."`
	Actor  string   `query:"actor" doc:"Filter by actor user public_id (UUID v7)."`
}

// ListTimelineForProjectInput is the request shape for GET
// /projects/{prjId}/timeline.
type ListTimelineForProjectInput struct {
	PrjID  string   `path:"prjId"`
	Limit  int32    `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32    `query:"offset" minimum:"0" default:"0"`
	Kind   []string `query:"kind" doc:"Filter by event kind. Repeat to OR multiple kinds."`
	Actor  string   `query:"actor" doc:"Filter by actor user public_id (UUID v7)."`
}

// ListTimelineForWorkspaceInput is the request shape for GET
// /workspaces/{wsId}/timeline.
type ListTimelineForWorkspaceInput struct {
	WsID   string   `path:"wsId"`
	Limit  int32    `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32    `query:"offset" minimum:"0" default:"0"`
	Kind   []string `query:"kind" doc:"Filter by event kind. Repeat to OR multiple kinds."`
	Actor  string   `query:"actor" doc:"Filter by actor user public_id (UUID v7)."`
}

// ListTimelineOutput is the response body for every timeline endpoint.
// total reflects the COUNT(*) OVER() of the filtered set, NextCursor is
// reserved for a future cursor-based migration and is currently nil.
type ListTimelineOutput struct {
	Body struct {
		Total      int64           `json:"total"`
		Events     []TimelineEvent `json:"events"`
		NextCursor *string         `json:"nextCursor"`
	}
}
