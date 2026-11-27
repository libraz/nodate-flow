package calendaroccurrence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarlabels"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// Override is one single-occurrence edit: the series, the occurrence's
// original start, and the members the caller asked to change.
type Override struct {
	WorkspaceID   uint32
	ActorID       uint32
	CalendarID    uint32
	Master        calendar.FindCalendarEventByPublicIdRow
	OriginalStart time.Time
	Patch         Patch
}

// WriteOverride replaces one occurrence of a series with an override row,
// or rewrites the override already standing in for it.
//
// The override is looked up first because
// uniq_calendar_events_recurrence_override counts soft-deleted rows: an
// occurrence overridden, reverted and edited again already has a row, and
// UpdateCalendarEventOverride is what revives it. What the caller's
// omissions fall back to is decided by that same lookup.
//
// The series lock is taken before the lookup, as the split and the end
// take it, so two edits of one occurrence serialize: the second reads the
// override the first committed and updates it, instead of losing the
// unique key to it. The lock must be the transaction's first read, or the
// lookup reads a snapshot from before the first edit committed.
//
// A refusal the caller can act on comes back as the APIError; any other
// error is a store failure.
func WriteOverride(ctx context.Context, q Queries, in Override) (Written, *apierrors.APIError, error) {
	locked, err := q.Calendar.LockCalendarEventSeries(ctx, calendar.LockCalendarEventSeriesParams{
		ID:          in.Master.ID,
		WorkspaceID: in.WorkspaceID,
	})
	if err != nil {
		return Written{}, nil, err
	}
	if refusal := occurrenceRefusal(lockedState(in.Master, locked), in.OriginalStart); refusal != nil {
		return Written{}, refusal, nil
	}
	parentID := nullID(in.Master.ID)
	originalStart := sql.NullTime{Time: in.OriginalStart, Valid: true}

	existing, err := q.Calendar.FindCalendarEventOverride(ctx, calendar.FindCalendarEventOverrideParams{
		WorkspaceID:             in.WorkspaceID,
		RecurrenceParentID:      parentID,
		RecurrenceOriginalStart: originalStart,
	})
	overridden := err == nil
	if !overridden && !errors.Is(err, sql.ErrNoRows) {
		return Written{}, nil, err
	}

	base := MasterBase(in.Master, in.OriginalStart)
	if overridden {
		base = OverrideBase(existing)
	}
	fields, refusal := in.Patch.Apply(base)
	if refusal != nil {
		return Written{}, refusal, nil
	}
	// The zone written is whichever survived the merge, so the merged
	// value is the one checked.
	if refusal := calendarrules.RequireValidTimezone("timezone", fields.Timezone); refusal != nil {
		return Written{}, refusal, nil
	}
	if refusal := calendarrules.RequireEventShape(string(fields.Kind), fields.StartAt.Valid, false, fields.NotificationOffset.Valid); refusal != nil {
		return Written{}, refusal, nil
	}

	if overridden {
		if _, err := q.Calendar.UpdateCalendarEventOverride(ctx, calendar.UpdateCalendarEventOverrideParams{
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
			WorkspaceID:        in.WorkspaceID,
		}); err != nil {
			return Written{}, nil, err
		}
		// A revived override must not keep the link it held when it was
		// withdrawn, so Keep re-reads the series' label for it.
		change := in.Patch.Label
		if change.Mode == calendarlabels.ChangeKeep && !existing.Enabled {
			change = calendarlabels.Change{}
			if err := seriesLabelChange(ctx, q, in.Master.ID, &change); err != nil {
				return Written{}, nil, err
			}
		}
		if change.Mode != calendarlabels.ChangeKeep {
			if _, refusal, err := calendarlabels.SetLink(ctx, q.Calendar, in.WorkspaceID, existing.ID, change.Value()); err != nil || refusal != nil {
				return Written{}, refusal, err
			}
		}
		return Written{ID: existing.ID, PublicID: existing.PublicID, CalendarID: existing.CalendarID}, nil, nil
	}

	pub := types.New()
	lastID, err := q.Calendar.CreateCalendarEventOverride(ctx, calendar.CreateCalendarEventOverrideParams{
		PublicID:                pub,
		WorkspaceID:             in.WorkspaceID,
		CalendarID:              in.CalendarID,
		RecurrenceParentID:      parentID,
		RecurrenceOriginalStart: originalStart,
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
		// The occurrence stays filed under whoever the series is filed
		// under: an override is the same commitment at another moment.
		OwnerUserID:        in.Master.OwnerUserID,
		CreatedByUserID:    in.ActorID,
		BlockLabel:         fields.BlockLabel,
		NotificationOffset: fields.NotificationOffset,
	})
	if err != nil {
		return Written{}, nil, err
	}
	id := rowID(lastID)

	// The occurrence keeps the series' label unless the edit says otherwise.
	change := in.Patch.Label
	if change.Mode == calendarlabels.ChangeKeep {
		if err := seriesLabelChange(ctx, q, in.Master.ID, &change); err != nil {
			return Written{}, nil, err
		}
	}
	if _, refusal, err := calendarlabels.SetLink(ctx, q.Calendar, in.WorkspaceID, id, change.Value()); err != nil || refusal != nil {
		return Written{}, refusal, err
	}

	// A reminder the series already sent for this occurrence stays sent.
	// The override carries the occurrence under its own id, and the
	// scheduler keys a non-recurring row's claim by its start, so an
	// override left where the occurrence was is not reminded twice; one
	// moved elsewhere is reminded again, as any rescheduled meeting is.
	if err := carryReminderClaims(ctx, q, in.WorkspaceID, in.Master.ID, id, in.OriginalStart,
		func(at time.Time) bool { return sameInstant(at, in.OriginalStart) }); err != nil {
		return Written{}, nil, err
	}
	return Written{ID: id, PublicID: pub, CalendarID: in.CalendarID}, nil, nil
}

