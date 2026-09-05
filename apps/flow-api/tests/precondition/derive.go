// Package precondition derives, from the committed SQL and the committed
// Go, which request paths write a column that carries an input rule — and
// proves each of them reaches the one function that applies it.
//
// Sharing a precondition helper is not the fix on its own. The chronology
// rule already existed, correctly, in the REST handlers; what was missing
// was anything that noticed the MCP tools writing the same columns
// without it. A helper moved into a shared package is reachable by the
// next tool, not reached by it, and the difference is invisible until an
// agent sends an inverted window and gets an unexplained server error
// where the browser gets a 422.
//
// So the scope is derived rather than listed:
//
//	sink        a statement in sql/queries that INSERTs into or UPDATEs
//	            the rule's table and writes one of the rule's columns. The
//	            SQL is the shared source both transports reach the row
//	            through, so a new write statement is in scope the moment
//	            it is committed.
//	entry       an MCP tool's run function, read out of the register
//	            calls, or a REST operation's handler, read out of the
//	            huma.Register calls. Neither is a list anyone maintains:
//	            a tool added tomorrow is an entry tomorrow.
//	in scope    an entry from which some sink statement is called.
//	held to     the entry has to reach the rule's enforcing function.
//
// A hand-written list of tool names would have covered exactly the tools
// that existed when it was written, which is the property that let the
// gap open in the first place.
//
// An entry that legitimately writes the columns without the rule says so
// at the entry, in a marker a machine reads, rather than in a sentence
// somewhere else — see [Rule.MarkerForm]. The reason is mandatory: an
// exemption with nothing after the marker is not an exemption.
//
// Call edges are literal. A call through an imported package name is an
// edge to that package's function; a method call on a value contributes
// its name — which is how a statement is matched — but no edge, so a
// helper is never credited to a package-level function that happens to
// share a name with a generated query method.
package precondition

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// modulePath is the flow-api module, which every import path this package
// resolves is rooted at.
const modulePath = "github.com/libraz/nodate-flow/apps/flow-api"

// Rule is one input precondition on a table's write.
type Rule struct {
	// Name is what a marker names and what a failure reports.
	Name string
	// Table is the table whose write puts a statement in scope. A rule
	// about an event window says nothing about a task, so a statement
	// against another table is not held to it however its columns are
	// spelled.
	Table string
	// Columns are the table's columns whose write puts a statement in
	// scope. A rule about a start/end window is not a rule about a title,
	// so a statement that writes neither column is not held to it.
	Columns []string
	// Enforcers are the package-qualified functions that apply the rule.
	// Reaching any one of them satisfies it: the shared entry point and
	// the wrappers around it are the same decision.
	Enforcers []string
	// Marker is the domain prefix of this rule's exemption marker. It is
	// per-rule because a prefix that names one domain reads as noise in
	// another: an exemption on a task write should not have to claim to
	// be about calendars for a machine to read it.
	Marker string
	// Why is the one-line reason the rule exists, quoted in the failure
	// so a reader does not have to find this file to understand it.
	Why string
}

// MarkerForm renders the machine-readable exemption for this rule,
// written in the doc comment or the body of the entry it exempts.
//
// The rule name is part of the marker: an entry exempt from one rule is
// not exempt from the others, and a single blanket marker would hide the
// next rule added rather than the one somebody reasoned about.
func (r Rule) MarkerForm() string {
	return r.Marker + ": " + r.Name +
		" not-applicable — <why this write cannot carry the input>"
}

// markerPattern builds the pattern that matches [Rule.MarkerForm] for one
// rule. Requiring the reason to start and end with a letter is what stops
// a mention of the marker from acting as one, which is the rule the
// direct-SQL gate and the affected-rows gate use for their own
// exemptions.
func markerPattern(rule Rule) *regexp.Regexp {
	return regexp.MustCompile(
		regexp.QuoteMeta(rule.Marker) + `:[ \t]*` + regexp.QuoteMeta(rule.Name) +
			`[ \t]*not-applicable[ \t]*—[ \t]*[A-Za-z][^\n]*[A-Za-z]`)
}

