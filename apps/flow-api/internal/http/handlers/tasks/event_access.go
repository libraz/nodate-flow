package tasks

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// calendarStanding is a calendar's kind together with the actor's role on
// it, as one value because the write rule needs both halves. A nil
// standing is an actor who holds no calendar_members row on it.
type calendarStanding struct {
	kind calendar.CalendarsKind
	role calendar.CalendarMembersRole
}

// resolveEventStanding looks up a calendar event by its public id within
// the workspace and reports what the actor holds on the calendar it lives
// on. It decides nothing: the caller applies the floor its own route
// takes, [shiftRefusal] for a write and [eventVisibilityRefusal] for a
// read.
//
// The lookup and the standing are one function on purpose. Scoping an
// event id to a workspace proves tenancy and nothing else — every member
// of a workspace can reach ids on calendars they hold no grant on — and a
// resolver that answers an id without the standing behind it reads as
// complete, which is how the check comes to be omitted.
//
// The standing is read from the calendars the actor may reach, the same
// statement the calendar surfaces are driven by, so a calendar absent
// from it is one they hold no live membership on. A nil standing with a
// nil error is therefore an event that exists and an actor who holds
// nothing on its calendar, which is a refusal on both routes and never a
// pass.
//
// The public id is returned parsed so a caller that echoes the event back
// does not parse the same string twice; on a parse failure it is the zero
// value, matching the not-found the caller is handed.
func resolveEventStanding(
	ctx context.Context,
	deps Deps,
	workspaceID, actorID uint32,
	publicID string,
) (uint32, types.PublicID, *calendarStanding, error) {
	pub, err := types.Parse(publicID)
	if err != nil {
		return 0, pub, nil, httpErr(apierrors.CalendarEventNotFound)
	}
	var eventID, calendarID uint32
	err = deps.DB.QueryRowContext(ctx,
		`SELECT id, calendar_id FROM calendar_events
		 WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE
		 LIMIT 1`,
		workspaceID, pub,
	).Scan(&eventID, &calendarID)
	if err != nil {
		return 0, pub, nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.CalendarEventNotFound, apierrors.InternalUnexpected))
	}
	// Without the calendar queries there is no standing to read, and an
	// answer that proceeds anyway is an unauthorized one.
	if deps.CalendarQueries == nil {
		return 0, pub, nil, httpErr(apierrors.InternalUnexpected)
	}
	rows, err := deps.CalendarQueries.ListCalendarsForUser(ctx, calendar.ListCalendarsForUserParams{
		UserID:      actorID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return 0, pub, nil, httpErr(apierrors.InternalUnexpected)
	}
	for _, row := range rows {
		if row.ID == calendarID {
			return eventID, pub, &calendarStanding{kind: row.Kind, role: row.Role}, nil
		}
	}
	return eventID, pub, nil, nil
}
