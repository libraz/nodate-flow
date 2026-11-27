package calendaroccurrence

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarlabels"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/recurrence"
)

// Queries is the pair of query handles a series transform writes through,
// both bound to the caller's transaction. The transforms open no
// transaction of their own, so the caller's retry loop covers every
// statement they issue.
type Queries struct {
	Calendar *calendar.Queries
	Core     *generated.Queries
}

// Written names the row a transform answered with.
type Written struct {
	ID         uint32
	PublicID   types.PublicID
	CalendarID uint32
}

// SeriesPatch is what a split says about the continuing series' own
// recurrence columns. A nil value carries the master's forward; a clear
// empties the column, and clearing the rule empties all three.
type SeriesPatch struct {
	Rule            json.RawMessage
	End             *time.Time
	Exceptions      json.RawMessage
	ClearRule       bool
	ClearEnd        bool
	ClearExceptions bool
}

// Split is one this-and-following edit.
type Split struct {
	WorkspaceID uint32
	ActorID     uint32
	CalendarID  uint32
	Master      calendar.FindCalendarEventByPublicIdRow
	SplitStart  time.Time
	Patch       Patch
	Series      SeriesPatch
}

// SplitSeries ends a series just before an occurrence and continues it
// from there as a new master carrying the edit, and answers with the new
// master.
//
// The continuing master is rebuilt from the whole series rather than from
// the columns an occurrence carries: the rule keeps its remaining COUNT
// instead of restarting it, the exceptions and overrides from the split
// onward follow their occurrences to wherever the edit put them, and the
// attendees, invites, share publications, checklist, attachments and sent
// reminders the series had carry over. The truncated master keeps only the
// exceptions naming occurrences it still produces.
func SplitSeries(ctx context.Context, q Queries, in Split) (Written, *apierrors.APIError, error) {
	// The continuing series starts from the master alone: it is a new
	// master, not an edit of an occurrence, so no override describes it.
	fields, refusal := in.Patch.Apply(MasterBase(in.Master, in.SplitStart))
	if refusal != nil {
		return Written{}, refusal, nil
	}
	if refusal := calendarrules.RequireValidTimezone("timezone", fields.Timezone); refusal != nil {
		return Written{}, refusal, nil
	}

	locked, err := q.Calendar.LockCalendarEventSeries(ctx, calendar.LockCalendarEventSeriesParams{
		ID:          in.Master.ID,
		WorkspaceID: in.WorkspaceID,
	})
	if err != nil {
		return Written{}, nil, err
	}
	before := lockedState(in.Master, locked)
	if refusal := occurrenceRefusal(before, in.SplitStart); refusal != nil {
		return Written{}, refusal, nil
	}
	oldEvt, oldOK := before.event()

	next := SeriesState{StartAt: fields.StartAt, EndAt: fields.EndAt, Timezone: fields.Timezone, AllDay: fields.AllDay}
	if !in.Series.ClearRule {
		next.Rule = in.Series.Rule
		if next.Rule == nil {
			next.Rule = locked.RecurrenceRule
			if oldOK {
				if next.Rule, err = continuingRule(locked.RecurrenceRule, oldEvt, in.SplitStart); err != nil {
					return Written{}, nil, err
				}
			}
		}
		switch {
		case in.Series.ClearEnd:
		case in.Series.End != nil:
			next.End = sql.NullTime{Time: *in.Series.End, Valid: true}
		default:
			next.End = locked.RecurrenceEnd
		}
	}
	nextEvt, nextOK := next.event()
	if refusal := calendarrules.RequireEventShape(string(fields.Kind), fields.StartAt.Valid, HasRule(next.Rule), fields.NotificationOffset.Valid); refusal != nil {
		return Written{}, refusal, nil
	}

	parentID := nullID(in.Master.ID)
	overrides, err := q.Calendar.ListCalendarEventOverridesFromStart(ctx, calendar.ListCalendarEventOverridesFromStartParams{
		WorkspaceID: in.WorkspaceID,
		ParentID:    parentID,
		FromStart:   sql.NullTime{Time: in.SplitStart, Valid: true},
	})
	if err != nil {
		return Written{}, nil, err
	}

	oldExceptions := recurrence.ParseExceptions(locked.RecurrenceExceptions)
	var m seriesMap
	if nextOK {
		var old *recurrence.Event
		if oldOK {
			old = &oldEvt
		}
		m = newSeriesMap(old, in.SplitStart, nextEvt, reachOf(before.zone(), oldExceptions, overrides))
	}
	if !in.Series.ClearRule && !in.Series.ClearExceptions {
		switch {
		case in.Series.Exceptions != nil:
			next.Exceptions = in.Series.Exceptions
		case nextOK:
			// A new row can leave the column NULL, so an empty list is
			// written as none at all, the way the series started.
			if carried, _ := m.carryExceptions(oldExceptions); len(carried) > 0 {
				if next.Exceptions, err = encodeExceptions(carried); err != nil {
					return Written{}, nil, err
				}
			}
		default:
			next.Exceptions = locked.RecurrenceExceptions
		}
	}

	truncate := calendar.PatchCalendarEventParams{
		RecurrenceEnd: sql.NullTime{Time: truncationPoint(locked.RecurrenceEnd, in.SplitStart), Valid: true},
		PublicID:      in.Master.PublicID,
		CalendarID:    in.CalendarID,
		WorkspaceID:   in.WorkspaceID,
	}
	if kept, changed := keepExceptionsBefore(oldExceptions, before.zone(), in.SplitStart); changed {
		if truncate.RecurrenceExceptions, err = encodeExceptions(kept); err != nil {
			return Written{}, nil, err
		}
	}
	// affected-rows: not-applicable — the master was read live under FOR
	// UPDATE in this transaction, so nothing can have withdrawn it, and a
	// zero only says the columns already held these values.
	if _, err := q.Calendar.PatchCalendarEvent(ctx, truncate); err != nil {
		return Written{}, nil, err
	}

	newPublicID := types.New()
	lastID, err := q.Calendar.CreateCalendarEvent(ctx, calendar.CreateCalendarEventParams{
		PublicID:             newPublicID,
		WorkspaceID:          in.WorkspaceID,
		CalendarID:           in.CalendarID,
		Kind:                 fields.Kind,
		Visibility:           fields.Visibility,
		ShowAs:               fields.ShowAs,
		Flexibility:          fields.Flexibility,
		Title:                fields.Title,
		AllDay:               fields.AllDay,
		StartAt:              fields.StartAt,
		EndAt:                fields.EndAt,
		Timezone:             fields.Timezone,
		Location:             fields.Location,
		Memo:                 fields.Memo,
		Url:                  fields.URL,
		OwnerUserID:          in.Master.OwnerUserID,
		CreatedByUserID:      in.ActorID,
		BlockLabel:           fields.BlockLabel,
		RecurrenceRule:       next.Rule,
		RecurrenceEnd:        next.End,
		RecurrenceExceptions: next.Exceptions,
		NotificationOffset:   fields.NotificationOffset,
	})
	if err != nil {
		return Written{}, nil, err
	}
	newID := rowID(lastID)

	// Overrides at or after the split describe occurrences of the new
	// series, soft-deleted ones included — left behind they would name an
	// occurrence the truncated master no longer produces.
	if _, err := q.Calendar.ReparentCalendarEventOverridesFromStart(ctx, calendar.ReparentCalendarEventOverridesFromStartParams{
		NewParentID: nullID(newID),
		WorkspaceID: in.WorkspaceID,
		OldParentID: parentID,
		SplitStart:  sql.NullTime{Time: in.SplitStart, Valid: true},
	}); err != nil {
		return Written{}, nil, err
	}
	if nextOK {
		if err := realignOverrides(ctx, q, in.WorkspaceID, overrides, m); err != nil {
			return Written{}, nil, err
		}
	} else if err := disableOverrides(ctx, q, in.WorkspaceID, overrides); err != nil {
		return Written{}, nil, err
	}

	if err := carrySeriesChildren(ctx, q, in.WorkspaceID, in.Master.CalendarID, in.Master.ID, newID); err != nil {
		return Written{}, nil, err
	}
	change := in.Patch.Label
	if change.Mode == calendarlabels.ChangeKeep {
		if err := seriesLabelChange(ctx, q, in.Master.ID, &change); err != nil {
			return Written{}, nil, err
		}
	}
	if _, refusal, err := calendarlabels.SetLink(ctx, q.Calendar, in.WorkspaceID, newID, change.Value()); err != nil || refusal != nil {
		return Written{}, refusal, err
	}
	// Only occurrences still to come can be reminded, so the claims worth
	// carrying are the ones from the later of the split and now. A claim
	// lands on the new master at the instant it was sent for: an
	// occurrence the edit left in place stays reminded, and one it moved
	// is reminded again at its new time.
	from := in.SplitStart
	if now := time.Now().UTC(); now.After(from) {
		from = now
	}
	if err := carryReminderClaims(ctx, q, in.WorkspaceID, in.Master.ID, newID, from,
		func(time.Time) bool { return true }); err != nil {
		return Written{}, nil, err
	}

	return Written{ID: newID, PublicID: newPublicID, CalendarID: in.CalendarID}, nil, nil
}

