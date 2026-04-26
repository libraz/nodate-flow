package notifications

import (
	"context"
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/resolve"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// List handles GET /me/notifications. When workspaceId is provided it
// scopes the result to that workspace (after verifying membership).
// When omitted it lists notifications across every workspace the actor
// belongs to.
//
// Pagination: when `cursor` is non-empty the keyset path runs
// (ListNotificationsFor*Keyset) and emits `nextCursor`; otherwise the
// historical OFFSET path runs unchanged. The keyset queries pass
// `read_filter = 'all'` (matching the OFFSET behaviour, which exposes
// no read-state filter).
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
			wsID, err := resolve.WorkspaceMember(ctx, deps.DB, in.WorkspaceID, actorID)
			if err != nil {
				return nil, err
			}
			if in.Cursor != "" {
				cursorAt, cursorPID, derr := handlerutil.DecodeCursor(in.Cursor)
				if derr != nil {
					return nil, httpErr(apierrors.ValidationQueryFieldInvalid)
				}
				rows, qerr := deps.Queries.ListNotificationsForWorkspaceKeyset(ctx, generated.ListNotificationsForWorkspaceKeysetParams{
					WorkspaceID:     wsID,
					RecipientUserID: actorID,
					ReadFilter:      "all",
					CursorCreatedAt: sql.NullTime{Time: cursorAt, Valid: !cursorAt.IsZero()},
					CursorPublicID:  cursorPID,
					Limit:           limit + 1,
				})
				if qerr != nil {
					return nil, httpErr(apierrors.InternalUnexpected)
				}
				hasMore := int32(len(rows)) > limit
				if hasMore {
					rows = rows[:limit]
				}
				for _, r := range rows {
					out.Body.Notifications = append(out.Body.Notifications, mapWorkspaceKeysetRow(r))
				}
				if hasMore {
					last := rows[len(rows)-1]
					nc := handlerutil.EncodeCursor(last.CreatedAt, last.PublicID)
					out.Body.NextCursor = &nc
				}
				return out, nil
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
				if int64(in.Offset+limit) < out.Body.Total {
					last := rows[len(rows)-1]
					nc := handlerutil.EncodeCursor(last.CreatedAt, last.PublicID)
					out.Body.NextCursor = &nc
				}
			}
			return out, nil
		}

		if in.Cursor != "" {
			cursorAt, cursorPID, derr := handlerutil.DecodeCursor(in.Cursor)
			if derr != nil {
				return nil, httpErr(apierrors.ValidationQueryFieldInvalid)
			}
			rows, qerr := deps.Queries.ListNotificationsForUserKeyset(ctx, generated.ListNotificationsForUserKeysetParams{
				RecipientUserID: actorID,
				ReadFilter:      "all",
				CursorCreatedAt: sql.NullTime{Time: cursorAt, Valid: !cursorAt.IsZero()},
				CursorPublicID:  cursorPID,
				Limit:           limit + 1,
			})
			if qerr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			hasMore := int32(len(rows)) > limit
			if hasMore {
				rows = rows[:limit]
			}
			for _, r := range rows {
				out.Body.Notifications = append(out.Body.Notifications, mapUserKeysetRow(r))
			}
			if hasMore {
				last := rows[len(rows)-1]
				nc := handlerutil.EncodeCursor(last.CreatedAt, last.PublicID)
				out.Body.NextCursor = &nc
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
			if int64(in.Offset+limit) < out.Body.Total {
				last := rows[len(rows)-1]
				nc := handlerutil.EncodeCursor(last.CreatedAt, last.PublicID)
				out.Body.NextCursor = &nc
			}
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
			wsID, err := resolve.WorkspaceMember(ctx, deps.DB, in.WorkspaceID, actorID)
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

// publicIDOrEmpty delegates to handlerutil.PublicIDOrEmpty.
var publicIDOrEmpty = handlerutil.PublicIDOrEmpty

// mapUserRow converts a ListNotificationsForUserRow to a NotificationDTO.
func mapUserRow(r generated.ListNotificationsForUserRow) NotificationDTO {
	return NotificationDTO{
		ID:               r.PublicID.String(),
		WorkspaceID:      r.WorkspacePublicID.String(),
		ActorID:          publicIDOrEmpty(r.ActorPublicID),
		ActorDisplayName: nullStr(r.ActorDisplayName),
		EventType:        r.EventType,
		ResourceType:     r.ResourceType,
		ResourceID:       publicIDOrEmpty(r.ResourcePublicID),
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
		ResourceID:       publicIDOrEmpty(r.ResourcePublicID),
		Title:            r.Title,
		Body:             nullStr(r.Body),
		Severity:         string(r.Severity),
		Channel:          string(r.Channel),
		ReadAt:           nullTimeUnix(r.ReadAt),
		DeliveredAt:      nullTimeUnix(r.DeliveredAt),
		CreatedAt:        r.CreatedAt.Unix(),
	}
}

// mapUserKeysetRow converts a ListNotificationsForUserKeysetRow to a
// NotificationDTO. Same projection as mapUserRow with the Total column
// dropped; kept as a separate function so the row-type signatures stay
// distinct (sqlc generates a fresh type per query).
func mapUserKeysetRow(r generated.ListNotificationsForUserKeysetRow) NotificationDTO {
	return NotificationDTO{
		ID:               r.PublicID.String(),
		WorkspaceID:      r.WorkspacePublicID.String(),
		ActorID:          publicIDOrEmpty(r.ActorPublicID),
		ActorDisplayName: nullStr(r.ActorDisplayName),
		EventType:        r.EventType,
		ResourceType:     r.ResourceType,
		ResourceID:       publicIDOrEmpty(r.ResourcePublicID),
		Title:            r.Title,
		Body:             nullStr(r.Body),
		Severity:         string(r.Severity),
		Channel:          string(r.Channel),
		ReadAt:           nullTimeUnix(r.ReadAt),
		DeliveredAt:      nullTimeUnix(r.DeliveredAt),
		CreatedAt:        r.CreatedAt.Unix(),
	}
}

// mapWorkspaceKeysetRow is the keyset twin of mapWorkspaceRow.
func mapWorkspaceKeysetRow(r generated.ListNotificationsForWorkspaceKeysetRow) NotificationDTO {
	return NotificationDTO{
		ID:               r.PublicID.String(),
		WorkspaceID:      r.WorkspacePublicID.String(),
		ActorID:          publicIDOrEmpty(r.ActorPublicID),
		ActorDisplayName: nullStr(r.ActorDisplayName),
		EventType:        r.EventType,
		ResourceType:     r.ResourceType,
		ResourceID:       publicIDOrEmpty(r.ResourcePublicID),
		Title:            r.Title,
		Body:             nullStr(r.Body),
		Severity:         string(r.Severity),
		Channel:          string(r.Channel),
		ReadAt:           nullTimeUnix(r.ReadAt),
		DeliveredAt:      nullTimeUnix(r.DeliveredAt),
		CreatedAt:        r.CreatedAt.Unix(),
	}
}
