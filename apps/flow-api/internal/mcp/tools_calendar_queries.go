package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/duedate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/recurrence"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// ----------------------------------------------------------------------------
// Calendar tools
// ----------------------------------------------------------------------------
func runListCalendars(ctx context.Context, deps Deps, s *session, _ json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	rows, err := deps.CalendarQueries.ListCalendarsForUser(ctx, calendar.ListCalendarsForUserParams{
		UserID:      s.userID,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, map[string]any{
			"id":   r.PublicID.String(),
			"kind": string(r.Kind),
			"name": r.Name,
			// calendar_members.role is the grant every calendar decision
			// is taken on, so it is the role reported here. Deriving one
			// from calendars.owner_user_id instead reported "editor" for
			// every member of a calendar that belongs to a group rather
			// than a person — viewers included — because that column is
			// NULL whenever a calendar outlives a single member. A caller
			// that reads this field to decide whether to attempt a write
			// was told it could, and then correctly refused.
			//
			// It is the membership grant and not a verdict on writing:
			// a system calendar's contents come from a provider feed and
			// are read-only at every role, which the write rule answers.
			"role":  string(r.Role),
			"color": r.Color,
			// member_color has been dropped from calendar_subscriptions;
			// fall back to display_color.
			"memberColor": r.DisplayColor,
			"visible":     r.Visible,
		})
	}
	return map[string]any{"calendars": items}, nil
}

// parseDayBoundary parses a YYYY-MM-DD string and returns the time at
// 00:00:00 UTC on that calendar day. Used for all-day events at the MCP
// boundary, mirroring the API/MCP convention.
func parseDayBoundary(s string) (time.Time, error) {
	return time.Parse("2006-01-02", s)
}

// mcpCalendarOccurrence is one thing on the calendar during a window:
// either a plain event or one occurrence of a series, flattened so the
// tools downstream do not have to care which.
type mcpCalendarOccurrence struct {
	row     calendar.ListCalendarEventsAcrossCalendarsRow
	startAt time.Time
	endAt   time.Time
	dated   bool
}

// Bounds on one calendar listing: the widest window a call may ask for
// and the most occurrences it may materialize, series expansions included.
const (
	maxCalendarListSpan    = 366 * 24 * time.Hour
	maxCalendarOccurrences = 5000
)

// errCalendarWindowTooLarge refuses a window whose events exceed
// maxCalendarOccurrences, so the caller narrows it rather than receiving a
// silently truncated list.
func errCalendarWindowTooLarge() error {
	return apierrors.Newf(apierrors.McpToolArgumentsInvalid, "the range holds more than %d events; narrow it", maxCalendarOccurrences)
}

