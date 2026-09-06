package kindscan

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
)

// kindColumns are the columns whose values are event kinds: events.type,
// notifications.event_type and webhook_deliveries.event_type.
//
// The match is on the exact name, and that is load-bearing rather than
// unfinished. Eight other columns in the schema end in _type and none of
// them hold a kind — resource_type, target_resource_type, content_type,
// widget_type, subject_type, target_type, entity_type, scope_type — and
// their values are ordinary words like task, project, page, burndown and
// MIME types. Widening this to a _type suffix would reject all of them.
//
// notifications is where that goes wrong first: it carries event_type and
// resource_type on adjacent lines, so one statement can hold
// `event_type = 'task.comment.added'` beside `resource_type = 'task'`,
// and a looser rule would report the second while the query is exactly
// right. A guard that rejects correct queries is a guard that gets turned
// off, which costs more than the hole it was widened to close.
//
// [IsKindColumn] is the matcher, and the schema guard holds this set to
// the schema so a column added by either name has to be classified rather
// than inherit an answer.
var kindColumns = map[string]bool{"type": true, "event_type": true}

// IsKindColumn reports whether a column name is one whose values are
// event kinds. See [kindColumns] for why the match is exact.
func IsKindColumn(name string) bool {
	return kindColumns[strings.ToLower(name)]
}

// SQLFinding is one event-kind string in a query file that names no
// declared kind.
type SQLFinding struct {
	// Pos is the "file:line" of the literal.
	Pos string
	// Column is the column the literal is written to or matched against.
	Column string
	// Value is the literal as written, unquoted.
	Value string
	// Prefix marks a LIKE pattern, which is held to a weaker rule: its
	// fixed prefix has to lead somewhere rather than name a kind outright.
	Prefix bool
	// Escape marks the report of an escape marker that covered nothing.
	Escape bool

	// line is the marker lookup key, kept apart from Pos so the escape can
	// be matched without parsing the position back out.
	line int
}

func (f SQLFinding) String() string {
	if f.Escape {
		return fmt.Sprintf("%s: a %s marker here covers no undeclared event kind; "+
			"the escape is for a name deliberately outside the registry, and one left behind silently covers the next literal written on this line", f.Pos, undeclaredMarker)
	}
	if f.Prefix {
		return fmt.Sprintf("%s: %s is matched against the pattern %q, whose fixed prefix names no declared event kind; "+
			"the kinds are declared in packages/go-shared/eventbus, and a pattern matching none of them selects nothing and says nothing", f.Pos, f.Column, f.Value)
	}
	return fmt.Sprintf("%s: %s is written as %q, which is not a declared event kind; "+
		"the kinds are declared in packages/go-shared/eventbus, and SQL names one by spelling it out — so a name that is not there inserts a kind nobody consumes, or matches no row, and reports neither", f.Pos, f.Column, f.Value)
}

// ScanSQL reads every .sql file under dir and reports each event-kind
// string that no constant in packages/go-shared/eventbus declares.
//
// The Go scan cannot reach these. A kind spelled inside a query is a
// string to the compiler and to sqlc alike — nothing in Go names it, so
// renaming the constant leaves the query behind and every build stays
// green. The query then writes a kind no subscriber knows, or filters on
// one no row carries, and both look like an empty result rather than a
// mistake.
//
// It is a text rule where the Go half is a type rule, and that is the
// whole of what it can be: SQL has no type that says "event kind". So it
// pairs a column name with the literal in its position and asks the same
// registry the Go side asks.
//
// Reading goes through an [os.Root] opened on dir, so every path the walk
// produces is resolved inside it. A walk and a read are two steps, and a
// path can stop meaning what it meant between them — a symlink out of the
// tree is the ordinary way. Scoping the reads to the root makes the walk
// unable to hand the scanner a file the caller did not point it at,
// rather than asserting that it will not.
func ScanSQL(dir string) ([]string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("kindscan: open %s: %w", dir, err)
	}
	defer func() { _ = root.Close() }()

	var (
		findings []SQLFinding
		read     int
	)
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") {
			return nil
		}
		src, rerr := root.ReadFile(name)
		if rerr != nil {
			return rerr
		}
		read++
		// The message names the file as the caller would open it; the read
		// that produced it was scoped to the root.
		findings = append(findings, scanSQLFile(filepath.Join(dir, name), string(src))...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("kindscan: walk %s: %w", dir, err)
	}
	// A root holding no query is not a clean tree, it is a scan pointed at
	// the wrong place — and it reports the same nothing as a clean one.
	if read == 0 {
		return nil, fmt.Errorf("kindscan: no .sql file under %s; a scan here would prove nothing", dir)
	}

	msgs := make([]string, 0, len(findings))
	for _, f := range findings {
		msgs = append(msgs, f.String())
	}
	sort.Strings(msgs)
	return msgs, nil
}

