package notifications

import (
	"context"
	"database/sql"
	"errors"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// resolveWorkspace validates the caller's membership of the workspace
// identified by a public UUID string.
func resolveWorkspace(ctx context.Context, db *sql.DB, wsPublic string, actorID uint32) (uint32, error) {
	if wsPublic == "" {
		return 0, httpErr(apierrors.WsWorkspaceNotFound)
	}
	pub, err := types.Parse(wsPublic)
	if err != nil {
		return 0, httpErr(apierrors.WsWorkspaceNotFound)
	}
	const wsLookup = `SELECT id FROM workspaces WHERE public_id = ? AND enabled = TRUE LIMIT 1`
	var wsID uint32
	if err := db.QueryRowContext(ctx, wsLookup, pub).Scan(&wsID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, httpErr(apierrors.WsWorkspaceNotFound)
		}
		return 0, httpErr(apierrors.InternalUnexpected)
	}
	const wsMemQuery = `SELECT 1 FROM workspace_members
WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`
	var one int
	if err := db.QueryRowContext(ctx, wsMemQuery, wsID, actorID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		return 0, httpErr(apierrors.InternalUnexpected)
	}
	return wsID, nil
}

// List handles GET /me/notifications. When workspaceId is provided it
// scopes the result to that workspace (after verifying membership).
// When omitted it lists notifications across every workspace the actor
// belongs to.
func List(deps Deps) func(context.Context, *ListInput) (*ListOutput, error) {
	return func(ctx context.Context, in *ListInput) (*ListOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		out := &ListOutput{}
		out.Body.Notifications = []NotificationDTO{}

		if in.WorkspaceID != "" {
			wsID, err := resolveWorkspace(ctx, deps.DB, in.WorkspaceID, actorID)
			if err != nil {
				return nil, err
			}
			rows, err := deps.Queries.ListNotificationsForWorkspace(ctx, generated.ListNotificationsForWorkspaceParams{
				WorkspaceID:     wsID,
				RecipientUserID: actorID,
				Limit:           limit,
				Offset:          in.Offset,
			})
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			for _, r := range rows {
				out.Body.Notifications = append(out.Body.Notifications, mapWorkspaceRow(r))
			}
			if len(rows) > 0 {
				out.Body.Total = totalAsInt64(rows[0].Total)
			}
			return out, nil
		}

		rows, err := deps.Queries.ListNotificationsForUser(ctx, generated.ListNotificationsForUserParams{
			RecipientUserID: actorID,
			Limit:           limit,
			Offset:          in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		for _, r := range rows {
			out.Body.Notifications = append(out.Body.Notifications, mapUserRow(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// CountUnread handles GET /me/notifications/unread-count.
func CountUnread(deps Deps) func(context.Context, *CountUnreadInput) (*CountUnreadOutput, error) {
	return func(ctx context.Context, in *CountUnreadInput) (*CountUnreadOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		out := &CountUnreadOutput{}

		if in.WorkspaceID != "" {
			wsID, err := resolveWorkspace(ctx, deps.DB, in.WorkspaceID, actorID)
			if err != nil {
				return nil, err
			}
			count, err := deps.Queries.CountUnreadNotificationsForWorkspace(ctx, generated.CountUnreadNotificationsForWorkspaceParams{
				WorkspaceID:     wsID,
				RecipientUserID: actorID,
			})
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			out.Body.UnreadCount = count
			return out, nil
		}

		count, err := deps.Queries.CountUnreadNotifications(ctx, actorID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out.Body.UnreadCount = count
		return out, nil
	}
}

// MarkRead handles POST /notifications/{notifId}/read.
func MarkRead(deps Deps) func(context.Context, *MarkReadInput) (*MarkReadOutput, error) {
	return func(ctx context.Context, in *MarkReadInput) (*MarkReadOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		pub, err := types.Parse(in.NotifID)
		if err != nil {
			return nil, httpErr(apierrors.WsNotificationNotFound)
		}
		if err := deps.Queries.MarkNotificationRead(ctx, generated.MarkNotificationReadParams{
			PublicID:        pub,
			RecipientUserID: actorID,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "notification.read",
			ActorID:      actorID,
			ResourceType: "notification",
			ResourceID:   in.NotifID,
		})
		out := &MarkReadOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// MarkAllRead handles POST /workspaces/{wsId}/notifications/read-all.
func MarkAllRead(deps Deps) func(context.Context, *MarkAllReadInput) (*MarkAllReadOutput, error) {
	return func(ctx context.Context, in *MarkAllReadInput) (*MarkAllReadOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsCtx, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		if err := deps.Queries.MarkAllNotificationsRead(ctx, generated.MarkAllNotificationsReadParams{
			WorkspaceID:     wsCtx.ID,
			RecipientUserID: actorID,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "notification.read-all",
			ActorID:      actorID,
			WorkspaceID:  wsCtx.ID,
			ResourceType: "notification",
		})
		out := &MarkAllReadOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// Archive handles POST /notifications/{notifId}/archive.
func Archive(deps Deps) func(context.Context, *ArchiveInput) (*ArchiveOutput, error) {
	return func(ctx context.Context, in *ArchiveInput) (*ArchiveOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		pub, err := types.Parse(in.NotifID)
		if err != nil {
			return nil, httpErr(apierrors.WsNotificationNotFound)
		}
		if err := deps.Queries.ArchiveNotification(ctx, generated.ArchiveNotificationParams{
			PublicID:        pub,
			RecipientUserID: actorID,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "notification.archive",
			ActorID:      actorID,
			ResourceType: "notification",
			ResourceID:   in.NotifID,
		})
		out := &ArchiveOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// publicIDOrEmpty returns the UUID string of a types.PublicID, or ""
// when it is the zero value (i.e. the LEFT JOIN returned NULL).
func publicIDOrEmpty(p types.PublicID) string {
	var zero types.PublicID
	if p == zero {
		return ""
	}
	return p.String()
}

// mapUserRow converts a ListNotificationsForUserRow to a NotificationDTO.
func mapUserRow(r generated.ListNotificationsForUserRow) NotificationDTO {
	return NotificationDTO{
		ID:               r.PublicID.String(),
		WorkspaceID:      r.WorkspacePublicID.String(),
		ActorID:          publicIDOrEmpty(r.ActorPublicID),
		ActorDisplayName: nullStr(r.ActorDisplayName),
		EventType:        r.EventType,
		ResourceType:     r.ResourceType,
		ResourceID:       nullStr(r.ResourcePublicID),
		Title:            r.Title,
		Body:             nullStr(r.Body),
		Severity:         string(r.Severity),
		Channel:          string(r.Channel),
		ReadAt:           nullTimeUnix(r.ReadAt),
		DeliveredAt:      nullTimeUnix(r.DeliveredAt),
		CreatedAt:        r.CreatedAt.Unix(),
	}
}

// mapWorkspaceRow converts a ListNotificationsForWorkspaceRow to a NotificationDTO.
func mapWorkspaceRow(r generated.ListNotificationsForWorkspaceRow) NotificationDTO {
	return NotificationDTO{
		ID:               r.PublicID.String(),
		WorkspaceID:      r.WorkspacePublicID.String(),
		ActorID:          publicIDOrEmpty(r.ActorPublicID),
		ActorDisplayName: nullStr(r.ActorDisplayName),
		EventType:        r.EventType,
		ResourceType:     r.ResourceType,
		ResourceID:       nullStr(r.ResourcePublicID),
		Title:            r.Title,
		Body:             nullStr(r.Body),
		Severity:         string(r.Severity),
		Channel:          string(r.Channel),
		ReadAt:           nullTimeUnix(r.ReadAt),
		DeliveredAt:      nullTimeUnix(r.DeliveredAt),
		CreatedAt:        r.CreatedAt.Unix(),
	}
}
