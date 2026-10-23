package calendars

import (
	"context"
	"database/sql"
	"encoding/json"
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

// The scope vocabulary, the fallback bases, the window merge, the
// truncation point and the exception list all live in
// [github.com/libraz/nodate-flow/apps/flow-api/internal/calendaroccurrence],
// because the MCP calendar tools offer the same three scopes over the
// same rows. What is left in this package is the part that is HTTP's:
// reading the scope out of a body or a query string, and rendering a
// refusal as RFC 9457 with the member it is about.
//
// The names are re-declared as aliases rather than spelled out at every
// call site: the handlers read the same as they did when the definitions
// were here, and there is still only one definition.
type occurrenceScope = calendaroccurrence.Scope

const (
	// scopeSeries rewrites the master, and with it every occurrence the
	// rule produces. An absent scope resolves here, so a caller that does
	// not send the field keeps the behaviour it always had.
	scopeSeries = calendaroccurrence.ScopeSeries
	// scopeOccurrence replaces exactly one occurrence with an override
	// row, leaving the rest of the series untouched.
	scopeOccurrence = calendaroccurrence.ScopeOccurrence
	// scopeThisAndFollowing splits the series: the master stops producing
	// occurrences at the split, and a new master carries the edit plus the
	// remainder of the rule.
	scopeThisAndFollowing = calendaroccurrence.ScopeThisAndFollowing
)

// invalidBodyField refuses a body member whose value the row cannot hold,
// naming the member so the caller knows which one to change.
func invalidBodyField(field string) error {
	return handlerutil.HTTPErrFromAPIError(
		apierr.New(apierrors.ValidationBodyFieldInvalid).WithDetail("field", field))
}

// occurrenceRefusal answers one of the scope / row combinations an
// occurrence-scoped request cannot be carried out on.
//
// Each such combination has a code of its own, because the reason is what
// the caller has to act on: a generic field-level refusal says the request
// was rejected without saying whether the row repeats, whether it already
// stands in for an occurrence, or which member is the one that cannot be
// there. The named member rides along as a detail so a form can still
// point at it.
func occurrenceRefusal(spec *apierrors.Spec, field string) error {
	return handlerutil.HTTPErrFromAPIError(
		apierr.New(spec).WithDetail("field", field))
}

// decodeOccurrenceScope reads the scope a patch names, defaulting to the
// whole series.
//
// An absent pointer is an omitted member, which the shared parser reads
// the same way it reads an empty string. The refusal is this route's own
// because the value arrived in a body here; the delete route reads the
// same scopes out of a query string and names the parameter instead.
func decodeOccurrenceScope(raw *string) (occurrenceScope, error) {
	value := ""
	if raw != nil {
		value = *raw
	}
	scope, ok := calendaroccurrence.ParseScope(value)
	if !ok {
		return "", invalidBodyField("scope")
	}
	return scope, nil
}

// patchTouchesRecurrenceFields reports whether a patch carries any of the
// three columns that describe a series, as a value or as a clear.
//
// A clear counts. Removing a rule from a row that never had one is a
// no-op the caller is told succeeded, which is the same failure
// parseClearFields refuses an unknown name for.
func patchTouchesRecurrenceFields(input *PatchEventInput) bool {
	if input.Body.RecurrenceRule != nil ||
		input.Body.RecurrenceEnd != nil ||
		input.Body.RecurrenceExceptions != nil {
		return true
	}
	for _, name := range input.Body.Clear {
		switch name {
		case "recurrenceRule", "recurrenceEnd", "recurrenceExceptions":
			return true
		}
	}
	return false
}

// requireOccurrenceScope refuses the scope / row combinations that name no
// occurrence, or that would write a row nothing downstream can read.
//
// Every refusal here is the handler's alone in the sense that matters: the
// projection guard trigger inspects only the row being written and never
// follows a parent link, so it cannot see a chain, and the one rule it
// does enforce answers as SQLSTATE 45000 — a write failure the caller
// cannot act on.
func requireOccurrenceScope(
	scope occurrenceScope,
	input *PatchEventInput,
	evt calendar.FindCalendarEventByPublicIdRow,
	parentID sql.NullInt32,
) error {
	isOverride := parentID.Valid
	touchesRecurrence := patchTouchesRecurrenceFields(input)

	if scope == scopeSeries {
		// An override owns no rule, no recurrence end and no exception
		// list; its parent link already says which occurrence it
		// replaces. PatchCalendarEvent matches by public id and will
		// match an override directly, and the trigger answers a rule on
		// such a row with SQLSTATE 45000.
		if isOverride && touchesRecurrence {
			return occurrenceRefusal(apierrors.CalendarEventRecurrenceOnOccurrenceNotAllowed, "recurrenceRule")
		}
		return nil
	}

	// A patch carries the occurrence in its body, where an omitted member
	// is a nil pointer rather than a zero; the shared refusal reads zero
	// as omitted, which is the form the delete route's query parameter
	// arrives in.
	var occurrenceStart int64
	if input.Body.OccurrenceStart != nil {
		occurrenceStart = *input.Body.OccurrenceStart
	}
	if spec, field := calendaroccurrence.ScopeRefusal(
		scope, occurrenceStart,
		calendaroccurrence.HasRule(evt.RecurrenceRule), isOverride); spec != nil {
		return occurrenceRefusal(spec, field)
	}

	// A single occurrence is a leaf. The rule, the recurrence end and the
	// exception list describe the series and belong to the master alone.
	// No MCP tool takes any of the three, so this refusal has no
	// counterpart to share and stays here.
	if scope == scopeOccurrence && touchesRecurrence {
		return occurrenceRefusal(apierrors.CalendarEventRecurrenceOnOccurrenceNotAllowed, "recurrenceRule")
	}
	return nil
}

// occurrencePatch reads the members a patch request sends into the shape
// the shared merge takes.
//
// It is the whole of what this route contributes to the merge: which HTTP
// member stands for which column, and that this route moves the window as
// a pair or not at all — an invariant settled before the handler reaches
// here, so a sent start implies a sent end and a request that sends
// neither keeps the occurrence's own window.
func occurrencePatch(input *PatchEventInput, cleared clearableEventFields) calendaroccurrence.Patch {
	p := calendaroccurrence.Patch{
		Kind:               input.Body.Kind,
		Visibility:         input.Body.Visibility,
		ShowAs:             input.Body.ShowAs,
		Flexibility:        input.Body.Flexibility,
		Title:              input.Body.Title,
		Timezone:           input.Body.Timezone,
		AllDay:             input.Body.AllDay,
		Location:           input.Body.Location,
		Memo:               input.Body.Memo,
		URL:                input.Body.URL,
		BlockLabel:         input.Body.BlockLabel,
		NotificationOffset: input.Body.NotificationOffset,
		Clear: calendaroccurrence.Clears{
			Location:           cleared.location == 1,
			Memo:               cleared.memo == 1,
			URL:                cleared.url == 1,
			BlockLabel:         cleared.blockLabel == 1,
			NotificationOffset: cleared.notificationOffset == 1,
		},
	}
	if input.Body.StartAt != nil && input.Body.EndAt != nil {
		start := handlerutil.UnixToTime(*input.Body.StartAt)
		end := handlerutil.UnixToTime(*input.Body.EndAt)
		p.StartAt, p.EndAt = &start, &end
	}
	return p
}

// seriesQueries binds both query handles to one transaction, in the shape
// the shared series transforms take.
func seriesQueries(deps Deps, tx *dbretry.Tx) calendaroccurrence.Queries {
	return calendaroccurrence.Queries{
		Calendar: deps.CalendarQueries.WithTx(tx.RawTx()),
		Core:     deps.Queries.WithTx(tx.RawTx()),
	}
}

// patchEventOccurrence replaces a single occurrence of a recurring series
// with an override row through [calendaroccurrence.WriteOverride], and
// answers with that row.
//
// The master cannot be task-linked here — the projection guard forbids a
// rule on a projected row, and a non-series scope requires a rule — so
// none of the itemkit propagation the series path performs applies.
func patchEventOccurrence(
	ctx context.Context,
	deps Deps,
	wsID, actorID uint32,
	cal calendar.FindCalendarByPublicIdRow,
	master calendar.FindCalendarEventByPublicIdRow,
	input *PatchEventInput,
	originalStart time.Time,
) (calendar.FindCalendarEventByPublicIdRow, error) {
	cleared, err := parseClearFields(input.Body.Clear)
	if err != nil {
		return calendar.FindCalendarEventByPublicIdRow{}, err
	}
	patch := occurrencePatch(input, cleared)

	var written calendar.FindCalendarEventByPublicIdRow
	var answered error
	txErr := dbretry.InTx(ctx, deps.DB, "calendars.PatchEventOccurrence", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		q := seriesQueries(deps, tx)
		row, refusal, err := calendaroccurrence.WriteOverride(ctx, q, calendaroccurrence.Override{
			WorkspaceID:   wsID,
			ActorID:       actorID,
			CalendarID:    cal.ID,
			Master:        master,
			OriginalStart: originalStart,
			Patch:         patch,
		})
		if refusal != nil {
			answered = handlerutil.HTTPErrFromAPIError(refusal)
			return refusal
		}
		if err != nil {
			return err
		}
		var readErr error
		written, readErr = q.Calendar.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
			PublicID:    row.PublicID,
			CalendarID:  row.CalendarID,
			WorkspaceID: wsID,
		})
		if readErr != nil {
			answered = httpErr(apierrors.CalendarEventStoreReadInterrupted)
		}
		return readErr
	})
	if answered != nil {
		return calendar.FindCalendarEventByPublicIdRow{}, answered
	}
	if txErr != nil {
		return calendar.FindCalendarEventByPublicIdRow{}, httpErr(apierrors.CalendarEventStoreWriteInterrupted)
	}
	return written, nil
}