// Rules are the preconditions held across both transports.
//
// The table states rules, not call sites. Which operations and tools are
// held to each one is derived from the SQL and the call graph, so the
// only thing that goes stale here is a rule that stops existing — and
// that fails too, because its enforcer stops being declared.
var Rules = []Rule{
	{
		Name:    "chronology",
		Table:   "calendar_events",
		Columns: []string{"start_at", "end_at"},
		Enforcers: []string{
			modulePath + "/internal/calendarrules.RequireEventChronology",
		},
		Marker: "calendar-precondition",
		Why: "an inverted window reaches chk_calendar_events_chronology, " +
			"which the transport cannot attribute and reports as a server error " +
			"instead of the 422 that says which way round the window goes",
	},
	{
		Name:    "all-day-bounds",
		Table:   "calendar_events",
		Columns: []string{"all_day"},
		Enforcers: []string{
			modulePath + "/internal/calendarrules.NormalizeAllDayBounds",
		},
		Marker: "calendar-precondition",
		Why: "an all-day row is a date, stored as UTC midnight; a transport " +
			"that writes the instant it was handed puts the same event on a " +
			"different square for readers in another zone",
	},
	{
		Name:    "date-order",
		Table:   "tasks",
		Columns: []string{"due_on", "started_on"},
		Enforcers: []string{
			modulePath + "/internal/taskrules.DateOrder",
		},
		Marker: "task-precondition",
		Why: "a due date earlier than the start date is a task nobody can " +
			"act on, and nothing in the database refuses the pair — the rule " +
			"is a plain function call, so a write path that does not make it " +
			"stores the inversion",
	},
	{
		Name:    "write-time-embedding",
		Table:   "tasks",
		Columns: []string{"title", "description"},
		Enforcers: []string{
			modulePath + "/internal/ai/embed.RefreshTaskAfterCommit",
		},
		Marker: "task-precondition",
		Why: "search is served from stored embeddings, so a path that " +
			"changes a task's text without refreshing its embedding leaves " +
			"the task findable only under text it no longer has",
	},
	{
		Name:    "description-history",
		Table:   "tasks",
		Columns: []string{"description"},
		Enforcers: []string{
			modulePath + "/internal/taskdesc.Snapshot",
		},
		Marker: "task-precondition",
		Why: "the version history is the only copy of a body the task no " +
			"longer carries, so a path that overwrites the description " +
			"without appending it destroys the one thing a restore could " +
			"have returned to",
	},
}

// Statement is one named statement in sql/queries, normalised.
type Statement struct {
	// Name is the sqlc query name, which is also the generated method
	// name a caller invokes.
	Name string
	// Path and Line locate its header.
	Path string
	Line int
	// SQL is the statement with comments stripped, lowercased and its
	// whitespace collapsed.
	SQL string
}

// Location renders the statement's position for a failure message.
func (s Statement) Location() string {
	return fmt.Sprintf("%s:%d", s.Path, s.Line)
}

// WritesColumn reports whether the statement writes the named column of
// the named table.
//
// Only a write counts. A SELECT that filters on start_at reads the
// window; it cannot store an inverted one, and holding it to the rule
// would put every list endpoint in scope of a check about input.
func (s Statement) WritesColumn(table, column string) bool {
	switch head(s.SQL) {
	case "insert":
		cols, ok := insertColumns(s.SQL, table)
		if !ok {
			return false
		}
		for _, c := range cols {
			if c == column {
				return true
			}
		}
		return false
	case "update":
		set, ok := setClause(s.SQL, table)
		if !ok {
			return false
		}
		return assignsColumn(set, column)
	default:
		return false
	}
}

// Sinks returns the statements that write any of the rule's columns on
// the rule's table.
func Sinks(statements []Statement, rule Rule) map[string]Statement {
	out := map[string]Statement{}
	for _, s := range statements {
		for _, col := range rule.Columns {
			if s.WritesColumn(rule.Table, col) {
				out[s.Name] = s
				break
			}
		}
	}
	return out
}

var headerPattern = regexp.MustCompile(`^--\s*name:\s*(\S+)\s+:(\S+)`)

// RepoRoot returns the repository root, found by walking up from the
// caller's working directory to the go.work that defines the workspace.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.work")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("precondition: go.work not found above the working directory")
		}
		dir = parent
	}
}

