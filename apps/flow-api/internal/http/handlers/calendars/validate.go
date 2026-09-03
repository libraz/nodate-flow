package calendars

import (
	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// The rules these wrappers apply live in
// [github.com/libraz/nodate-flow/apps/flow-api/internal/calendarrules],
// because the MCP tools write the same rows and have to answer the same
// way. What is left here is the translation: the shared package returns
// the refusal a caller is owed, and an HTTP handler owes it as RFC 9457.

// requireValidTimezone rejects a client-supplied IANA timezone that does
// not resolve, naming the JSON member it arrived in.
func requireValidTimezone(field, tz string) error {
	if err := calendarrules.RequireValidTimezone(field, tz); err != nil {
		return handlerutil.HTTPErrFromAPIError(err)
	}
	return nil
}

// requireEventChronology rejects an event whose end precedes its start.
func requireEventChronology(startAt, endAt *int64) error {
	if err := calendarrules.RequireEventChronology(startAt, endAt); err != nil {
		return handlerutil.HTTPErrFromAPIError(err)
	}
	return nil
}

// requireEventStartEndPair rejects a window with one end missing.
func requireEventStartEndPair(startAt, endAt *int64) error {
	if err := calendarrules.RequireEventStartEndPair(startAt, endAt); err != nil {
		return handlerutil.HTTPErrFromAPIError(err)
	}
	return nil
}