// patchEventFollowing splits a series at an occurrence through
// [calendaroccurrence.SplitSeries]: the master stops before the split and
// a new master carries the edit and the remainder of the series. It
// answers with the new master.
//
// What this route adds is the part only it can send: a rule, recurrence
// end or exception list for the continuing series, as a value or a clear.
func patchEventFollowing(
	ctx context.Context,
	deps Deps,
	wsID, actorID uint32,
	cal calendar.FindCalendarByPublicIdRow,
	master calendar.FindCalendarEventByPublicIdRow,
	input *PatchEventInput,
	splitStart time.Time,
) (calendar.FindCalendarEventByPublicIdRow, error) {
	cleared, err := parseClearFields(input.Body.Clear)
	if err != nil {
		return calendar.FindCalendarEventByPublicIdRow{}, err
	}
	series := calendaroccurrence.SeriesPatch{
		ClearRule:       cleared.recurrenceRule == 1,
		ClearEnd:        cleared.recurrenceEnd == 1,
		ClearExceptions: cleared.recurrenceExceptions == 1,
	}
	if input.Body.RecurrenceRule != nil {
		series.Rule = json.RawMessage(*input.Body.RecurrenceRule)
	}
	if input.Body.RecurrenceEnd != nil {
		end := handlerutil.UnixToTime(*input.Body.RecurrenceEnd)
		series.End = &end
	}
	if input.Body.RecurrenceExceptions != nil {
		series.Exceptions = json.RawMessage(*input.Body.RecurrenceExceptions)
	}
	split := calendaroccurrence.Split{
		WorkspaceID: wsID,
		ActorID:     actorID,
		CalendarID:  cal.ID,
		Master:      master,
		SplitStart:  splitStart,
		Patch:       occurrencePatch(input, cleared),
		Series:      series,
	}

	var written calendar.FindCalendarEventByPublicIdRow
	var answered error
	txErr := dbretry.InTx(ctx, deps.DB, "calendars.PatchEventFollowing", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		q := seriesQueries(deps, tx)
		row, refusal, err := calendaroccurrence.SplitSeries(ctx, q, split)
		if refusal != nil {
			answered = handlerutil.HTTPErrFromAPIError(refusal)
			return refusal
		}
		if err != nil {
			return err
		}
		var readErr error
		written, readErr = q.Calendar.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
			PublicID:    row.PublicID,
			CalendarID:  row.CalendarID,
			WorkspaceID: wsID,
		})
		if readErr != nil {
			answered = httpErr(apierrors.CalendarEventStoreReadInterrupted)
		}
		return readErr
	})
	if answered != nil {
		return calendar.FindCalendarEventByPublicIdRow{}, answered
	}
	if txErr != nil {
		return calendar.FindCalendarEventByPublicIdRow{}, httpErr(apierrors.CalendarEventStoreWriteInterrupted)
	}
	return written, nil
}