// Column is one column of a table, named the way a guard reports it.
type Column struct {
	// Table is the table the column belongs to, lower-cased.
	Table string
	// Name is the column's own name, lower-cased.
	Name string
	// Pos is the "file:line" the column is declared at.
	Pos string
}

// Qualified spells the column as table.name.
func (c Column) Qualified() string {
	return c.Table + "." + c.Name
}

// SchemaColumns reads every CREATE TABLE under dir and returns the
// columns they declare.
//
// It exists so the rule [IsKindColumn] applies can be held to the schema
// rather than to a list somebody keeps up to date. What makes matching on
// a bare column name safe is a fact about the schema, and a fact about
// the schema is exactly the kind of thing that stops being true without
// anyone touching the code that relies on it.
func SchemaColumns(dir string) ([]Column, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("kindscan: open %s: %w", dir, err)
	}
	defer func() { _ = root.Close() }()

	var (
		out  []Column
		read int
	)
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") {
			return nil
		}
		src, rerr := root.ReadFile(name)
		if rerr != nil {
			return rerr
		}
		read++
		out = append(out, tableColumns(filepath.Join(dir, name), string(src))...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("kindscan: walk %s: %w", dir, err)
	}
	if read == 0 {
		return nil, fmt.Errorf("kindscan: no .sql file under %s; a scan here would prove nothing", dir)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("kindscan: no CREATE TABLE under %s; a scan here would prove nothing", dir)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Table != out[j].Table {
			return out[i].Table < out[j].Table
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// tableConstraints are the words that open a table element which declares
// no column, so the identifier after them is not a column name.
var tableConstraints = map[string]bool{
	"primary": true, "unique": true, "key": true, "index": true,
	"constraint": true, "foreign": true, "check": true,
	"fulltext": true, "spatial": true, "period": true,
}

// tableColumns reads the columns declared by the CREATE TABLE statements
// in one file.
func tableColumns(file, src string) []Column {
	var out []Column
	for _, stmt := range sqlStatements(sqlTokens(src)) {
		if len(stmt) < 2 || stmt[0].str || stmt[0].text != "create" {
			continue
		}
		open := indexToken(stmt, 0, "(")
		if open < 0 {
			continue
		}
		table, ok := createdTable(stmt[:open])
		if !ok {
			continue
		}
		elements, _, listOK := parenList(stmt, open)
		if !listOK {
			continue
		}
		for _, element := range elements {
			if len(element) == 0 || element[0].str || tableConstraints[element[0].text] {
				continue
			}
			out = append(out, Column{
				Table: table,
				Name:  element[0].text,
				Pos:   fmt.Sprintf("%s:%d", file, element[0].line),
			})
		}
	}
	return out
}

// createdTable returns the table name from the head of a CREATE TABLE
// statement, and whether the statement creates a table at all.
func createdTable(head []sqlToken) (string, bool) {
	if indexToken(head, 0, "table") < 0 {
		return "", false
	}
	// The name is the last identifier before the column list, which skips
	// TEMPORARY, IF NOT EXISTS and any schema qualifier without having to
	// enumerate them.
	for i := len(head) - 1; i >= 0; i-- {
		t := head[i]
		if t.str || !isSQLIdent(t.text[0]) || t.text == "exists" {
			continue
		}
		return t.text, true
	}
	return "", false
}

// undeclaredMarker is the comment that sanctions a kind no constant
// names, and the SQL counterpart of [Undeclared].
//
// Some statements need a kind the registry deliberately does not carry.
// A conformance fixture asserting that the log orders appends has to
// append something, and it must be a name no writer emits, or the row it
// counts is only its own until the next handler picks the same word. The
// value not existing is the point, exactly as in Go.
//
// So the two cases are made to look different rather than left to be told
// apart: the marker reads as a decision at the line it sits on, and it
// greps as one across the tree. It cannot launder a real kind, because a
// declared name is not reported in the first place — and a marker that
// suppressed nothing is itself reported, so escapes do not accumulate
// past the literal that earned them.
const undeclaredMarker = "kindscan:undeclared"

// scanSQLFile reports the kind literals one file gets wrong, and the
// escape markers in it that cover nothing.
func scanSQLFile(file, src string) []SQLFinding {
	var found []SQLFinding
	for _, stmt := range sqlStatements(sqlTokens(src)) {
		found = append(found, insertWrites(file, stmt)...)
		found = append(found, columnMatches(file, stmt)...)
	}

	markers := markerLines(src)
	out := make([]SQLFinding, 0, len(found))
	for _, f := range found {
		if _, marked := markers[f.line]; marked {
			markers[f.line] = true
			continue
		}
		out = append(out, f)
	}
	for line, used := range markers {
		if !used {
			out = append(out, SQLFinding{Pos: fmt.Sprintf("%s:%d", file, line), Escape: true, line: line})
		}
	}
	return out
}

// markerLines returns the lines carrying an escape marker, mapped to
// whether it has covered a finding yet.
//
// The marker counts only inside a comment. A statement that happens to
// hold the text in a string literal is data, not a decision about the
// rule, and reading it as one would let a value turn the guard off for
// its own line.
func markerLines(src string) map[int]bool {
	out := map[int]bool{}
	for i, text := range strings.Split(src, "\n") {
		marker := strings.Index(text, undeclaredMarker)
		if marker < 0 {
			continue
		}
		comment := len(text)
		for _, opener := range []string{"--", "#", "/*"} {
			if at := strings.Index(text, opener); at >= 0 && at < comment {
				comment = at
			}
		}
		if marker > comment {
			out[i+1] = false
		}
	}
	return out
}

// sqlToken is one lexical unit of a query.
type sqlToken struct {
	// text is the identifier or keyword folded to lower case, the
	// punctuation character, or the unquoted value of a string literal.
	text string
	// str marks a string literal.
	str bool
	// line is the 1-based line the token starts on.
	line int
}

// sqlTokens splits a query file into tokens, dropping comments.
//
// It is a lexer and not a parser because the rule needs two things a
// lexer already has: which column name a literal sits against, and where
// the literal is. Parsing the dialect would buy neither.
func sqlTokens(src string) []sqlToken {
	var out []sqlToken
	line := 1
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '#', c == '-' && i+1 < len(src) && src[i+1] == '-':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i+1 < len(src) && (src[i] != '*' || src[i+1] != '/') {
				if src[i] == '\n' {
					line++
				}
				i++
			}
			i = min(i+2, len(src))
		case c == '\'' || c == '"':
			var (
				value string
				start = line
			)
			value, i, line = sqlString(src, i, line)
			out = append(out, sqlToken{text: value, str: true, line: start})
		case c == '`':
			i++
			start := i
			for i < len(src) && src[i] != '`' {
				i++
			}
			out = append(out, sqlToken{text: strings.ToLower(src[start:i]), line: line})
			i = min(i+1, len(src))
		case isSQLIdent(c):
			start := i
			for i < len(src) && isSQLIdent(src[i]) {
				i++
			}
			out = append(out, sqlToken{text: strings.ToLower(src[start:i]), line: line})
		default:
			out = append(out, sqlToken{text: string(c), line: line})
			i++
		}
	}
	return out
}

