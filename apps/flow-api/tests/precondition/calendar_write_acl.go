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
//	            calendar_* table, and every hand-written function whose
//	            body issues such a write — either by calling the generated
//	            method that carries the statement, or by building the SQL
//	            as a literal. The function kind is not an afterthought: a
//	            statement is shared by every caller, so the statement on
//	            its own says only that the column is written somewhere,
//	            while the function says which write site it is. Reading
//	            only the statement collapses a deletion performed under
//	            the write gate together with one performed by a task
//	            propagation that never sees it, and the pair then
//	            disqualifies each other.
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

// WriteForm says how a sink issues its write. It is what the check
// asserts on to prove each half of the derivation is still matching: a
// half that stops matching removes sinks rather than adding findings, so
// nothing else would notice.
type WriteForm int

const (
	// StatementSink is a named statement in sql/queries. Every caller of
	// the generated method shares it, so it says the column is written,
	// not which site wrote it.
	StatementSink WriteForm = iota
	// NamedCallSink is a Go function that issues the write by calling the
	// generated method carrying a named statement. This is the form the
	// repository asks new code to be written in.
	NamedCallSink
	// LiteralSink is a Go function that builds the write as a string
	// literal. Inline SQL is not gone from this tree, so this form still
	// has to be read.
	LiteralSink
)

// String renders the form for a failure message.
func (f WriteForm) String() string {
	switch f {
	case StatementSink:
		return "named statement"
	case NamedCallSink:
		return "call to a named statement"
	case LiteralSink:
		return "SQL literal"
	default:
		return "unknown"
	}
}

// WriteSink is one place a calendar table is written.
type WriteSink struct {
	// Name is how a call to it appears in the call graph.
	Name string
	// Form says how the write is issued.
	Form WriteForm
	// Symbol is the package-qualified function for a sink that is a write
	// site in the Go tree, and empty for a sqlc statement. The two are
	// matched differently and deliberately so: a statement is performed
	// through a method on a generated querier, which the call graph can
	// only match by name, whereas a function is matched by symbol so a
	// same-named method on some other value is never mistaken for it.
	Symbol string
	// Table is the calendar table written.
	Table string
	// Where locates the write.
	Where string
}

// Location renders the sink's position for a failure message.
func (s WriteSink) Location() string { return s.Where }

// Key identifies a sink uniquely.
//
// A name is not enough. A statement's name is unique within sql/queries,
// but a handler that issues it commonly carries the same name, and two
// packages can each declare a write site by the same name. Keying a
// report by name alone would merge a governed site with an ungoverned one
// and answer for both at once.
func (s WriteSink) Key() string {
	if s.Symbol != "" {
		return s.Symbol
	}
	return s.Name
}

// reachedBy reports whether the reach sets of an entry include this sink.
func (s WriteSink) reachedBy(qualified, names map[string]bool) bool {
	if s.Symbol != "" {
		return qualified[s.Symbol]
	}
	return names[s.Name]
}

// calendarWritingStatements indexes, by statement name, the calendar
// table each named statement writes.
//
// The statement name is also the name of the generated method a caller
// invokes, so the same index answers both "does this statement write a
// calendar table" and "does this call write one". Deriving the table from
// sql/queries rather than from the generated Go keeps the source of truth
// where the repository puts it: the generated package is rebuilt from
// these files and carries no statement text of its own that this would be
// reading instead.
func calendarWritingStatements(statements []Statement) map[string]string {
	out := map[string]string{}
	for _, st := range statements {
		if m := calendarStatementWrite.FindStringSubmatch(st.SQL); m != nil {
			out[st.Name] = m[1]
		}
	}
	return out
}

// CalendarWriteSinks derives every candidate sink: the sqlc statements
// that write a calendar table, and the hand-written functions that issue
// such a write — through the generated method that carries the statement,
// or as a Go string literal.
//
// Both kinds of Go write site have to be read, and reading only one is
// how this derivation has already gone blind once. A derivation that read
// only the literals saw exactly the call sites that break the
// repository's own rule that SQL lives in sql/queries, and stopped seeing
// a write the moment it was moved to the named statement it belongs in —
// so it covered less as the tree got cleaner, silently.
func CalendarWriteSinks(src *Source, statements []Statement) []WriteSink {
	var out []WriteSink
	writes := calendarWritingStatements(statements)
	for _, st := range statements {
		if table, ok := writes[st.Name]; ok {
			out = append(out, WriteSink{Name: st.Name, Table: table, Where: st.Location()})
		}
	}
	for symbol, fn := range src.funcs {
		site, found := calendarWriteSite(src, fn, writes)
		if !found {
			continue
		}
		out = append(out, WriteSink{
			Name:   symbol[strings.LastIndex(symbol, ".")+1:],
			Form:   site.form,
			Symbol: symbol,
			Table:  site.table,
			Where:  site.pos,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Symbol < out[j].Symbol
	})
	return out
}

// writeSite is what a function's own body was found to do.
type writeSite struct {
	table string
	pos   string
	form  WriteForm
}

// calendarWriteSite returns the calendar write a function's own body
// issues. The first write in source order answers for the function; a
// function that writes two calendar tables is one sink either way, and
// taking the first keeps a failure naming the same position on every run.
//
// Two forms are read. A string literal carrying the statement is matched
// by its text. A call to a generated query method is matched by the
// method's name, which is the statement's name in sql/queries — the same
// name-based match the statement half of the derivation already relies
// on, because a generated querier is a value and a call on it has no
// import path to resolve against.
//
// A call qualified by an imported package name is not one of those: it is
// a call to a Go function, whatever that function is called, and if it
// issues a write it is derived as its own sink from its own body. Reading
// it here would credit the caller with a write it does not perform.
func calendarWriteSite(src *Source, fn *funcDecl, writes map[string]string) (writeSite, bool) {
	var site writeSite
	found := false
	ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch node := n.(type) {
		case *ast.BasicLit:
			if node.Kind != token.STRING {
				return true
			}
			m := calendarLiteralWrite.FindStringSubmatch(node.Value)
			if m == nil {
				return true
			}
			site = writeSite{
				table: strings.ToLower(m[1]),
				pos:   src.fset.Position(node.Pos()).String(),
				form:  LiteralSink,
			}
			found = true
			return false
		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			written, ok := writes[sel.Sel.Name]
			if !ok {
				return true
			}
			if qualifier, ok := sel.X.(*ast.Ident); ok {
				if _, imported := fn.owner.imports[qualifier.Name]; imported {
					return true
				}
			}
			site = writeSite{
				table: written,
				pos:   src.fset.Position(node.Pos()).String(),
				form:  NamedCallSink,
			}
			found = true
			return false
		}
		return true
	})
	return site, found
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
//
// The report is keyed by [WriteSink.Key] rather than by name, so a
// statement and a handler that share a name each answer for themselves.
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
			ungoverned[sink.Key()] = append(ungoverned[sink.Key()], r.entry.Name)
		}
		if writers > 0 && writers == gated {
			governed = append(governed, sink)
			continue
		}
		if writers == 0 {
			ungoverned[sink.Key()] = nil
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
