package calendars

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendaroccurrence"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
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

// occurrenceFields is the whole occurrence an override row carries.
type occurrenceFields = calendaroccurrence.Fields

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

// mergeOccurrenceFields folds the caller's sent members over the values
// the occurrence falls back to, and renders a refusal from the shared
// rules as this transport's own.
func mergeOccurrenceFields(
	base occurrenceFields,
	input *PatchEventInput,
	cleared clearableEventFields,
) (occurrenceFields, error) {
	fields, err := occurrencePatch(input, cleared).Apply(base)
	if err != nil {
		return occurrenceFields{}, handlerutil.HTTPErrFromAPIError(err)
	}
	return fields, nil
}

// patchEventOccurrence replaces a single occurrence of a recurring series
// with an override row, and answers with that row.
//
// The override is looked up before it is written because
// uniq_calendar_events_recurrence_override counts soft-deleted rows: an
// occurrence that was overridden, reverted to the series and then edited
// again already has a row, and inserting a second one collides.
// UpdateCalendarEventOverride sets enabled back to TRUE, which is what
// revives it.
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

	parentID := handlerutil.NullInt32From(master.ID)
	originalStartNT := sql.NullTime{Time: originalStart, Valid: true}

	var written calendar.FindCalendarEventByPublicIdRow
	var answered error
	txErr := dbretry.InTx(ctx, deps.DB, "calendars.PatchEventOccurrence", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		cqtx := deps.CalendarQueries.WithTx(tx.RawTx())

		existing, findErr := cqtx.FindCalendarEventOverride(ctx, calendar.FindCalendarEventOverrideParams{
			WorkspaceID:             wsID,
			RecurrenceParentID:      parentID,
			RecurrenceOriginalStart: originalStartNT,
		})
		overridden := findErr == nil
		if !overridden && !errors.Is(findErr, sql.ErrNoRows) {
			answered = httpErr(apierrors.CalendarEventStoreReadInterrupted)
			return findErr
		}

		// What the caller's omissions fall back to is decided by the same
		// lookup that decides whether the write is an update or an insert,
		// so it is decided here rather than before the transaction: an
		// override written between the two would otherwise be updated from
		// the series' values.
		base := calendaroccurrence.MasterBase(master, originalStart)
		if overridden {
			base = calendaroccurrence.OverrideBase(existing)
		}
		fields, mergeErr := mergeOccurrenceFields(base, input, cleared)
		if mergeErr != nil {
			answered = mergeErr
			return mergeErr
		}
		// The zone written is whichever survived the merge — the caller's,
		// the override's or the master's. Checking the merged value is what
		// keeps a column no renderer can resolve out of the row, whichever
		// side it came from.
		if tzErr := requireValidTimezone("timezone", fields.Timezone); tzErr != nil {
			answered = tzErr
			return tzErr
		}

		if overridden {
			if _, err := cqtx.UpdateCalendarEventOverride(ctx, calendar.UpdateCalendarEventOverrideParams{
				Kind:               fields.Kind,
				Visibility:         fields.Visibility,
				ShowAs:             fields.ShowAs,
				Flexibility:        fields.Flexibility,
				Title:              fields.Title,
				AllDay:             fields.AllDay,
				StartAt:            fields.StartAt,
				EndAt:              fields.EndAt,
				Timezone:           fields.Timezone,
				Location:           fields.Location,
				Memo:               fields.Memo,
				Url:                fields.URL,
				BlockLabel:         fields.BlockLabel,
				NotificationOffset: fields.NotificationOffset,
				PublicID:           existing.PublicID,
				CalendarID:         existing.CalendarID,
				WorkspaceID:        wsID,
			}); err != nil {
				return err
			}
		} else {
			if _, err := cqtx.CreateCalendarEventOverride(ctx, calendar.CreateCalendarEventOverrideParams{
				PublicID:                types.New(),
				WorkspaceID:             wsID,
				CalendarID:              cal.ID,
				RecurrenceParentID:      parentID,
				RecurrenceOriginalStart: originalStartNT,
				Kind:                    fields.Kind,
				Visibility:              fields.Visibility,
				ShowAs:                  fields.ShowAs,
				Flexibility:             fields.Flexibility,
				Title:                   fields.Title,
				AllDay:                  fields.AllDay,
				StartAt:                 fields.StartAt,
				EndAt:                   fields.EndAt,
				Timezone:                fields.Timezone,
				Location:                fields.Location,
				Memo:                    fields.Memo,
				Url:                     fields.URL,
				OwnerUserID:             master.OwnerUserID,
				CreatedByUserID:         actorID,
				BlockLabel:              fields.BlockLabel,
				NotificationOffset:      fields.NotificationOffset,
			}); err != nil {
				return err
			}
			// Re-read through the same lookup the revive path uses, so
			// both arrive at the response by one route.
			var reErr error
			existing, reErr = cqtx.FindCalendarEventOverride(ctx, calendar.FindCalendarEventOverrideParams{
				WorkspaceID:             wsID,
				RecurrenceParentID:      parentID,
				RecurrenceOriginalStart: originalStartNT,
			})
			if reErr != nil {
				answered = httpErr(apierrors.CalendarEventStoreReadInterrupted)
				return reErr
			}
		}

		var readErr error
		written, readErr = cqtx.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
			PublicID:    existing.PublicID,
			CalendarID:  existing.CalendarID,
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

// patchEventFollowing splits a series at an occurrence: the master stops
// before the split and a new master carries the edit and the remainder of
// the rule. It answers with the new master.
//
// Where the master stops, and why that bound is recurrence_end rather
// than the rule's own until, is [seriesTruncationPoint] — the delete-side
// split truncates a series the same way and reads the same answer.
//
// Overrides at or after the split describe occurrences of the new series
// and are handed to it, soft-deleted ones included — left behind they
// would name an occurrence the truncated master no longer produces.
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
	// The continuing series starts from the master alone: it is a new
	// master carrying the remainder of the rule, not an edit of an
	// occurrence, so no override row describes it.
	fields, err := mergeOccurrenceFields(calendaroccurrence.MasterBase(master, splitStart), input, cleared)
	if err != nil {
		return calendar.FindCalendarEventByPublicIdRow{}, err
	}
	// The continuing series carries whichever zone survived the merge, so
	// it is checked here rather than only where the caller's value entered.
	if err := requireValidTimezone("timezone", fields.Timezone); err != nil {
		return calendar.FindCalendarEventByPublicIdRow{}, err
	}

	truncateAt := calendaroccurrence.TruncationPoint(master, splitStart)

	newPublicID := types.New()
	newMaster := calendar.CreateCalendarEventParams{
		PublicID:           newPublicID,
		WorkspaceID:        wsID,
		CalendarID:         cal.ID,
		Kind:               fields.Kind,
		Visibility:         fields.Visibility,
		ShowAs:             fields.ShowAs,
		Flexibility:        fields.Flexibility,
		Title:              fields.Title,
		AllDay:             fields.AllDay,
		StartAt:            fields.StartAt,
		EndAt:              fields.EndAt,
		Timezone:           fields.Timezone,
		Location:           fields.Location,
		Memo:               fields.Memo,
		Url:                fields.URL,
		OwnerUserID:        master.OwnerUserID,
		CreatedByUserID:    actorID,
		BlockLabel:         fields.BlockLabel,
		NotificationOffset: fields.NotificationOffset,
	}
	// Clearing the rule clears the two columns that only describe a
	// series, the same way PatchCalendarEvent does: leaving them on a row
	// that no longer repeats keeps state nothing reads.
	if cleared.recurrenceRule != 1 {
		newMaster.RecurrenceRule = master.RecurrenceRule
		if input.Body.RecurrenceRule != nil {
			newMaster.RecurrenceRule = json.RawMessage(*input.Body.RecurrenceRule)
		}
		if cleared.recurrenceEnd != 1 {
			newMaster.RecurrenceEnd = master.RecurrenceEnd
			if input.Body.RecurrenceEnd != nil {
				newMaster.RecurrenceEnd = sql.NullTime{Time: handlerutil.UnixToTime(*input.Body.RecurrenceEnd), Valid: true}
			}
		}
		if cleared.recurrenceExceptions != 1 {
			// The master's exceptions carry over whole. An entry naming an
			// occurrence before the split simply never matches what the
			// new series produces, and deciding which entries to drop
			// would mean expanding the rule to find out.
			newMaster.RecurrenceExceptions = master.RecurrenceExceptions
			if input.Body.RecurrenceExceptions != nil {
				newMaster.RecurrenceExceptions = json.RawMessage(*input.Body.RecurrenceExceptions)
			}
		}
	}

	var written calendar.FindCalendarEventByPublicIdRow
	var answered error
	txErr := dbretry.InTx(ctx, deps.DB, "calendars.PatchEventFollowing", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		cqtx := deps.CalendarQueries.WithTx(tx.RawTx())

		if _, err := cqtx.PatchCalendarEvent(ctx, calendar.PatchCalendarEventParams{
			RecurrenceEnd: sql.NullTime{Time: truncateAt, Valid: true},
			PublicID:      master.PublicID,
			CalendarID:    cal.ID,
			WorkspaceID:   wsID,
		}); err != nil {
			return err
		}

		newID, err := cqtx.CreateCalendarEvent(ctx, newMaster)
		if err != nil {
			return err
		}

		if _, err := cqtx.ReparentCalendarEventOverridesFromStart(ctx, calendar.ReparentCalendarEventOverridesFromStartParams{
			// LAST_INSERT_ID answers as int64; the column behind the
			// parameter is INT UNSIGNED and sqlc emits sql.NullInt32 for
			// it, the same narrowing every internal id here goes through.
			NewParentID: sql.NullInt32{Int32: int32(newID), Valid: true}, //#nosec G115 -- internal row id, bounded by realistic workspace size
			WorkspaceID: wsID,
			OldParentID: handlerutil.NullInt32From(master.ID),
			SplitStart:  sql.NullTime{Time: splitStart, Valid: true},
		}); err != nil {
			return err
		}

		var readErr error
		written, readErr = cqtx.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
			PublicID:    newPublicID,
			CalendarID:  cal.ID,
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
}
