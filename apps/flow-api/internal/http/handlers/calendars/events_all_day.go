package calendars

import (
	"context"
	"database/sql"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendaroccurrence"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// allDayDateLayout is the wire form of an all-day date.
const allDayDateLayout = "2006-01-02"

// minAllDayDate is the first day an all-day date may name; the last is
// 9999-12-31, the end of what time.Parse reads.
var minAllDayDate = time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC)

// parseAllDayDate reads one startDate / endDate member as that day's UTC
// midnight, in unix seconds.
func parseAllDayDate(field string, raw *string) (*int64, error) {
	if raw == nil {
		return nil, nil
	}
	day, err := time.Parse(allDayDateLayout, *raw)
	if err != nil || day.Before(minAllDayDate) {
		return nil, invalidBodyField(field)
	}
	unix := day.Unix()
	return &unix, nil
}

// resolveAllDayDates turns the dates of an all-day write into the stored
// window, and refuses a window that is not the canonical shape. A member
// that is not sent comes from the stored row when fallback is valid;
// otherwise both must be sent. An end equal to the start is a milestone's
// and anything else not after it is DateRangeInvalid.
func resolveAllDayDates(kind string, startDate, endDate *string, fallbackStart, fallbackEnd sql.NullTime) (start, end *int64, err error) {
	if start, err = parseAllDayDate("startDate", startDate); err != nil {
		return nil, nil, err
	}
	if end, err = parseAllDayDate("endDate", endDate); err != nil {
		return nil, nil, err
	}
	if start == nil {
		if !fallbackStart.Valid {
			return nil, nil, invalidBodyField("startDate")
		}
		unix := fallbackStart.Time.Unix()
		start = &unix
	}
	if end == nil {
		if !fallbackEnd.Valid {
			return nil, nil, invalidBodyField("endDate")
		}
		unix := fallbackEnd.Time.Unix()
		end = &unix
	}
	window := func(unix int64) sql.NullTime {
		return sql.NullTime{Time: handlerutil.UnixToTime(unix), Valid: true}
	}
	if refusal := calendarrules.RequireAllDayWindow(true, kind, window(*start), window(*end)); refusal != nil {
		return nil, nil, handlerutil.HTTPErrFromAPIError(refusal)
	}
	return start, end, nil
}

// truncateToUTCDay returns midnight UTC on the calendar day t falls on
// in UTC.
func truncateToUTCDay(t time.Time) time.Time {
	return calendarrules.TruncateToUTCDay(t)
}

// resolvePatchAllDay applies the all-day rules to a PATCH body: an
// all-day row is named by dates and a timed one by instants, switching
// the form needs the new form's whole window, and a series that names
// occurrences cannot switch at all. The dates it accepts are written back
// to the body as the stored window, so everything after it sees one shape.
// wholeSeries is false when the patch reaches a single occurrence or a
// split, which have no stored window to fall back on for a missing end.
func resolvePatchAllDay(
	ctx context.Context,
	deps Deps,
	wsID uint32,
	evt calendar.FindCalendarEventByPublicIdRow,
	input *PatchEventInput,
	wholeSeries bool,
) error {
	body := &input.Body
	allDay := evt.AllDay
	toggled := body.AllDay != nil && *body.AllDay != evt.AllDay
	if toggled {
		allDay = *body.AllDay
		if calendaroccurrence.HasRule(evt.RecurrenceRule) {
			named, err := seriesNamesOccurrences(ctx, deps.CalendarQueries, wsID, evt)
			if err != nil {
				return httpErr(apierrors.CalendarEventStoreReadInterrupted)
			}
			if named {
				return httpErr(apierrors.CalendarEventAllDayToggleWithExceptions)
			}
		}
	}

	instants := body.StartAt != nil || body.EndAt != nil
	dates := body.StartDate != nil || body.EndDate != nil
	if refusal := calendarrules.RequireAllDayInput(allDay, instants, dates); refusal != nil {
		return handlerutil.HTTPErrFromAPIError(refusal)
	}
	newForm := instants
	if allDay {
		newForm = dates
	}
	if toggled && evt.StartAt.Valid && !newForm {
		return httpErr(apierrors.CalendarEventAllDayShape)
	}

	kind := string(evt.Kind)
	if body.Kind != nil {
		kind = *body.Kind
	}
	switch {
	case allDay && dates:
		var storedStart, storedEnd sql.NullTime
		if wholeSeries && !toggled {
			storedStart, storedEnd = evt.StartAt, evt.EndAt
		}
		start, end, err := resolveAllDayDates(kind, body.StartDate, body.EndDate, storedStart, storedEnd)
		if err != nil {
			return err
		}
		body.StartAt, body.EndAt = start, end
	case allDay && !toggled && body.Kind != nil && wholeSeries && evt.StartAt.Valid:
		// A kind change alone can turn the stored window into a wrong one,
		// a milestone being a point.
		if refusal := calendarrules.RequireAllDayWindow(true, kind, evt.StartAt, evt.EndAt); refusal != nil {
			return handlerutil.HTTPErrFromAPIError(refusal)
		}
	}
	return nil
}