// sqlString reads the literal starting at src[i], returning its value,
// the index after it and the line the scan has reached.
func sqlString(src string, i, line int) (string, int, int) {
	quote := src[i]
	i++
	var b strings.Builder
	for i < len(src) {
		switch {
		case src[i] == '\\' && i+1 < len(src):
			b.WriteByte(src[i+1])
			i += 2
		case src[i] == quote && i+1 < len(src) && src[i+1] == quote:
			b.WriteByte(quote)
			i += 2
		case src[i] == quote:
			i++
			return b.String(), i, line
		default:
			if src[i] == '\n' {
				line++
			}
			b.WriteByte(src[i])
			i++
		}
	}
	return b.String(), i, line
}

func isSQLIdent(c byte) bool {
	return c == '_' || c == '$' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// sqlStatements splits a token stream on semicolons.
func sqlStatements(toks []sqlToken) [][]sqlToken {
	var (
		out   [][]sqlToken
		start int
	)
	for i, t := range toks {
		if !t.str && t.text == ";" {
			if i > start {
				out = append(out, toks[start:i])
			}
			start = i + 1
		}
	}
	if start < len(toks) {
		out = append(out, toks[start:])
	}
	return out
}

// insertWrites reports the kind literals an INSERT writes, pairing the
// column list with the VALUES list by position.
//
// An INSERT ... SELECT names no literal at a column position and is left
// alone; a literal it selects is a comparison or a projection, and the
// first is [columnMatches]' business.
func insertWrites(file string, stmt []sqlToken) []SQLFinding {
	if len(stmt) == 0 || stmt[0].str || stmt[0].text != "insert" {
		return nil
	}
	open := indexToken(stmt, 0, "(")
	if open < 0 {
		return nil
	}
	columns, next, ok := parenList(stmt, open)
	if !ok {
		return nil
	}
	if next >= len(stmt) || stmt[next].str || (stmt[next].text != "values" && stmt[next].text != "value") {
		return nil
	}

	var out []SQLFinding
	for i := next + 1; i < len(stmt); {
		if stmt[i].str || stmt[i].text != "(" {
			break
		}
		values, after, listOK := parenList(stmt, i)
		if !listOK {
			break
		}
		for col, name := range columns {
			if len(name) != 1 || name[0].str || !IsKindColumn(name[0].text) {
				continue
			}
			if col >= len(values) || len(values[col]) != 1 || !values[col][0].str {
				continue
			}
			out = append(out, exactFinding(file, name[0].text, values[col][0]))
		}
		i = after
		if i < len(stmt) && !stmt[i].str && stmt[i].text == "," {
			i++
		}
	}
	return filterDeclared(out)
}

// columnMatches reports the kind literals a statement compares a
// kind-bearing column against — in a WHERE, a CASE, or the SET of an
// UPDATE, which are the same shape and carry the same hazard as a write:
// a name that no longer exists matches nothing and raises nothing.
func columnMatches(file string, stmt []sqlToken) []SQLFinding {
	var out []SQLFinding
	for i, t := range stmt {
		if t.str || !IsKindColumn(t.text) {
			continue
		}
		j := i + 1
		if j < len(stmt) && !stmt[j].str && stmt[j].text == "not" {
			j++
		}
		if j >= len(stmt) || stmt[j].str {
			continue
		}
		switch stmt[j].text {
		case "like":
			if j+1 < len(stmt) && stmt[j+1].str {
				out = append(out, prefixFinding(file, t.text, stmt[j+1]))
			}
		case "in":
			if j+1 < len(stmt) && !stmt[j+1].str && stmt[j+1].text == "(" {
				values, _, ok := parenList(stmt, j+1)
				if !ok {
					continue
				}
				for _, v := range values {
					if len(v) == 1 && v[0].str {
						out = append(out, exactFinding(file, t.text, v[0]))
					}
				}
			}
		default:
			// A run of comparison punctuation covers =, !=, <> and <=>,
			// which the lexer emits one character at a time.
			k := j
			for k < len(stmt) && !stmt[k].str && len(stmt[k].text) == 1 && strings.ContainsAny(stmt[k].text, "=!<>") {
				k++
			}
			if k > j && k < len(stmt) && stmt[k].str {
				out = append(out, exactFinding(file, t.text, stmt[k]))
			}
		}
	}
	return filterDeclared(out)
}

func exactFinding(file, column string, tok sqlToken) SQLFinding {
	return SQLFinding{Pos: fmt.Sprintf("%s:%d", file, tok.line), Column: column, Value: tok.text, line: tok.line}
}

func prefixFinding(file, column string, tok sqlToken) SQLFinding {
	return SQLFinding{Pos: fmt.Sprintf("%s:%d", file, tok.line), Column: column, Value: tok.text, Prefix: true, line: tok.line}
}

// filterDeclared drops the literals that name a kind the registry knows,
// which is all of them on a tree that is in order.
func filterDeclared(in []SQLFinding) []SQLFinding {
	out := in[:0]
	for _, f := range in {
		if f.Prefix {
			if !kindHasPrefix(f.Value) {
				out = append(out, f)
			}
			continue
		}
		if !IsDeclaredKind(f.Value) {
			out = append(out, f)
		}
	}
	return out
}

// kindHasPrefix reports whether any declared kind begins with the fixed
// part of a LIKE pattern.
//
// The fixed part stops at the first % or _, both of which MySQL reads as
// wildcards. Kinds spell words with underscores — task.auto_completed,
// agent.task.handoff_to_user — so a pattern meaning one literally is cut
// short here and checked against less than it says. That direction is
// safe: a shorter prefix matches more kinds, so the rule under-reports
// rather than accusing a correct pattern.
func kindHasPrefix(pattern string) bool {
	fixed := pattern
	if i := strings.IndexAny(fixed, "%_"); i >= 0 {
		fixed = fixed[:i]
	}
	// A pattern with no fixed part matches every kind and constrains
	// none, so there is nothing here for a rename to break.
	if fixed == "" {
		return true
	}
	for _, k := range eventbus.Kinds() {
		if strings.HasPrefix(string(k), fixed) {
			return true
		}
	}
	return false
}

// indexToken returns the index of the first token at or after from whose
// text is want, or -1.
func indexToken(toks []sqlToken, from int, want string) int {
	for i := from; i < len(toks); i++ {
		if !toks[i].str && toks[i].text == want {
			return i
		}
	}
	return -1
}

// parenList splits the parenthesised list opening at toks[open] into its
// top-level comma-separated elements, and returns the index after the
// closing parenthesis.
func parenList(toks []sqlToken, open int) ([][]sqlToken, int, bool) {
	if open >= len(toks) || toks[open].str || toks[open].text != "(" {
		return nil, open, false
	}
	var (
		out   [][]sqlToken
		depth int
		start = open + 1
	)
	for i := open; i < len(toks); i++ {
		if toks[i].str {
			continue
		}
		switch toks[i].text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				if i > start || len(out) > 0 {
					out = append(out, toks[start:i])
				}
				return out, i + 1, true
			}
		case ",":
			if depth == 1 {
				out = append(out, toks[start:i])
				start = i + 1
			}
		}
	}
	return nil, open, false
}
