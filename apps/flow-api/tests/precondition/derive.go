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
//	sink        a place the rule's columns are written on the rule's
//	            table: a statement in sql/queries that INSERTs into or
//	            UPDATEs it, and every hand-written function whose body
//	            builds such a write as a Go string literal. Both forms are
//	            read because reading only the first covers exactly the
//	            paths that follow the convention SQL lives in sql/queries
//	            — coverage that falls as more is written inline. A write a
//	            reader cannot attribute to a column is reported rather
//	            than skipped; see [UnattributableWrite].
//	entry       an MCP tool's run function, read out of the register
//	            calls, or a REST operation's handler, read out of the
//	            huma.Register calls. Neither is a list anyone maintains:
//	            a tool added tomorrow is an entry tomorrow.
//	in scope    every sink an entry reaches, each on its own. An entry
//	            with two of them answers for two writes.
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
// A marker exempts one write. That is the granularity the thing being
// asserted has: a reason is written about a particular write, having
// looked at it, and it says nothing about a write nobody had seen. So a
// marker naming no sink is good only while the entry has one — the case
// where there is nothing for a name to distinguish — and an entry that
// gains a second sink fails until a marker names it. Without that, adding
// a write to an entry that already carried a marker would be covered by
// the reason written for the write beside it, silently, and the check
// would report a decision nobody made.
//
// Call edges are literal. A call through an imported package name is an
// edge to that package's function and nothing more; a method call on a
// value contributes its name — which is how a statement is matched — but
// no edge. Neither stands in for the other, so a helper is never credited
// to a package-level function that happens to share a name with a
// generated query method, and a package-level function is never credited
// with the statement whose name it happens to carry.
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
//
// This is the form for an entry that writes the rule's columns through one
// sink. Where it reaches more than one, each takes its own marker naming
// it; see [Rule.MarkerFormFor].
func (r Rule) MarkerForm() string {
	return r.Marker + ": " + r.Name +
		" not-applicable — <why this write cannot carry the input>"
}

// MarkerFormFor renders the exemption for one sink of this rule.
//
// A marker exempts one write, not an entry. An entry that reaches two
// sinks has made two decisions, and one reason cannot stand for both: the
// sink added second would inherit an exemption written before it existed,
// which is the exemption reading as though it had been reasoned about when
// nobody had seen the write it now covers. Naming the sink is what makes
// the second write fail until somebody states a reason for it.
//
// The unnamed form stays valid for an entry with a single sink, where
// there is nothing for a name to distinguish — and it stops being valid
// the moment a second sink appears, which is the case this exists for.
func (r Rule) MarkerFormFor(sink WriteSink) string {
	return r.Marker + ": " + r.Name + " not-applicable for " + sink.Key() +
		" — <why this write cannot carry the input>"
}

// markerPattern builds the pattern that matches [Rule.MarkerForm] for one
// rule. Requiring the reason to start and end with a letter is what stops
// a mention of the marker from acting as one, which is the rule the
// direct-SQL gate and the affected-rows gate use for their own
// exemptions.
//
// It does not match [Rule.MarkerFormFor]: the sink name sits between
// "not-applicable" and the dash this requires to follow it directly, so a
// marker written about one sink is never read as the entry's blanket one.
func markerPattern(rule Rule) *regexp.Regexp {
	return regexp.MustCompile(
		regexp.QuoteMeta(rule.Marker) + `:[ \t]*` + regexp.QuoteMeta(rule.Name) +
			`[ \t]*not-applicable[ \t]*—[ \t]*[A-Za-z][^\n]*[A-Za-z]`)
}

