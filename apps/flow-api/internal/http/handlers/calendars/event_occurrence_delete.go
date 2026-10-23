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

// deleteEventOccurrence cancels one occurrence of a series through
// [calendaroccurrence.CancelOccurrence], which refuses an occurrenceStart
// the series does not produce and disables any override standing in for
// the occurrence in the same transaction.
func deleteEventOccurrence(
	ctx context.Context,
	deps Deps,
	wsID uint32,
	master calendar.FindCalendarEventByPublicIdRow,
	occurrenceStart time.Time,
) error {
	var answered error
	txErr := dbretry.InTx(ctx, deps.DB, "calendars.DeleteEventOccurrence", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		refusal, err := calendaroccurrence.CancelOccurrence(ctx, seriesQueries(deps, tx), wsID, master, occurrenceStart)
		if refusal != nil {
			answered = handlerutil.HTTPErrFromAPIError(refusal)
			return refusal
		}
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
	var refusal *apierrors.APIError
	if errors.As(txErr, &refusal) {
		return handlerutil.HTTPErrFromAPIError(refusal)
	}
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
