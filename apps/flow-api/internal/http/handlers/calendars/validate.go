package calendars

import (
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// requireValidTimezone is the single check a client-supplied IANA
// timezone passes before it can reach a column, on any calendar surface:
// events, public shares, and anything added later.
//
// region.ValidateTimezone already existed and was already called from
// resolveEffectiveTimezone, so the defect was never a missing rule — it
// was that each write path decided for itself whether to apply it. Event
// create and patch did not, and stored "JST" or "GMT+9" verbatim: the
// row came back from GET intact while every client that places it on a
// grid resolved the zone, got nothing, and drew no pill. The share
// endpoints did call it but reported the refusal as a storage failure,
// so a caller who sent a bad name was told the server broke.
//
// Routing every surface through one function makes both of those one
// decision rather than four: the answer is a 422 naming the field, which
// is what auth-api already answers for the same input on user and
// workspace profiles.
//
// field is the JSON path of the offending member ("timezone" on a flat
// body), forwarded as an RFC 9457 extension so a caller sending several
// fields learns which one was rejected.
func requireValidTimezone(field, tz string) error {
	if err := region.ValidateTimezone(tz); err != nil {
		return handlerutil.HTTPErrFromAPIError(
			apierr.New(apierrors.ValidationBodyFieldInvalid).WithDetail("field", field))
	}
	return nil
}

// requireEventChronology rejects an event whose end precedes its start.
//
// The ordering is a database CHECK (chk_calendar_events_chronology), so
// the row could never have been written either way — but reaching the
// constraint means the driver reports a violation the handler cannot
// attribute, and the caller gets a 500 that says the event could not be
// saved. That is the same "correct outcome, useless explanation" the
// timezone paths had. Checking here answers 422 and names the field.
//
// A nil pair is an undated planning-stage event and a half pair is
// caught earlier by the start/end pair invariant, so both are left
// alone here. Equal instants are accepted: a milestone is a zero-length
// event, which is exactly how the calendar UI writes one.
//
// The values are the raw unix seconds off the request, checked before
// all-day normalisation. Truncating to UTC midnight is monotonic, so a
// pair that passes here still passes after normalisation, and checking
// first means an all-day request with the days inverted is refused for
// the reason it is wrong rather than silently collapsing to one day.
func requireEventChronology(startAt, endAt *int64) error {
	if startAt == nil || endAt == nil {
		return nil
	}
	if *endAt < *startAt {
		return httpErr(apierrors.CalendarEventEndBeforeStart)
	}
	return nil
}
