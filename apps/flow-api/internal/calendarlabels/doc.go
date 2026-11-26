// Package calendarlabels is the one place a calendar event's colour and
// label link are decided, for every transport that reads or writes them: the
// REST handlers under internal/http/handlers/calendars, the calendar tools in
// internal/mcp, and internal/calendaroccurrence.
//
// Refusals come back as [apierrors.APIError], as in calendarrules; each
// transport translates them. The package imports only the calendar query
// layer, the error catalogue and the shared id type, so calendaroccurrence
// can depend on it and never the other way round.
package calendarlabels
