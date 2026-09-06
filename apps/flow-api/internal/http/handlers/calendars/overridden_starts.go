package calendars

import (
	"context"
	"database/sql"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// overriddenStartsByMaster returns, per recurring master internal id, the
// occurrence starts a live override row already stands in for, spelled as
// RFC 3339 UTC.
//
// The range endpoints hand these to the client on the master itself. The
// client-side expander subtracts them from what it emits, so the occurrence
// appears once — drawn by the override row at its own time — instead of
// twice. Left unsupplied, the master keeps emitting the occurrence its
// override replaced, and an edited occurrence renders alongside the
// original it was meant to become.
//
// RFC 3339 UTC because the same instants are read by the same parser as
// recurrence_exceptions, which is a list of ISO-8601 starts; an explicit
// offset is what makes the two resolve to the instants the expander
// generates rather than to a wall clock in whoever's zone.
//
// One read for the whole page. The masters are keyed by their internal
// id, which is the only identifier ListCalendarEventOverriddenStarts
// carries, so both range queries project ce.id for this.
//
// viewerUserID is the user the surrounding listing answers for and is
// passed straight through: the query is scoped to the overrides that
// viewer may see, and widening it here would suppress a master's
// occurrence while the replacement stayed hidden — the meeting would
// leave that viewer's calendar entirely. An occurrence whose replacement
// is invisible to the viewer therefore keeps showing at its original
// time, which is the same answer every other calendar read gives them.
func overriddenStartsByMaster(
	ctx context.Context,
	q *calendar.Queries,
	workspaceID, viewerUserID uint32,
	masterIDs []uint32,
) (map[uint32][]string, error) {
	if len(masterIDs) == 0 {
		return nil, nil
	}

	parentIDs := make([]sql.NullInt32, 0, len(masterIDs))
	for _, id := range masterIDs {
		parentIDs = append(parentIDs, handlerutil.NullInt32From(id))
	}

	rows, err := q.ListCalendarEventOverriddenStarts(ctx, calendar.ListCalendarEventOverriddenStartsParams{
		WorkspaceID:  workspaceID,
		ParentIds:    parentIDs,
		ViewerUserID: viewerUserID,
	})
	if err != nil {
		return nil, err
	}

	out := make(map[uint32][]string, len(rows))
	for _, row := range rows {
		if !row.RecurrenceParentID.Valid || !row.RecurrenceOriginalStart.Valid {
			continue
		}
		parentID := handlerutil.Int32ToUint32(row.RecurrenceParentID)
		out[parentID] = append(out[parentID], row.RecurrenceOriginalStart.Time.UTC().Format(time.RFC3339))
	}
	return out, nil
}

// overriddenStartsByWorkspaceMaster is the cross-workspace form, keyed by
// workspace id and master internal id.
//
// ListCalendarEventOverriddenStarts is scoped to one workspace, which is
// the tenant boundary and not something to widen for a read path. The
// masters are therefore grouped by workspace and read one batch per
// workspace: bounded by the caller's memberships, not by the number of
// events in the range.
func overriddenStartsByWorkspaceMaster(
	ctx context.Context,
	q *calendar.Queries,
	viewerUserID uint32,
	mastersByWorkspace map[uint32][]uint32,
) (map[uint32]map[uint32][]string, error) {
	if len(mastersByWorkspace) == 0 {
		return nil, nil
	}

	out := make(map[uint32]map[uint32][]string, len(mastersByWorkspace))
	for workspaceID, masterIDs := range mastersByWorkspace {
		byMaster, err := overriddenStartsByMaster(ctx, q, workspaceID, viewerUserID, masterIDs)
		if err != nil {
			return nil, err
		}
		if len(byMaster) > 0 {
			out[workspaceID] = byMaster
		}
	}
	return out, nil
}

// overriddenStartsByShareEvent is the public-share form, folded out of the
// rows the render already selected rather than read separately.
//
// The two functions above take a viewer and hand that viewer straight to
// ListCalendarEventOverriddenStarts, which scopes the read to the overrides
// that viewer may see. A public share has no viewer, so there is nothing to
// pass: calling them here would mean a zero user id, and a zero user id in
// that query means every non-confidential override in the workspace — from
// calendars the share never touched, naming instants the share was never
// given. Subtracting one of those would remove an occurrence from the page
// with nothing to replace it, and the absence would be readable: an
// anonymous reader would learn that a series they can see was edited
// somewhere they cannot. That is a fact about an unpublished row, which is
// the one thing this field must never carry.
//
// So the scope is the share's own contents. An occurrence is subtracted
// only when the override standing in for it is itself published on this
// share — which is exactly the condition for the override being in this
// result set, since the render query returns one row per attached, enabled,
// non-confidential, dated event and nothing else. Attaching only the master
// leaves the page as it was: the original occurrence still renders, which is
// what the share was given.
//
// Read the other way: the field tells an anonymous reader that two events
// already on the page are the same meeting, and names an instant the
// master's own recurrence rule already generates for them. It cannot name
// an instant, an occurrence or an event the page does not otherwise show.
func overriddenStartsByShareEvent(rows []calendar.ListPublicShareEventsByTokenHashRow) map[uint32][]string {
	var out map[uint32][]string
	for _, row := range rows {
		if !row.RecurrenceParentID.Valid || !row.RecurrenceOriginalStart.Valid {
			continue
		}
		if out == nil {
			out = make(map[uint32][]string)
		}
		parentID := handlerutil.Int32ToUint32(row.RecurrenceParentID)
		out[parentID] = append(out[parentID], row.RecurrenceOriginalStart.Time.UTC().Format(time.RFC3339))
	}
	return out
}
