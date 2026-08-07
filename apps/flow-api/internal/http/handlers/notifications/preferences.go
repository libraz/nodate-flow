package notifications

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/notification/prefs"
)

// ListPreferences handles
// GET /workspaces/{wsId}/notification-preferences.
//
// Always the caller's own preferences: there is no user path segment
// and no query parameter to name someone else, so membership in the
// workspace is the whole authorisation check.
func ListPreferences(deps Deps) func(context.Context, *ListPreferencesInput) (*PreferencesOutput, error) {
	return func(ctx context.Context, _ *ListPreferencesInput) (*PreferencesOutput, error) {
		actorID, wsID, err := selfInWorkspace(ctx)
		if err != nil {
			return nil, err
		}
		stored, err := loadStoredPreferences(ctx, deps, wsID, actorID)
		if err != nil {
			return nil, err
		}
		out := &PreferencesOutput{}
		out.Body.Preferences = effectiveMatrix(stored)
		return out, nil
	}
}

// UpdatePreferences handles
// PUT /workspaces/{wsId}/notification-preferences.
//
// Every listed cell is validated before any of them is written, so a
// body naming one unknown category cannot leave half of it applied.
func UpdatePreferences(deps Deps) func(context.Context, *UpdatePreferencesInput) (*PreferencesOutput, error) {
	return func(ctx context.Context, in *UpdatePreferencesInput) (*PreferencesOutput, error) {
		actorID, wsID, err := selfInWorkspace(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range in.Body.Preferences {
			if !prefs.ValidCategory(p.EventCategory) || !prefs.ValidChannel(p.Channel) {
				return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
			}
		}

		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer func() { _ = tx.Rollback() }()
		qtx := deps.Queries.WithTx(tx)

		for _, p := range in.Body.Preferences {
			if err := qtx.UpsertNotificationPreference(ctx, generated.UpsertNotificationPreferenceParams{
				PublicID:      types.New(),
				WorkspaceID:   wsID,
				UserID:        actorID,
				EventCategory: p.EventCategory,
				Channel:       generated.NotificationPreferencesChannel(p.Channel),
				IsMuted:       p.Muted,
			}); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "notification.preferences.update",
			ActorID:      actorID,
			WorkspaceID:  wsID,
			ResourceType: "notification_preference",
		})

		stored, err := loadStoredPreferences(ctx, deps, wsID, actorID)
		if err != nil {
			return nil, err
		}
		out := &PreferencesOutput{}
		out.Body.Preferences = effectiveMatrix(stored)
		return out, nil
	}
}

// selfInWorkspace pulls the actor and the resolved workspace out of the
// request context. Both are placed there by the auth and workspace
// middleware the preference routes are mounted behind.
func selfInWorkspace(ctx context.Context) (actorID uint32, wsID uint32, err error) {
	actorID, ok := middleware.ActorFromContext(ctx)
	if !ok {
		return 0, 0, httpErr(apierrors.WsWorkspaceAccessDenied)
	}
	wsCtx, ok := middleware.WorkspaceFromContext(ctx)
	if !ok {
		return 0, 0, httpErr(apierrors.WsWorkspaceNotFound)
	}
	return actorID, wsCtx.ID, nil
}

// loadStoredPreferences reads the caller's stored rows, keyed by event
// category. Categories with no stored row are simply absent; the
// defaults are applied in [effectiveMatrix].
func loadStoredPreferences(ctx context.Context, deps Deps, wsID, actorID uint32) (map[string][]prefs.Pref, error) {
	rows, err := deps.Queries.ListNotificationPreferencesForUser(ctx, generated.ListNotificationPreferencesForUserParams{
		WorkspaceID: wsID,
		UserID:      actorID,
	})
	if err != nil {
		return nil, httpErr(apierrors.InternalUnexpected)
	}
	byCategory := make(map[string][]prefs.Pref, len(prefs.Categories))
	for _, r := range rows {
		byCategory[r.EventCategory] = append(byCategory[r.EventCategory], prefs.Pref{
			Channel: generated.NotificationsChannel(r.Channel),
			Muted:   r.IsMuted,
		})
	}
	return byCategory, nil
}

// effectiveMatrix renders every category × channel cell with the value
// fan-out would act on, resolving stored rows against the channel
// defaults through the same [prefs.ChannelMuted] the delivery
// path uses. A row stored for a category outside
// [prefs.Categories] is ignored rather than surfaced, because a
// cell no client can render is a cell no client can turn back off.
func effectiveMatrix(stored map[string][]prefs.Pref) []NotificationPreferenceDTO {
	out := make([]NotificationPreferenceDTO, 0, len(prefs.Categories)*len(prefs.Channels))
	for _, category := range prefs.Categories {
		stored := stored[category]
		for _, channel := range prefs.Channels {
			out = append(out, NotificationPreferenceDTO{
				EventCategory: category,
				Channel:       string(channel),
				Muted:         prefs.ChannelMuted(stored, channel),
			})
		}
	}
	return out
}
