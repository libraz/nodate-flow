package calendars

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// occurrenceScope names which occurrences of a recurring series a patch
// reaches.
//
// A series is one master row, so a patch that says nothing rewrites every
// occurrence it produces. The scope is what separates "move this week's
// stand-up" from "move the stand-up".
type occurrenceScope string

const (
	// scopeSeries rewrites the master, and with it every occurrence the
	// rule produces. An absent scope resolves here, so a caller that does
	// not send the field keeps the behaviour it always had.
	scopeSeries occurrenceScope = "series"
	// scopeOccurrence replaces exactly one occurrence with an override
	// row, leaving the rest of the series untouched.
	scopeOccurrence occurrenceScope = "occurrence"
	// scopeThisAndFollowing splits the series: the master stops producing
	// occurrences at the split, and a new master carries the edit plus the
	// remainder of the rule.
	scopeThisAndFollowing occurrenceScope = "thisAndFollowing"
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
// The enum is also declared on the field, so an unrecognised value is
// normally refused before the handler runs; repeating the closed set here
// keeps a value the schema stops describing from silently selecting the
// series path and rewriting every occurrence.
func decodeOccurrenceScope(raw *string) (occurrenceScope, error) {
	if raw == nil || *raw == "" {
		return scopeSeries, nil
	}
	switch occurrenceScope(*raw) {
	case scopeSeries:
		return scopeSeries, nil
	case scopeOccurrence:
		return scopeOccurrence, nil
	case scopeThisAndFollowing:
		return scopeThisAndFollowing, nil
	}
	return "", invalidBodyField("scope")
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

	// Which occurrence is singled out is the whole content of a
	// non-series scope. Without it the request names nothing.
	if input.Body.OccurrenceStart == nil {
		return occurrenceRefusal(apierrors.CalendarEventOccurrenceStartRequired, "occurrenceStart")
	}

	// A two-level chain. An override of an override inserts happily and
	// is then unreachable: the expander subtracts the occurrence the
	// first override replaced from the master, and nothing expands an
	// override, so the second row is written and never read.
	if isOverride {
		return occurrenceRefusal(apierrors.CalendarEventAlreadyOccurrenceOverride, "scope")
	}

	// There is no occurrence to single out on a row that produces one.
	if !hasRecurrenceRule(evt.RecurrenceRule) {
		return occurrenceRefusal(apierrors.CalendarEventNotRecurring, "scope")
	}

	// A single occurrence is a leaf. The rule, the recurrence end and the
	// exception list describe the series and belong to the master alone.
	if scope == scopeOccurrence && touchesRecurrence {
		return occurrenceRefusal(apierrors.CalendarEventRecurrenceOnOccurrenceNotAllowed, "recurrenceRule")
	}
	return nil
}

// hasRecurrenceRule reports whether a stored recurrence_rule column holds
// a rule. A NULL column scans as a nil slice; a JSON null is stored by
// nothing but reads as one value here either way.
func hasRecurrenceRule(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}

// occurrenceFields is the whole occurrence an override row carries.
//
// Every column has a value because an override is not a delta: the row
// stands in for the occurrence entirely, and a column left unset would
// read as the absence of a value rather than as the series' own.
type occurrenceFields struct {
	Kind               calendar.CalendarEventsKind
	Visibility         calendar.CalendarEventsVisibility
	ShowAs             calendar.CalendarEventsShowAs
	Flexibility        calendar.CalendarEventsFlexibility
	Title              string
	AllDay             bool
	StartAt            sql.NullTime
	EndAt              sql.NullTime
	Timezone           string
	Location           sql.NullString
	Memo               sql.NullString
	URL                sql.NullString
	BlockLabel         sql.NullString
	NotificationOffset sql.NullInt32
}

// masterOccurrenceBase is what a member the caller did not send falls
// back to when the occurrence has no override yet: the series' own
// values, in the slot the occurrence holds under the rule — its original
// start, for as long as the master's own window lasts.
func masterOccurrenceBase(
	master calendar.FindCalendarEventByPublicIdRow,
	originalStart time.Time,
) occurrenceFields {
	var duration time.Duration
	if master.StartAt.Valid && master.EndAt.Valid {
		duration = master.EndAt.Time.Sub(master.StartAt.Time)
	}
	return occurrenceFields{
		Kind:               master.Kind,
		Visibility:         master.Visibility,
		ShowAs:             master.ShowAs,
		Flexibility:        master.Flexibility,
		Title:              master.Title,
		AllDay:             master.AllDay,
		StartAt:            sql.NullTime{Time: originalStart, Valid: true},
		EndAt:              sql.NullTime{Time: originalStart.Add(duration), Valid: true},
		Timezone:           master.Timezone,
		Location:           master.Location,
		Memo:               master.Memo,
		URL:                master.Url,
		BlockLabel:         master.BlockLabel,
		NotificationOffset: master.NotificationOffset,
	}
}

// overrideOccurrenceBase is what a member the caller did not send falls
// back to once an override already stands in for the occurrence.
//
// The override row is the occurrence. It exists precisely where the
// occurrence differs from the series, so a default read off the master
// returns it to the values the override was created to leave behind: an
// occurrence moved to another time and later renamed would move back.
//
// Whether the row is enabled does not enter here. A soft-deleted override
// is still the occurrence's own last state, and the update that follows
// revives it.
func overrideOccurrenceBase(existing calendar.FindCalendarEventOverrideRow) occurrenceFields {
	return occurrenceFields{
		Kind:               existing.Kind,
		Visibility:         existing.Visibility,
		ShowAs:             existing.ShowAs,
		Flexibility:        existing.Flexibility,
		Title:              existing.Title,
		AllDay:             existing.AllDay,
		StartAt:            existing.StartAt,
		EndAt:              existing.EndAt,
		Timezone:           existing.Timezone,
		Location:           existing.Location,
		Memo:               existing.Memo,
		URL:                existing.Url,
		BlockLabel:         existing.BlockLabel,
		NotificationOffset: existing.NotificationOffset,
	}
}

// mergeOccurrenceFields folds the caller's sent members over the values
// the occurrence falls back to.
//
// A clear wins over a value sent for the same member, matching the
// precedence PatchCalendarEvent applies: sending both is contradictory,
// and the destructive reading is the one that cannot silently leave a
// value the caller asked to be rid of.
//
// The window moves as a pair or not at all. That invariant is settled
// before this runs, so a sent start implies a sent end, and a request
// that sends neither keeps the base's window.
func mergeOccurrenceFields(
	base occurrenceFields,
	input *PatchEventInput,
	cleared clearableEventFields,
) occurrenceFields {
	f := occurrenceFields{
		Kind:               base.Kind,
		Visibility:         base.Visibility,
		ShowAs:             base.ShowAs,
		Flexibility:        base.Flexibility,
		Title:              base.Title,
		AllDay:             base.AllDay,
		StartAt:            base.StartAt,
		EndAt:              base.EndAt,
		Timezone:           mergeString(base.Timezone, input.Body.Timezone),
		Location:           base.Location,
		Memo:               base.Memo,
		URL:                base.URL,
		BlockLabel:         base.BlockLabel,
		NotificationOffset: base.NotificationOffset,
	}
	if input.Body.StartAt != nil && input.Body.EndAt != nil {
		f.StartAt = sql.NullTime{Time: handlerutil.UnixToTime(*input.Body.StartAt), Valid: true}
		f.EndAt = sql.NullTime{Time: handlerutil.UnixToTime(*input.Body.EndAt), Valid: true}
	}
	if input.Body.Kind != nil {
		f.Kind = calendar.CalendarEventsKind(*input.Body.Kind)
	}
	if input.Body.Visibility != nil {
		f.Visibility = calendar.CalendarEventsVisibility(*input.Body.Visibility)
	}
	if input.Body.ShowAs != nil {
		f.ShowAs = calendar.CalendarEventsShowAs(*input.Body.ShowAs)
	}
	if input.Body.Flexibility != nil {
		f.Flexibility = calendar.CalendarEventsFlexibility(*input.Body.Flexibility)
	}
	if input.Body.Title != nil {
		f.Title = *input.Body.Title
	}
	if input.Body.AllDay != nil {
		f.AllDay = *input.Body.AllDay
	}
	f.Location = mergeNullString(f.Location, input.Body.Location, cleared.location)
	f.Memo = mergeNullString(f.Memo, input.Body.Memo, cleared.memo)
	f.URL = mergeNullString(f.URL, input.Body.URL, cleared.url)
	f.BlockLabel = mergeNullString(f.BlockLabel, input.Body.BlockLabel, cleared.blockLabel)
	switch {
	case cleared.notificationOffset == 1:
		f.NotificationOffset = sql.NullInt32{}
	case input.Body.NotificationOffset != nil:
		f.NotificationOffset = sql.NullInt32{Int32: *input.Body.NotificationOffset, Valid: true}
	}
	f.StartAt, f.EndAt = normalizeAllDayBounds(f.AllDay, f.StartAt, f.EndAt)
	return f
}

// mergeString takes the sent value for a NOT NULL text member, or keeps
// the stored one. There is no clear flag: the column cannot hold nothing.
func mergeString(stored string, sent *string) string {
	if sent != nil {
		return *sent
	}
	return stored
}

// mergeNullString applies one nullable text member's clear flag and sent
// value over the stored value.
func mergeNullString(stored sql.NullString, sent *string, cleared int64) sql.NullString {
	if cleared == 1 {
		return sql.NullString{}
	}
	if sent != nil {
		return sql.NullString{String: *sent, Valid: true}
	}
	return stored
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
		base := masterOccurrenceBase(master, originalStart)
		if overridden {
			base = overrideOccurrenceBase(existing)
		}
		fields := mergeOccurrenceFields(base, input, cleared)
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
	fields := mergeOccurrenceFields(masterOccurrenceBase(master, splitStart), input, cleared)
	// The continuing series carries whichever zone survived the merge, so
	// it is checked here rather than only where the caller's value entered.
	if err := requireValidTimezone("timezone", fields.Timezone); err != nil {
		return calendar.FindCalendarEventByPublicIdRow{}, err
	}

	truncateAt := seriesTruncationPoint(master, splitStart)

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

// recordOccurrencePatch appends the domain event and the audit entry for a
// patch that reached one occurrence rather than the series.
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
	appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, calID, eventbus.CalEventUpdated, &actorID, payload, "calendars.PatchEvent")

	deps.Audit.Record(ctx, audit.Entry{
		Action:       "calendar.event.update",
		ActorID:      actorID,
		WorkspaceID:  wsID,
		ResourceType: "calendar.event",
		ResourceID:   input.EvtID,
		Metadata: map[string]any{
			"calendarId":      input.CalID,
			"scope":           string(scope),
			"occurrenceStart": *input.Body.OccurrenceStart,
			"writtenEventId":  written.PublicID.String(),
		},
	})
}
