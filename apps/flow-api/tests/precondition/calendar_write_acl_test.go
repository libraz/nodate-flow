package precondition

import (
	"strings"
	"testing"
)

// TestMCPCalendarWritesReachTheWriteRule holds every MCP tool that writes
// a calendar's contents to the rule the REST handlers apply to the same
// write.
//
// The failure it is written against is a permission difference, not a
// message difference. REST refuses a calendar write from a member below
// editor, and refuses one to a system calendar at any role; the MCP tools
// stopped at "does a calendar_members row exist". A viewer — a member
// invited to read a calendar and nothing more — could create events on it
// through an agent, and so could anyone on a system calendar whose rows
// come from a provider feed and are overwritten by the next refresh.
func TestMCPCalendarWritesReachTheWriteRule(t *testing.T) {
	t.Parallel()

	src, statements := load(t)
	if !src.Declares(CalendarWriteDecisionSymbol) {
		t.Fatalf("%s is not declared under internal/; the rule this check holds tools to does not exist",
			CalendarWriteDecisionSymbol)
	}
	if !src.Declares(CalendarWriteGateSymbol) {
		t.Fatalf("%s is not declared under internal/; without it no sink can be identified as holding a calendar's contents",
			CalendarWriteGateSymbol)
	}

	candidates := CalendarWriteSinks(src, statements)
	if len(candidates) == 0 {
		t.Fatal("no calendar write was derived from sql/queries or from the committed Go; the sink derivation has stopped matching rather than the writes having gone away")
	}

	reach := reachAll(src)
	governed, _ := GovernedWriteSinks(reach, candidates)
	if len(governed) == 0 {
		t.Fatal("no calendar write is reached by REST only through the write gate; the REST reference the MCP tools are held to has stopped being readable")
	}

	findings, inScope := CheckCalendarWriteACL(src, reach, governed)
	for _, f := range findings {
		switch f.Kind {
		case Unenforced:
			t.Errorf("MCP tool %s (%s, registered at %s) writes %s through %s (%s, %s) but nothing reachable from it calls %s.\n"+
				"  Every REST operation that writes the same sink goes through %s, so a caller reaches the same rows under two different rules: REST refuses a member below editor and refuses a provider-fed calendar at any role, and this tool refuses neither.\n"+
				"  Route the write through the shared decision, or say at the tool why this write is not a write to a calendar's contents: %s",
				f.Entry.Name, f.Entry.Symbol, f.Entry.Pos,
				f.Via.Table, f.Via.Name, f.Via.Form, f.Via.Location(),
				CalendarWriteDecisionSymbol, CalendarWriteGateSymbol, WriteACLMarkerForm)
		case StaleMarker:
			t.Errorf("MCP tool %s (%s) carries a write-ACL exemption that covers nothing — it either writes no governed calendar sink or applies the rule anyway. It exempts nothing and reads as though something was reasoned about; drop it",
				f.Entry.Name, f.Entry.Symbol)
		}
	}

	// A derived check that stops matching reports nothing rather than
	// reporting a problem, so what it covered is asserted before the
	// absence of findings is read as a pass.
	if len(inScope) == 0 {
		t.Error("the rule was held against no MCP tool at all; the calendar tools write these tables through the derived sinks, so an empty scope means the derivation stopped matching")
	}
}