// CancelOccurrence deletes one occurrence of a series: an exception entry
// on the master, so the rule stops producing it, and the override standing
// in for it, if any, disabled in the same transaction — the override is a
// row the non-recurring range queries select on their own.
func CancelOccurrence(
	ctx context.Context,
	q Queries,
	workspaceID uint32,
	master calendar.FindCalendarEventByPublicIdRow,
	at time.Time,
) (*apierrors.APIError, error) {
	locked, err := q.Calendar.LockCalendarEventSeries(ctx, calendar.LockCalendarEventSeriesParams{
		ID:          master.ID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return nil, err
	}
	if refusal := occurrenceRefusal(lockedState(master, locked), at); refusal != nil {
		return refusal, nil
	}
	if err := tombstone(ctx, q, workspaceID, master.ID, locked.RecurrenceExceptions, at, master.AllDay); err != nil {
		return nil, err
	}

	override, err := q.Calendar.FindCalendarEventOverride(ctx, calendar.FindCalendarEventOverrideParams{
		WorkspaceID:             workspaceID,
		RecurrenceParentID:      nullID(master.ID),
		RecurrenceOriginalStart: sql.NullTime{Time: at, Valid: true},
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, err
	}
	if !override.Enabled {
		return nil, nil
	}
	// affected-rows: not-applicable — the override was read live in this
	// transaction, and the occurrence is cancelled by the exception whether
	// or not an override stood in for it.
	_, err = q.Calendar.DisableCalendarEvent(ctx, calendar.DisableCalendarEventParams{
		PublicID:    override.PublicID,
		CalendarID:  override.CalendarID,
		WorkspaceID: workspaceID,
	})
	return nil, err
}

// CancelOverride records, on the master, the cancellation an override row
// deleted on its own leaves behind, and must run in the transaction that
// disables the row. Without it the master produces the occurrence again at
// its original slot. A row that is not an override, or whose master is
// gone, has nothing to record.
func CancelOverride(ctx context.Context, q Queries, workspaceID uint32, row calendar.FindCalendarEventByPublicIdRow) error {
	if !row.RecurrenceParentID.Valid {
		return nil
	}
	link, err := q.Calendar.FindCalendarEventRecurrenceLink(ctx, calendar.FindCalendarEventRecurrenceLinkParams{
		WorkspaceID: workspaceID,
		PublicID:    row.PublicID,
	})
	if err != nil {
		return err
	}
	if !link.RecurrenceParentID.Valid || !link.RecurrenceOriginalStart.Valid {
		return nil
	}
	masterID := uint32(link.RecurrenceParentID.Int32) //#nosec G115 -- internal row id read back from an INT UNSIGNED column
	locked, err := q.Calendar.LockCalendarEventSeries(ctx, calendar.LockCalendarEventSeriesParams{
		ID:          masterID,
		WorkspaceID: workspaceID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// The entry is spelled by the series' shape, which the override's own
	// all_day flag need not agree with.
	masterAllDay, err := q.Calendar.FindCalendarEventAllDay(ctx, calendar.FindCalendarEventAllDayParams{
		ID:          masterID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return err
	}
	return tombstone(ctx, q, workspaceID, masterID, locked.RecurrenceExceptions, link.RecurrenceOriginalStart.Time, masterAllDay)
}

// tombstone appends one occurrence's original start to a locked master's
// exception list, spelled for a series of whole days or of instants.
func tombstone(ctx context.Context, q Queries, workspaceID, masterID uint32, stored json.RawMessage, at time.Time, allDay bool) error {
	exceptions, changed, err := AppendOccurrenceException(stored, at, allDay)
	if err != nil || !changed {
		return err
	}
	return q.Calendar.SetCalendarEventSeriesExceptions(ctx, calendar.SetCalendarEventSeriesExceptionsParams{
		RecurrenceExceptions: exceptions,
		ID:                   masterID,
		WorkspaceID:          workspaceID,
	})
}
