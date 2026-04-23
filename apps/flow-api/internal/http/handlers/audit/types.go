// Package audit contains Huma operation handlers for reading the
// workspace audit log. Only the workspace-admin tier is allowed to
// inspect audit entries; the enclosing chi group wires
// RequireWorkspaceRole(admin) before these handlers run.
//
// Audit writes are produced as side effects of other endpoints via
// the internal/audit.Recorder helper and land in the audit_logs table.
// This package only exposes a paginated read surface for that table,
// backed by sqlc's ListWorkspaceAuditLogs query.
package audit

import (
	"database/sql"
	"encoding/json"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// Deps is the dependency bundle passed to each handler in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
}

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// nullStrPtr delegates to handlerutil.NullStrPtr (returns *string, nil for NULL).
var nullStrPtr = handlerutil.NullStrPtr

// publicIDOrNilString returns the canonical UUID string for a PublicID, or
// nil when the value is the zero PublicID (e.g. a LEFT JOIN that produced
// SQL NULL on a non-nullable Go type).
var publicIDOrNilString = handlerutil.PublicIDOrEmpty

// totalAsInt64 delegates to handlerutil.TotalAsInt64.
var totalAsInt64 = handlerutil.TotalAsInt64

// --- DTOs ---

// AuditLogEntryDTO is the public DTO for a single audit_logs row. Field
// names mirror the frontend's apps/flow-web/src/features/audit/api.ts
// AuditLogEntry shape so the React query consumes this response unchanged.
type AuditLogEntryDTO struct {
	// PublicID is the audit entry UUID v7.
	PublicID string `json:"publicId" doc:"Audit entry public id (UUID v7)"`
	// ActorUserPublicID is the acting user's UUID, or nil for system /
	// anonymous entries (actor_user_id IS NULL in the database).
	ActorUserPublicID *string `json:"actorUserPublicId"`
	// ActorDisplayName is the acting user's display name, or nil when
	// the actor user row was deleted or the action was system-initiated.
	ActorDisplayName *string `json:"actorDisplayName"`
	// Action is the dot-separated action identifier (e.g. "task.create").
	Action string `json:"action"`
	// ResourceType identifies the kind of resource affected.
	ResourceType string `json:"resourceType"`
	// ResourcePublicID is the affected resource's UUID, or nil when
	// the action was not scoped to a specific resource.
	ResourcePublicID *string `json:"resourcePublicId"`
	// IPAddress is the caller's client IP, or nil when not recorded.
	IPAddress *string `json:"ipAddress"`
	// UserAgent is the caller's user-agent header, or nil when not recorded.
	UserAgent *string `json:"userAgent"`
	// MetadataJSON carries additional pre-redacted context. Null when
	// the action recorded no extra metadata.
	MetadataJSON json.RawMessage `json:"metadataJson"`
	// OccurredAt is the logical occurrence time (unix seconds).
	OccurredAt int64 `json:"occurredAt"`
}

// --- List ---

// ListAuditLogsInput is the query for GET /workspaces/{wsId}/audit-logs.
//
// Filters are all optional and combine with AND semantics. Empty-string
// and omitted-ness are equivalent (no filter applied). Date filters use
// calendar-date wire format (YYYY-MM-DD) and are interpreted as the
// caller's requested lower/upper bound at UTC midnight; this matches
// the frontend's DatePicker output in apps/flow-web/src/features/audit.
type ListAuditLogsInput struct {
	WsID         string `path:"wsId" doc:"Workspace public id (UUID v7)"`
	Action       string `query:"action" doc:"Exact match on action (e.g. 'task.create')"`
	ResourceType string `query:"resourceType" doc:"Exact match on resource_type (e.g. 'task')"`
	ActorSearch  string `query:"actorSearch" doc:"Substring match against actor display_name or email"`
	DateFrom     string `query:"dateFrom" doc:"Inclusive lower bound on occurred_at (YYYY-MM-DD, UTC)"`
	DateTo       string `query:"dateTo" doc:"Inclusive upper bound on occurred_at (YYYY-MM-DD, UTC)"`
	Limit        int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset       int32  `query:"offset" minimum:"0" default:"0"`
}

// ListAuditLogsBody is the response body for GET /workspaces/{wsId}/audit-logs.
// The response shape matches AuditLogListResponse in
// apps/flow-web/src/features/audit/api.ts.
type ListAuditLogsBody struct {
	// Total is the number of rows matching the filters before pagination.
	Total int64 `json:"total"`
	// Entries is the current page of matching rows, newest first.
	Entries []AuditLogEntryDTO `json:"entries"`
}

// ListAuditLogsOutput is the response for GET /workspaces/{wsId}/audit-logs.
type ListAuditLogsOutput struct {
	Body ListAuditLogsBody
}