// recordOccurrencePatch records a patch that reached one occurrence
// rather than the series, in both logs.
//
// The resource is the event the caller addressed, so the trail reads as
// one entry per request; scope and the resulting row's id say which row
// the write actually landed on.
func recordOccurrencePatch(
	ctx context.Context,
	deps Deps,
	wsID, actorID uint32,
	input *PatchEventInput,
	scope occurrenceScope,
	written calendar.FindCalendarEventByPublicIdRow,
	calID uint32,
) {
	payload := map[string]any{
		"eventId":         input.EvtID,
		"calendarId":      input.CalID,
		"scope":           string(scope),
		"occurrenceStart": *input.Body.OccurrenceStart,
		"writtenEventId":  written.PublicID.String(),
	}
	recordCalendarChange(ctx, deps, wsID, calID, actorID, mutationlog.Mutation{
		EventType:    eventbus.CalEventUpdated,
		AuditAction:  "calendar.event.update",
		ResourceType: "calendar.event",
		ResourceID:   input.EvtID,
		Payload:      payload,
		CallSite:     "calendars.PatchEvent",
	})
	// A split also brings a series into existence, and a subscriber that
	// learns only of the truncation never hears of the continuing master.
	if scope == scopeThisAndFollowing {
		recordCalendarChange(ctx, deps, wsID, calID, actorID, mutationlog.Mutation{
			EventType:    eventbus.CalEventCreated,
			AuditAction:  "calendar.event.create",
			ResourceType: "calendar.event",
			ResourceID:   written.PublicID.String(),
			Payload: map[string]any{
				"eventId":      written.PublicID.String(),
				"calendarId":   input.CalID,
				"title":        written.Title,
				"kind":         string(written.Kind),
				"splitFromId":  input.EvtID,
				"splitStartAt": *input.Body.OccurrenceStart,
			},
			CallSite: "calendars.PatchEvent",
		})
	}
}