// Statements reads every named statement under sql/queries.
func Statements(root string) ([]Statement, error) {
	var out []Statement
	err := filepath.WalkDir(filepath.Join(root, "sql", "queries"),
		func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
				return nil
			}
			raw, readErr := os.ReadFile(path) //#nosec G304,G122 -- repository path walked at test time
			if readErr != nil {
				return readErr
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			out = append(out, parseQueryFile(filepath.ToSlash(rel), string(raw))...)
			return nil
		})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// parseQueryFile cuts a query file at its sqlc `-- name:` headers.
func parseQueryFile(path, text string) []Statement {
	var out []Statement
	var current *Statement
	var body []string

	flush := func() {
		if current == nil {
			return
		}
		current.SQL = normalizeSQL(strings.Join(body, "\n"))
		out = append(out, *current)
		current = nil
		body = nil
	}

	for i, line := range strings.Split(text, "\n") {
		if match := headerPattern.FindStringSubmatch(strings.TrimSpace(line)); match != nil {
			flush()
			current = &Statement{Name: match[1], Path: path, Line: i + 1}
			continue
		}
		if current == nil {
			continue
		}
		body = append(body, line)
	}
	flush()
	return out
}

// normalizeSQL strips comments, lowercases and collapses whitespace so a
// clause can be matched without caring how it was wrapped.
func normalizeSQL(body string) string {
	var out strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if code, _, found := strings.Cut(line, "--"); found {
			line = code
		}
		out.WriteString(line)
		out.WriteString(" ")
	}
	return strings.Join(strings.Fields(strings.ToLower(out.String())), " ")
}

// head returns the leading keyword of a normalised statement.
func head(sql string) string {
	fields := strings.Fields(sql)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// insertColumns returns the column list of an INSERT INTO the named
// table.
func insertColumns(sql, table string) ([]string, bool) {
	rest, ok := afterTable(sql, "insert into", table)
	if !ok {
		return nil, false
	}
	open := strings.Index(rest, "(")
	if open < 0 {
		return nil, false
	}
	depth := 0
	for i := open; i < len(rest); i++ {
		switch rest[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return identifiers(rest[open+1 : i]), true
			}
		}
	}
	return nil, false
}

// setClause returns the assignments of a normalised UPDATE of the named
// table, cut before the predicate. Reading the whole statement instead
// would let a WHERE on the same column read as a write to it.
func setClause(sql, table string) (string, bool) {
	rest, ok := afterTable(sql, "update", table)
	if !ok {
		return "", false
	}
	at := setKeyword.FindStringIndex(rest)
	if at == nil {
		return "", false
	}
	rest = rest[at[1]:]
	if end := whereKeyword.FindStringIndex(rest); end != nil {
		return rest[:end[0]], true
	}
	return rest, true
}

// afterTable returns what follows "<verb> <table>" in a normalised
// statement, and whether the statement addresses that table at all.
//
// The name has to end where the table's name ends: calendar_events and
// calendar_event_attendees share a prefix, and a rule about one holding
// writes to the other would report a column the statement never wrote.
func afterTable(sql, verb, table string) (string, bool) {
	target := verb + " " + table
	if !strings.HasPrefix(sql, target) {
		return "", false
	}
	rest := sql[len(target):]
	if rest != "" && isIdentifierByte(rest[0]) {
		return "", false
	}
	return rest, true
}

// isIdentifierByte reports whether the byte can continue a normalised SQL
// identifier.
func isIdentifierByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

// assignsColumn reports whether the SET clause assigns to the column,
// rather than merely mentioning it — the right-hand side of a COALESCE
// names the column it preserves, and preserving a value is not writing
// one.
func assignsColumn(set, column string) bool {
	pattern := regexp.MustCompile(`(^|[^a-z0-9_])` + regexp.QuoteMeta(column) + `\s*=`)
	return pattern.MatchString(set)
}

// identifiers splits a comma-separated column list.
func identifiers(list string) []string {
	var out []string
	for _, part := range strings.Split(list, ",") {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, name)
		}
	}
	return out
}

