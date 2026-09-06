package calendars

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	generated "github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// resolveEffectiveTimezone returns the timezone to use for a request in
// priority order: explicit request value > user preference > workspace default
// > region.DefaultTimezone.
//
// It answers a [region.Zone] rather than a name, so the caller has the
// resolved zone in hand and no second place to decide what an
// unresolvable one means. Handing back a string is how the endpoints
// downstream ended up with four different readings of the same column:
// one erroring, one silently substituting UTC, one falling through to
// the next candidate and one refusing the request.
//
// An explicit value is checked through requireValidTimezone, so the error it
// returns is already the API error every calendar surface answers for a
// timezone it cannot resolve — callers propagate it rather than choosing a
// code of their own. Each caller choosing is how the same rejected input came
// back as a 400 from one endpoint, a 422 from another and a 500 from a third.
func resolveEffectiveTimezone(ctx context.Context, q *generated.Queries, wsID, actorID uint32, explicit string) (region.Zone, error) {
	if explicit != "" {
		if err := requireValidTimezone("timezone", explicit); err != nil {
			return region.Zone{}, err
		}
	}
	var userTz string
	if profile, err := q.FindUserProfileById(ctx, actorID); err == nil {
		userTz = storedTimezone(profile.Timezone)
	}
	var wsTz string
	// Best-effort preference lookups; the chain's own fallback covers a
	// row that could not be read.
	if row, err := q.FindWorkspaceTimezoneCountryById(ctx, wsID); err == nil {
		wsTz = storedTimezone(row.Timezone)
	}
	z, err := region.Resolve(explicit, userTz, wsTz)
	if err != nil {
		// Unreachable: the explicit value passed requireValidTimezone,
		// the stored ones passed storedTimezone, and the chain's own
		// fallback is DefaultTimezone. Refuse rather than continue, so
		// that if it ever does become reachable it does so loudly.
		return region.Zone{}, handlerutil.HTTPErrFromAPIError(
			apierr.New(apierrors.ValidationBodyFieldInvalid).WithDetail("field", "timezone"))
	}
	return z, nil
}

// storedTimezone drops a stored preference the zoneinfo database cannot
// resolve, so the chain moves on to the next tier.
//
// The tiers below it are the ones the schema already documents — the
// workspace's default, then UTC — so a user whose zone was retired from
// the database gets their organisation's calendar rather than a column
// full of a name no client can place an event with. Refusing the request
// instead would take the whole calendar away from someone whose profile
// they cannot see, for a value they may never have typed.
func storedTimezone(tz string) string {
	if region.ValidateTimezone(tz) != nil {
		return ""
	}
	return tz
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

// workspaceRoleOf returns the actor's workspace role, for the calendar
// routes that have to apply a rule the role takes part in.
//
// It is a separate read rather than a fourth return from
// [resolveWorkspace] because the calendar groups mount RequireAuth only:
// workspace membership is settled inside the handler, so there is no
// WorkspaceContext to take a role from the way the task routes do.
//
// A role the enum does not know is refused rather than defaulted. A
// default would have to be one of "deny everything" or "allow
// everything", and the second is how a drifted row turns into elevated
// access nobody granted.
func workspaceRoleOf(ctx context.Context, q *generated.Queries, wsID, actorID uint32) (acl.WorkspaceRole, error) {
	member, err := q.FindWorkspaceMemberByUserId(ctx, generated.FindWorkspaceMemberByUserIdParams{
		WorkspaceID: wsID,
		UserID:      actorID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errAccessDenied
		}
		return "", err
	}
	role := acl.WorkspaceRole(member.Role)
	if !role.IsValid() {
		return "", httpErr(apierrors.InternalUnexpected)
	}
	return role, nil
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
// actionable rather than a leak — and the code names the floor this call
// site asked for, through [roleFloorSpec], so the role the caller is told
// to request is the one that would actually admit them.
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
		return calendar.FindCalendarByPublicIdRow{}, calendar.FindCalendarMemberRow{}, errRoleFloor(least)
	}
	return cal, member, nil
}

// CalendarWriteDecision is the answer the calendar-write rule gives about
// a member's standing on a calendar.
//
// The decision is exported, and separate from [resolveCalendarWrite],
// because the MCP transport reaches the same calendars through resolvers
// of its own and has to reach the same answer. Sharing the decision
// rather than the sentence describing it is the difference between one
// rule and two implementations of one rule: the MCP calendar tools
// already came apart from these handlers once that way, deciding on
// calendars.owner_user_id what the web app decided on calendar_members.
type CalendarWriteDecision int

