package calendars

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendaroccurrence"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// invalidQueryField refuses a query parameter whose value the request
// cannot be carried out with, naming the parameter so the caller knows
// which one to change.
//
// The delete carries no body, so its scope arrives in the query string
// and a body-shaped refusal would send the caller looking at a document
// they never sent.
func invalidQueryField(field string) error {
	return handlerutil.HTTPErrFromAPIError(
		apierr.New(apierrors.ValidationQueryFieldInvalid).WithDetail("field", field))
}

// requireDeleteOccurrenceScope refuses the scope / row combinations that
// name no occurrence to delete.
//
// The projection guard trigger cannot answer any of these: it inspects
// only the row being written and never follows a parent link, so it
// cannot see that a row is already an override, and the one rule it does
// enforce arrives as SQLSTATE 45000 — a write failure the caller cannot
// act on.
func requireDeleteOccurrenceScope(
	scope occurrenceScope,
	occurrenceStart int64,
	evt calendar.FindCalendarEventByPublicIdRow,
	parentID sql.NullInt32,
) error {
	if spec, field := calendaroccurrence.ScopeRefusal(
		scope, occurrenceStart,
		calendaroccurrence.HasRule(evt.RecurrenceRule), parentID.Valid); spec != nil {
		return occurrenceRefusal(spec, field)
	}
	return nil
}

// deleteEventOccurrence cancels one occurrence of a series.
//
// Cancellation is an entry in the master's recurrence_exceptions: the
// expanders subtract it and the occurrence stops being produced. When an
// override already stands in for that occurrence it is disabled in the
// same transaction — the override is a row of its own that the
// non-recurring range queries select directly, so an exception alone
// would cancel the occurrence and leave the copy the user had moved
// sitting on the calendar.
func deleteEventOccurrence(
	ctx context.Context,
	deps Deps,
	wsID uint32,
	cal calendar.FindCalendarByPublicIdRow,
	master calendar.FindCalendarEventByPublicIdRow,
	occurrenceStart time.Time,
) error {
	exceptions, changed, err := calendaroccurrence.AppendException(master.RecurrenceExceptions, occurrenceStart)
	if err != nil {
		return httpErr(apierrors.CalendarEventStoreReadInterrupted)
	}

	parentID := handlerutil.NullInt32From(master.ID)
	originalStart := sql.NullTime{Time: occurrenceStart, Valid: true}

	var answered error
	txErr := dbretry.InTx(ctx, deps.DB, "calendars.DeleteEventOccurrence", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		cqtx := deps.CalendarQueries.WithTx(tx.RawTx())

		if changed {
			if _, err := cqtx.PatchCalendarEvent(ctx, calendar.PatchCalendarEventParams{
				RecurrenceExceptions: exceptions,
				PublicID:             master.PublicID,
				CalendarID:           cal.ID,
				WorkspaceID:          wsID,
			}); err != nil {
				return err
			}
		}

		override, findErr := cqtx.FindCalendarEventOverride(ctx, calendar.FindCalendarEventOverrideParams{
			WorkspaceID:             wsID,
			RecurrenceParentID:      parentID,
			RecurrenceOriginalStart: originalStart,
		})
		switch {
		case errors.Is(findErr, sql.ErrNoRows):
			return nil
		case findErr != nil:
			answered = httpErr(apierrors.CalendarEventStoreReadInterrupted)
			return findErr
		}
		if !override.Enabled {
			return nil
		}
		// affected-rows: not-applicable — this only runs on an override the
		// two statements above read as live in this same transaction, and
		// what the caller asked to cancel is the occurrence, which the
		// master's exception removes whether or not an override stood in
		// for it.
		_, err := cqtx.DisableCalendarEvent(ctx, calendar.DisableCalendarEventParams{
			PublicID:    override.PublicID,
			CalendarID:  override.CalendarID,
			WorkspaceID: wsID,
		})
		return err
	})
	if answered != nil {
		return answered
	}
	if txErr != nil {
		return httpErr(apierrors.CalendarEventStoreDeleteInterrupted)
	}
	return nil
}

// deleteEventFollowing stops a series at an occurrence through
// [calendaroccurrence.EndSeries]: the master keeps the occurrences before
// the split, and the exceptions and overrides from it onwards go with the
// rest. The MCP delete tool ends a series through the same function.
func deleteEventFollowing(
	ctx context.Context,
	deps Deps,
	wsID, actorID uint32,
	cal calendar.FindCalendarByPublicIdRow,
	master calendar.FindCalendarEventByPublicIdRow,
	splitStart time.Time,
) error {
	txErr := dbretry.InTx(ctx, deps.DB, "calendars.DeleteEventFollowing", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		return calendaroccurrence.EndSeries(ctx, seriesQueries(deps, tx), wsID, cal.ID, actorID, master, splitStart)
	})
	if txErr != nil {
		return httpErr(apierrors.CalendarEventStoreDeleteInterrupted)
	}
	return nil
}

// recordOccurrenceDelete records a delete that reached one occurrence
// rather than the series, in both logs.
//
// The kind is the update one because that is what happened to the row
// named: the master survives with an exception or an earlier end, and a
// deleted-kind event carrying its id would tell every subscriber to drop
// a row that is still there. The scope and the occurrence say which part
// of the series went.
func recordOccurrenceDelete(
	ctx context.Context,
	deps Deps,
	wsID, actorID uint32,
	input *DeleteEventInput,
	scope occurrenceScope,
	master calendar.FindCalendarEventByPublicIdRow,
	calID uint32,
) {
	recordCalendarChange(ctx, deps, wsID, calID, actorID, mutationlog.Mutation{
		EventType:    eventbus.CalEventUpdated,
		AuditAction:  "calendar.event.delete",
		ResourceType: "calendar.event",
		ResourceID:   input.EvtID,
		Payload: map[string]any{
			"eventId":         input.EvtID,
			"calendarId":      input.CalID,
			"title":           master.Title,
			"scope":           string(scope),
			"occurrenceStart": input.OccurrenceStart,
		},
		CallSite: "calendars.DeleteEvent",
	})
}
