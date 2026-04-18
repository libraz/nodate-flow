package calendars

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/http/middleware"
)

// resolveWorkspace parses the wsId UUID string, looks up the internal workspace
// ID, and verifies the actor is a workspace member.
func resolveWorkspace(ctx context.Context, q *generated.Queries, wsIDStr string) (uint32, uint32, error) {
	actorID, ok := middleware.ActorFromContext(ctx)
	if !ok {
		return 0, 0, errAccessDenied
	}
	uid, err := uuid.Parse(wsIDStr)
	if err != nil {
		return 0, 0, errWorkspaceNotFound
	}
	ws, err := q.FindWorkspaceByPublicId(ctx, types.FromUUID(uid))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, errWorkspaceNotFound
		}
		return 0, 0, err
	}
	_, err = q.FindWorkspaceMemberByUserId(ctx, generated.FindWorkspaceMemberByUserIdParams{
		WorkspaceID: ws.ID,
		UserID:      actorID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, errAccessDenied
		}
		return 0, 0, err
	}
	return ws.ID, actorID, nil
}

// resolveCalendar parses the calId UUID string within a workspace and returns
// the calendar row along with the actor's subscription to it.
func resolveCalendar(
	ctx context.Context,
	q *generated.Queries,
	wsID uint32,
	actorID uint32,
	calIDStr string,
) (generated.FindCalendarByPublicIdRow, generated.FindCalendarSubscriptionRow, error) {
	uid, err := uuid.Parse(calIDStr)
	if err != nil {
		return generated.FindCalendarByPublicIdRow{}, generated.FindCalendarSubscriptionRow{}, errCalendarNotFound
	}
	cal, err := q.FindCalendarByPublicId(ctx, generated.FindCalendarByPublicIdParams{
		PublicID:    types.FromUUID(uid),
		WorkspaceID: wsID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return generated.FindCalendarByPublicIdRow{}, generated.FindCalendarSubscriptionRow{}, errCalendarNotFound
		}
		return generated.FindCalendarByPublicIdRow{}, generated.FindCalendarSubscriptionRow{}, err
	}
	sub, err := q.FindCalendarSubscription(ctx, generated.FindCalendarSubscriptionParams{
		CalendarID: cal.ID,
		UserID:     actorID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return generated.FindCalendarByPublicIdRow{}, generated.FindCalendarSubscriptionRow{}, errCalendarAccessDenied
		}
		return generated.FindCalendarByPublicIdRow{}, generated.FindCalendarSubscriptionRow{}, err
	}
	return cal, sub, nil
}
