package mcp

// Test-only entry point for the calendar list, whose reported role is
// pinned by a database-backed test in this package.
//
// It goes through the registry for the same reason the entries alongside
// it do: registration is where the declared floor is bound to the call, so
// an entry point that reached the implementation directly would run the
// tool with no floor at all and prove nothing about what the transport
// enforces.
var RunListCalendars = toolEntry("list_calendars")
