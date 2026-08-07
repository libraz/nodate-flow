// Package notifications contains Huma operation handlers for the
// notification endpoints (list, count-unread, mark-read, mark-all-read,
// archive).
package notifications

import (
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// Deps is the dependency bundle for handlers in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	Audit   *audit.Recorder
}

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// nullStr delegates to handlerutil.NullStr.
var nullStr = handlerutil.NullStr

// nullTimeUnix delegates to handlerutil.NullTimeUnix (returns *int64, nil for NULL).
var nullTimeUnix = handlerutil.NullTimeUnix

// totalAsInt64 delegates to handlerutil.TotalAsInt64.
var totalAsInt64 = handlerutil.TotalAsInt64

// NotificationDTO is the public DTO for a notification row.
type NotificationDTO struct {
	ID               string `json:"id" doc:"Notification public id (UUID v7)"`
	WorkspaceID      string `json:"workspaceId"`
	ActorID          string `json:"actorId,omitempty"`
	ActorDisplayName string `json:"actorDisplayName,omitempty"`
	EventType        string `json:"eventType"`
	ResourceType     string `json:"resourceType"`
	ResourceID       string `json:"resourceId,omitempty"`
	Title            string `json:"title"`
	Body             string `json:"body,omitempty"`
	Severity         string `json:"severity"`
	Channel          string `json:"channel"`
	ReadAt           *int64 `json:"readAt"`
	DeliveredAt      *int64 `json:"deliveredAt"`
	CreatedAt        int64  `json:"createdAt"`
}

// --- List ---

// ListInput is the query for GET /me/notifications.
//
// `cursor` opt-in routes through ListNotificationsForUserKeyset (or the
// per-workspace variant). The keyset queries always pass `read_filter
// = 'all'` since the historical OFFSET endpoint exposes no read-state
// filter; callers wanting to filter by read/unread should keep using
// the OFFSET path until a `state` query parameter is added.
type ListInput struct {
	WorkspaceID string `query:"workspaceId" doc:"Optional workspace public id to filter by"`
	Cursor      string `query:"cursor" doc:"Opaque cursor returned by previous page; pass to fetch next page. Empty when at end."`
	Limit       int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset      int32  `query:"offset" minimum:"0" default:"0"`
}

// ListOutputBody is the response body for GET /me/notifications.
type ListOutputBody struct {
	Total         int64             `json:"total"`
	Notifications []NotificationDTO `json:"notifications"`
	NextCursor    *string           `json:"nextCursor"`
}

// ListOutput is the response for GET /me/notifications.
type ListOutput struct {
	Body ListOutputBody
}

// --- CountUnread ---

// CountUnreadInput is the query for GET /me/notifications/unread-count.
type CountUnreadInput struct {
	WorkspaceID string `query:"workspaceId" doc:"Optional workspace public id to filter by"`
}

// CountUnreadOutputBody is the response body for GET /me/notifications/unread-count.
type CountUnreadOutputBody struct {
	UnreadCount int64 `json:"unreadCount"`
}

// CountUnreadOutput is the response for GET /me/notifications/unread-count.
type CountUnreadOutput struct {
	Body CountUnreadOutputBody
}

// --- MarkRead ---

// MarkReadInput is the path for POST /notifications/{notifId}/read.
type MarkReadInput struct {
	NotifID string `path:"notifId" doc:"Notification public id (UUID v7)"`
}

// MarkReadOutputBody is the response body for POST /notifications/{notifId}/read.
type MarkReadOutputBody struct {
	Ok bool `json:"ok"`
}

// MarkReadOutput is the response for POST /notifications/{notifId}/read.
type MarkReadOutput struct {
	Body MarkReadOutputBody
}

// --- MarkAllRead ---

// MarkAllReadInput is the path for POST /workspaces/{wsId}/notifications/read-all.
type MarkAllReadInput struct {
	WsID string `path:"wsId" doc:"Workspace public id (UUID v7)"`
}

// MarkAllReadOutputBody is the response body for POST /workspaces/{wsId}/notifications/read-all.
type MarkAllReadOutputBody struct {
	Ok bool `json:"ok"`
}

// MarkAllReadOutput is the response for POST /workspaces/{wsId}/notifications/read-all.
type MarkAllReadOutput struct {
	Body MarkAllReadOutputBody
}

// --- Preferences ---

// NotificationPreferenceDTO is one (event category, channel) cell of
// the caller's preference matrix.
//
// There is deliberately no public id on this DTO. The stored row is an
// implementation detail of "the caller muted this cell": a cell the
// caller has never touched has no row at all, and one they set back to
// the default still does. Addressing the cell by (eventCategory,
// channel) keeps the write idempotent and spares the client from
// tracking which cells happen to be materialised.
type NotificationPreferenceDTO struct {
	EventCategory string `json:"eventCategory" doc:"Event category the setting applies to"`
	Channel       string `json:"channel" enum:"in_app,email,push" doc:"Delivery channel"`
	Muted         bool   `json:"muted" doc:"True when delivery of this category on this channel is suppressed"`
}

// ListPreferencesInput is the path for
// GET /workspaces/{wsId}/notification-preferences.
type ListPreferencesInput struct {
	WsID string `path:"wsId" doc:"Workspace public id (UUID v7)"`
}

// PreferencesOutputBody is the response body shared by the list and
// update preference operations.
//
// The matrix is always complete: every category × channel pair is
// returned with its effective value, whether or not a row backs it.
// Returning only the stored rows would push the default rules into
// every client, and a client that guessed them differently from the
// fan-out would show people settings the server does not honour.
type PreferencesOutputBody struct {
	Preferences []NotificationPreferenceDTO `json:"preferences"`
}

// PreferencesOutput is the response for the preference operations.
type PreferencesOutput struct {
	Body PreferencesOutputBody
}

// UpdatePreferencesInput is the request for
// PUT /workspaces/{wsId}/notification-preferences.
//
// The body carries only the cells being changed; unlisted cells keep
// whatever they had. It is a PUT rather than a PATCH because each
// listed cell is written whole — there is no partial update of a
// boolean — and repeating the request is a no-op.
type UpdatePreferencesInput struct {
	WsID string `path:"wsId" doc:"Workspace public id (UUID v7)"`
	Body struct {
		Preferences []NotificationPreferenceDTO `json:"preferences" minItems:"1" maxItems:"64" doc:"Cells to write; each is addressed by (eventCategory, channel)"`
	}
}

// --- Archive ---

// ArchiveInput is the path for POST /notifications/{notifId}/archive.
type ArchiveInput struct {
	NotifID string `path:"notifId" doc:"Notification public id (UUID v7)"`
}

// ArchiveOutputBody is the response body for POST /notifications/{notifId}/archive.
type ArchiveOutputBody struct {
	Ok bool `json:"ok"`
}

// ArchiveOutput is the response for POST /notifications/{notifId}/archive.
type ArchiveOutput struct {
	Body ArchiveOutputBody
}
