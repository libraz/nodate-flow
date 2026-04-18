// Package notifications contains Huma operation handlers for the
// notification endpoints (list, count-unread, mark-read, mark-all-read,
// archive).
package notifications

import (
	"database/sql"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
)

// Deps is the dependency bundle for handlers in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	Audit   *audit.Recorder
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

func nullTimeUnix(t sql.NullTime) *int64 {
	if !t.Valid {
		return nil
	}
	v := t.Time.Unix()
	return &v
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

// NotificationDTO is the public DTO for a notification row.
type NotificationDTO struct {
	ID               string `json:"id"`
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
type ListInput struct {
	WorkspaceID string `query:"workspaceId" doc:"Optional workspace public id to filter by"`
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