// sinkMarkerPattern builds the pattern that matches [Rule.MarkerFormFor],
// capturing the sink the marker names.
func sinkMarkerPattern(rule Rule) *regexp.Regexp {
	return regexp.MustCompile(
		regexp.QuoteMeta(rule.Marker) + `:[ \t]*` + regexp.QuoteMeta(rule.Name) +
			`[ \t]*not-applicable[ \t]+for[ \t]+(\S+)[ \t]*—[ \t]*[A-Za-z][^\n]*[A-Za-z]`)
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
	{
		Name:    "description-mentions",
		Table:   "tasks",
		Columns: []string{"description"},
		Enforcers: []string{
			modulePath + "/internal/mentions.SyncTaskDescription",
		},
		Marker: "task-precondition",
		Why: "the mentions table caches who a body names, so a path that " +
			"stores a description without re-deriving it leaves people " +
			"notified about a mention nothing says any more, and the ones " +
			"the new body names not notified at all",
	},
	{
		Name:    "comment-mentions",
		Table:   "comments",
		Columns: []string{"body"},
		Enforcers: []string{
			modulePath + "/internal/mentions.SyncComment",
		},
		Marker: "comment-precondition",
		Why: "the mentions table caches who a body names, so a path that " +
			"stores a comment without re-deriving it leaves people notified " +
			"about a mention nothing says any more, and the ones the new " +
			"body names not notified at all",
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
	fset *token.FileSet
	// root is the repository root the tree was read under, so a position
	// can be reported relative to it.
	root  string
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
	src := &Source{fset: token.NewFileSet(), root: root, funcs: map[string]*funcDecl{}}
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
				qualifier, isIdent := callee.X.(*ast.Ident)
				path, imported := "", false
				if isIdent {
					path, imported = fn.owner.imports[qualifier.Name]
				}
				if !imported {
					// A method on a value, not a package function. Its
					// name is what matches a statement performed through
					// a generated querier; crediting it to a same-named
					// package function as well is how a rule would appear
					// to run without doing so.
					names[callee.Sel.Name] = true
					return true
				}
				// A call to an imported package's function. It is an edge
				// to that function and nothing else: a generated querier
				// is a value, so no statement is ever issued this way,
				// and recording the name would let a package function
				// that happens to share a statement's name stand in for
				// the write.
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

// Marker is one exemption written at an entry, together with the sink it
// was written about.
type Marker struct {
	// Sink is the sink the marker names, empty for one that names none.
	Sink string
	// Line is where the marker is written, so a failure about it sends the
	// reader to the comment rather than to the entry.
	Line int
}

// Markers returns the entry's exemptions for one rule, in source order,
// read from its doc comment or anywhere in its body.
//
// Both forms are read and kept apart. A marker naming a sink carries that
// name; a marker naming none carries the empty string and is only good for
// an entry with a single sink, which is what [pairMarkers] decides.
func (s *Source) Markers(symbol string, rule Rule) []Marker {
	fn := s.funcs[symbol]
	if fn == nil {
		return nil
	}
	bare := markerPattern(rule)
	named := sinkMarkerPattern(rule)
	start := fn.decl.Pos()
	if fn.decl.Doc != nil {
		start = fn.decl.Doc.Pos()
	}

	var out []Marker
	for _, group := range fn.owner.file.Comments {
		for _, c := range group.List {
			if c.Pos() < start || c.End() > fn.decl.End() {
				continue
			}
			if m := named.FindStringSubmatch(c.Text); m != nil {
				out = append(out, Marker{Sink: m[1], Line: s.fset.Position(c.Pos()).Line})
				continue
			}
			if bare.MatchString(c.Text) {
				out = append(out, Marker{Line: s.fset.Position(c.Pos()).Line})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// Finding is one thing the check has to say about an entry.
type Finding struct {
	// Entry is the request path the finding is about.
	Entry Entry
	// Rule is the rule's name.
	Rule string
	// Via is the sink the finding is about — a named statement or a write
	// built as a Go string literal — and is zero for a marker that covers
	// nothing.
	Via WriteSink
	// Marker is the exemption a StaleMarker finding is about, and is zero
	// for an Unenforced one.
	Marker Marker
	// Reason says how a stale marker failed to exempt anything, so the
	// failure names the mistake and not only the entry.
	Reason string
	// Kind says which of the two failures this is.
	Kind FindingKind
}

// FindingKind distinguishes a rule that is not applied from a marker that
// exempts nothing.
type FindingKind int

const (
	// Unenforced is a write that reaches no enforcer, with no marker to
	// account for it. It is one finding per write and not per entry: an
	// entry with two sinks has two of them to answer for.
	Unenforced FindingKind = iota
	// StaleMarker is a marker that exempts none of the entry's writes.
	// Reporting it is what stops the exemption table from outliving the
	// code it exempts.
	StaleMarker
)

// The ways a marker can exempt nothing. Each reads to a later reader as
// though the write it stands over was considered, which is the thing the
// marker exists to make impossible.
const (
	// markerCoversNoWrite is a marker on an entry that writes none of the
	// rule's columns: the write it was about has moved or gone.
	markerCoversNoWrite = "the entry writes none of the rule's columns"
	// markerCoversAnEnforcedWrite is a marker on an entry that applies the
	// rule anyway. Exempting a write that needs no exemption is the same
	// defect as omitting one that does.
	markerCoversAnEnforcedWrite = "the entry reaches the rule's enforcement, so there is nothing to exempt"
	// markerIsAmbiguous is a marker naming no sink on an entry that
	// reaches more than one. With two writes in reach a reason that does
	// not say which it is about states nothing a reader can check, and the
	// write added second would inherit it.
	markerIsAmbiguous = "the entry writes the rule's columns through more than one sink, " +
		"so a marker naming none of them does not say which write it is about"
	// markerNamesNoSink is a marker naming a sink the entry does not
	// reach.
	markerNamesNoSink = "the entry reaches no sink by that name"
	// markerNamesTwoSinks is a marker whose name fits two of the entry's
	// sinks, so it settles neither.
	markerNamesTwoSinks = "the name fits more than one of the entry's sinks; name the sink by its key"
	// markerRepeatsAnExemption is a marker for a sink an earlier marker
	// already covers. One marker covers one write in both directions, so
	// the surplus reason stands for no decision.
	markerRepeatsAnExemption = "the write it names is already exempted by an earlier marker"
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
// Check is per write and not per entry. An entry that writes the rule's
// columns through two sinks is two decisions, and answering for it once
// would let the second write ride on whatever was settled for the first —
// which is what happens when a sink is added to an entry that already
// carried a marker.
func Check(src *Source, statements []Statement, rules []Rule) ([]Finding, InScope) {
	var findings []Finding
	scope := InScope{}

	for _, rule := range rules {
		sinks := Sinks(src, statements, rule)
		scope[rule.Name] = map[string][]Entry{}
		for _, entry := range src.Entries {
			qualified, called := src.Reach(entry.Symbol)
			reached := reachedSinks(qualified, called, sinks)
			markers := src.Markers(entry.Symbol, rule)

			stale := func(reason string) {
				for _, marker := range markers {
					findings = append(findings, Finding{
						Entry:  entry,
						Rule:   rule.Name,
						Marker: marker,
						Reason: reason,
						Kind:   StaleMarker,
					})
				}
			}

			if len(reached) == 0 {
				stale(markerCoversNoWrite)
				continue
			}
			scope[rule.Name][entry.Surface] = append(scope[rule.Name][entry.Surface], entry)

			if reachesAny(qualified, rule.Enforcers) {
				stale(markerCoversAnEnforcedWrite)
				continue
			}

			exempt, unpaired := pairMarkers(reached, markers)
			for _, marker := range unpaired {
				findings = append(findings, Finding{
					Entry:  entry,
					Rule:   rule.Name,
					Marker: marker.Marker,
					Reason: marker.Reason,
					Kind:   StaleMarker,
				})
			}
			for i, sink := range reached {
				if exempt[i] {
					continue
				}
				findings = append(findings, Finding{
					Entry: entry, Rule: rule.Name, Via: sink, Kind: Unenforced,
				})
			}
		}
	}
	return findings, scope
}

// reachedSinks returns every sink an entry reaches, in a stable order so a
// failure names them the same way on every run.
//
// The two kinds are matched differently and deliberately so. A statement
// is performed through a method on a generated querier, which the call
// graph can only match by name; a write site in the Go tree is matched by
// its package-qualified symbol, so a same-named method on some other
// value is never mistaken for it.
func reachedSinks(qualified, called map[string]bool, sinks map[string]WriteSink) []WriteSink {
	keys := make([]string, 0, len(sinks))
	for key, sink := range sinks {
		if sink.reachedBy(qualified, called) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := make([]WriteSink, 0, len(keys))
	for _, key := range keys {
		out = append(out, sinks[key])
	}
	return out
}

// unpairedMarker is a marker that exempted no write, with the reason it
// exempted none.
type unpairedMarker struct {
	Marker Marker
	Reason string
}

// pairMarkers gives each marker the write it was written about, and
// reports the ones that were written about nothing.
//
// A marker naming a sink takes that sink. A marker naming none takes the
// entry's only sink, and takes nothing where the entry has more than one:
// naming no sink is unambiguous exactly while there is one write to be
// about, so an entry that gains a second one has to say which. Taking
// rather than merely matching is what keeps one marker to one write in
// both directions — two markers for one sink leaves the second standing
// for no decision, and it is reported rather than ignored.
func pairMarkers(reached []WriteSink, markers []Marker) (exempt []bool, unpaired []unpairedMarker) {
	exempt = make([]bool, len(reached))
	for _, marker := range markers {
		report := func(reason string) {
			unpaired = append(unpaired, unpairedMarker{Marker: marker, Reason: reason})
		}
		if marker.Sink == "" {
			switch {
			case len(reached) > 1:
				report(markerIsAmbiguous)
			case exempt[0]:
				report(markerRepeatsAnExemption)
			default:
				exempt[0] = true
			}
			continue
		}
		var hits []int
		for i, sink := range reached {
			if sink.Key() == marker.Sink || sink.Name == marker.Sink {
				hits = append(hits, i)
			}
		}
		switch {
		case len(hits) == 0:
			report(markerNamesNoSink)
		case len(hits) > 1:
			report(markerNamesTwoSinks)
		case exempt[hits[0]]:
			report(markerRepeatsAnExemption)
		default:
			exempt[hits[0]] = true
		}
	}
	return exempt, unpaired
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
