package calendars

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	generated "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/region"
)

// resolveEffectiveTimezone returns the IANA timezone to use for a request in
// priority order: explicit request value > user preference > workspace default
// > region.DefaultTimezone. The returned string is validated; if explicit is
// provided but invalid, the error is surfaced so the caller can return 422.
func resolveEffectiveTimezone(ctx context.Context, q *generated.Queries, wsID, actorID uint32, explicit string) (string, error) {
	if explicit != "" {
		if err := region.ValidateTimezone(explicit); err != nil {
			return "", err
		}
		return explicit, nil
	}
	var userTz string
	if profile, err := q.FindUserProfileById(ctx, actorID); err == nil {
		userTz = profile.Timezone
	}
	var wsTz string
	// Best-effort workspace lookup; fallback to UTC if it fails.
	if row, err := q.FindWorkspaceTimezoneCountryById(ctx, wsID); err == nil {
		wsTz = row.Timezone
	}
	return region.EffectiveTimezone(userTz, wsTz), nil
}

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

// roleRank orders calendar_members.role so a check can be written as
// "at least this much". owner > manager > editor > viewer.
func roleRank(r calendar.CalendarMembersRole) int {
	switch r {
	case calendar.CalendarMembersRoleOwner:
		return 4
	case calendar.CalendarMembersRoleManager:
		return 3
	case calendar.CalendarMembersRoleEditor:
		return 2
	case calendar.CalendarMembersRoleViewer:
		return 1
	}
	// An unrecognised value grants nothing. A role added to the enum but
	// not to this switch must fail closed rather than outrank a viewer.
	return 0
}

// resolveCalendar parses the calId UUID string within a workspace and returns
// the calendar row along with the actor's membership of it.
//
// The lookup and the access check are one function on purpose. Resolving a
// calendar id without consulting calendar_members is how an authorization
// check gets omitted — not by anyone deciding to skip it, but by the two
// steps being separable at all. Handlers that need more than read access
// call resolveCalendarWrite or resolveCalendarAdmin instead, which are the
// same resolution with a floor on the role.
//
// Absent membership resolves to the same not-found error as an unknown
// calendar id, so a non-member cannot tell which one they hit.
func resolveCalendar(
	ctx context.Context,
	cq *calendar.Queries,
	wsID uint32,
	actorID uint32,
	calIDStr string,
) (calendar.FindCalendarByPublicIdRow, calendar.FindCalendarMemberRow, error) {
	uid, err := uuid.Parse(calIDStr)
	if err != nil {
		return calendar.FindCalendarByPublicIdRow{}, calendar.FindCalendarMemberRow{}, errCalendarNotFound
	}
	cal, err := cq.FindCalendarByPublicId(ctx, calendar.FindCalendarByPublicIdParams{
		PublicID:    types.FromUUID(uid),
		WorkspaceID: wsID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return calendar.FindCalendarByPublicIdRow{}, calendar.FindCalendarMemberRow{}, errCalendarNotFound
		}
		return calendar.FindCalendarByPublicIdRow{}, calendar.FindCalendarMemberRow{}, err
	}
	member, err := cq.FindCalendarMember(ctx, calendar.FindCalendarMemberParams{
		CalendarID: cal.ID,
		UserID:     actorID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return calendar.FindCalendarByPublicIdRow{}, calendar.FindCalendarMemberRow{}, errCalendarAccessDenied
		}
		return calendar.FindCalendarByPublicIdRow{}, calendar.FindCalendarMemberRow{}, err
	}
	return cal, member, nil
}

// findSubscription returns the actor's display preferences for a calendar,
// or nil when they have none.
//
// A missing row is the normal case, not an error: preferences are created
// the first time someone changes a colour or hides a layer, and access does
// not depend on them. Read errors collapse to nil for the same reason —
// failing a calendar read because a preference lookup hiccuped would trade
// a cosmetic default for an outage.
func findSubscription(
	ctx context.Context,
	cq *calendar.Queries,
	calID uint32,
	actorID uint32,
) *calendar.FindCalendarSubscriptionRow {
	sub, err := cq.FindCalendarSubscription(ctx, calendar.FindCalendarSubscriptionParams{
		CalendarID: calID,
		UserID:     actorID,
	})
	if err != nil {
		return nil
	}
	return &sub
}

// resolveCalendarAtLeast resolves the calendar and refuses unless the actor
// holds at least the given role.
//
// The two refusals are distinct on purpose. No membership resolves to
// access-denied, the same answer a non-member gets for any calendar.
// Insufficient role resolves to role-required, because the actor can
// already see the calendar and telling them they need a higher role is
// actionable rather than a leak.
func resolveCalendarAtLeast(
	ctx context.Context,
	cq *calendar.Queries,
	wsID uint32,
	actorID uint32,
	calIDStr string,
	least calendar.CalendarMembersRole,
) (calendar.FindCalendarByPublicIdRow, calendar.FindCalendarMemberRow, error) {
	cal, member, err := resolveCalendar(ctx, cq, wsID, actorID, calIDStr)
	if err != nil {
		return cal, member, err
	}
	if roleRank(member.Role) < roleRank(least) {
		return calendar.FindCalendarByPublicIdRow{}, calendar.FindCalendarMemberRow{}, errForbidden
	}
	return cal, member, nil
}

// resolveCalendarWrite resolves the calendar for a handler that changes its
// contents: events, attendees, comments, attachments, memos.
func resolveCalendarWrite(
	ctx context.Context,
	cq *calendar.Queries,
	wsID uint32,
	actorID uint32,
	calIDStr string,
) (calendar.FindCalendarByPublicIdRow, calendar.FindCalendarMemberRow, error) {
	return resolveCalendarAtLeast(ctx, cq, wsID, actorID, calIDStr, calendar.CalendarMembersRoleEditor)
}

// resolveCalendarAdmin resolves the calendar for a handler that changes the
// calendar itself or who may reach it.
func resolveCalendarAdmin(
	ctx context.Context,
	cq *calendar.Queries,
	wsID uint32,
	actorID uint32,
	calIDStr string,
) (calendar.FindCalendarByPublicIdRow, calendar.FindCalendarMemberRow, error) {
	return resolveCalendarAtLeast(ctx, cq, wsID, actorID, calIDStr, calendar.CalendarMembersRoleManager)
}