// seriesLabelChange turns c into a Set of the series' current label, or a
// Clear when it has none.
func seriesLabelChange(ctx context.Context, q Queries, masterID uint32, c *calendarlabels.Change) error {
	id, err := calendarlabels.CurrentLabel(ctx, q.Calendar, masterID)
	if err != nil {
		return err
	}
	if id == 0 {
		*c = calendarlabels.Change{Mode: calendarlabels.ChangeClear}
	} else {
		*c = calendarlabels.Change{Mode: calendarlabels.ChangeSet, LabelID: id}
	}
	return nil
}

// EndSeries stops a series at an occurrence: the master keeps the
// occurrences before the split and produces none from it onwards.
//
// The exceptions naming occurrences from the split go, because nothing is
// left for them to cancel, and so do the overrides at or after it: each is
// a row the non-recurring range queries select on its own, so left enabled
// it outlives the part of the series it belongs to. The withdrawal is
// scoped to the overrides viewerID can see — see
// DisableCalendarEventOverridesFromStart.
//
// A splitStart that names no occurrence comes back as an
// *apierrors.APIError, with nothing written, for the caller to unwrap.
func EndSeries(
	ctx context.Context,
	q Queries,
	workspaceID, calendarID, viewerID uint32,
	master calendar.FindCalendarEventByPublicIdRow,
	splitStart time.Time,
) error {
	locked, err := q.Calendar.LockCalendarEventSeries(ctx, calendar.LockCalendarEventSeriesParams{
		ID:          master.ID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return err
	}
	if refusal := occurrenceRefusal(lockedState(master, locked), splitStart); refusal != nil {
		return refusal
	}
	truncate := calendar.PatchCalendarEventParams{
		RecurrenceEnd: sql.NullTime{Time: truncationPoint(locked.RecurrenceEnd, splitStart), Valid: true},
		PublicID:      master.PublicID,
		CalendarID:    calendarID,
		WorkspaceID:   workspaceID,
	}
	exceptions := recurrence.ParseExceptions(locked.RecurrenceExceptions)
	if kept, changed := keepExceptionsBefore(exceptions, SeriesStateOf(master).zone(), splitStart); changed {
		if truncate.RecurrenceExceptions, err = encodeExceptions(kept); err != nil {
			return err
		}
	}
	if _, err := q.Calendar.PatchCalendarEvent(ctx, truncate); err != nil {
		return err
	}
	// affected-rows: not-applicable — the count is how many overrides the
	// series happened to carry past the split, and zero is the ordinary
	// answer for a series nobody edited one occurrence of.
	_, err = q.Calendar.DisableCalendarEventOverridesFromStart(ctx, calendar.DisableCalendarEventOverridesFromStartParams{
		WorkspaceID:  workspaceID,
		ParentID:     nullID(master.ID),
		SplitStart:   sql.NullTime{Time: splitStart, Valid: true},
		ViewerUserID: viewerID,
	})
	return err
}

// RealignSeries brings a master's overrides and exception list along after
// a series write changed where its occurrences fall — its start, zone,
// rule or bounds — and answers with the exception list it wrote, if any.
//
// Each override and exception follows its occurrence to the same position
// in the rule when the rule's pattern is unchanged, or stays where it is
// when the new series still produces that instant; otherwise the override
// is disabled and the exception dropped, so nothing is left naming an
// occurrence that no longer exists. Clearing the rule disables every
// override, since there is no series left for them to stand in.
//
// keepExceptions leaves the stored list alone, for a write whose caller
// sent the list it wants.
func RealignSeries(
	ctx context.Context,
	q Queries,
	workspaceID uint32,
	before SeriesState,
	after calendar.FindCalendarEventByPublicIdRow,
	keepExceptions bool,
) (json.RawMessage, error) {
	parentID := nullID(after.ID)
	if !HasRule(after.RecurrenceRule) {
		// affected-rows: not-applicable — a series that was never edited
		// one occurrence at a time has no overrides to withdraw.
		_, err := q.Calendar.DisableCalendarEventOverridesByParent(ctx, calendar.DisableCalendarEventOverridesByParentParams{
			WorkspaceID:        workspaceID,
			RecurrenceParentID: parentID,
		})
		return nil, err
	}
	nextEvt, ok := SeriesStateOf(after).event()
	if !ok {
		return nil, nil
	}
	oldEvt, oldOK := before.event()
	from := nextEvt.StartAt
	if oldOK && oldEvt.StartAt.Before(from) {
		from = oldEvt.StartAt
	}

	overrides, err := q.Calendar.ListCalendarEventOverridesFromStart(ctx, calendar.ListCalendarEventOverridesFromStartParams{
		WorkspaceID: workspaceID,
		ParentID:    parentID,
		FromStart:   sql.NullTime{Time: from, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	exceptions := recurrence.ParseExceptions(after.RecurrenceExceptions)

	var old *recurrence.Event
	if oldOK {
		old = &oldEvt
	}
	m := newSeriesMap(old, from, nextEvt, reachOf(SeriesStateOf(after).zone(), exceptions, overrides))
	if err := realignOverrides(ctx, q, workspaceID, overrides, m); err != nil {
		return nil, err
	}
	if keepExceptions {
		return nil, nil
	}
	carried, changed := m.carryExceptions(exceptions)
	if !changed {
		return nil, nil
	}
	encoded, err := encodeExceptions(carried)
	if err != nil {
		return nil, err
	}
	if _, err := q.Calendar.PatchCalendarEvent(ctx, calendar.PatchCalendarEventParams{
		RecurrenceExceptions: encoded,
		PublicID:             after.PublicID,
		CalendarID:           after.CalendarID,
		WorkspaceID:          workspaceID,
	}); err != nil {
		return nil, err
	}
	return encoded, nil
}

// realignOverrides moves each listed override to the occurrence its
// original start maps to, and disables a live one whose occurrence is
// gone.
//
// The unique key on (recurrence_parent_id, recurrence_original_start)
// counts disabled rows, so the moves run in the direction the series
// moved — a row is moved off its slot before the row behind it lands
// there — and a move onto a slot some other row still holds is refused
// and the mover disabled instead.
func realignOverrides(ctx context.Context, q Queries, workspaceID uint32, rows []calendar.ListCalendarEventOverridesFromStartRow, m seriesMap) error {
	type move struct {
		row    calendar.ListCalendarEventOverridesFromStartRow
		target time.Time
	}
	occupied := make(map[int64]bool, len(rows))
	var moves []move
	for _, r := range rows {
		if !r.RecurrenceOriginalStart.Valid {
			continue
		}
		at := r.RecurrenceOriginalStart.Time
		occupied[at.UnixMilli()] = true
		target, ok := m.instant(at)
		switch {
		case !ok:
			if err := disableOverride(ctx, q, workspaceID, r); err != nil {
				return err
			}
		case !sameInstant(target, at):
			moves = append(moves, move{row: r, target: target})
		}
	}
	if len(moves) == 0 {
		return nil
	}

	forward := moves[0].target.After(moves[0].row.RecurrenceOriginalStart.Time)
	sort.Slice(moves, func(i, j int) bool {
		a, b := moves[i].row.RecurrenceOriginalStart.Time, moves[j].row.RecurrenceOriginalStart.Time
		if forward {
			return a.After(b)
		}
		return a.Before(b)
	})
	for _, mv := range moves {
		from := mv.row.RecurrenceOriginalStart.Time.UnixMilli()
		delete(occupied, from)
		if occupied[mv.target.UnixMilli()] {
			occupied[from] = true
			if err := disableOverride(ctx, q, workspaceID, mv.row); err != nil {
				return err
			}
			continue
		}
		if err := q.Calendar.RebaseCalendarEventOverride(ctx, calendar.RebaseCalendarEventOverrideParams{
			RecurrenceOriginalStart: sql.NullTime{Time: mv.target, Valid: true},
			ID:                      mv.row.ID,
			WorkspaceID:             workspaceID,
		}); err != nil {
			return err
		}
		occupied[mv.target.UnixMilli()] = true
	}
	return nil
}

// disableOverrides withdraws every live override listed, for a continuing
// series that produces no occurrences for them to stand in.
func disableOverrides(ctx context.Context, q Queries, workspaceID uint32, rows []calendar.ListCalendarEventOverridesFromStartRow) error {
	for _, r := range rows {
		if err := disableOverride(ctx, q, workspaceID, r); err != nil {
			return err
		}
	}
	return nil
}

func disableOverride(ctx context.Context, q Queries, workspaceID uint32, r calendar.ListCalendarEventOverridesFromStartRow) error {
	if !r.Enabled {
		return nil
	}
	// affected-rows: not-applicable — the row was listed live in this
	// transaction; a zero means another writer withdrew it first.
	_, err := q.Calendar.DisableCalendarEvent(ctx, calendar.DisableCalendarEventParams{
		PublicID:    r.PublicID,
		CalendarID:  r.CalendarID,
		WorkspaceID: workspaceID,
	})
	return err
}

// nullID narrows an internal row id onto the nullable column type sqlc
// emits for a nullable INT UNSIGNED foreign key.
func nullID(id uint32) sql.NullInt32 {
	return sql.NullInt32{Int32: int32(id), Valid: true} //#nosec G115 -- internal row id, bounded by realistic workspace size
}

// rowID narrows LAST_INSERT_ID onto the INT UNSIGNED id it names.
func rowID(lastID int64) uint32 {
	return uint32(lastID) //#nosec G115 -- LAST_INSERT_ID of an INT UNSIGNED column
}
