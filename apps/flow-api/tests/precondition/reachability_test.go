package precondition

import (
	"sort"
	"strings"
	"testing"
)

// TestCalendarWritesReachTheirPreconditions holds every request path that
// writes a rule's calendar_events columns to the function that applies
// the rule.
//
// The failure it is written against is not a wrong answer. Both
// transports were already refused by the database, so no bad row was ever
// stored; what differed was what the caller was told. The REST handlers
// checked the window and answered 422 naming the field, the MCP tools did
// not and handed back the constraint violation as an execution failure —
// so the same request was a validation error in the browser and a server
// fault through an agent.
func TestCalendarWritesReachTheirPreconditions(t *testing.T) {
	t.Parallel()

	src, statements := load(t)
	byName := map[string]Rule{}
	for _, rule := range Rules {
		byName[rule.Name] = rule
		for _, enforcer := range rule.Enforcers {
			if !src.Declares(enforcer) {
				t.Errorf("rule %q names %s as its enforcement, but no function by that name is declared under internal/; the rule holds nothing",
					rule.Name, enforcer)
			}
		}
		if len(Sinks(statements, rule)) == 0 {
			t.Fatalf("no statement in sql/queries writes any of %s on calendar_events, so rule %q is held against nothing; the SQL derivation has stopped matching rather than the writes having gone away",
				strings.Join(rule.Columns, " / "), rule.Name)
		}
	}

	findings, scope := Check(src, statements, Rules)
	for _, f := range findings {
		rule := byName[f.Rule]
		switch f.Kind {
		case Unenforced:
			t.Errorf("%s %s (%s, registered at %s) writes %s through %s (%s) but nothing reachable from it calls %s.\n"+
				"  Why the rule exists: %s.\n"+
				"  Route the write through the shared rule, or say at the entry why this one cannot carry the input: %s",
				f.Entry.Surface, f.Entry.Name, f.Entry.Symbol, f.Entry.Pos,
				strings.Join(rule.Columns, " / "), f.Via.Name, f.Via.Location(),
				strings.Join(rule.Enforcers, " / "), rule.Why, MarkerForm)
		case StaleMarker:
			t.Errorf("%s %s (%s) carries a %q exemption that covers nothing — it either writes none of %s or applies the rule anyway. It exempts nothing and reads as though something was reasoned about; drop it",
				f.Entry.Surface, f.Entry.Name, f.Entry.Symbol, f.Rule, strings.Join(rule.Columns, " / "))
		}
	}

	// A derived check that stops matching reports nothing rather than
	// reporting a problem, so the scope it covered is asserted before the
	// absence of findings is read as a pass.
	for _, rule := range Rules {
		for _, surface := range []string{"MCP tool", "REST operation"} {
			if len(scope[rule.Name][surface]) == 0 {
				t.Errorf("rule %q was held against no %s; a rule that reaches one transport and not the other is the state this check exists to end",
					rule.Name, surface)
			}
		}
	}
}

// TestEntriesAreDerivedFromBothRegistries proves the entry derivation
// found both registries at all. A parse that silently reads zero
// registrations reports every rule as satisfied.
func TestEntriesAreDerivedFromBothRegistries(t *testing.T) {
	t.Parallel()

	src, _ := load(t)

	counts := map[string]int{}
	for _, entry := range src.Entries {
		counts[entry.Surface]++
		if !src.Declares(entry.Symbol) {
			t.Errorf("%s %s names %s, which is not a package-level function under internal/; the call graph has no entry point for it",
				entry.Surface, entry.Name, entry.Symbol)
		}
	}
	if counts["MCP tool"] < 40 {
		t.Errorf("only %d MCP tools were read out of the register calls; the registry walk has stopped matching", counts["MCP tool"])
	}
	if counts["REST operation"] < 100 {
		t.Errorf("only %d REST operations were read out of the huma.Register calls; the registry walk has stopped matching", counts["REST operation"])
	}
}

// TestSinksAreWritesRatherThanMentions pins what puts a statement in
// scope, against the committed SQL.
//
// The distinction the derivation rests on is between storing a window and
// reading one. A list query filters on start_at and a patch preserves it
// inside a COALESCE; neither can store an inverted pair, and holding them
// to a rule about input would put every read endpoint in scope.
func TestSinksAreWritesRatherThanMentions(t *testing.T) {
	t.Parallel()

	_, statements := load(t)
	byName := map[string]Statement{}
	for _, s := range statements {
		byName[s.Name] = s
	}

	chronology := Rules[0]
	if chronology.Name != "chronology" {
		t.Fatalf("the first rule is %q; this check is about the window rule", chronology.Name)
	}
	sinks := Sinks(statements, chronology)

	for _, want := range []string{"CreateCalendarEvent", "PatchCalendarEvent"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("sql/queries no longer declares %s; the derivation is being checked against a statement that does not exist", want)
		}
		if _, ok := sinks[want]; !ok {
			t.Errorf("%s writes the event window but is not derived as a sink", want)
		}
	}
	for _, unwanted := range []string{"ListCalendarEventsByRange", "FindCalendarEventByPublicId"} {
		if _, ok := byName[unwanted]; !ok {
			t.Fatalf("sql/queries no longer declares %s; the derivation is being checked against a statement that does not exist", unwanted)
		}
		if _, ok := sinks[unwanted]; ok {
			t.Errorf("%s only reads the event window but is derived as a sink; a rule about input would then cover every read", unwanted)
		}
	}
}

// load parses the tree and the SQL once per test, failing loudly on an
// empty read rather than letting the checks range over nothing.
func load(t *testing.T) (*Source, []Statement) {
	t.Helper()

	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	statements, err := Statements(root)
	if err != nil {
		t.Fatalf("read sql/queries: %v", err)
	}
	if len(statements) == 0 {
		t.Fatal("no statement was read from sql/queries; the checks would be looking at nothing")
	}
	src, err := Parse(root)
	if err != nil {
		t.Fatalf("parse apps/flow-api/internal: %v", err)
	}
	if len(src.Entries) == 0 {
		t.Fatal("no MCP tool and no REST operation was read from the source; the checks would be looking at nothing")
	}
	sort.Slice(src.Entries, func(i, j int) bool { return src.Entries[i].Name < src.Entries[j].Name })
	return src, statements
}
