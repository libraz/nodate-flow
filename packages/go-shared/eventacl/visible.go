// Package eventacl holds the shared visibility decision for
// calendar_events rows. The same predicate is applied by:
//   - flow-api calendar list / detail handlers
//   - itemkit (before mutating any cross-table link)
//   - the reconciler when scanning for drift
//
// Keeping the rule in one place prevents the "I'm assigned but can't
// see the event" bug and the reverse — confidential events leaking
// through a list endpoint that forgot a filter.
//
// Rule:
//
//	visibility='public'       → every ws member sees the event
//	visibility='default'      → inherits calendar's effective visibility
//	                            (we treat it identically to 'public' for
//	                             listing purposes because a calendar-level
//	                             default is always computed by the caller
//	                             before reaching this layer)
//	visibility='private'      → owner + attendees only
//	visibility='confidential' → owner only (attendees forbidden)
//
// System calendars (holiday feeds) are visible to every workspace
// member regardless of visibility because the rows are intentionally
// public by definition.
package eventacl

// Visibility mirrors the calendar_events.visibility ENUM string.
type Visibility string

const (
	VisibilityDefault      Visibility = "default"
	VisibilityPublic       Visibility = "public"
	VisibilityPrivate      Visibility = "private"
	VisibilityConfidential Visibility = "confidential"
)

// CalendarKind mirrors the calendars.kind ENUM. Only the values
// relevant to the ACL decision are listed.
type CalendarKind string

const (
	CalendarKindPersonal CalendarKind = "personal"
	CalendarKindSystem   CalendarKind = "system"
)

// Event is the minimal projection of a calendar_events row needed to
// decide visibility for an actor. Callers fill in the fields from
// their sqlc-generated row before calling CanSee.
type Event struct {
	Visibility      Visibility
	CalendarKind    CalendarKind
	OwnerUserID     uint32
	CalendarOwnerID uint32 // calendars.owner_user_id (zero for non-personal)
}

// Actor is the minimal projection of the requesting user's context
// needed to decide visibility. IsAttendee is true when the actor has
// a row in calendar_event_attendees for the target event.
type Actor struct {
	UserID            uint32
	IsWorkspaceMember bool
	IsAttendee        bool
}

// CanSee is the single visibility predicate. Returns true when actor
// may read the event's details. System-calendar rows are always
// visible to workspace members (holidays are intentionally public).
func CanSee(evt Event, actor Actor) bool {
	if !actor.IsWorkspaceMember {
		return false
	}
	if evt.CalendarKind == CalendarKindSystem {
		return true
	}
	if actor.UserID != 0 && (evt.OwnerUserID == actor.UserID || evt.CalendarOwnerID == actor.UserID) {
		return true
	}
	switch evt.Visibility {
	case VisibilityPublic, VisibilityDefault, "":
		return true
	case VisibilityPrivate:
		return actor.IsAttendee
	case VisibilityConfidential:
		return false
	default:
		return false
	}
}

// ListFilterSQL returns a WHERE-clause fragment that filters a
// calendar_events list query down to rows the given actor may see.
// The fragment assumes the query already joins `calendar_events ce`
// and `calendars c`. Bind the actor's internal user id twice for the
// `?` placeholders.
//
// Attendee membership is checked via a correlated EXISTS rather than
// a JOIN so list queries don't double-row events with multiple
// attendees. The workspace_members scope is the caller's
// responsibility — this fragment only encodes the visibility rule.
const ListFilterSQL = `(
	c.kind = 'system'
	OR c.owner_user_id = ?
	OR ce.owner_user_id = ?
	OR (ce.visibility IN ('public','default'))
	OR (ce.visibility = 'private' AND EXISTS (
		SELECT 1 FROM calendar_event_attendees a
		WHERE a.event_id = ce.id AND a.user_id = ? AND a.enabled
	))
)`