const (
	// CalendarWriteAllowed means the member may change the calendar's
	// contents.
	CalendarWriteAllowed CalendarWriteDecision = iota
	// CalendarWriteRoleTooLow is a member below editor.
	CalendarWriteRoleTooLow
	// CalendarWriteCalendarReadOnly is a calendar whose contents belong to
	// a provider feed, refused at every role.
	CalendarWriteCalendarReadOnly
)

// DecideCalendarWrite answers whether a member holding role may change the
// contents of a calendar of the given kind: events, attendees, comments,
// attachments, memos, checklists, invites.
//
// Two refusals rather than one. A viewer is refused because writing is what
// separates an editor from a viewer, and a calendar's contents are visible to
// every one of its members — a read-only member who can post is a read-only
// member who can address the whole audience. A system calendar is refused at
// any role because its contents come from a provider feed: a row a user adds
// there has no source to be reconciled against and survives no refresh.
//
// Membership itself is the caller's to establish first. A caller who holds
// no calendar_members row arrives here with the zero role, which ranks
// below viewer and is refused — but being refused for the wrong reason is
// still the wrong answer, so both transports resolve membership before
// asking this.
func DecideCalendarWrite(kind calendar.CalendarsKind, role calendar.CalendarMembersRole) CalendarWriteDecision {
	if roleRank(role) < roleRank(calendarWriteFloor) {
		return CalendarWriteRoleTooLow
	}
	if kind == calendar.CalendarsKindSystem {
		return CalendarWriteCalendarReadOnly
	}
	return CalendarWriteAllowed
}

// calendarWriteFloor is the calendar_members role a change to a calendar's
// contents requires. It is named once so the comparison and the refusal
// that reports it cannot come to name different roles.
const calendarWriteFloor = calendar.CalendarMembersRoleEditor

// CalendarWriteRefusalSpec answers which API error a
// [CalendarWriteDecision] is reported as, or nil when the write may
// proceed.
//
// It is exported alongside [DecideCalendarWrite] because sharing only the
// decision left each transport to name a code for it, and a client could
// tell which transport it had reached from the refusal alone. The two
// halves of one rule — what is decided and what the caller is told —
// travel together.
//
// A role that is too low is reported as the role the write actually needs.
// A system calendar is reported as the calendar access code instead of a
// role code, because that refusal is not about the caller's role at all:
// its rows are owned by a provider feed, so a row written by hand has no
// source to be reconciled against and no member, at any role, is the
// exception.
func CalendarWriteRefusalSpec(d CalendarWriteDecision) *apierrors.Spec {
	switch d {
	case CalendarWriteRoleTooLow:
		return roleFloorSpec(calendarWriteFloor)
	case CalendarWriteCalendarReadOnly:
		return apierrors.CalendarCalendarAccessDenied
	case CalendarWriteAllowed:
	}
	return nil
}

// CalendarStanding is a calendar's kind together with a caller's role on
// it, as one value because the write rule needs both halves. A nil
// *CalendarStanding is a caller who holds no calendar_members row on it.
type CalendarStanding struct {
	Kind calendar.CalendarsKind
	Role calendar.CalendarMembersRole
}

// EventPathWriteRefusalSpec answers which API error a caller earns for
// writing to a calendar they reached by naming an event rather than the
// calendar itself, or nil when the write may proceed.
//
// It differs from [CalendarWriteRefusalSpec] only in the no-membership
// case, which collapses to not-found. When the request never names a
// calendar, a refusal distinguishable from "no such event" is a pure
// existence oracle: it answers whether that id names a live event in this
// workspace, to a caller who cannot open, list or read the calendar it
// sits on and who therefore cannot act on the answer.
//
// The other two refusals stay distinct from each other because by then the
// caller is a member: they can already read the calendar, so neither
// answer discloses anything new, and asking for a higher role is
// actionable where a provider feed is not.
func EventPathWriteRefusalSpec(standing *CalendarStanding) *apierrors.Spec {
	if standing == nil {
		return apierrors.CalendarEventNotFound
	}
	return CalendarWriteRefusalSpec(DecideCalendarWrite(standing.Kind, standing.Role))
}

// resolveCalendarWrite resolves the calendar for a handler that changes its
// contents, applying [DecideCalendarWrite] to the resolved membership.
func resolveCalendarWrite(
	ctx context.Context,
	cq *calendar.Queries,
	wsID uint32,
	actorID uint32,
	calIDStr string,
) (calendar.FindCalendarByPublicIdRow, calendar.FindCalendarMemberRow, error) {
	cal, member, err := resolveCalendar(ctx, cq, wsID, actorID, calIDStr)
	if err != nil {
		return cal, member, err
	}
	if spec := CalendarWriteRefusalSpec(DecideCalendarWrite(cal.Kind, member.Role)); spec != nil {
		return calendar.FindCalendarByPublicIdRow{}, calendar.FindCalendarMemberRow{}, httpErr(spec)
	}
	return cal, member, nil
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
