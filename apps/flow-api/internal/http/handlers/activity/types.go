// Package activity contains the Huma operation handler for the unified
// workspace activity feed (GET /workspaces/{wsId}/activity).
//
// The feed is backed by v_workspace_activity, a UNION ALL read model over
// audit_logs, ai_invocations, and mcp_invocations (see
// sql/views/v_workspace_activity.sql). It is cursor-paginated over the
// (occurred_at DESC, public_id DESC) keyset and exposes only public_ids;
// the view never projects internal ids, and the audit metadata it draws
// from is pre-redacted at write time.
//
// Access is gated to workspace members (RequireWorkspaceMember), mirroring
// the workspace timeline endpoint, which is the same class of workspace-wide
// activity read.
package activity

import (
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// Deps is the dependency bundle passed to the activity handler.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
}

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// --- DTOs ---

// Entry is the public DTO for a single v_workspace_activity row. Time
// fields use the project-wide convention: *At = int64 unix seconds. Every
// id field is a public_id (UUID v7 string); no internal id is ever exposed.
type Entry struct {
	// PublicID is the source row's UUID v7 (unique per source table).
	PublicID string `json:"publicId" doc:"Activity entry public id (UUID v7)"`
	// Source is the originating stream: "audit" | "ai" | "mcp".
	Source string `json:"source" doc:"Originating stream ('audit' | 'ai' | 'mcp')"`
	// SourceTable is the literal source table name, for drill-down / debug.
	SourceTable string `json:"sourceTable" doc:"Source table name (audit_logs | ai_invocations | mcp_invocations)"`
	// ActorUserPublicID is the acting user's UUID, or nil for system /
	// agent-initiated entries (no user actor on the source row).
	ActorUserPublicID *string `json:"actorUserPublicId"`
	// ActorKind classifies the actor: "user" | "agent" | "system".
	ActorKind string `json:"actorKind" doc:"Actor classification ('user' | 'agent' | 'system')"`
	// Action is the source-specific action label (audit.action /
	// ai.purpose / mcp.tool_name).
	Action string `json:"action"`
	// ResourceType identifies the kind of resource affected
	// (audit.resource_type / 'ai_invocation' / 'mcp_invocation').
	ResourceType string `json:"resourceType"`
	// ResourcePublicID is the affected resource's UUID, or nil when the
	// entry was not scoped to a specific resource.
	ResourcePublicID *string `json:"resourcePublicId"`
	// Severity is the derived level: "info" | "warn" | "error".
	Severity string `json:"severity" doc:"Derived severity ('info' | 'warn' | 'error')"`
	// OccurredAt is the logical occurrence time (unix seconds, UTC).
	OccurredAt int64 `json:"occurredAt"`
}

// --- List ---

// ListActivityInput is the request shape for GET /workspaces/{wsId}/activity.
//
// Filters are optional and combine with AND semantics. Pagination uses an
// opaque keyset cursor: pass the previous response's nextCursor back in the
// cursor query parameter to fetch the following page. An empty cursor
// returns the first (newest) page.
type ListActivityInput struct {
	WsID   string `path:"wsId" doc:"Workspace public id (UUID v7)"`
	Source string `query:"source" enum:"audit,ai,mcp" doc:"Filter by originating stream ('audit' | 'ai' | 'mcp'). Omit for all."`
	Cursor string `query:"cursor" doc:"Opaque keyset cursor from a previous response's nextCursor. Omit for the first page."`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

// ListActivityOutput is the response for GET /workspaces/{wsId}/activity.
// total reflects the COUNT(*) of the filtered set before pagination;
// nextCursor is nil when there is no further page.
type ListActivityOutput struct {
	Body struct {
		// Total is the number of rows matching the filters before pagination.
		Total int64 `json:"total"`
		// Activity is the current page of matching entries, newest first.
		Activity []Entry `json:"activity"`
		// NextCursor is the opaque cursor for the next page, or nil at end.
		NextCursor *string `json:"nextCursor"`
	}
}
