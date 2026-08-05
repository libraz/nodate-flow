package mcp

import (
	"strings"
	"testing"
)

// The calendar tools sit in writeToolsWithoutProjectGate, exempt from the
// project-role floor because a calendar is not a project. Their exemption
// entries say what gates them instead — "gated by the calendar_members
// grant", "plus canEditCalendarEvent" — and until now nothing checked
// that sentence. An exemption whose justification is prose is a ledger
// entry: it records that somebody decided, not that anything enforces it.
//
// The checks below turn those sentences into reachability facts, using
// the same call graph TestMCPWriteToolsPassProjectRoleGate walks.

// calendarMembershipGates are the resolvers that require a
// calendar_members row. A tool that touches a calendar must reach one.
var calendarMembershipGates = map[string]bool{
	// Two shapes, one requirement. The tools that take a calendar id
	// resolve it and get membership checked on the way; the tools that
	// take an event id resolve the event first and check membership on
	// the calendar it turns out to live on.
	"resolveCalendar":           true,
	"requireCalendarMembership": true,
}

// calendarEventEditGates are the resolvers that apply the shared
// event-edit rule. A tool that changes or removes somebody's event must
// reach one; creating an event on a calendar the actor may write to does
// not, which is why create_calendar_event is not listed below.
var calendarEventEditGates = map[string]bool{
	"canEditCalendarEvent": true,
}

// calendarWriteTools are the write-scoped tools whose target is a
// calendar, mapped to whether they change an existing event (as opposed
// to creating one or touching a memo).
var calendarWriteTools = map[string]bool{
	"create_calendar_event": false,
	"update_calendar_event": true,
	"delete_calendar_event": true,
	"toggle_calendar_memo":  false,
}

// TestMCPCalendarWriteToolsReachTheirGates proves the calendar tools
// reach the gates their exemption entries claim.
func TestMCPCalendarWriteToolsReachTheirGates(t *testing.T) {
	t.Parallel()

	h := NewHandler(Deps{})
	graph := mcpPackageCallGraph(t)

	seen := map[string]bool{}
	for name, editsExisting := range calendarWriteTools {
		tl, ok := h.tools[name]
		if !ok {
			t.Errorf("calendarWriteTools lists %q, which is not a registered tool; drop the stale entry", name)
			continue
		}
		seen[name] = true
		entry := mcpRunFuncName(t, name, tl.run)

		if !reachesAny(graph, entry, calendarMembershipGates) {
			t.Errorf("calendar tool %q (%s) never reaches %s; a calendar is reachable only through calendar_members, and workspace membership is not a substitute",
				name, entry, strings.Join(sortedKeys(calendarMembershipGates), " / "))
		}
		if editsExisting && !reachesAny(graph, entry, calendarEventEditGates) {
			t.Errorf("calendar tool %q (%s) changes an existing event but never reaches %s; without it the tool decides for itself who may move somebody else's event",
				name, entry, strings.Join(sortedKeys(calendarEventEditGates), " / "))
		}
	}

	// Every calendar exemption in the write-gate allowlist has to be
	// covered here, so a new calendar tool cannot be exempted from the
	// project floor and then checked by nothing at all.
	for name, reason := range writeToolsWithoutProjectGate {
		if !strings.Contains(reason, "calendar") {
			continue
		}
		if !seen[name] {
			t.Errorf("write tool %q is exempted from the project-role floor on calendar grounds (%q) but is not covered by calendarWriteTools; add it so the exemption is checked rather than asserted",
				name, reason)
		}
	}
}

// TestMCPCalendarEventEditRuleIsShared proves the edit decision is the
// one REST uses rather than a second opinion.
//
// It used to be a second opinion, keyed on calendars.owner_user_id. A
// shared calendar leaves that NULL by design, so no manager qualified on
// exactly the calendars managers exist for: the same edit worked in the
// web app and failed through an agent. The rule now lives in eventacl
// and both transports call it, so the way to reintroduce the divergence
// is to stop calling it — which is what this checks.
func TestMCPCalendarEventEditRuleIsShared(t *testing.T) {
	t.Parallel()

	src := readMCPSource(t, "acl.go")
	if !strings.Contains(src, "eventacl.CanEdit") {
		t.Fatal("canEditCalendarEvent must decide through eventacl.CanEdit so MCP and REST answer alike")
	}
	if strings.Contains(src, "SELECT owner_user_id FROM calendars") {
		t.Error("the calendar-owner lookup is back in the edit decision; it is NULL on every shared calendar, which is where managers exist")
	}
}

// TestMCPFreeSlotsResolvesTimezone pins the working-day window to the
// user whose day it is.
//
// Two constants in UTC read as a working day everywhere and are one
// everywhere the offset is zero. For a Tokyo user the window named
// 18:00–03:00: the real day fell outside it, so the meetings in it were
// invisible, the day was reported free, and the agent booked the night.
func TestMCPFreeSlotsResolvesTimezone(t *testing.T) {
	t.Parallel()

	h := NewHandler(Deps{})
	tl, ok := h.tools["list_free_slots"]
	if !ok {
		t.Fatal("list_free_slots is not registered")
	}
	graph := mcpPackageCallGraph(t)
	entry := mcpRunFuncName(t, "list_free_slots", tl.run)

	if !reachesAny(graph, entry, map[string]bool{"resolveUserTimezone": true}) {
		t.Error("list_free_slots must reach resolveUserTimezone; a working day built in UTC is a working day only at offset zero")
	}

	src := readMCPSource(t, "tools.go")
	if strings.Contains(src, "9, 0, 0, 0, time.UTC)") {
		t.Error("the working-day window is being constructed in UTC again")
	}
}
