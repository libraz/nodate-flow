package precondition

// The calendar-write ACL derivation. It answers a different question from
// the input-precondition rules above it — not "was the input checked" but
// "was the caller allowed" — and it answers it the same way, from the
// committed SQL and the committed Go rather than from a list of tool
// names.
//
// What it is written against: the REST handlers refuse a calendar write
// from a member below editor, and refuse one to a system calendar at any
// role, because a calendar's contents are visible to every one of its
// members and a system calendar's rows come from a provider feed. The MCP
// tools checked only that a calendar_members row existed. A viewer could
// therefore create events through an agent that the web app refused, on
// any calendar including a provider-fed one.
//
// The derivation has three steps, none of which names a tool:
//
//	sink        a place a calendar table is written: a statement in
//	            sql/queries that INSERTs into, UPDATEs or DELETEs FROM a
//	            calendar_* table, or a hand-written function whose body
//	            builds such a statement as a literal. The second kind is
//	            not an afterthought — event deletion goes through one, so
//	            a derivation that read only sql/queries would have held
//	            the delete tool to nothing.
//	governed    a sink every REST operation reaches through the calendar
//	            write gate. REST is the reference because it is where the
//	            rule is stated; a sink some REST operation writes without
//	            the gate is one where the product means something other
//	            than "this is a calendar's contents" — an attendee
//	            answering their own invitation, a task rename propagating
//	            onto its event — and holding MCP to a rule REST does not
//	            hold itself to would invent a divergence rather than close
//	            one.
//	held to     an MCP tool that reaches a governed sink has to reach the
//	            shared decision.
//
// Reaching the *decision*, not a wrapper around it, is the whole point.
// The two transports each need their own resolver — one takes a public id
// out of a tool argument, the other a path parameter, and they answer with
// different error envelopes — but there is one rule, exported from the
// REST handlers, and a tool that does not reach it is deciding for itself.
//
// An MCP tool that legitimately writes a governed sink without the rule
// says so at the tool, in a marker a machine reads — see
// [WriteACLMarkerForm]. The reason is mandatory and is prose: it must not
// name the gate, because a scan that credited a sentence would be back to
// checking that somebody decided rather than that anything enforces.

import (
	"fmt"
	"go/ast"
	"go/token"
	"regexp"
	"sort"
	"strings"
)

// CalendarWriteDecisionSymbol is the shared rule both transports have to
// reach: the exported decision in the REST calendar handlers.
const CalendarWriteDecisionSymbol = modulePath +
	"/internal/http/handlers/calendars.DecideCalendarWrite"

// CalendarWriteGateSymbol is the REST resolver that applies the decision.
// It is what marks a sink as holding a calendar's contents.
const CalendarWriteGateSymbol = modulePath +
	"/internal/http/handlers/calendars.resolveCalendarWrite"

// WriteACLMarkerForm is the machine-readable exemption, written in the doc
// comment or the body of the tool it exempts.
const WriteACLMarkerForm = "calendar-write-acl: not-applicable — <why this write is not a write to a calendar's contents>"

// writeACLMarkerPattern matches [WriteACLMarkerForm]. Requiring the reason
// to start and end with a letter is what stops a mention of the marker
// from acting as one.
var writeACLMarkerPattern = regexp.MustCompile(
	`calendar-write-acl:[ \t]*not-applicable[ \t]*—[ \t]*[A-Za-z][^\n]*[A-Za-z]`)

// calendarStatementWrite matches a normalised sqlc statement that writes a
// calendar table.
var calendarStatementWrite = regexp.MustCompile(
	`^(?:insert into|update|delete from) (calendar[a-z_]*)\b`)

// calendarLiteralWrite matches a calendar-table write built as a Go string
// literal, which is how the soft-delete of an event is performed.
var calendarLiteralWrite = regexp.MustCompile(
	`(?is)(?:insert\s+into|update|delete\s+from)\s+(calendar[a-z_]*)\b`)

// WriteSink is one place a calendar table is written.
type WriteSink struct {
	// Name is how a call to it appears in the call graph.
	Name string
	// Symbol is the package-qualified function for a sink built from a
	// literal, and empty for a sqlc statement. The two are matched
	// differently and deliberately so: a statement is performed through a
	// method on a generated querier, which the call graph can only match
	// by name, whereas a function is matched by symbol so a same-named
	// method on some other value is never mistaken for it.
	Symbol string
	// Table is the calendar table written.
	Table string
	// Where locates the write.
	Where string
}

// Location renders the sink's position for a failure message.
func (s WriteSink) Location() string { return s.Where }

// reachedBy reports whether the reach sets of an entry include this sink.
func (s WriteSink) reachedBy(qualified, names map[string]bool) bool {
	if s.Symbol != "" {
		return qualified[s.Symbol]
	}
	return names[s.Name]
}