var (
	setKeyword   = regexp.MustCompile(`\bset\b`)
	whereKeyword = regexp.MustCompile(`\bwhere\b`)
)

// goFile is one parsed source file together with what its identifiers
// resolve to.
type goFile struct {
	path string
	file *ast.File
	// importPath is the package the file belongs to, so two packages
	// that share a short name are never merged.
	importPath string
	// imports maps the name the file refers to an import by onto that
	// import's path.
	imports map[string]string
}

// Entry is one request path: an MCP tool or a REST operation, named by
// the function the transport enters it at.
type Entry struct {
	// Surface is "MCP tool" or "REST operation", for the failure text.
	Surface string
	// Name is the tool name or the OperationID.
	Name string
	// Symbol is the package-qualified entry function.
	Symbol string
	// Pos is where the registration sits.
	Pos string
}

// Source is the parsed flow-api tree, indexed for reachability.
type Source struct {
	fset  *token.FileSet
	files []*goFile
	// funcs maps "importPath.Function" onto its declaration.
	funcs map[string]*funcDecl
	// Entries are the derived request paths, in a stable order.
	Entries []Entry
}

type funcDecl struct {
	owner *goFile
	decl  *ast.FuncDecl
}

// Parse reads every hand-written Go file under apps/flow-api/internal and
// indexes its package-level functions, its MCP registrations and its REST
// registrations.
//
// Generated queriers are skipped: they declare the statement methods
// rather than call them, so indexing them would make every caller look
// like a callee of every other.
func Parse(root string) (*Source, error) {
	src := &Source{fset: token.NewFileSet(), funcs: map[string]*funcDecl{}}
	base := filepath.Join(root, "apps", "flow-api", "internal")

	packageNames := map[string]string{}
	var paths []string
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "generated" {
				return fs.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)

	for _, path := range paths {
		parsed, perr := parser.ParseFile(src.fset, path, nil, parser.ParseComments)
		if perr != nil {
			return nil, fmt.Errorf("parse %s: %w", path, perr)
		}
		rel, relErr := filepath.Rel(filepath.Join(root, "apps", "flow-api"), filepath.Dir(path))
		if relErr != nil {
			return nil, relErr
		}
		importPath := modulePath + "/" + filepath.ToSlash(rel)
		packageNames[importPath] = parsed.Name.Name
		src.files = append(src.files, &goFile{path: path, file: parsed, importPath: importPath})
	}

	for _, f := range src.files {
		f.imports = fileImports(f.file, packageNames)
		for _, decl := range f.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Recv != nil {
				continue
			}
			src.funcs[f.importPath+"."+fn.Name.Name] = &funcDecl{owner: f, decl: fn}
		}
	}

	src.Entries = append(src.collectMCPTools(), src.collectRESTOperations()...)
	sort.Slice(src.Entries, func(i, j int) bool {
		if src.Entries[i].Surface != src.Entries[j].Surface {
			return src.Entries[i].Surface < src.Entries[j].Surface
		}
		return src.Entries[i].Name < src.Entries[j].Name
	})
	return src, nil
}

// fileImports maps the name a file refers to each import by onto that
// import's path. An unaliased in-tree import binds the package's declared
// name, which is not always its directory name.
func fileImports(file *ast.File, packageNames map[string]string) map[string]string {
	out := map[string]string{}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		local := ""
		switch {
		case imp.Name != nil:
			local = imp.Name.Name
		case packageNames[path] != "":
			local = packageNames[path]
		default:
			local = path[strings.LastIndex(path, "/")+1:]
		}
		if local == "_" || local == "." {
			continue
		}
		out[local] = path
	}
	return out
}

