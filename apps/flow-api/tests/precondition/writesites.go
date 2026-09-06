package precondition

// The Go half of a rule's sink set: a write built as a string literal in
// the source rather than declared in sql/queries.
//
// SQL belongs in sql/queries and reaches Go through sqlc, so a literal is
// the exception — and a derivation that reads only the query files covers
// exactly the write paths that follow the convention. Its coverage then
// falls as more SQL is written inline, silently, because a sink set that
// stops matching subtracts findings rather than adding them. The same
// inversion has already been found and closed once on the calendar-write
// side of this package.
//
// Only the literal form is derived here. A Go function that issues a
// named statement through the generated method is already in scope: the
// statement is a sink under its own name, and a call to the method
// carries that name into the reach set, so deriving the function as a
// second sink would report the same write twice.
//
// A literal a reader cannot attribute is not skipped. See
// [UnattributableWrite]: a write assembled from fragments can have its
// table recovered and its column not, so no column-scoped rule can decide
// whether it is in scope — and a derivation that quietly dropped it would
// be reporting full coverage of a set it knows it did not read.

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// literalWrite matches a write built as a Go string literal, capturing the
// table.
//
// It is anchored at the start of the literal because that is what a
// statement looks like and prose does not: an unanchored match would read
// a sentence describing an UPDATE as one, which is a failure this
// repository's scans have shipped before.
var literalWrite = regexp.MustCompile(`(?is)^\s*(insert\s+into|update)\s+([a-z_][a-z0-9_]*)\b`)

// Sinks returns every place a rule's columns are written: the named
// statements in sql/queries, and the hand-written functions whose own body
// builds such a write as a Go string literal.
//
// src may be nil, which derives the SQL half alone. That is what the
// query-file control asks for — it has statements and no tree — and it is
// the only case where half an answer is the whole question.
func Sinks(src *Source, statements []Statement, rule Rule) map[string]WriteSink {
	out := map[string]WriteSink{}
	for _, s := range statements {
		if writesAnyColumn(s.SQL, rule) {
			sink := WriteSink{Name: s.Name, Form: StatementSink, Table: rule.Table, Where: s.Location()}
			out[sink.Key()] = sink
		}
	}
	if src == nil {
		return out
	}
	for _, sink := range src.literalSinks(rule) {
		out[sink.Key()] = sink
	}
	return out
}

// writesAnyColumn reports whether a normalised statement writes any of the
// rule's columns on the rule's table.
func writesAnyColumn(sql string, rule Rule) bool {
	probe := Statement{SQL: sql}
	for _, column := range rule.Columns {
		if probe.WritesColumn(rule.Table, column) {
			return true
		}
	}
	return false
}

// literalSinks returns the functions whose own body writes the rule's
// columns as a Go string literal.
//
// The first such write in source order answers for the function: a
// function writing the same rule's columns twice is one sink either way,
// and taking the first keeps a failure naming the same position on every
// run.
func (s *Source) literalSinks(rule Rule) []WriteSink {
	var out []WriteSink
	for symbol, fn := range s.funcs {
		unreadable := s.unreadableLiterals(fn)
		var sink *WriteSink
		ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
			if sink != nil {
				return false
			}
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || unreadable[lit.Pos()] {
				return true
			}
			text, ok := goStringLiteral(lit.Value)
			if !ok {
				return true
			}
			m := literalWrite.FindStringSubmatch(text)
			if m == nil || !strings.EqualFold(m[2], rule.Table) {
				return true
			}
			if !writesAnyColumn(normalizeSQL(text), rule) {
				return true
			}
			sink = &WriteSink{
				Name:   symbol[strings.LastIndex(symbol, ".")+1:],
				Form:   LiteralSink,
				Symbol: symbol,
				Table:  rule.Table,
				Where:  s.location(lit.Pos()),
			}
			return false
		})
		if sink != nil {
			out = append(out, *sink)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out
}

// UnattributableWrite is a write to a table some rule governs whose
// columns the source does not state.
//
// The shape is a statement assembled from pieces — a column name
// concatenated in, a select list or a predicate substituted through a
// format verb. The table survives that, so the write is visibly in the
// rules' territory; the column does not, so no column-scoped rule can say
// whether it is in scope. Reporting it is the only honest answer: a
// derivation that dropped it would claim coverage of a statement it could
// not read, which is the failure this whole line of checks removes.
type UnattributableWrite struct {
	// Symbol is the package-qualified function the write sits in.
	Symbol string
	// Table is the table the literal names.
	Table string
	// Text is the literal as written, for the failure message.
	Text string
	// File is the repository-relative file and Line the position of the
	// literal.
	File string
	Line int
}

