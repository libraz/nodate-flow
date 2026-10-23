package calendaroccurrence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"time"

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
// A refusal the caller can act on comes back as the APIError; any other
// error is a store failure.
func WriteOverride(ctx context.Context, q Queries, in Override) (Written, *apierrors.APIError, error) {
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
		return Written{PublicID: existing.PublicID, CalendarID: existing.CalendarID}, nil, nil
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
	before := SeriesState{
		StartAt:    in.Master.StartAt,
		EndAt:      in.Master.EndAt,
		Timezone:   in.Master.Timezone,
		Rule:       locked.RecurrenceRule,
		End:        locked.RecurrenceEnd,
		Exceptions: locked.RecurrenceExceptions,
	}
	oldEvt, oldOK := before.event()

	next := SeriesState{StartAt: fields.StartAt, EndAt: fields.EndAt, Timezone: fields.Timezone}
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
		m = newSeriesMap(old, in.SplitStart, nextEvt, reachOf(in.Master.Timezone, oldExceptions, overrides))
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
	if kept, changed := keepExceptionsBefore(oldExceptions, in.Master.Timezone, in.SplitStart); changed {
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

	if err := carrySeriesChildren(ctx, q, in.WorkspaceID, in.Master.ID, newID); err != nil {
		return Written{}, nil, err
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

// EndSeries stops a series at an occurrence: the master keeps the
// occurrences before the split and produces none from it onwards.
//
// The exceptions naming occurrences from the split go, because nothing is
// left for them to cancel, and so do the overrides at or after it: each is
// a row the non-recurring range queries select on its own, so left enabled
// it outlives the part of the series it belongs to. The withdrawal is
// scoped to the overrides viewerID can see — see
// DisableCalendarEventOverridesFromStart.
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
	truncate := calendar.PatchCalendarEventParams{
		RecurrenceEnd: sql.NullTime{Time: truncationPoint(locked.RecurrenceEnd, splitStart), Valid: true},
		PublicID:      master.PublicID,
		CalendarID:    calendarID,
		WorkspaceID:   workspaceID,
	}
	exceptions := recurrence.ParseExceptions(locked.RecurrenceExceptions)
	if kept, changed := keepExceptionsBefore(exceptions, master.Timezone, splitStart); changed {
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

// SeriesState is the part of a master row that decides which instants its
// occurrences fall on.
type SeriesState struct {
	StartAt    sql.NullTime
	EndAt      sql.NullTime
	Timezone   string
	Rule       json.RawMessage
	End        sql.NullTime
	Exceptions json.RawMessage
}

// SeriesStateOf reads the series columns off a master row.
func SeriesStateOf(row calendar.FindCalendarEventByPublicIdRow) SeriesState {
	return SeriesState{
		StartAt:    row.StartAt,
		EndAt:      row.EndAt,
		Timezone:   row.Timezone,
		Rule:       row.RecurrenceRule,
		End:        row.RecurrenceEnd,
		Exceptions: row.RecurrenceExceptions,
	}
}

// event projects the state onto what the expander reads, without the
// exception list. False means the state produces no occurrences to map:
// no rule, a rule that does not parse, or no anchor.
func (s SeriesState) event() (recurrence.Event, bool) {
	if !s.StartAt.Valid || !HasRule(s.Rule) {
		return recurrence.Event{}, false
	}
	rule, err := recurrence.ParseRule(s.Rule)
	if err != nil || rule == nil || !rule.Freq.Valid() {
		return recurrence.Event{}, false
	}
	evt := recurrence.Event{
		StartAt:  s.StartAt.Time,
		EndAt:    s.StartAt.Time,
		Timezone: s.Timezone,
		Rule:     rule,
	}
	if s.EndAt.Valid {
		evt.EndAt = s.EndAt.Time
	}
	if s.End.Valid {
		end := s.End.Time
		evt.RecurrenceEnd = &end
	}
	return evt, true
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
	m := newSeriesMap(old, from, nextEvt, reachOf(nextEvt.Timezone, exceptions, overrides))
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

// continuingRule is the master's rule as the continuing series carries
// it: a COUNT shortened by the occurrences the truncated master already
// produced, so the two halves add up to the series the rule defined.
// UNTIL and recurrence_end are instants and carry over unchanged.
func continuingRule(raw json.RawMessage, old recurrence.Event, splitStart time.Time) (json.RawMessage, error) {
	if old.Rule == nil || old.Rule.Count == nil {
		return raw, nil
	}
	remaining := *old.Rule.Count - recurrence.CountBefore(old, splitStart)
	if remaining < 0 {
		remaining = 0
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return nil, err
	}
	count, err := json.Marshal(remaining)
	if err != nil {
		return nil, err
	}
	members["count"] = count
	return json.Marshal(members)
}

// seriesMap carries occurrences of one series onto another.
//
// When the rule's pattern is unchanged an occurrence keeps its position:
// the third meeting from the split is still the third, at whatever instant
// the new anchor or zone gives it. Otherwise, and for anything past the
// new series' end, an occurrence survives only where the new series still
// produces its instant.
type seriesMap struct {
	next      recurrence.Event
	timezone  string
	oldStarts []time.Time
	newStarts []time.Time
	position  map[int64]int
}

// newSeriesMap builds the map for occurrences of old at or after from,
// up to reach. A nil old maps nothing by position.
func newSeriesMap(old *recurrence.Event, from time.Time, next recurrence.Event, reach time.Time) seriesMap {
	m := seriesMap{next: next, timezone: next.Timezone}
	if old == nil {
		return m
	}
	m.timezone = old.Timezone
	if !samePattern(old.Rule, next.Rule) {
		return m
	}
	m.oldStarts = recurrence.Starts(*old, from, math.MaxInt32, reach.Add(time.Second))
	m.newStarts = recurrence.Starts(next, next.StartAt, len(m.oldStarts), time.Time{})
	m.position = make(map[int64]int, len(m.oldStarts))
	for k, s := range m.oldStarts {
		m.position[s.UnixMilli()] = k
	}
	return m
}

// instant maps one occurrence start of the old series.
func (m seriesMap) instant(at time.Time) (time.Time, bool) {
	if k, ok := m.position[at.UnixMilli()]; ok && k < len(m.newStarts) {
		return m.newStarts[k], true
	}
	if recurrence.Produces(m.next, at) {
		return at, true
	}
	return time.Time{}, false
}

// entry maps one stored exception entry, keeping its spelling wherever
// the occurrence it names did not move.
func (m seriesMap) entry(raw string) (string, bool) {
	for k, s := range m.oldStarts {
		if !recurrence.EntryNames(raw, m.timezone, s) {
			continue
		}
		if k < len(m.newStarts) {
			if sameInstant(m.newStarts[k], s) {
				return raw, true
			}
			return m.newStarts[k].UTC().Format(time.RFC3339), true
		}
		break
	}
	if recurrence.ProducesEntry(m.next, raw) {
		return raw, true
	}
	return "", false
}

// carryExceptions maps a stored exception list, and reports whether the
// result differs from it.
func (m seriesMap) carryExceptions(list []string) ([]string, bool) {
	out := make([]string, 0, len(list))
	seen := map[string]bool{}
	changed := false
	for _, raw := range list {
		mapped, ok := m.entry(raw)
		if !ok || seen[mapped] {
			changed = true
			continue
		}
		if mapped != raw {
			changed = true
		}
		seen[mapped] = true
		out = append(out, mapped)
	}
	return out, changed
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

// carrySeriesChildren gives the continuing master of a split what the
// series carried: its attendees, the live invites addressed to them, its
// share publications, its checklist and its attachments.
//
// Attendees, publications, checklist items and attachments are copied,
// because the truncated master's occurrences still happened with them.
// Invites move, because an invite is a single token-bound grant per
// attendee and the link its recipient holds should answer for the
// occurrences still to come.
func carrySeriesChildren(ctx context.Context, q Queries, workspaceID, oldID, newID uint32) error {
	attendees, err := q.Calendar.ListCalendarEventAttendees(ctx, calendar.ListCalendarEventAttendeesParams{
		EventID:     nullID(oldID),
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return err
	}
	attendeeOf := make(map[uint32]uint32, len(attendees))
	for _, a := range attendees {
		lastID, err := q.Calendar.CreateCalendarEventAttendee(ctx, calendar.CreateCalendarEventAttendeeParams{
			PublicID:    types.New(),
			WorkspaceID: workspaceID,
			EventID:     nullID(newID),
			UserID:      a.UserID,
			Rsvp:        a.Rsvp,
			CanEdit:     a.CanEdit,
		})
		if err != nil {
			return err
		}
		attendeeOf[a.ID] = rowID(lastID)
	}

	// Every page is read before any invite moves: a moved invite leaves
	// the old master's list, and paging over a shrinking list skips rows.
	var invites []calendar.ListCalendarEventInvitesForEventRow
	for offset := int32(0); ; offset += childPage {
		page, err := q.Calendar.ListCalendarEventInvitesForEvent(ctx, calendar.ListCalendarEventInvitesForEventParams{
			EventID: nullID(oldID),
			Limit:   childPage,
			Offset:  offset,
		})
		if err != nil {
			return err
		}
		invites = append(invites, page...)
		if len(page) < childPage {
			break
		}
	}
	for _, inv := range invites {
		if !inv.AttendeeID.Valid {
			continue
		}
		newAttendee, ok := attendeeOf[uint32(inv.AttendeeID.Int32)] //#nosec G115 -- internal row id read back from an INT UNSIGNED column
		if !ok {
			continue
		}
		if err := q.Calendar.ReassignCalendarEventInvite(ctx, calendar.ReassignCalendarEventInviteParams{
			NewEventID:    nullID(newID),
			NewAttendeeID: nullID(newAttendee),
			ID:            inv.ID,
			WorkspaceID:   workspaceID,
		}); err != nil {
			return err
		}
	}

	links, err := q.Calendar.ListCalendarEventShareLinks(ctx, calendar.ListCalendarEventShareLinksParams{
		WorkspaceID: workspaceID,
		EventID:     oldID,
	})
	if err != nil {
		return err
	}
	for _, l := range links {
		if _, err := q.Calendar.AttachEventToShare(ctx, calendar.AttachEventToShareParams{
			PublicID:    types.New(),
			WorkspaceID: workspaceID,
			ShareID:     l.ShareID,
			EventID:     newID,
			SortWeight:  l.SortWeight,
		}); err != nil {
			return err
		}
	}

	for offset := int32(0); ; offset += childPage {
		items, err := q.Calendar.ListCalendarChecklistItems(ctx, calendar.ListCalendarChecklistItemsParams{
			EventID:     oldID,
			WorkspaceID: workspaceID,
			Limit:       childPage,
			Offset:      offset,
		})
		if err != nil {
			return err
		}
		for _, it := range items {
			if err := q.Calendar.CopyCalendarChecklistItem(ctx, calendar.CopyCalendarChecklistItemParams{
				PublicID:        types.New(),
				WorkspaceID:     workspaceID,
				EventID:         newID,
				CreatedByUserID: it.CreatedByUserID,
				Title:           it.Title,
				Done:            it.Done,
				SortWeight:      it.SortWeight,
			}); err != nil {
				return err
			}
		}
		if len(items) < childPage {
			break
		}
	}

	refs, err := q.Calendar.ListCalendarEventAttachmentRefs(ctx, calendar.ListCalendarEventAttachmentRefsParams{
		WorkspaceID: workspaceID,
		EventID:     nullID(oldID),
	})
	if err != nil {
		return err
	}
	for _, r := range refs {
		if _, err := q.Calendar.CreateCalendarEventAttachment(ctx, calendar.CreateCalendarEventAttachmentParams{
			PublicID:        types.New(),
			WorkspaceID:     workspaceID,
			EventID:         nullID(newID),
			UploaderID:      r.UploaderID,
			StorageObjectID: r.StorageObjectID,
			Filename:        r.Filename,
		}); err != nil {
			return err
		}
		// Each copy is another referencing row; the blob outlives the
		// first of them to be deleted only if the count says so.
		if err := q.Core.IncrementStorageObjectRefCount(ctx, r.StorageObjectID); err != nil {
			return err
		}
	}
	return nil
}

// childPage is the page size the child copies read the paginated lists
// in.
const childPage = 500

// carryReminderClaims copies the reminder claims one row holds from a
// point onwards onto another row, for the instants keep accepts.
func carryReminderClaims(
	ctx context.Context,
	q Queries,
	workspaceID, fromID, toID uint32,
	from time.Time,
	keep func(time.Time) bool,
) error {
	claims, err := q.Calendar.ListCalendarEventReminderClaims(ctx, calendar.ListCalendarEventReminderClaimsParams{
		WorkspaceID: workspaceID,
		EventID:     fromID,
		FromStart:   from,
	})
	if err != nil {
		return err
	}
	for _, at := range claims {
		if !keep(at) {
			continue
		}
		if err := q.Calendar.CopyCalendarEventReminderClaim(ctx, calendar.CopyCalendarEventReminderClaimParams{
			WorkspaceID:     workspaceID,
			EventID:         toID,
			OccurrenceStart: at,
		}); err != nil {
			return err
		}
	}
	return nil
}

// keepExceptionsBefore drops the entries naming an occurrence at or after
// split, and reports whether any went. An entry that names nothing is
// kept: it is not this write's to judge.
func keepExceptionsBefore(list []string, timezone string, split time.Time) ([]string, bool) {
	out := make([]string, 0, len(list))
	for _, raw := range list {
		if latest, ok := recurrence.EntryLatest(raw, timezone); ok && !latest.Before(split) {
			continue
		}
		out = append(out, raw)
	}
	return out, len(out) != len(list)
}

// reachOf is the latest instant any exception or override names, which is
// how far the old series has to be walked to place them all.
func reachOf(timezone string, exceptions []string, overrides []calendar.ListCalendarEventOverridesFromStartRow) time.Time {
	var reach time.Time
	for _, raw := range exceptions {
		if latest, ok := recurrence.EntryLatest(raw, timezone); ok && latest.After(reach) {
			reach = latest
		}
	}
	for _, r := range overrides {
		if r.RecurrenceOriginalStart.Valid && r.RecurrenceOriginalStart.Time.After(reach) {
			reach = r.RecurrenceOriginalStart.Time
		}
	}
	return reach
}

// samePattern reports whether two rules lay occurrences out the same way,
// whatever their bounds: only then does an occurrence's position in one
// name an occurrence of the other.
func samePattern(a, b *recurrence.Rule) bool {
	if a == nil || b == nil || a.Freq != b.Freq || intervalOf(a) != intervalOf(b) {
		return false
	}
	return sameStrings(a.ByDay, b.ByDay) && sameInts(a.ByMonthDay, b.ByMonthDay)
}

func intervalOf(r *recurrence.Rule) int {
	if r.Interval == nil || *r.Interval < 1 {
		return 1
	}
	return *r.Interval
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// encodeExceptions writes an exception list back in the column's shape.
func encodeExceptions(list []string) (json.RawMessage, error) {
	if list == nil {
		list = []string{}
	}
	return json.Marshal(list)
}

// sameInstant compares two instants at the millisecond precision the
// columns store.
func sameInstant(a, b time.Time) bool {
	return a.UnixMilli() == b.UnixMilli()
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
