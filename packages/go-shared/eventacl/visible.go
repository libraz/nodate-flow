// Package eventacl holds the visibility decision for calendar_events
// rows, in the one place every reader has to go through: the flow-api
// REST list and detail handlers, and the MCP calendar tools.
//
// The rule has two levels, and conflating them is what went wrong
// before. calendar_events.visibility distinguishes hiding a row from
// hiding what is written on it:
//
//	public        every calendar member sees the event and its details
//	default       treated as public (see the note below)
//	private       every calendar member sees that the time is taken;
//	              the location, memo and URL are for the owner and the
//	              people actually invited
//	confidential  only the owner knows the event exists at all
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
// System calendars (holiday feeds) carry no personal data and no
// confidential rows; they need no exception here, and leaving one out
// keeps the SQL fragment free of a join on `calendars` that several of
// the list queries do not have.
//
// `default` is treated as public because nothing resolves it against a
// calendar-level setting yet — there is no such setting. That is a
// separate gap; this package does not paper over it by guessing.
package eventacl

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
}

// Actor is the requesting user's context. IsAttendee is true when the
// actor has an enabled row in calendar_event_attendees for this event;
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

// CanSee reports whether the actor may know the event exists.
//
// Only `confidential` hides a row, and only from anyone who is not its
// owner. Attendance is deliberately not an exception: a confidential
// event with attendees would be a contradiction the column cannot
// express, and treating attendance as consent here would let the owner
// leak the event by adding someone to it.
func CanSee(evt Event, actor Actor) bool {
	if evt.Visibility != VisibilityConfidential {
		return true
	}
	return actor.UserID != 0 && evt.OwnerUserID == actor.UserID
}

// CanSeeDetails reports whether the actor may read the event's
// free-text fields: location, memo and URL.
//
// A row the actor may not see at all has no readable details either, so
// this is strictly narrower than CanSee. Beyond that, `private` limits
// the details to the owner and the invited.
func CanSeeDetails(evt Event, actor Actor) bool {
	if !CanSee(evt, actor) {
		return false
	}
	if evt.Visibility != VisibilityPrivate {
		return true
	}
	if actor.UserID != 0 && evt.OwnerUserID == actor.UserID {
		return true
	}
	return actor.IsAttendee
}

// RowVisibilitySQL is the WHERE fragment that removes rows the actor
// may not know about. Every calendar_events list query AND-s it in.
//
// The fragment names `ce` for calendar_events and takes the viewer's
// internal user id as the named argument viewer_user_id, which the
// queries reuse for AttendeeExistsSQL so both cost one bind between
// them. It deliberately joins nothing, so a query that does not already
// have `calendars` can still apply it.
const RowVisibilitySQL = `(ce.visibility <> 'confidential' OR ce.owner_user_id = sqlc.arg(viewer_user_id))`

// AttendeeExistsSQL is the SELECT fragment that tells a list query
// whether the viewer is on the event's attendee list, which decides
// whether the private-event details are theirs to read.
//
// A correlated EXISTS rather than a join, so an event with several
// attendees still produces one row.
const AttendeeExistsSQL = `EXISTS (
    SELECT 1 FROM calendar_event_attendees a
    WHERE a.event_id = ce.id
      AND a.user_id = sqlc.arg(viewer_user_id)
      AND a.enabled = TRUE
  )`
