// Package eventacl holds the visibility decision for calendar_events
// rows, in the one place every reader has to go through: the flow-api
// REST list and detail handlers, and the MCP calendar tools.
//
// The rule has two levels, and conflating them is what went wrong
// before. calendar_events.visibility distinguishes hiding a row from
// hiding what is written on it:
//
//	public        every calendar member sees the event and its details
//	default       whatever the calendar's default_event_visibility says,
//	              which is public or private and never confidential
//	private       every calendar member sees that the time is taken;
//	              the location, memo and URL are for the owner, the
//	              creator and the people actually invited
//	confidential  only the owner, the creator and the attendees know
//	              the event exists at all
//
// So `private` is a field-level decision and `confidential` is a
// row-level one. A single boolean cannot express "visible but
// redacted", which is why this package exposes two predicates:
//
//	CanSee         may the actor know the event exists?
//	CanSeeDetails  may the actor read its free-text fields?
//
// Row-level filtering has to happen in SQL, not in a mapper: dropping
// rows after the query would leave list totals counting events the
// caller may not know about. RowVisibilitySQL is the fragment every
// list query AND-s in, and it is a constant here rather than copied
// into each query so a new list endpoint cannot quietly omit it —
// TestEventListQueriesCarryVisibilityFilter checks that the query file
// still contains it verbatim.
//
// Attendance is the exception that makes `private` useful: someone
// invited to a meeting needs the room and the call link, which is
// precisely what a naive "owner only" scrub withholds. AttendeeExistsSQL
// is the matching fragment; list queries project it as is_attendee so
// the mapper can decide per row without a query per row.
//
// Who is an attendee of one occurrence follows the override-attendee
// rule in sql/core/PROTOCOL.md: an override row with any attendee row of
// its own replaces the series' list; one with none inherits it.
//
// System calendars (holiday feeds) carry no personal data and no
// confidential rows; they need no exception here, and leaving one out
// keeps the SQL fragment free of a join on `calendars` that several of
// the list queries do not have.
//
// `default` resolves against calendars.default_event_visibility, which
// is the setting the event column's own comment always referred to.
// Callers supply it as Event.CalendarDefault; the zero value reads as
// public, which is the column default and the behaviour that predates
// the setting.
package eventacl

import "strings"

// Visibility mirrors the calendar_events.visibility ENUM string.
type Visibility string

const (
	VisibilityDefault      Visibility = "default"
	VisibilityPublic       Visibility = "public"
	VisibilityPrivate      Visibility = "private"
	VisibilityConfidential Visibility = "confidential"
)

// Event is the minimal projection of a calendar_events row needed to
// decide visibility. Callers fill it from their sqlc-generated row.
type Event struct {
	Visibility  Visibility
	OwnerUserID uint32
	// CreatedByUserID is the row's created_by_user_id. Zero means the
	// caller does not know it, and the creator is then not a party.
	CreatedByUserID uint32
	// CalendarDefault is the calendars.default_event_visibility of the
	// calendar the event sits on, and is what Visibility resolves to when
	// it is 'default'. Leaving it zero means public, which is both the
	// column's own default and the behaviour that predates it.
	CalendarDefault Visibility
}

// effective resolves 'default' against the calendar's setting, and reads
// anything it does not recognise as confidential.
//
// The setting cannot name confidential, so 'default' never hides a row —
// which is what lets the SQL row filter stay free of a join on calendars.
// An absent setting reads as public, matching the column default rather
// than inventing a stricter answer the operator did not choose; the zero
// CalendarDefault is a documented "public" that callers leave unset, so it
// is a value here rather than an unrecognised one.
//
// A visibility outside the four constants is different: nobody chose it,
// and the two predicates below are written as "everything except the one
// restrictive value", so passing it through would make it the most
// permissive answer the type can give. Confidential is the most
// restrictive — the row is not disclosed and neither are its fields — and
// matching roleRank's fail-closed direction is what keeps a value added to
// the column but not to this package from being published on the way in.
func (e Event) effective() Visibility {
	switch e.Visibility {
	case VisibilityPublic, VisibilityPrivate, VisibilityConfidential:
		return e.Visibility
	case VisibilityDefault, "":
		if e.CalendarDefault == VisibilityPrivate {
			return VisibilityPrivate
		}
		return VisibilityPublic
	default:
		return VisibilityConfidential
	}
}

// Actor is the requesting user's context. IsAttendee is true when the
// actor is an enabled attendee of this occurrence (see AttendeeExistsSQL);
// list queries supply it from the is_attendee column, detail handlers
// from a direct lookup.
//
// Reaching this package already means the caller established that the
// actor may reach the calendar — membership is checked by the calendar
// resolution helpers, and this package would give a wrong answer if
// asked to stand in for them.
type Actor struct {
	UserID     uint32
	IsAttendee bool
}

// isParty reports whether the actor is the event's owner or creator, or
// an attendee of the occurrence.
func (e Event) isParty(actor Actor) bool {
	if actor.UserID != 0 && (e.OwnerUserID == actor.UserID || e.CreatedByUserID == actor.UserID) {
		return true
	}
	return actor.IsAttendee
}

// CanSee reports whether the actor may know the event exists.
//
// Only `confidential` hides a row, and only from anyone who is not a
// party to it: the owner, the creator or an attendee of the occurrence.
func CanSee(evt Event, actor Actor) bool {
	if evt.effective() != VisibilityConfidential {
		return true
	}
	return evt.isParty(actor)
}

// CanSeeDetails reports whether the actor may read the event's
// free-text fields: location, memo and URL.
//
// A row the actor may not see at all has no readable details either, so
// this is strictly narrower than CanSee. Beyond that, `private` limits
// the details to the parties.
func CanSeeDetails(evt Event, actor Actor) bool {
	if !CanSee(evt, actor) {
		return false
	}
	if evt.effective() != VisibilityPrivate {
		return true
	}
	return evt.isParty(actor)
}

