// Package calendarrules holds the input preconditions a calendar_events
// write has to pass, in the one place every transport that performs one
// goes through: the REST handlers under internal/http/handlers/calendars
// and the calendar tools in internal/mcp.
//
// The rules themselves were never in doubt — the database states them as
// CHECK constraints, and the error catalogue describes the refusals a
// caller is owed. What was in doubt is who applies them. Each transport
// decided for itself, so a rule the REST handlers gained did not reach
// the tools, and the difference was invisible: the constraint still held,
// so nothing was corrupted, but an agent sending an inverted window got a
// driver error the handler could not attribute and a 500 that says the
// event could not be saved, where the browser sending the same window got
// a 422 naming the field.
//
// So the functions here return the refusal, and the transports translate
// it. That is the whole boundary: [apierrors.APIError] is what both sides
// already carry — MCP returns it as it stands, and the REST handlers
// render it as RFC 9457 through handlerutil — so a rule stated once has
// one answer on both surfaces rather than two that happen to agree.
//
// The times are unix seconds because that is the shape of the API
// boundary (see docs/conventions/api-types.md): *_at fields cross as
// int64 unixtime, and checking them in that form means the check runs on
// what the caller actually sent. [UnixSeconds] converts for a caller that
// has already resolved an instant.
//
// Reaching these functions is checked rather than asked for: every
// operation and every tool that writes a calendar_events row's start_at /
// end_at has to reach the chronology rule, derived from the committed SQL
// in apps/flow-api/tests/precondition.
package calendarrules

import (
	"database/sql"
	"time"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// RequireEventStartEndPair rejects a window with one end missing.
//
// The pair is what chk_calendar_events_start_end_pair enforces: an event
// is dated or it is not, and a start without an end is neither. Both
// absent is a planning-stage event and is allowed.
func RequireEventStartEndPair(startAt, endAt *int64) *apierrors.APIError {
	if (startAt == nil) != (endAt == nil) {
		return apierrors.New(apierrors.CalendarEventStartEndPairRequired)
	}
	return nil
}

// RequireEventChronology rejects an event whose end precedes its start.
//
// The ordering is a database CHECK (chk_calendar_events_chronology), so
// the row could never have been written either way — but reaching the
// constraint means the driver reports a violation the caller's transport
// cannot attribute, and the answer becomes "the event could not be
// saved". Checking here answers 422 and says which way round the window
// has to be.
//
// A nil pair is an undated planning-stage event and a half pair is caught
// by [RequireEventStartEndPair], so both are left alone here. Equal
// instants are accepted: a milestone is a zero-length event, which is
// exactly how the calendar UI writes one.
//
// The values are the window as the caller sent it, checked before all-day
// normalisation. Truncating to UTC midnight is monotonic, so a pair that
// passes here still passes after normalisation, and checking first means
// an all-day request with the days inverted is refused for the reason it
// is wrong rather than silently collapsing to one day.
func RequireEventChronology(startAt, endAt *int64) *apierrors.APIError {
	if startAt == nil || endAt == nil {
		return nil
	}
	if *endAt < *startAt {
		return apierrors.New(apierrors.CalendarEventEndBeforeStart)
	}
	return nil
}

// RequireValidTimezone is the single check a client-supplied IANA
// timezone passes before it can reach a column, on any calendar surface:
// events, public shares, and anything added later.
//
// region.ValidateTimezone holds the rule itself; what this adds is that
// no write path decides for itself whether to apply it, or how to report
// it. A path that skips it stores "JST" or "GMT+9" verbatim, and the row
// reads back from GET intact while every client that places it on a grid
// resolves the zone, gets nothing, and draws no pill. A path that calls
// it but reports the refusal as a storage failure tells a caller who sent
// a bad name that the server broke.
//
// field is the JSON path of the offending member ("timezone" on a flat
// body), forwarded as an RFC 9457 extension so a caller sending several
// fields learns which one was rejected.
func RequireValidTimezone(field, tz string) *apierrors.APIError {
	if err := region.ValidateTimezone(tz); err != nil {
		return apierrors.New(apierrors.ValidationBodyFieldInvalid).WithDetail("field", field)
	}
	return nil
}

// NormalizeAllDayBounds pins an all-day event's stored instants to UTC
// midnight.
//
// "All day on 5 August" is a date, not an interval on the world clock: it
// means the same square on the calendar for everyone. The column pair is
// DATETIME, so the date has to be encoded as an instant, and which
// instant it is has to be one thing — otherwise the same row reads as a
// different day depending on who wrote it and who is looking.
//
// Left to the writer it is two things: a browser dialog sends local
// midnight and a tool sends UTC midnight, so a Tokyo user's company
// holiday arrives as 2026-08-04T15:00Z, every reader bucketing by local
// date shows it on 4 August in Europe, and an agent reports startDate
// 2026-08-04 for a day the creator called the 5th.
//
// Normalising on the way in makes the row canonical whichever client
// wrote it, so a reader that takes the UTC date parts gets the date the
// author meant. Clients still have to read it that way — a reader
// bucketing all-day rows by local date undoes this — but the stored value
// is not the thing that disagrees.
func NormalizeAllDayBounds(allDay bool, start, end sql.NullTime) (sql.NullTime, sql.NullTime) {
	if !allDay {
		return start, end
	}
	if start.Valid {
		start.Time = TruncateToUTCDay(start.Time)
	}
	if end.Valid {
		end.Time = TruncateToUTCDay(end.Time)
	}
	return start, end
}

// TruncateToUTCDay returns midnight UTC on the calendar day t falls on in
// UTC.
//
// The zone is stated rather than assumed. An all-day row's stored instant
// is defined as midnight UTC on the author's date — that is the canonical
// form the readers depend on — so UTC here is the answer, not the absence
// of one, and [region.UTC] is how the two are told apart at a glance.
func TruncateToUTCDay(t time.Time) time.Time {
	return region.DayOf(t, region.UTC()).DateColumn()
}

// UnixSeconds renders an instant as the unix-seconds pointer the rules
// above take, for a caller that resolved the window before checking it.
//
// A zero instant answers nil rather than year one: it is what a caller
// holds when neither the request nor the stored row supplied that end of
// the window, and treating it as a real time would compare a missing
// value against a present one and refuse the wrong request.
func UnixSeconds(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	secs := t.Unix()
	return &secs
}
