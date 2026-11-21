package mcp_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// storedWindow reads an event's window as stored.
func storedWindow(t *testing.T, db *sql.DB, eventID string) (time.Time, time.Time) {
	t.Helper()
	id, err := uuid.Parse(eventID)
	require.NoError(t, err)
	var start, end time.Time
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT start_at, end_at FROM calendar_events WHERE public_id = ?`, id[:]).Scan(&start, &end))
	return start.UTC(), end.UTC()
}

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// An all-day event is named by its first and last day. It is stored as
// [first day, day after the last), so a one-day event spans 24 hours, and
// reads back with the last day it was given.
func TestMCPAllDayEventStoresTheMidnightAfterItsLastDay(t *testing.T) {
	requireMCPIntegration(t)
	db := helpers.StartShared(t).DB
	deps := occurrenceScopeDeps(db)
	fx := seedRecurringSeriesFixture(t, db)
	calPub := calendarPublicID(t, db, fx.calendarID)
	ctx := context.Background()

	create := func(args map[string]any) map[string]any {
		args["calendarId"] = calPub
		args["title"] = "Holiday"
		args["allDay"] = true
		out, err := mcp.RunCreateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, args))
		require.NoError(t, err)
		return out.(map[string]any)
	}

	one := create(map[string]any{"startDate": "2030-06-10", "endDate": "2030-06-10"})
	start, end := storedWindow(t, db, one["id"].(string))
	require.Equal(t, day(2030, 6, 10), start)
	require.Equal(t, day(2030, 6, 11), end, "a one-day all-day event ends at the next midnight")
	require.Equal(t, "2030-06-10", one["endDate"], "the answer carries the last day, as it was given")

	multi := create(map[string]any{"startDate": "2030-06-10", "endDate": "2030-06-12"})
	_, end = storedWindow(t, db, multi["id"].(string))
	require.Equal(t, day(2030, 6, 13), end)

	point := create(map[string]any{"startDate": "2030-06-10", "endDate": "2030-06-10", "kind": "milestone"})
	start, end = storedWindow(t, db, point["id"].(string))
	require.Equal(t, start, end, "a milestone is a point")
	require.Equal(t, "2030-06-10", point["endDate"])

	_, err := mcp.RunCreateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
		"calendarId": calPub, "title": "Backwards", "allDay": true,
		"startDate": "2030-06-10", "endDate": "2030-06-09",
	}))
	requireSpec(t, err, apierrors.CalendarEventDateRangeInvalid)

	// Reversed and empty ranges answer with the one code.
	_, err = mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
		"eventId": one["id"], "startDate": "2030-06-11",
	}))
	requireSpec(t, err, apierrors.CalendarEventDateRangeInvalid)
	_, err = mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
		"eventId": one["id"], "startDate": "2030-06-12",
	}))
	requireSpec(t, err, apierrors.CalendarEventDateRangeInvalid)
}

// An all-day event takes dates and a timed one takes instants; mixing them
// is refused, on create and on update.
func TestMCPAllDayRefusesInstantsAndTimedRefusesDates(t *testing.T) {
	requireMCPIntegration(t)
	db := helpers.StartShared(t).DB
	deps := occurrenceScopeDeps(db)
	fx := seedRecurringSeriesFixture(t, db)
	calPub := calendarPublicID(t, db, fx.calendarID)
	ctx := context.Background()
	at := day(2030, 6, 10)

	_, err := mcp.RunCreateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
		"calendarId": calPub, "title": "Holiday", "allDay": true,
		"startAt": at.Unix(), "endAt": at.Add(time.Hour).Unix(),
	}))
	requireSpec(t, err, apierrors.CalendarEventAllDayShape)

	_, err = mcp.RunCreateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
		"calendarId": calPub, "title": "Meeting",
		"startDate": "2030-06-10", "endDate": "2030-06-10",
	}))
	requireSpec(t, err, apierrors.CalendarEventAllDayShape)

	out, err := mcp.RunCreateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
		"calendarId": calPub, "title": "Holiday", "allDay": true,
		"startDate": "2030-06-10", "endDate": "2030-06-10",
	}))
	require.NoError(t, err)
	id := out.(map[string]any)["id"].(string)
	_, err = mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
		"eventId": id, "startAt": at.Unix(),
	}))
	requireSpec(t, err, apierrors.CalendarEventAllDayShape)

	// The timed one-off the fixture seeds refuses dates the same way.
	_, err = mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
		"eventId": fx.oneOffPub.String(), "startDate": "2030-06-10",
	}))
	requireSpec(t, err, apierrors.CalendarEventAllDayShape)
}

// What list_calendar_events returns for an all-day event can be handed
// straight back to update_calendar_event without stretching the event.
func TestMCPListThenUpdateWithTheReturnedDatesLeavesTheRowUnchanged(t *testing.T) {
	requireMCPIntegration(t)
	db := helpers.StartShared(t).DB
	deps := occurrenceScopeDeps(db)
	fx := seedRecurringSeriesFixture(t, db)
	calPub := calendarPublicID(t, db, fx.calendarID)
	ctx := context.Background()

	for _, kind := range []string{"event", "milestone"} {
		out, err := mcp.RunCreateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"calendarId": calPub, "title": "Trip " + kind, "allDay": true, "kind": kind,
			"startDate": "2031-02-03", "endDate": "2031-02-03",
		}))
		require.NoError(t, err)
		id := out.(map[string]any)["id"].(string)
		wantStart, wantEnd := storedWindow(t, db, id)

		listed, err := mcp.RunListCalendarEvents(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"startDate": "2031-02-01", "endDate": "2031-02-28",
		}))
		require.NoError(t, err)
		var item map[string]any
		for _, e := range listed.(map[string]any)["events"].([]map[string]any) {
			if e["id"] == id {
				item = e
			}
		}
		require.NotNil(t, item, "%s: the event is listed", kind)

		_, err = mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":   id,
			"startDate": item["startDate"],
			"endDate":   item["endDate"],
		}))
		require.NoError(t, err)

		gotStart, gotEnd := storedWindow(t, db, id)
		require.Equal(t, wantStart, gotStart, kind)
		require.Equal(t, wantEnd, gotEnd, "%s: writing back what was read must not move the end", kind)
	}
}

// Cancelling one occurrence of an all-day series records the date, the
// spelling the series is read in.
func TestMCPCancelOfAnAllDayOccurrenceWritesADate(t *testing.T) {
	requireMCPIntegration(t)
	db := helpers.StartShared(t).DB
	deps := occurrenceScopeDeps(db)
	fx := seedRecurringSeriesFixture(t, db)
	ctx := context.Background()

	pub := uuid.Must(uuid.NewV7())
	first := day(2030, 3, 4)
	_, err := db.ExecContext(ctx,
		`INSERT INTO calendar_events
		   (public_id, workspace_id, calendar_id, title, all_day, start_at, end_at, timezone,
		    owner_user_id, created_by_user_id, recurrence_rule)
		 VALUES (?, ?, ?, 'Daily holiday', TRUE, ?, ?, 'America/Los_Angeles', ?, ?, CAST(? AS JSON))`,
		pub[:], fx.wsID, fx.calendarID, first, first.AddDate(0, 0, 1), fx.userID, fx.userID, seriesRule)
	require.NoError(t, err)

	_, err = mcp.RunDeleteCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
		"eventId":         pub.String(),
		"scope":           "occurrence",
		"occurrenceStart": day(2030, 3, 6).Unix(),
	}))
	require.NoError(t, err)

	var exceptions string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT CAST(recurrence_exceptions AS CHAR) FROM calendar_events WHERE public_id = ?`, pub[:]).
		Scan(&exceptions))
	require.JSONEq(t, `["2030-03-06"]`, exceptions)
}

func calendarPublicID(t *testing.T, db *sql.DB, calendarID uint32) string {
	t.Helper()
	var raw []byte
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT public_id FROM calendars WHERE id = ?`, calendarID).Scan(&raw))
	id, err := uuid.FromBytes(raw)
	require.NoError(t, err)
	return id.String()
}