// AnonymousView is what a reader reaching an event through a public
// share or embed may be shown. Times and all_day are shown whenever
// Exists is true; memo and url are never shown.
type AnonymousView struct {
	// Exists is false for an event the reader is told nothing about.
	Exists   bool
	Title    bool
	Location bool
}

// AnonymousFields decides the anonymous column of the visibility table.
func AnonymousFields(evt Event) AnonymousView {
	switch evt.effective() {
	case VisibilityPublic:
		return AnonymousView{Exists: true, Title: true, Location: true}
	case VisibilityPrivate:
		return AnonymousView{Exists: true}
	default:
		return AnonymousView{}
	}
}

// RowVisibilitySQL is the WHERE fragment that removes rows the actor
// may not know about. Every calendar_events list query AND-s it in.
//
// The fragment names `ce` for calendar_events and takes the viewer's
// internal user id as the named argument viewer_user_id, which the
// queries reuse for AttendeeExistsSQL so every use costs one bind. It
// deliberately joins nothing, so a query that does not already have
// `calendars` can still apply it.
const RowVisibilitySQL = `(ce.visibility <> 'confidential'
    OR ce.owner_user_id = sqlc.arg(viewer_user_id)
    OR ce.created_by_user_id = sqlc.arg(viewer_user_id)
    OR ` + AttendeeExistsSQL + `)`

// RowVisibilityFilter is [RowVisibilitySQL] for a statement assembled at
// runtime: the same fragment with the named argument as a positional
// bind, so a hand-written query cannot spell the rule differently. The
// viewer is bound once per occurrence of the named argument.
func RowVisibilityFilter(viewerUserID uint32) (fragment string, args []any) {
	const arg = "sqlc.arg(viewer_user_id)"
	n := strings.Count(RowVisibilitySQL, arg)
	args = make([]any, n)
	for i := range args {
		args[i] = viewerUserID
	}
	return strings.ReplaceAll(RowVisibilitySQL, arg, "?"), args
}

// AttendeeExistsSQL is the SELECT fragment that tells a list query
// whether the viewer is an enabled attendee of the occurrence, which
// decides whether the private-event details are theirs to read and
// whether a confidential event exists for them.
//
// A correlated EXISTS rather than a join, so an event with several
// attendees still produces one row. An override whose own attendee rows
// are absent (enabled or not) reads the master's; one that has any
// replaces them.
const AttendeeExistsSQL = `EXISTS (
    SELECT 1 FROM calendar_event_attendees a
    WHERE a.event_id = CASE
        WHEN ce.recurrence_parent_id IS NOT NULL
          AND NOT EXISTS (SELECT 1 FROM calendar_event_attendees o WHERE o.event_id = ce.id)
        THEN ce.recurrence_parent_id ELSE ce.id END
      AND a.user_id = sqlc.arg(viewer_user_id)
      AND a.enabled = TRUE
  )`

// ----------------------------------------------------------------------------
// Write rule
// ----------------------------------------------------------------------------

// Role mirrors the calendar_members.role ENUM.
type Role string

const (
	RoleOwner   Role = "owner"
	RoleManager Role = "manager"
	RoleEditor  Role = "editor"
	RoleViewer  Role = "viewer"
	// RoleNone is the zero value: the actor holds no membership row.
	RoleNone Role = ""
)

// roleRank orders the roles so a check reads as "at least this much".
// An unrecognised value ranks below viewer: a role added to the enum but
// not here must fail closed rather than outrank everyone.
func roleRank(r Role) int {
	switch r {
	case RoleOwner:
		return 4
	case RoleManager:
		return 3
	case RoleEditor:
		return 2
	case RoleViewer:
		return 1
	}
	return 0
}

// Editor is the actor's standing on a calendar, as far as changing what
// is on it is concerned.
type Editor struct {
	UserID uint32
	// CalendarRole is the actor's calendar_members.role, or RoleNone
	// when they hold no membership.
	CalendarRole Role
	// AttendeeCanEdit is true when the actor is an attendee of the event
	// whose can_edit flag the owner set.
	AttendeeCanEdit bool
}

// CanEdit reports whether the actor may change an existing event.
//
// Three ways to qualify: owning the event, being an attendee the owner
// marked can_edit, or holding manager or owner on the calendar.
// Workspace membership alone is not one of them — a calendar's audience
// is narrower than its workspace, and an editor may add their own events
// without gaining the right to rewrite everyone else's.
//
// The manager path is the delegation case, and it is why this rule reads
// calendar_members rather than calendars.owner_user_id. A shared
// calendar leaves owner_user_id NULL on purpose (naming an owner makes
// the FK cascade delete everyone's history with that user), so an
// implementation keyed on it refuses every manager on exactly the
// calendars managers exist for.
func CanEdit(eventOwnerUserID uint32, actor Editor) bool {
	if actor.UserID != 0 && eventOwnerUserID == actor.UserID {
		return true
	}
	if actor.AttendeeCanEdit {
		return true
	}
	return roleRank(actor.CalendarRole) >= roleRank(RoleManager)
}

// CanSetOwner reports whether the actor may file an event under the
// given owner, which decides whose layer and colour it appears on.
//
// Anyone may create their own events. Filing one under somebody else is
// delegation and takes manager or owner on the calendar; the alternative
// is that any editor can put commitments on a colleague's layer.
func CanSetOwner(ownerUserID uint32, actor Editor) bool {
	if actor.UserID != 0 && actor.UserID == ownerUserID {
		return true
	}
	return roleRank(actor.CalendarRole) >= roleRank(RoleManager)
}