// collectMCPTools reads the tool registry out of the source rather than
// reflecting on the built table.
//
// Registration wraps each run function so the declared floor is bound to
// the call, so the value in the table is a closure and reflection can
// only report the wrapper. The declaration names the implementation,
// which is what a call-graph walk has to be entered at.
func (s *Source) collectMCPTools() []Entry {
	var out []Entry
	for _, f := range s.files {
		ast.Inspect(f.file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "register" {
				return true
			}
			lit, ok := call.Args[1].(*ast.CompositeLit)
			if !ok {
				return true
			}
			name, run := "", ""
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				switch key.Name {
				case "name":
					if v, ok := kv.Value.(*ast.BasicLit); ok && v.Kind == token.STRING {
						name = strings.Trim(v.Value, `"`)
					}
				case "run":
					if v, ok := kv.Value.(*ast.Ident); ok {
						run = v.Name
					}
				}
			}
			if name == "" || run == "" {
				return true
			}
			out = append(out, Entry{
				Surface: "MCP tool",
				Name:    name,
				Symbol:  f.importPath + "." + run,
				Pos:     s.fset.Position(call.Pos()).String(),
			})
			return true
		})
	}
	return out
}

// collectRESTOperations reads the operation inventory out of the
// huma.Register calls, pairing each OperationID with the function the
// router hands it.
func (s *Source) collectRESTOperations() []Entry {
	var out []Entry
	for _, f := range s.files {
		ast.Inspect(f.file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isHumaRegister(call) || len(call.Args) < 3 {
				return true
			}
			id := operationIDArg(call.Args[1])
			if id == "" {
				return true
			}
			symbol, ok := rootSymbol(call.Args[2], f)
			if !ok {
				return true
			}
			out = append(out, Entry{
				Surface: "REST operation",
				Name:    id,
				Symbol:  symbol,
				Pos:     s.fset.Position(call.Pos()).String(),
			})
			return true
		})
	}
	return out
}

// Reach walks the call graph out of an entry and returns the
// package-qualified functions it reaches, plus every call name seen along
// the way — which is how a statement performed through a generated query
// method is matched.
func (s *Source) Reach(symbol string) (qualified, names map[string]bool) {
	qualified = map[string]bool{}
	names = map[string]bool{}
	visited := map[string]bool{}
	queue := []string{symbol}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		fn := s.funcs[cur]
		if fn == nil {
			continue
		}
		ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch callee := call.Fun.(type) {
			case *ast.Ident:
				names[callee.Name] = true
				sym := fn.owner.importPath + "." + callee.Name
				qualified[sym] = true
				queue = append(queue, sym)
			case *ast.SelectorExpr:
				names[callee.Sel.Name] = true
				qualifier, ok := callee.X.(*ast.Ident)
				if !ok {
					return true
				}
				path, ok := fn.owner.imports[qualifier.Name]
				if !ok {
					// A method on a value, not a package function. Its
					// name is recorded above; crediting it to a
					// same-named function here is how a rule would
					// appear to run without doing so.
					return true
				}
				sym := path + "." + callee.Sel.Name
				qualified[sym] = true
				queue = append(queue, sym)
			}
			return true
		})
	}
	return qualified, names
}

// Declares reports whether the tree defines the named function. An
// enforcer that no longer exists is a rule nothing can satisfy, which has
// to fail rather than pass vacuously.
func (s *Source) Declares(symbol string) bool {
	return s.funcs[symbol] != nil
}

// Marked reports whether the entry carries a marker for the rule, in its
// doc comment or anywhere in its body.
func (s *Source) Marked(symbol string, rule Rule) bool {
	fn := s.funcs[symbol]
	if fn == nil {
		return false
	}
	pattern := markerPattern(rule)
	start := fn.decl.Pos()
	if fn.decl.Doc != nil {
		start = fn.decl.Doc.Pos()
	}
	for _, group := range fn.owner.file.Comments {
		for _, c := range group.List {
			if c.Pos() >= start && c.End() <= fn.decl.End() && pattern.MatchString(c.Text) {
				return true
			}
		}
	}
	return false
}

// Finding is one thing the check has to say about an entry.
type Finding struct {
	// Entry is the request path the finding is about.
	Entry Entry
	// Rule is the rule's name.
	Rule string
	// Via is the statement that put the entry in scope, empty for a
	// marker that covers nothing.
	Via Statement
	// Kind says which of the two failures this is.
	Kind FindingKind
}

// FindingKind distinguishes a rule that is not applied from a marker that
// exempts nothing.
type FindingKind int

