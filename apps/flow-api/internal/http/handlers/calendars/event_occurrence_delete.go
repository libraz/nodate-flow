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
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
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
	if scope == scopeSeries {
		return nil
	}

	// Which occurrence is singled out is the whole content of a
	// non-series scope. Without it the request names nothing.
	if occurrenceStart == 0 {
		return occurrenceRefusal(apierrors.CalendarEventOccurrenceStartRequired, "occurrenceStart")
	}

	// An override stands in for exactly one occurrence and produces
	// none, so it has no occurrence to single out and no series to
	// truncate. Deleting it is the series scope's job.
	if parentID.Valid {
		return occurrenceRefusal(apierrors.CalendarEventAlreadyOccurrenceOverride, "scope")
	}

	// Nor does a row that repeats not at all.
	if !hasRecurrenceRule(evt.RecurrenceRule) {
		return occurrenceRefusal(apierrors.CalendarEventNotRecurring, "scope")
	}
	return nil
}

// seriesTruncationPoint returns the recurrence_end that stops a series
// just before a split.
//
// The master is truncated through recurrence_end rather than by
// rewriting the rule's own until. The expanders read the two as
// independent upper bounds and honour whichever is earlier, so a
// recurrence_end set just before the split truncates a rule bounded by
// until and a rule bounded by count alike; rewriting until would leave a
// count-bounded rule emitting the occurrences the split removed, and
// would rewrite JSON the caller supplied.
//
// A master that already stops earlier keeps its own bound, so this never
// extends a series that a later recurrence_end would revive.
func seriesTruncationPoint(master calendar.FindCalendarEventByPublicIdRow, splitStart time.Time) time.Time {
	truncateAt := splitStart.Add(-time.Millisecond)
	if master.RecurrenceEnd.Valid && master.RecurrenceEnd.Time.Before(truncateAt) {
		return master.RecurrenceEnd.Time
	}
	return truncateAt
}

// appendRecurrenceException adds one occurrence start to a stored
// exception list, and reports whether the list changed.
//
// The entry is written as an RFC 3339 instant in UTC, the spelling
// buildExceptions turns into an exact skip keyed by unix seconds — so
// the entry matches the occurrence whatever timezone the series is drawn
// in. An entry already naming the same instant is left alone rather than
// repeated.
//
// A stored list that does not parse is an error rather than a fresh
// list: starting over would drop every occurrence the series had already
// cancelled, and they would all come back.
func appendRecurrenceException(stored json.RawMessage, start time.Time) (json.RawMessage, bool, error) {
	var list []string
	if len(stored) > 0 && string(stored) != "null" {
		if err := json.Unmarshal(stored, &list); err != nil {
			return nil, false, err
		}
	}

	entry := start.UTC().Format(time.RFC3339)
	for _, existing := range list {
		if existing == entry {
			return nil, false, nil
		}
		if at, err := time.Parse(time.RFC3339, existing); err == nil && at.Equal(start) {
			return nil, false, nil
		}
	}

	encoded, err := json.Marshal(append(list, entry))
	if err != nil {
		return nil, false, err
	}
	return encoded, true, nil
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
	exceptions, changed, err := appendRecurrenceException(master.RecurrenceExceptions, occurrenceStart)
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

// deleteEventFollowing stops a series at an occurrence: the master keeps
// the occurrences before the split and produces none from it onwards.
//
// The overrides at or after the split go with them. They describe
// occurrences the truncated master no longer produces, and each is a row
// the non-recurring range queries select on its own, so left enabled
// they outlive the part of the series they belong to.
func deleteEventFollowing(
	ctx context.Context,
	deps Deps,
	wsID, actorID uint32,
	cal calendar.FindCalendarByPublicIdRow,
	master calendar.FindCalendarEventByPublicIdRow,
	splitStart time.Time,
) error {
	truncateAt := seriesTruncationPoint(master, splitStart)
	parentID := handlerutil.NullInt32From(master.ID)

	var answered error
	txErr := dbretry.InTx(ctx, deps.DB, "calendars.DeleteEventFollowing", nil, func(ctx context.Context, tx *dbretry.Tx) error {
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

		// DisableCalendarEventOverridesByParent takes the whole series and
		// cannot be given a range, and the overrides before the split
		// describe occurrences the master still produces. So the rows are
		// listed and disabled one at a time: the list is already confined
		// to this parent's live overrides, and a series carries few enough
		// of them that the loop is bounded by the same runaway limit the
		// expander reads them under.
		//
		// The list is scoped to the acting user, so a confidential override
		// belonging to somebody else stays enabled past the truncation and
		// keeps rendering to its owner. That is the same row the actor is
		// not served anywhere else; disabling one they cannot see would
		// remove an occurrence from a calendar they have no view of.
		starts, err := cqtx.ListCalendarEventOverriddenStarts(ctx, calendar.ListCalendarEventOverriddenStartsParams{
			WorkspaceID:  wsID,
			ParentIds:    []sql.NullInt32{parentID},
			ViewerUserID: actorID,
		})
		if err != nil {
			answered = httpErr(apierrors.CalendarEventStoreReadInterrupted)
			return err
		}
		for _, s := range starts {
			if !s.RecurrenceOriginalStart.Valid || s.RecurrenceOriginalStart.Time.Before(splitStart) {
				continue
			}
			override, findErr := cqtx.FindCalendarEventOverride(ctx, calendar.FindCalendarEventOverrideParams{
				WorkspaceID:             wsID,
				RecurrenceParentID:      parentID,
				RecurrenceOriginalStart: s.RecurrenceOriginalStart,
			})
			if findErr != nil {
				answered = httpErr(apierrors.CalendarEventStoreReadInterrupted)
				return findErr
			}
			// affected-rows: not-applicable — the loop walks the live
			// overrides listed in this transaction and disables each one it
			// re-read by key, so a zero says another writer got there first
			// rather than that anything was missing. The caller named the
			// series, and truncating the master is what removes the
			// occurrences these describe.
			if _, err := cqtx.DisableCalendarEvent(ctx, calendar.DisableCalendarEventParams{
				PublicID:    override.PublicID,
				CalendarID:  override.CalendarID,
				WorkspaceID: wsID,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if answered != nil {
		return answered
	}
	if txErr != nil {
		return httpErr(apierrors.CalendarEventStoreDeleteInterrupted)
	}
	return nil
}

// recordOccurrenceDelete appends the domain event and the audit entry for
// a delete that reached one occurrence rather than the series.
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
	appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, calID, eventbus.CalEventUpdated, &actorID, map[string]any{
		"eventId":         input.EvtID,
		"calendarId":      input.CalID,
		"scope":           string(scope),
		"occurrenceStart": input.OccurrenceStart,
	}, "calendars.DeleteEvent")

	deps.Audit.Record(ctx, audit.Entry{
		Action:       "calendar.event.delete",
		ActorID:      actorID,
		WorkspaceID:  wsID,
		ResourceType: "calendar.event",
		ResourceID:   input.EvtID,
		Metadata: map[string]any{
			"calendarId":      input.CalID,
			"title":           master.Title,
			"scope":           string(scope),
			"occurrenceStart": input.OccurrenceStart,
		},
	})
}