// listCalendarOccurrences returns everything on the caller's calendars
// between startTime and endTime, with recurring series expanded into
// their concrete occurrences.
//
// Both calendar tools go through here. Reading only the non-recurring
// query — which is what they did — meant the standing meetings were
// absent from every answer: an agent asked for Tuesday's schedule got a
// day with the weekly one-to-ones missing, and the free-slot search
// built its busy map from the same truncated set and offered those hours
// as available.
//
// An occurrence a separate override row replaces is left to that row,
// which the non-recurring query above already returns at its moved time.
func listCalendarOccurrences(
	ctx context.Context,
	deps Deps,
	membershipUserID uint32,
	viewerUserID uint32,
	workspaceID uint32,
	startTime, endTime time.Time,
) ([]mcpCalendarOccurrence, error) {
	plain, err := deps.CalendarQueries.ListCalendarEventsAcrossCalendars(ctx, calendar.ListCalendarEventsAcrossCalendarsParams{
		ViewerUserID: viewerUserID,
		UserID:       membershipUserID,
		WorkspaceID:  workspaceID,
		RangeEnd:     sql.NullTime{Time: endTime, Valid: true},
		RangeStart:   sql.NullTime{Time: startTime, Valid: true},
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	if len(plain) > maxCalendarOccurrences {
		return nil, errCalendarWindowTooLarge()
	}
	out := make([]mcpCalendarOccurrence, 0, len(plain))
	for _, r := range plain {
		occ := mcpCalendarOccurrence{row: r}
		if r.StartAt.Valid && r.EndAt.Valid {
			occ.startAt, occ.endAt, occ.dated = r.StartAt.Time, r.EndAt.Time, true
		}
		out = append(out, occ)
	}

	series, err := deps.CalendarQueries.ListRecurringCalendarEventsAcrossCalendars(ctx, calendar.ListRecurringCalendarEventsAcrossCalendarsParams{
		ViewerUserID:  viewerUserID,
		UserID:        membershipUserID,
		WorkspaceID:   workspaceID,
		StartAt:       sql.NullTime{Time: endTime, Valid: true},
		RecurrenceEnd: sql.NullTime{Time: startTime, Valid: true},
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	overridden, err := overriddenOccurrenceStarts(ctx, deps, workspaceID, viewerUserID, series)
	if err != nil {
		return nil, err
	}

	for _, r := range series {
		if !r.StartAt.Valid || !r.EndAt.Valid {
			continue
		}
		rule, perr := recurrence.ParseRule(r.RecurrenceRule)
		if perr != nil || rule == nil {
			continue
		}
		var seriesEnd *time.Time
		if r.RecurrenceEnd.Valid {
			seriesEnd = &r.RecurrenceEnd.Time
		}
		for _, inst := range recurrence.Expand(recurrence.Event{
			StartAt:          r.StartAt.Time,
			EndAt:            r.EndAt.Time,
			Timezone:         r.Timezone,
			AllDay:           r.AllDay,
			Rule:             rule,
			Exceptions:       recurrence.ParseExceptions(r.RecurrenceExceptions),
			OverriddenStarts: overridden[r.ID],
			RecurrenceEnd:    seriesEnd,
		}, startTime, endTime) {
			out = append(out, mcpCalendarOccurrence{
				row:     recurringRowAsPlain(r),
				startAt: inst.StartAt,
				endAt:   inst.EndAt,
				dated:   true,
			})
			if len(out) > maxCalendarOccurrences {
				return nil, errCalendarWindowTooLarge()
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		switch {
		case out[i].dated && out[j].dated && !out[i].startAt.Equal(out[j].startAt):
			return out[i].startAt.Before(out[j].startAt)
		case out[i].dated != out[j].dated:
			return out[i].dated
		default:
			return out[i].row.PublicID.String() < out[j].row.PublicID.String()
		}
	})
	return out, nil
}

// overriddenOccurrenceStarts returns, per master id, the occurrence
// starts a live override row already stands in for, spelled as RFC 3339
// UTC so the expander's exception parser resolves them to the same
// instants it generates.
//
// One read for every series in the expansion. The masters are keyed by
// their internal id, which is why the recurring query projects ce.id and
// the id has to be taken from that row rather than from the narrowed one
// recurringRowAsPlain returns.
//
// The viewer is the user the surrounding listing answers for, so the
// subtraction matches what that user is served: an override they cannot
// see does not reach them from any other read either, and taking its
// original start away would remove the occurrence from their calendar
// while the replacement stayed hidden. Such an occurrence keeps showing
// at its original time instead.
func overriddenOccurrenceStarts(
	ctx context.Context,
	deps Deps,
	workspaceID, viewerUserID uint32,
	series []calendar.ListRecurringCalendarEventsAcrossCalendarsRow,
) (map[uint32][]string, error) {
	if len(series) == 0 {
		return nil, nil
	}
	parentIDs := make([]sql.NullInt32, 0, len(series))
	for _, r := range series {
		parentIDs = append(parentIDs, handlerutil.NullInt32From(r.ID))
	}

	rows, err := deps.CalendarQueries.ListCalendarEventOverriddenStarts(ctx, calendar.ListCalendarEventOverriddenStartsParams{
		WorkspaceID:  workspaceID,
		ParentIds:    parentIDs,
		ViewerUserID: viewerUserID,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
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

// recurringRowAsPlain narrows a recurring row to the shape the callers
// project. sqlc names the two row types separately, so the copy is
// mechanical; the recurring row additionally carries the internal id the
// override grouping keys on, and the plain shape has no field for it, so
// anything needing that id must read it before the narrowing.
func recurringRowAsPlain(r calendar.ListRecurringCalendarEventsAcrossCalendarsRow) calendar.ListCalendarEventsAcrossCalendarsRow {
	return calendar.ListCalendarEventsAcrossCalendarsRow{
		PublicID:                  r.PublicID,
		CalendarID:                r.CalendarID,
		CalendarPublicID:          r.CalendarPublicID,
		Kind:                      r.Kind,
		Visibility:                r.Visibility,
		ShowAs:                    r.ShowAs,
		Flexibility:               r.Flexibility,
		Title:                     r.Title,
		AllDay:                    r.AllDay,
		StartAt:                   r.StartAt,
		EndAt:                     r.EndAt,
		Timezone:                  r.Timezone,
		Location:                  r.Location,
		OwnerUserID:               r.OwnerUserID,
		BlockLabel:                r.BlockLabel,
		TaskID:                    r.TaskID,
		UpdatedAt:                 r.UpdatedAt,
		CreatedAt:                 r.CreatedAt,
		IsAttendee:                r.IsAttendee,
		CalendarDefaultVisibility: r.CalendarDefaultVisibility,
	}
}
func runListCalendarEvents(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		StartDate string `json:"startDate"`
		EndDate   string `json:"endDate"`
		StartAt   *int64 `json:"startAt"`
		EndAt     *int64 `json:"endAt"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var startTime, endTime time.Time
	switch {
	case in.StartAt != nil:
		startTime = time.Unix(*in.StartAt, 0).UTC()
	case in.StartDate != "":
		t, err := parseDayBoundary(in.StartDate)
		if err != nil {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid startDate: %v", err)
		}
		startTime = t
	default:
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	switch {
	case in.EndAt != nil:
		endTime = time.Unix(*in.EndAt, 0).UTC()
	case in.EndDate != "":
		t, err := parseDayBoundary(in.EndDate)
		if err != nil {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid endDate: %v", err)
		}
		endTime = t
	default:
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if !endTime.After(startTime) {
		return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "the range end must be after its start")
	}
	if endTime.Sub(startTime) > maxCalendarListSpan {
		return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "the range may not span more than %d days", int(maxCalendarListSpan/(24*time.Hour)))
	}
	occurrences, err := listCalendarOccurrences(ctx, deps, s.userID, s.userID, s.workspaceID, startTime, endTime)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(occurrences))
	for _, occ := range occurrences {
		r := occ.row
		item := map[string]any{
			"id":          r.PublicID.String(),
			"calendarId":  r.CalendarPublicID.String(),
			"kind":        string(r.Kind),
			"showAs":      string(r.ShowAs),
			"flexibility": string(r.Flexibility),
			"title":       r.Title,
			"allDay":      r.AllDay,
		}
		// Each occurrence carries its own times. A series is reported as
		// the meetings it produces in the window rather than as one row
		// plus a rule the caller would have to expand, because the
		// caller here is a model answering "what is on Tuesday".
		if occ.dated {
			if r.AllDay {
				item["startDate"] = occ.startAt.UTC().Format("2006-01-02")
				// endDate is the last day, so a value read here can be
				// written back to update_calendar_event unchanged.
				item["endDate"] = calendarrules.AllDayLastDay(string(r.Kind), occ.endAt).UTC().Format("2006-01-02")
			} else {
				item["startAt"] = occ.startAt.Unix()
				item["endAt"] = occ.endAt.Unix()
			}
		}
		items = append(items, item)
	}
	return map[string]any{"events": items}, nil
}

// resolveUserTimezone returns the zone to interpret a user's day in:
// their own preference, else the workspace default, else UTC — the chain
// [duedate.UserZone] resolves for every surface. No MCP tool takes a
// timezone argument, so there is no explicit-request tier above it.
func resolveUserTimezone(ctx context.Context, deps Deps, workspaceID, userID uint32) region.Zone {
	return duedate.UserZone(ctx, deps.Queries, workspaceID, userID)
}
func runListFreeSlots(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		UserID          string `json:"userId"`
		Date            string `json:"date"`
		DurationMinutes int    `json:"durationMinutes"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Date == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}

	targetUserID := s.userID
	if in.UserID != "" {
		// Scope the lookup to workspace membership so the free/busy
		// probe cannot act as an existence oracle for users outside
		// the workspace.
		resolved, uerr := resolveWorkspaceUser(ctx, deps, s, in.UserID, apierrors.WsMemberNotFound)
		if uerr != nil {
			return nil, uerr
		}
		targetUserID = resolved
	}

	day, err := region.ParseDay(in.Date)
	if err != nil {
		return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid date: %v", err)
	}
	if in.DurationMinutes <= 0 {
		in.DurationMinutes = 60
	}

	// The working day belongs to the person whose day it is, so the
	// window is built in their timezone rather than in UTC. Fixed at UTC
	// it named 18:00–03:00 for a Tokyo user: the real working day fell
	// outside the query window, so every meeting in it was invisible,
	// the day was reported wholly free, and the agent booked the middle
	// of the night.
	zone := resolveUserTimezone(ctx, deps, s.workspaceID, targetUserID)
	workStart := day.At(zone, workDayStartHour, 0, 0)
	workEnd := day.At(zone, workDayEndHour, 0, 0)

	// The viewer here is the target, not the caller. This tool returns
	// free windows and never any event data, so the question it has to
	// answer is which of the target's own commitments occupy the day —
	// filtering by the caller's rights would drop the target's
	// confidential events and report an occupied slot as free. What the
	// caller learns is availability, which is what show_as governs: an
	// owner who does not want a block to read as taken marks it free.
	occurrences, err := listCalendarOccurrences(ctx, deps, targetUserID, targetUserID, s.workspaceID, workStart, workEnd)
	if err != nil {
		return nil, err
	}

	// Collect busy intervals, clamped to working hours.
	type interval struct{ start, end time.Time }
	busy := make([]interval, 0, len(occurrences))
	for _, occ := range occurrences {
		r := occ.row
		if r.AllDay {
			continue
		}
		if string(r.ShowAs) == "free" {
			continue
		}
		// Undated events (planning-stage placeholders) occupy no time,
		// so they contribute nothing to the busy map.
		if !occ.dated {
			continue
		}
		s := occ.startAt
		e := occ.endAt
		if s.Before(workStart) {
			s = workStart
		}
		if e.After(workEnd) {
			e = workEnd
		}
		if s.Before(e) {
			busy = append(busy, interval{s, e})
		}
	}
	sort.Slice(busy, func(i, j int) bool { return busy[i].start.Before(busy[j].start) })

	// Find gaps.
	minDur := time.Duration(in.DurationMinutes) * time.Minute
	slots := []map[string]any{}
	cursor := workStart
	for _, b := range busy {
		if b.start.After(cursor) {
			gap := b.start.Sub(cursor)
			if gap >= minDur {
				slots = append(slots, map[string]any{
					"startAt":         cursor.Unix(),
					"endAt":           b.start.Unix(),
					"durationMinutes": int(gap.Minutes()),
				})
			}
		}
		if b.end.After(cursor) {
			cursor = b.end
		}
	}
	if cursor.Before(workEnd) {
		gap := workEnd.Sub(cursor)
		if gap >= minDur {
			slots = append(slots, map[string]any{
				"startAt":         cursor.Unix(),
				"endAt":           workEnd.Unix(),
				"durationMinutes": int(gap.Minutes()),
			})
		}
	}
	return map[string]any{"slots": slots}, nil
}
func runListCalendarMemos(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		CalendarID string `json:"calendarId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.CalendarID == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	calID, err := resolveCalendar(ctx, deps, s, in.CalendarID)
	if err != nil {
		return nil, err
	}
	rows, err := deps.CalendarQueries.ListCalendarMemos(ctx, calendar.ListCalendarMemosParams{
		CalendarID:  calID,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, map[string]any{
			"id":        r.PublicID.String(),
			"title":     r.Title,
			"done":      r.Done,
			"createdBy": r.DisplayName,
		})
	}
	return map[string]any{"memos": items}, nil
}