const (
	// Unenforced is an entry that writes the rule's columns and reaches
	// no enforcer, with no marker to account for it.
	Unenforced FindingKind = iota
	// StaleMarker is a marker on an entry that either writes none of the
	// rule's columns or applies the rule anyway. Reporting it is what
	// stops the exemption table from outliving the code it exempts.
	StaleMarker
)

// InScope is which entries a rule was actually held against, keyed by
// rule name and then by surface. An empty bucket is a rule that was
// checked against nothing, which the caller has to treat as a failure
// rather than as a pass.
type InScope map[string]map[string][]Entry

// Check holds every derived entry to every rule and returns what it
// found, together with the scope it covered.
//
// Returning the scope is not decoration. The whole failure mode of a
// derived check is that the derivation stops matching — a renamed column,
// a registry read that returns nothing — and then it passes because it
// looked at nothing. The caller asserts on the scope for that reason.
func Check(src *Source, statements []Statement, rules []Rule) ([]Finding, InScope) {
	var findings []Finding
	scope := InScope{}

	for _, rule := range rules {
		sinks := Sinks(statements, rule)
		scope[rule.Name] = map[string][]Entry{}
		for _, entry := range src.Entries {
			qualified, called := src.Reach(entry.Symbol)
			via, writes := firstSink(called, sinks)
			marked := src.Marked(entry.Symbol, rule)

			if !writes {
				if marked {
					findings = append(findings, Finding{Entry: entry, Rule: rule.Name, Kind: StaleMarker})
				}
				continue
			}
			scope[rule.Name][entry.Surface] = append(scope[rule.Name][entry.Surface], entry)

			if reachesAny(qualified, rule.Enforcers) {
				if marked {
					findings = append(findings, Finding{Entry: entry, Rule: rule.Name, Via: via, Kind: StaleMarker})
				}
				continue
			}
			if marked {
				continue
			}
			findings = append(findings, Finding{Entry: entry, Rule: rule.Name, Via: via, Kind: Unenforced})
		}
	}
	return findings, scope
}

// firstSink returns the sink statement an entry calls, in a stable order
// so a failure names the same statement on every run.
func firstSink(called map[string]bool, sinks map[string]Statement) (Statement, bool) {
	names := make([]string, 0, len(sinks))
	for name := range sinks {
		if called[name] {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return Statement{}, false
	}
	sort.Strings(names)
	return sinks[names[0]], true
}

// reachesAny reports whether any enforcer is in the reached set.
func reachesAny(qualified map[string]bool, enforcers []string) bool {
	for _, e := range enforcers {
		if qualified[e] {
			return true
		}
	}
	return false
}

// isHumaRegister reports whether the call is huma.Register.
func isHumaRegister(call *ast.CallExpr) bool {
	fun := call.Fun
	if idx, ok := fun.(*ast.IndexExpr); ok {
		fun = idx.X
	}
	if idx, ok := fun.(*ast.IndexListExpr); ok {
		fun = idx.X
	}
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Register" {
		return false
	}
	qualifier, ok := sel.X.(*ast.Ident)
	return ok && qualifier.Name == "huma"
}

// operationIDArg reads the OperationID out of a huma.Operation literal.
func operationIDArg(arg ast.Expr) string {
	lit, ok := arg.(*ast.CompositeLit)
	if !ok {
		return ""
	}
	if sel, ok := lit.Type.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Operation" {
		return ""
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "OperationID" {
			continue
		}
		if val, ok := kv.Value.(*ast.BasicLit); ok && val.Kind == token.STRING {
			return strings.Trim(val.Value, `"`)
		}
	}
	return ""
}

// rootSymbol resolves a handler expression to the package-level function
// at its root: Create(deps) in package tasks resolves to that package's
// Create, and calendars.CreateEvent(deps) to the calendars one.
func rootSymbol(expr ast.Expr, f *goFile) (string, bool) {
	switch e := expr.(type) {
	case *ast.CallExpr:
		return rootSymbol(e.Fun, f)
	case *ast.Ident:
		return f.importPath + "." + e.Name, true
	case *ast.SelectorExpr:
		qualifier, ok := e.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		path, ok := f.imports[qualifier.Name]
		if !ok {
			return "", false
		}
		return path + "." + e.Sel.Name, true
	default:
		return "", false
	}
}