// Location renders the write's position for a failure message.
func (u UnattributableWrite) Location() string {
	return u.File + ":" + strconv.Itoa(u.Line)
}

// UnattributableWrites returns every write, to a table one of the rules
// governs, whose column list cannot be read out of the source.
func UnattributableWrites(src *Source, rules []Rule) []UnattributableWrite {
	governed := map[string]bool{}
	for _, rule := range rules {
		governed[strings.ToLower(rule.Table)] = true
	}

	var out []UnattributableWrite
	for symbol, fn := range src.funcs {
		unreadable := src.unreadableLiterals(fn)
		ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || !unreadable[lit.Pos()] {
				return true
			}
			text, ok := goStringLiteral(lit.Value)
			if !ok {
				return true
			}
			m := literalWrite.FindStringSubmatch(text)
			if m == nil || !governed[strings.ToLower(m[2])] {
				return true
			}
			file, line := src.position(lit.Pos())
			out = append(out, UnattributableWrite{
				Symbol: symbol,
				Table:  strings.ToLower(m[2]),
				Text:   collapse(text),
				File:   file,
				Line:   line,
			})
			return true
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// UnattributableException is a write this package accepts it cannot
// attribute, together with the reason it cannot.
//
// It exists so the derivation can say "there is a write here I did not
// read" out loud and still be usable, rather than choosing between a
// permanent failure and a silent gap. It is not an exemption from a rule:
// nothing here says the write is correct, only that its columns are not
// in the source to be read.
//
// An entry that covers no write is refused, for the same reason a stale
// marker is: an exception nobody is relying on reads as though the write
// it names was considered and cleared, when the write may simply have
// gone.
type UnattributableException struct {
	// File is the repository-relative file the write sits in.
	File string
	// Prefix is the leading text of the literal, so the exception covers
	// one write rather than the file.
	Prefix string
	// Reason states why the columns cannot be recovered. It is mandatory.
	Reason string
}

// UnattributableExceptions are the writes whose columns the source does
// not state.
var UnattributableExceptions = []UnattributableException{
	{
		File:   "apps/flow-api/internal/itemkit/reschedule.go",
		Prefix: "UPDATE tasks SET",
		Reason: "the column is a parameter and is concatenated into the statement, so the " +
			"table reaches the source and the column does not; no column-scoped rule can " +
			"decide whether this write is one it governs",
	},
}

// Covers reports whether the exception is about this write.
func (e UnattributableException) Covers(w UnattributableWrite) bool {
	return w.File == e.File && strings.HasPrefix(w.Text, collapse(e.Prefix))
}

// collapse reduces text to single-spaced fields, which is the form
// [UnattributableWrite.Text] is held in.
func collapse(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// unreadableLiterals marks the string literals in a function that carry
// only part of a statement.
//
// Two shapes produce one: an operand of a `+` concatenation, and the
// format string of a call whose verbs are filled in at run time. In both
// the text in the source is a fragment, so what it says about the
// statement's columns is what the fragment happens to contain rather than
// what the statement writes.
func (s *Source) unreadableLiterals(fn *funcDecl) map[token.Pos]bool {
	out := map[token.Pos]bool{}
	mark := func(expr ast.Expr) {
		ast.Inspect(expr, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				out[lit.Pos()] = true
			}
			return true
		})
	}
	ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.BinaryExpr:
			if node.Op == token.ADD {
				mark(node)
			}
		case *ast.CallExpr:
			if len(node.Args) > 0 && isFormatCall(node, fn.owner) {
				mark(node.Args[0])
			}
		}
		return true
	})
	return out
}

// isFormatCall reports whether the call is fmt.Sprintf, whose first
// argument is a template rather than a statement.
func isFormatCall(call *ast.CallExpr, owner *goFile) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Sprintf" {
		return false
	}
	qualifier, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return owner.imports[qualifier.Name] == "fmt"
}

// goStringLiteral returns the text a Go string literal holds, in either
// spelling: the interpreted form the one-line writes use and the raw form
// the wrapped ones do.
func goStringLiteral(value string) (string, bool) {
	text, err := strconv.Unquote(value)
	if err != nil {
		return "", false
	}
	return text, true
}

// location renders a position the way a failure message quotes it.
func (s *Source) location(pos token.Pos) string {
	file, line := s.position(pos)
	return file + ":" + strconv.Itoa(line)
}

// position returns a position as a repository-relative file and a line, so
// what a check reports and what an exemption names are the same string on
// every machine.
func (s *Source) position(pos token.Pos) (string, int) {
	at := s.fset.Position(pos)
	path := at.Filename
	if s.root != "" {
		if rel, err := filepath.Rel(s.root, path); err == nil {
			path = rel
		}
	}
	return filepath.ToSlash(path), at.Line
}