// CalendarWriteSinks derives every candidate sink: the sqlc statements
// that write a calendar table, and the hand-written functions that build
// such a write as a literal.
func CalendarWriteSinks(src *Source, statements []Statement) []WriteSink {
	var out []WriteSink
	for _, st := range statements {
		if m := calendarStatementWrite.FindStringSubmatch(st.SQL); m != nil {
			out = append(out, WriteSink{Name: st.Name, Table: m[1], Where: st.Location()})
		}
	}
	for symbol, fn := range src.funcs {
		table, pos := literalCalendarWrite(src, fn)
		if table == "" {
			continue
		}
		out = append(out, WriteSink{
			Name:   symbol[strings.LastIndex(symbol, ".")+1:],
			Symbol: symbol,
			Table:  table,
			Where:  pos,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// literalCalendarWrite returns the calendar table a function writes
// through a string literal, and where the literal sits.
func literalCalendarWrite(src *Source, fn *funcDecl) (table, pos string) {
	ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
		if table != "" {
			return false
		}
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		m := calendarLiteralWrite.FindStringSubmatch(lit.Value)
		if m == nil {
			return true
		}
		table = strings.ToLower(m[1])
		pos = src.fset.Position(lit.Pos()).String()
		return false
	})
	return table, pos
}

// reachSet is one entry's call-graph closure, computed once.
type reachSet struct {
	entry     Entry
	qualified map[string]bool
	names     map[string]bool
}

// reachAll walks every derived entry once.
func reachAll(src *Source) []reachSet {
	out := make([]reachSet, 0, len(src.Entries))
	for _, e := range src.Entries {
		qualified, names := src.Reach(e.Symbol)
		out = append(out, reachSet{entry: e, qualified: qualified, names: names})
	}
	return out
}

// GovernedWriteSinks keeps the candidate sinks that REST writes only
// through the calendar write gate, and reports the rest with the REST
// operations that disqualified them.
//
// A sink no REST operation reaches at all is not governed either: nothing
// states that it holds a calendar's contents, and inferring it from the
// table name would make the rule an opinion of this file rather than a
// reading of the product.
func GovernedWriteSinks(reach []reachSet, candidates []WriteSink) (governed []WriteSink, ungoverned map[string][]string) {
	ungoverned = map[string][]string{}
	for _, sink := range candidates {
		writers, gated := 0, 0
		for _, r := range reach {
			if r.entry.Surface != "REST operation" || !sink.reachedBy(r.qualified, r.names) {
				continue
			}
			writers++
			if r.qualified[CalendarWriteGateSymbol] {
				gated++
				continue
			}
			ungoverned[sink.Name] = append(ungoverned[sink.Name], r.entry.Name)
		}
		if writers > 0 && writers == gated {
			governed = append(governed, sink)
			continue
		}
		if writers == 0 {
			ungoverned[sink.Name] = nil
		}
	}
	return governed, ungoverned
}

// WriteACLFinding is one thing the check has to say about an MCP tool.
type WriteACLFinding struct {
	// Entry is the tool.
	Entry Entry
	// Via is the governed sink that put it in scope, zero for a marker
	// that covers nothing.
	Via WriteSink
	// Kind says which of the two failures this is.
	Kind FindingKind
}

// CheckCalendarWriteACL holds every MCP tool that writes a governed sink
// to the shared decision, and returns what it found together with the
// tools it was held against.
//
// The scope is returned for the same reason the input-precondition check
// returns its own: a derivation that stops matching reports nothing, which
// is indistinguishable from a clean tree unless the caller asserts that
// something was looked at.
func CheckCalendarWriteACL(src *Source, reach []reachSet, governed []WriteSink) (findings []WriteACLFinding, inScope []Entry) {
	for _, r := range reach {
		if r.entry.Surface != "MCP tool" {
			continue
		}
		via, writes := firstGoverned(r, governed)
		marked := src.MarkedWriteACL(r.entry.Symbol)

		if !writes {
			if marked {
				findings = append(findings, WriteACLFinding{Entry: r.entry, Kind: StaleMarker})
			}
			continue
		}
		inScope = append(inScope, r.entry)

		if r.qualified[CalendarWriteDecisionSymbol] {
			if marked {
				findings = append(findings, WriteACLFinding{Entry: r.entry, Via: via, Kind: StaleMarker})
			}
			continue
		}
		if marked {
			continue
		}
		findings = append(findings, WriteACLFinding{Entry: r.entry, Via: via, Kind: Unenforced})
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Entry.Name != findings[j].Entry.Name {
			return findings[i].Entry.Name < findings[j].Entry.Name
		}
		return findings[i].Kind < findings[j].Kind
	})
	sort.Slice(inScope, func(i, j int) bool { return inScope[i].Name < inScope[j].Name })
	return findings, inScope
}

// firstGoverned returns the governed sink an entry writes, in a stable
// order so a failure names the same sink on every run.
func firstGoverned(r reachSet, governed []WriteSink) (WriteSink, bool) {
	var hits []WriteSink
	for _, sink := range governed {
		if sink.reachedBy(r.qualified, r.names) {
			hits = append(hits, sink)
		}
	}
	if len(hits) == 0 {
		return WriteSink{}, false
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Name < hits[j].Name })
	return hits[0], true
}

// MarkedWriteACL reports whether the entry carries a write-ACL exemption,
// in its doc comment or anywhere in its body.
func (s *Source) MarkedWriteACL(symbol string) bool {
	fn := s.funcs[symbol]
	if fn == nil {
		return false
	}
	start := fn.decl.Pos()
	if fn.decl.Doc != nil {
		start = fn.decl.Doc.Pos()
	}
	for _, group := range fn.owner.file.Comments {
		for _, c := range group.List {
			if c.Pos() >= start && c.End() <= fn.decl.End() && writeACLMarkerPattern.MatchString(c.Text) {
				return true
			}
		}
	}
	return false
}

// Describe renders a governed-sink set for a failure message.
func Describe(sinks []WriteSink) string {
	parts := make([]string, 0, len(sinks))
	for _, s := range sinks {
		parts = append(parts, fmt.Sprintf("%s (%s, %s)", s.Name, s.Table, s.Where))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}