// TestGovernedSinksSeparateContentsFromEverythingElse pins the
// classification against the committed tree.
//
// The classification is the load-bearing half: it decides which writes the
// MCP tools are held to, and it is derived from what REST does rather than
// from a table name. Both directions matter. A calendar's contents have to
// be in, or the check holds the write tools to nothing; and the writes REST
// itself performs without the gate have to stay out, or the check invents a
// divergence — an attendee answering their own invitation, a calendar's own
// settings, a member list — and reports it as a security gap.
func TestGovernedSinksSeparateContentsFromEverythingElse(t *testing.T) {
	t.Parallel()

	src, statements := load(t)
	candidates := CalendarWriteSinks(src, statements)
	// Keyed rather than named: a statement and the handler that issues it
	// commonly share a name, and one of the pair being governed says
	// nothing about the other.
	byKey := map[string]WriteSink{}
	for _, s := range candidates {
		byKey[s.Key()] = s
	}

	governed, ungoverned := GovernedWriteSinks(reachAll(src), candidates)
	isGoverned := map[string]bool{}
	for _, s := range governed {
		isGoverned[s.Key()] = true
	}

	// Writes to a calendar's contents, each performed by a REST operation
	// that goes through the write gate. Naming them here is not the scope
	// of the check — the scope is every sink the derivation finds — it is
	// the assertion that the derivation still finds anything. Event
	// deletion is named by symbol because it is a write site in the Go
	// tree rather than a statement.
	for _, want := range []string{
		"CreateCalendarEvent",
		"PatchCalendarEvent",
		"UpdateCalendarMemo",
		modulePath + "/internal/itemkit.DeleteEvent",
	} {
		if _, ok := byKey[want]; !ok {
			t.Fatalf("%s is no longer derived as a calendar write at all; the check is being verified against a sink that does not exist", want)
		}
		if !isGoverned[want] {
			t.Errorf("%s is not classified as a write to a calendar's contents, so no MCP tool is held to the rule for it. REST operations reaching it without the write gate: %v",
				want, ungoverned[want])
		}
	}

	// Every form the derivation reads has to survive classification. Each
	// form is a separate way of finding a write, and a form that stops
	// matching removes sinks instead of adding findings — so the check
	// would go on passing while holding the tools it covered to nothing.
	//
	// The named-call form is the one the repository asks new code to use:
	// SQL lives in sql/queries and reaches Go through the generated
	// method. A derivation blind to it covers less as the tree gets
	// cleaner, which is the opposite of what a guard is for.
	forms := map[WriteForm]int{}
	for _, s := range governed {
		forms[s.Form]++
	}
	if forms[StatementSink] == 0 {
		t.Error("no sqlc statement survived classification; the statement half of the derivation has stopped matching")
	}
	if forms[NamedCallSink] == 0 {
		t.Error("no write issued through a generated query method survived classification; that is how this repository asks a write to be spelled, so the derivation is now blind to the call sites that follow its own rule")
	}
	if forms[LiteralSink] == 0 {
		t.Error("no literal-built write survived classification; inline SQL is not gone from this tree, and a derivation that stops reading it holds those paths to nothing")
	}

	// Writes REST performs outside the gate. Holding MCP to a rule REST
	// does not hold itself to would report a divergence that does not
	// exist.
	for _, unwanted := range []string{"UpdateAttendeeRsvp", "PatchCalendar", "UpsertCalendarMember", "PatchCalendarSubscription"} {
		if _, ok := byKey[unwanted]; !ok {
			t.Fatalf("%s is no longer derived as a calendar write at all; the check is being verified against a sink that does not exist", unwanted)
		}
		if isGoverned[unwanted] {
			t.Errorf("%s is classified as a write to a calendar's contents, but REST reaches it without the write gate; the classification is no longer reading REST", unwanted)
		}
	}
}

// TestWriteACLMarkerNeedsAReason pins the exemption form against the
// failure this repository has already shipped once: an exemption whose
// justification is a sentence somewhere else, and a mention of the marker
// that acts as one.
func TestWriteACLMarkerNeedsAReason(t *testing.T) {
	t.Parallel()

	accepted := []string{
		"// calendar-write-acl: not-applicable — the row it writes is the caller's own answer to an invitation",
		"calendar-write-acl: not-applicable — a provider refresh writes it, not a member",
	}
	for _, s := range accepted {
		if !writeACLMarkerPattern.MatchString(s) {
			t.Errorf("a marker with a reason was not accepted: %q", s)
		}
	}

	refused := []string{
		"// calendar-write-acl: not-applicable —",
		"// calendar-write-acl: not-applicable — ",
		"// the marker form is calendar-write-acl: not-applicable — <why>",
		"// this tool is exempt from the calendar write rule, see below",
	}
	for _, s := range refused {
		if writeACLMarkerPattern.MatchString(s) && !strings.Contains(s, "<why>") {
			t.Errorf("a marker with no reason was accepted: %q", s)
		}
	}
	// The placeholder line is the one near miss worth stating separately:
	// documenting the form must not exempt the file that documents it.
	if writeACLMarkerPattern.MatchString(WriteACLMarkerForm) {
		t.Error("the documented marker form matches itself, so naming the form would exempt whatever names it")
	}
}
