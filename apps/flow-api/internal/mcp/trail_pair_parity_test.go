// Package mcp_test cross-transport trail checks.
//
// A tool and the request operation it answers for write the same change, so
// they leave the same trail: a row in `events` under one kind and a row in
// `audit_logs` under one action. Everything downstream reads the trail
// rather than the change — the notification fan-out routes on the kind, the
// timeline lists by it, the live stream taps it, and an administrator
// queries audit_logs by action name. Two transports filing one change under
// two names therefore do not disagree loudly; one of them simply reaches
// nobody, and the write still returns success.
//
// So the pair of names is derived from both sides and compared. Neither
// side is written down here: an expected value stated in this file would be
// a third place to keep correct, and it would be wrong in exactly the case
// this exists to catch, because whoever changed one side would change it
// here too.
//
//	join        mcpRESTCounterparts, which already pairs each tool with the
//	            OperationID of the operation performing the same change and
//	            is already held exhaustive in both directions
//	tool side   the kinds and actions named anywhere reachable, inside this
//	            package, from the function the tool's registration names
//	rest side   the same, from the function the operation's huma.Register
//	            call names, inside that handler's package
//	halves      the kind and the action are compared separately, because a
//	            change is routinely filed on the timeline by one piece of
//	            code and in audit_logs by another
//
// The tools listed in mcpToolsWithoutRESTOperation are out of scope by
// construction rather than by exemption: they are absent from the join key,
// so nothing here mentions them, and a tool moved out of that list into
// mcpRESTCounterparts becomes covered without an edit to this file.
//
// One shape of unread half is not a gap at all: both sides handing the half
// to the same fully-qualified function. Neither value was read, and neither
// has to be — whatever that function files, both file it, and they cannot
// come apart unless it changes under both at once. That is a firmer answer
// than two literals that agree today, because it leaves one place where the
// value can be edited rather than two. It is recorded as agreement.
//
// What is left is unresolved: the value is assembled at run time, or one
// side hands the change to a package the walk stops at while the other does
// not. Neither is silence. Silence read as agreement is how a check like
// this passes while describing nothing, and a blind spot read as silence is
// how it invents a finding against code that is doing the right thing.
//
// Unresolved halves are counted and printed, never failed. Requiring a
// marker on each would put one on every change written through a shared
// transactional helper, and a marker that many changes carry teaches people
// to write markers rather than to look. What fails is a divergence, and a
// half one transport files where the other files nothing.
package mcp_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// modulePrefix is the import-path prefix every package in this repository
// shares. Stripping it turns an import path into the directory holding it,
// which is how a handler named through its package qualifier is found.
const modulePrefix = "github.com/libraz/nodate-flow/"

// mcpPackageDir is this package, named as a repository directory so the
// walk reaches it the same way it reaches a handler package.
var mcpPackageDir = filepath.ToSlash(filepath.Join("apps", "flow-api", "internal", "mcp"))

// httpTreeRel is the tree the request operations are registered in. Both
// the per-feature register.go files and the router that mounts the calendar
// sub-API live under it, and an operation registered in either is reached
// the same way.
var httpTreeRel = filepath.Join("apps", "flow-api", "internal", "http")

// sharedKindsRel is the file the event kinds are declared in. Kinds are
// compared by the string consumers key on rather than by the Go constant
// naming it: flow-api re-exports the shared constants, so one kind is
// reachable under two spellings, and a rename that keeps the string is not
// a divergence.
var sharedKindsRel = filepath.Join("packages", "go-shared", "eventbus", "kinds.go")

// eventbusQualifiers are the package identifiers the kind constants are
// reached through — the shared package and the flow-api re-export.
var eventbusQualifiers = map[string]bool{"eventbus": true, "sharedbus": true}

// namedValue is one value the walk read, with the place it was read at so a
// failure and the control can both point at it.
type namedValue struct {
	value string
	file  string
	line  int
}

func (v namedValue) where() string { return fmt.Sprintf("%s:%d", v.file, v.line) }

// trail is what one side of a pair files its change under.
//
// unread holds the places a value was assembled rather than named, and
// delegates the calls that hand the change to another package. Both are
// kept apart from the values, because a side whose value was not read is
// not a side that named nothing, and reporting the two as the same thing is
// how a reading turns its own blind spot into a finding.
type trail struct {
	kinds     []namedValue
	actions   []namedValue
	unread    map[bool][]namedValue // keyed the way the halves are: true is the kind
	delegates []funcRef
}

// isKind and isAction name the half of a trail a value belongs to, so the
// kind and the action are carried apart from each other all the way
// through.
const (
	isKind   = true
	isAction = false
)

func (t trail) kindValues() []string   { return sortedValues(t.kinds) }
func (t trail) actionValues() []string { return sortedValues(t.actions) }

func sortedValues(vs []namedValue) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.value)
	}
	sort.Strings(out)
	return out
}

func joinValues(vs []string) string {
	if len(vs) == 0 {
		return "nothing"
	}
	return strings.Join(vs, ", ")
}

// describe renders unreadable values with the place they were read, so a
// pair reported as unresolved says which expression could not be read.
func describe(vs []namedValue) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, fmt.Sprintf("%s (%s)", v.value, v.where()))
	}
	sort.Strings(out)
	return out
}

// funcRef locates a function: the repository directory of the package
// declaring it, and its name.
type funcRef struct{ dir, name string }

func (f funcRef) String() string { return f.dir + "." + f.name }

// packageTrail holds one package's reading: what each function names
// directly, and the functions it calls — inside the package or in another
// one this repository declares.
//
// The calls matter as much as the namings, because neither transport
// writes its own event row. A tool hands the pair to the recorder every
// tool shares, which is in the tool's own package: those calls are walked,
// and the value is read. Half the request handlers hand the change to a
// helper that writes task and event together in one transaction, which is
// not: those calls are kept as delegations, and the value is not read.
// A reading that stopped at the function's own body would have neither, and
// would report both as filing nothing.
type packageTrail struct {
	direct map[string]trail
	calls  map[string][]funcRef
}

// kindRegistrySuffix marks the packages that declare the kinds rather than
// file changes under them. They are never treated as somewhere a change was
// filed: the registry names every kind there is, so counting a call into it
// as delegation would make every side look like it wrote something.
const kindRegistrySuffix = "/eventbus"

// sourceTrail reads packages on demand and answers what one function files
// its change under.
type sourceTrail struct {
	root  string
	kinds map[string]string
	pkgs  map[string]packageTrail
}

func newSourceTrail(root string, kinds map[string]string) *sourceTrail {
	return &sourceTrail{root: root, kinds: kinds, pkgs: map[string]packageTrail{}}
}

// add registers a package the caller read itself, which is how the control
// supplies source it states in full instead of source read off disk.
func (s *sourceTrail) add(dir string, pkg packageTrail) { s.pkgs[dir] = pkg }

// packageAt returns the reading of one repository directory, loading it the
// first time it is asked for. A directory holding no Go source is not an
// error: it is a package the walk cannot enter, and it answers as one.
func (s *sourceTrail) packageAt(dir string) (packageTrail, bool) {
	if pkg, loaded := s.pkgs[dir]; loaded {
		return pkg, len(pkg.direct) > 0
	}
	sources, err := readSources(filepath.Join(s.root, dir))
	if err != nil || len(sources) == 0 {
		s.pkgs[dir] = packageTrail{direct: map[string]trail{}, calls: map[string][]funcRef{}}
		return s.pkgs[dir], false
	}
	pkg, err := readPackageTrail(dir, sources, s.kinds)
	if err != nil {
		pkg = packageTrail{direct: map[string]trail{}, calls: map[string][]funcRef{}}
	}
	s.pkgs[dir] = pkg
	return pkg, len(pkg.direct) > 0
}

// resolve returns what entry files its change under, and reports whether
// entry is a function the repository declares. A missing entry is not an
// empty trail: it means the walk was pointed at nothing.
//
// The walk stays inside entry's own package. Following calls out of it
// reaches the right function but reads the wrong values: a shared write
// helper resolves, within its own package, to every kind that package
// files, and the side ends up holding a dozen kinds where it writes one.
// Calls out of the package are recorded as delegations instead, which is
// enough to tell a change filed somewhere the walk does not go from a
// change filed nowhere.
func (s *sourceTrail) resolve(entry funcRef) (trail, bool) {
	pkg, _ := s.packageAt(entry.dir)
	if _, declared := pkg.direct[entry.name]; !declared {
		return trail{}, false
	}

	out := trail{unread: map[bool][]namedValue{}}
	seenKind := map[string]bool{}
	seenAction := map[string]bool{}
	seenDynamic := map[string]bool{}
	seenDelegate := map[funcRef]bool{}
	visited := map[string]bool{}

	var walk func(string)
	walk = func(name string) {
		if visited[name] {
			return
		}
		visited[name] = true
		for _, k := range pkg.direct[name].kinds {
			if !seenKind[k.value] {
				seenKind[k.value] = true
				out.kinds = append(out.kinds, k)
			}
		}
		for _, a := range pkg.direct[name].actions {
			if !seenAction[a.value] {
				seenAction[a.value] = true
				out.actions = append(out.actions, a)
			}
		}
		for _, half := range []bool{isKind, isAction} {
			for _, d := range pkg.direct[name].unread[half] {
				// Keyed by place as well as text: the same expression shape
				// is unreadable in more than one function, and collapsing
				// them would drop the location a reader has to go to.
				key := d.where() + " " + d.value
				if seenDynamic[key] {
					continue
				}
				seenDynamic[key] = true
				out.unread[half] = append(out.unread[half], d)
			}
		}
		for _, callee := range pkg.calls[name] {
			if callee.dir == entry.dir {
				walk(callee.name)
				continue
			}
			if strings.HasSuffix(callee.dir, kindRegistrySuffix) || seenDelegate[callee] {
				continue
			}
			seenDelegate[callee] = true
			out.delegates = append(out.delegates, callee)
		}
	}
	walk(entry.name)
	return out, true
}

// filedThroughTheSame reports whether both sides hand this half of the
// trail to exactly the same function. It is what turns two unread values
// into a conclusion, and it is the only resolution here that reaches one
// without reading either side's value.
//
// That the values went unread does not matter: whatever the shared function
// files, both sides file, and they cannot come apart unless it changes
// under both at once. It is a firmer guarantee than two literals that agree
// today, because it leaves one place where the value can be edited instead
// of two — the comparison this walk cannot make has already been made by
// the code.
//
// It applies only where neither side named a value of its own. A side that
// names one and a side that delegates are doing different things, and
// whether they arrive at the same name is exactly what is not known.
func (s *sourceTrail) filedThroughTheSame(a, b trail, half bool) bool {
	aValues, bValues := a.kinds, b.kinds
	if half == isAction {
		aValues, bValues = a.actions, b.actions
	}
	if len(aValues) > 0 || len(bValues) > 0 {
		return false
	}
	shared := refNames(s.filedElsewhere(a, half))
	return len(shared) > 0 && equalValues(shared, refNames(s.filedElsewhere(b, half)))
}

// filedElsewhere returns the delegations that themselves file a change. It
// is what tells a side that records nothing from one that handed the record
// to a package this walk stops at — a limit on the reading, reported as
// unresolved, rather than the finding a missing record would be.
//
// It answers about one side. Whether that limit becomes a conclusion is
// [sourceTrail.filedThroughTheSame]'s question, and it asks this one twice
// to get there.
//
// The delegate is resolved inside its own package, where the reading is
// good enough to answer whether anything is filed there and not good enough
// to say what. That is the whole claim being made — which is also why a
// delegate whose own value the reading could not make out still counts: it
// files something either way.
func (s *sourceTrail) filedElsewhere(t trail, half bool) []funcRef {
	var out []funcRef
	for _, d := range t.delegates {
		at, ok := s.resolve(d)
		if !ok {
			continue
		}
		values := at.actions
		if half == isKind {
			values = at.kinds
		}
		if len(values) > 0 || len(at.unread[half]) > 0 {
			out = append(out, d)
		}
	}
	return out
}

// readPackageTrail reads one package, given its directory and its files
// keyed by path.
//
// It takes sources rather than reading the directory itself so the control
// can hold the reading against source it states in full, rather than
// against whatever the tree happens to contain.
func readPackageTrail(dir string, sources map[string]string, kinds map[string]string) (packageTrail, error) {
	fset := token.NewFileSet()
	paths := make([]string, 0, len(sources))
	for path := range sources {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	out := packageTrail{direct: map[string]trail{}, calls: map[string][]funcRef{}}
	for _, path := range paths {
		file, err := parser.ParseFile(fset, path, sources[path], 0)
		if err != nil {
			return packageTrail{}, fmt.Errorf("parse %s: %w", path, err)
		}
		imports := importedPackageDirs(file)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := fn.Name.Name
			merged := out.direct[name]
			if merged.unread == nil {
				merged.unread = map[bool][]namedValue{}
			}
			read := readTrail(fset, fn, kinds)
			merged.kinds = append(merged.kinds, read.kinds...)
			merged.actions = append(merged.actions, read.actions...)
			merged.unread[isKind] = append(merged.unread[isKind], read.unread[isKind]...)
			merged.unread[isAction] = append(merged.unread[isAction], read.unread[isAction]...)
			out.direct[name] = merged

			seen := map[funcRef]bool{}
			for _, callee := range out.calls[name] {
				seen[callee] = true
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				ref, ok := calleeRef(call.Fun, dir, imports)
				if !ok || seen[ref] {
					return true
				}
				seen[ref] = true
				out.calls[name] = append(out.calls[name], ref)
				return true
			})
			if _, present := out.calls[name]; !present {
				out.calls[name] = nil
			}
		}
	}
	return out, nil
}

// calleeRef resolves what a call names: a function in the same package, or
// one in another package this repository declares. A method call — the
// receiver is a value rather than a package — resolves to neither, and a
// call into a dependency is out of the repository the walk reads.
func calleeRef(fun ast.Expr, dir string, imports map[string]string) (funcRef, bool) {
	switch f := fun.(type) {
	case *ast.Ident:
		return funcRef{dir: dir, name: f.Name}, true
	case *ast.SelectorExpr:
		qualifier, ok := f.X.(*ast.Ident)
		if !ok {
			return funcRef{}, false
		}
		target, known := imports[qualifier.Name]
		if !known {
			return funcRef{}, false
		}
		return funcRef{dir: target, name: f.Sel.Name}, true
	default:
		return funcRef{}, false
	}
}

// readTrail reads the kinds and actions one function names.
//
// A kind is read wherever the constant appears, not only as a record field:
// half the writes on both transports hand the kind to a helper that appends
// on their behalf, and a reading that only knew about struct fields would
// report those as naming nothing. An action is read from the record that
// carries it, because the value is a plain string and has no other marker.
//
// A field filled from the function's own parameters is a forward rather
// than a naming, and contributes neither a value nor an unread marker. Every
// tool's write goes through the one recorder in mutationlog.go, whose record
// literals restate every value that ever passes through it — read as
// namings they would give every change the same trail, and read as unread
// they would make every change unreadable. The value is named where the
// parameter is supplied, which is the call site the walk already reaches.
func readTrail(fset *token.FileSet, fn *ast.FuncDecl, kinds map[string]string) trail {
	out := trail{unread: map[bool][]namedValue{}}
	params := parameterNames(fn)
	at := func(n ast.Node) namedValue {
		pos := fset.Position(n.Pos())
		return namedValue{file: pos.Filename, line: pos.Line}
	}
	unread := func(half bool, n ast.Node, format string, args ...any) {
		v := at(n)
		v.value = fmt.Sprintf(format, args...)
		out.unread[half] = append(out.unread[half], v)
	}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			qualifier, ok := node.X.(*ast.Ident)
			if !ok || !eventbusQualifiers[qualifier.Name] {
				return true
			}
			if value, known := kinds[node.Sel.Name]; known {
				v := at(node)
				v.value = value
				out.kinds = append(out.kinds, v)
				return true
			}
			// TaskTransition builds the kind from a transition name known
			// only at run time. Both transports may well call it, but
			// neither has been read, so say so rather than record silence.
			if node.Sel.Name == "TaskTransition" {
				unread(isKind, node, "%s.TaskTransition builds the kind at run time", qualifier.Name)
			}
		case *ast.CompositeLit:
			typ := literalTypeName(node.Type)
			for _, key := range recordKindKeys[typ] {
				value, found := literalField(node, key)
				if !found || forwardsParameter(value, params) {
					continue
				}
				if !namesKnownKind(value, kinds) {
					unread(isKind, value, "%s.%s is not written as a kind constant", typ, key)
				}
			}
			for _, key := range recordActionKeys[typ] {
				value, found := literalField(node, key)
				if !found || forwardsParameter(value, params) {
					continue
				}
				action, ok := stringLiteral(value)
				if !ok {
					unread(isAction, value, "%s.%s is not written as a literal", typ, key)
					continue
				}
				v := at(value)
				v.value = action
				out.actions = append(out.actions, v)
			}
		}
		return true
	})
	return out
}

// parameterNames returns every identifier a function's own signature — and
// the signatures of the closures it returns — binds. A handler is a
// constructor around a closure, so both levels are the function.
func parameterNames(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	add := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			for _, name := range field.Names {
				out[name.Name] = true
			}
		}
	}
	add(fn.Type.Params)
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if lit, ok := n.(*ast.FuncLit); ok {
			add(lit.Type.Params)
		}
		return true
	})
	return out
}

// forwardsParameter reports whether a record field is filled from a value
// the function was handed rather than from one it names.
func forwardsParameter(expr ast.Expr, params map[string]bool) bool {
	switch v := expr.(type) {
	case *ast.Ident:
		return params[v.Name]
	case *ast.SelectorExpr:
		base, ok := v.X.(*ast.Ident)
		return ok && params[base.Name]
	default:
		return false
	}
}

// recordKindKeys names the records whose field carries the event kind, so a
// field written some other way is reported rather than passed over.
var recordKindKeys = map[string][]string{
	"mutation":        {"EventType"},
	"eventbus.Event":  {"Type"},
	"sharedbus.Event": {"Type"},
}

// recordActionKeys names the records whose field carries the audit action.
var recordActionKeys = map[string][]string{
	"mutation":    {"AuditAction"},
	"audit.Entry": {"Action"},
}

// literalTypeName renders a composite literal's type as it is written.
func literalTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name + "." + t.Sel.Name
		}
	}
	return ""
}

// literalField returns the value a keyed composite literal gives one field.
func literalField(lit *ast.CompositeLit, key string) (ast.Expr, bool) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if id, ok := kv.Key.(*ast.Ident); ok && id.Name == key {
			return kv.Value, true
		}
	}
	return nil, false
}

// namesKnownKind reports whether an expression is one of the declared kind
// constants, reached through either package that exports it.
func namesKnownKind(expr ast.Expr, kinds map[string]string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	qualifier, ok := sel.X.(*ast.Ident)
	if !ok || !eventbusQualifiers[qualifier.Name] {
		return false
	}
	_, known := kinds[sel.Sel.Name]
	return known
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

// operationHandler is the function a request operation is registered with,
// and the package it is declared in.
type operationHandler struct {
	dir  string
	fn   string
	site string
}

// TestMCPRESTPairsFileTheSameTrail compares, for every paired tool and
// operation, the event kind and audit action each one files its change
// under.
//
// The existing mutation check requires that a tool names an action at all.
// It has nothing to compare the name against, so a tool naming a plausible
// but different one passes it, and the change goes on reaching nobody.
//
// It fails on a divergence and on a half one side files and the other does
// not. It does not fail on a half it could not read: those are counted and
// printed instead, so the reach of the comparison is something someone can
// look at rather than assume. Failing on them would ask for an exemption on
// every change written through a helper this walk stops at, and a marker
// that many changes carry is one nobody reads.
func TestMCPRESTPairsFileTheSameTrail(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	kinds := declaredEventKinds(t, root)
	toolRuns := mcpToolRunFunctions(t)
	operations := registeredOperationHandlers(t, root)
	source := newSourceTrail(root, kinds)

	var (
		compared       int
		silent         int
		sharedDelegate int
		oneSilent      []string
		mismatched     []string
		unresolved     []string
		toolValues     int
		opValues       int
	)

	names := make([]string, 0, len(mcpRESTCounterparts))
	for name := range mcpRESTCounterparts {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		opID := mcpRESTCounterparts[name]
		pair := name + " · " + opID

		run, registered := toolRuns[name]
		if !registered {
			unresolved = append(unresolved, pair+" · the tool registry names no run function for it")
			continue
		}
		toolSide, declared := source.resolve(funcRef{dir: mcpPackageDir, name: run})
		if !declared {
			unresolved = append(unresolved, fmt.Sprintf("%s · %s is not a function this package declares", pair, run))
			continue
		}

		handler, found := operations[opID]
		if !found {
			unresolved = append(unresolved, pair+" · no huma.Register call in the HTTP tree names that operation")
			continue
		}
		opSide, declared := source.resolve(funcRef{dir: handler.dir, name: handler.fn})
		if !declared {
			unresolved = append(unresolved, fmt.Sprintf("%s · %s is not a function %s declares (%s)", pair, handler.fn, handler.dir, handler.site))
			continue
		}

		toolValues += len(toolSide.kinds) + len(toolSide.actions)
		opValues += len(opSide.kinds) + len(opSide.actions)

		// The kind and the action are compared apart from each other. A
		// change can be filed on the timeline by a shared helper and in
		// audit_logs by the handler itself, so one half of a pair is often
		// readable when the other is not, and folding them together would
		// give up the half that is.
		for _, half := range []struct {
			label     string
			kinds     bool
			toolValue []string
			opValue   []string
		}{
			{"kind", true, toolSide.kindValues(), opSide.kindValues()},
			{"action", false, toolSide.actionValues(), opSide.actionValues()},
		} {
			toolBlind := describe(toolSide.unread[half.kinds])
			opBlind := describe(opSide.unread[half.kinds])
			var toolElsewhere, opElsewhere []funcRef
			if len(half.toolValue) == 0 {
				toolElsewhere = source.filedElsewhere(toolSide, half.kinds)
			}
			if len(half.opValue) == 0 {
				opElsewhere = source.filedElsewhere(opSide, half.kinds)
			}

			if len(toolBlind) == 0 && len(opBlind) == 0 && source.filedThroughTheSame(toolSide, opSide, half.kinds) {
				compared++
				sharedDelegate++
				continue
			}

			if reason := unreadReason(toolBlind, toolElsewhere, opBlind, opElsewhere); reason != "" {
				unresolved = append(unresolved, fmt.Sprintf("%s · %s · %s", pair, half.label, reason))
				continue
			}

			compared++
			switch {
			case len(half.toolValue) == 0 && len(half.opValue) == 0:
				silent++
			case len(half.toolValue) == 0 || len(half.opValue) == 0:
				oneSilent = append(oneSilent, fmt.Sprintf("%s · %s %s · %s %s",
					pair, half.label, joinValues(half.toolValue), half.label, joinValues(half.opValue)))
			case !equalValues(half.toolValue, half.opValue):
				mismatched = append(mismatched, fmt.Sprintf("%s · %s %s · %s %s",
					pair, half.label, joinValues(half.toolValue), half.label, joinValues(half.opValue)))
			}
		}
	}

	// Printed on every run, passing or failing. The run this has to be
	// readable on is the one where nothing fails, because that is the run
	// where an unnoticed gap looks like coverage.
	t.Logf("compared %d halves across %d pairs: %d agree on a value, %d agree by filing through the same function, %d name nothing on either side; %d could not be read",
		compared, len(mcpRESTCounterparts), compared-silent-sharedDelegate-len(oneSilent)-len(mismatched), sharedDelegate, silent, len(unresolved))
	for _, u := range unresolved {
		t.Logf("unresolved: %s; the half was not compared, so nothing here says the two agree", u)
	}

	for _, s := range oneSilent {
		t.Errorf("one side files nothing: %s; a change recorded by one transport and not the other is the same divergence with one side missing", s)
	}
	for _, m := range mismatched {
		t.Errorf("divergent trail: %s; the two transports write one change under two names, so whichever consumer reads the other name never sees it", m)
	}

	if compared == 0 {
		t.Fatal("every pair came back unresolved; a walk that reads nothing reports a clean result, which is what this would have been")
	}
	if toolValues == 0 {
		t.Fatal("no tool named a kind or an action; the tool side of the join was read as empty, which it is not")
	}
	if opValues == 0 {
		t.Fatal("no operation named a kind or an action; the request side of the join was read as empty, which it is not")
	}
}

// unreadReason renders why a half was not compared, naming only the side
// the reading fell short on. A side that was read outright is left out
// rather than rendered as an absence: "nothing" beside a transport reads as
// a claim that it files nothing, which is the opposite finding.
func unreadReason(toolBlind []string, toolElsewhere []funcRef, opBlind []string, opElsewhere []funcRef) string {
	var parts []string
	for _, side := range []struct {
		label     string
		blind     []string
		elsewhere []funcRef
	}{
		{"tool", toolBlind, toolElsewhere},
		{"operation", opBlind, opElsewhere},
	} {
		reasons := append(append([]string{}, side.blind...), refNames(side.elsewhere)...)
		if len(reasons) == 0 {
			continue
		}
		parts = append(parts, side.label+": "+strings.Join(reasons, ", "))
	}
	return strings.Join(parts, " · ")
}

func refNames(refs []funcRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, "filed by "+r.String())
	}
	sort.Strings(out)
	return out
}

// equalValues compares two sorted value sets. Both sides are deduplicated
// and sorted before they get here, so order carries no meaning of its own.
func equalValues(a, b []string) bool { return slices.Equal(a, b) }

// mcpToolRunFunctions reads the tool registry out of this package's source
// and returns the implementation each tool names.
//
// Registration wraps the run function in a closure that binds the declared
// floor, so the built table holds the wrapper; the declaration is the only
// place the implementation is named, and it is what the walk has to be
// entered at.
func mcpToolRunFunctions(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	out := map[string]string{}
	for path, src := range packageSources(t, ".") {
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
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
			name, _ := literalField(lit, "name")
			run, _ := literalField(lit, "run")
			toolName, ok := stringLiteral(name)
			if !ok {
				return true
			}
			if id, ok := run.(*ast.Ident); ok {
				out[toolName] = id.Name
			}
			return true
		})
	}
	if len(out) == 0 {
		t.Fatal("no tool registrations were read from the source; the join has no left side")
	}
	return out
}

// registeredOperationHandlers reads every huma.Register call under the HTTP
// tree and returns the function each OperationID is served by.
//
// A handler named through its package qualifier is resolved through the
// file's imports, so the calendar operations mounted by the router resolve
// to the package that actually declares them rather than to the router.
func registeredOperationHandlers(t *testing.T, root string) map[string]operationHandler {
	t.Helper()
	fset := token.NewFileSet()
	out := map[string]operationHandler{}

	base := filepath.Join(root, httpTreeRel)
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		fileDir, relErr := filepath.Rel(root, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		fileDir = filepath.ToSlash(fileDir)
		imports := importedPackageDirs(file)

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 3 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Register" {
				return true
			}
			opLit, ok := call.Args[1].(*ast.CompositeLit)
			if !ok {
				return true
			}
			idExpr, found := literalField(opLit, "OperationID")
			if !found {
				return true
			}
			opID, ok := stringLiteral(idExpr)
			if !ok {
				return true
			}
			handlerCall, ok := call.Args[2].(*ast.CallExpr)
			if !ok {
				return true
			}
			site := fset.Position(call.Pos()).String()
			switch fn := handlerCall.Fun.(type) {
			case *ast.Ident:
				out[opID] = operationHandler{dir: fileDir, fn: fn.Name, site: site}
			case *ast.SelectorExpr:
				qualifier, ok := fn.X.(*ast.Ident)
				if !ok {
					return true
				}
				dir, known := imports[qualifier.Name]
				if !known {
					return true
				}
				out[opID] = operationHandler{dir: dir, fn: fn.Sel.Name, site: site}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk the HTTP tree: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no operations were read from the HTTP tree; the join has no right side")
	}
	return out
}

// importedPackageDirs maps the identifier a file reaches a repository
// package through onto the directory declaring it. Packages outside this
// repository are left out: a handler cannot be declared in one.
func importedPackageDirs(file *ast.File) map[string]string {
	out := map[string]string{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || !strings.HasPrefix(path, modulePrefix) {
			continue
		}
		dir := strings.TrimPrefix(path, modulePrefix)
		name := dir[strings.LastIndex(dir, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		out[name] = dir
	}
	return out
}

// declaredEventKinds returns every event-kind constant name with the string
// it stands for.
func declaredEventKinds(t *testing.T, root string) map[string]string {
	t.Helper()
	path := filepath.Join(root, sharedKindsRel)
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				value, ok := stringLiteral(vs.Values[i])
				if !ok {
					continue
				}
				out[name.Name] = value
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("no event kinds were read from %s; every kind would read as unknown", sharedKindsRel)
	}
	return out
}

// readSources returns the non-test Go files of one directory, keyed by
// path. A directory with none is not an error: it is a package the walk
// cannot enter, and the caller says so rather than failing.
func readSources(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		raw, readErr := os.ReadFile(path) //#nosec G304 -- repository path walked at test time
		if readErr != nil {
			return nil, readErr
		}
		out[path] = string(raw)
	}
	return out, nil
}

// packageSources is [readSources] where the directory has to be readable.
func packageSources(t *testing.T, dir string) map[string]string {
	t.Helper()
	out, err := readSources(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	if len(out) == 0 {
		t.Fatalf("%s holds no source files", dir)
	}
	return out
}

// repoRoot walks up from the working directory to the go.work that defines
// the workspace.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.work")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.work not found above the working directory")
		}
		dir = parent
	}
}

// TestTrailReadingSeesADivergentPair is the positive control: it holds the
// reading against source it states in full, and proves it reports the
// mismatch it is meant to report rather than passing because it read
// nothing.
func TestTrailReadingSeesADivergentPair(t *testing.T) {
	t.Parallel()

	kinds := map[string]string{
		"TaskCommentAdded":   "task.comment.added",
		"CommentAddedLegacy": "comment.added",
	}

	// The tool names its kind on the record and reaches the shared recorder
	// that restates it; the operation names its kind through a helper
	// argument and its action inline. Between them the sample covers every
	// shape the reading has to tell apart, and they name different values in
	// both halves.
	const toolSrc = `package p

func runAddComment() {
	recordMutation(mutation{
		EventType:   eventbus.CommentAddedLegacy,
		AuditAction: "comment.add",
	})
}

func recordMutation(m mutation) {
	appendEvent(eventbus.Event{Type: m.EventType})
	record(audit.Entry{Action: m.AuditAction})
}
`
	const restSrc = `package q

func AddComment() {
	appendComment(eventbus.TaskCommentAdded)
	record(audit.Entry{Action: "comment.create"})
}
`
	// Two more sides file their change through the same function in another
	// package entirely. That is the case a reading has to keep apart both
	// from filing nothing and from a gap.
	const delegatingSrc = `package r

import "github.com/libraz/nodate-flow/sample/commentkit"

func EditComment() {
	commentkit.Append()
}
`
	const alsoDelegatingSrc = `package s

import "github.com/libraz/nodate-flow/sample/commentkit"

func runEditComment() {
	commentkit.Append()
}
`
	const helperSrc = `package commentkit

func Append() {
	appendEvent(eventbus.Event{Type: eventbus.TaskCommentAdded})
}
`

	source := newSourceTrail(t.TempDir(), kinds)
	for dir, sources := range map[string]map[string]string{
		"sample/tools":           {"tool.go": toolSrc},
		"sample/handlers":        {"rest.go": restSrc},
		"sample/delegating":      {"delegating.go": delegatingSrc},
		"sample/also-delegating": {"also_delegating.go": alsoDelegatingSrc},
		"sample/commentkit":      {"commentkit.go": helperSrc},
	} {
		pkg, err := readPackageTrail(dir, sources, kinds)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		source.add(dir, pkg)
	}

	toolSide, declared := source.resolve(funcRef{dir: "sample/tools", name: "runAddComment"})
	if !declared {
		t.Fatal("the tool sample's run function was not read")
	}
	restSide, declared := source.resolve(funcRef{dir: "sample/handlers", name: "AddComment"})
	if !declared {
		t.Fatal("the operation sample's handler was not read")
	}

	// The recorder restates both of the tool's values and names neither, so
	// nothing in the sample is unreadable — and nothing in it is counted
	// twice either.
	for _, half := range []bool{isKind, isAction} {
		if len(toolSide.unread[half]) != 0 || len(restSide.unread[half]) != 0 {
			t.Fatalf("the sample reads as unresolved (%v / %v); every value in it is either named outright or forwarded from a parameter",
				describe(toolSide.unread[half]), describe(restSide.unread[half]))
		}
	}

	// The kind and the action on each side, pinned to where the sample
	// writes them: a reading that drifted onto the wrong expression would
	// still produce two values and pass a bare count.
	assertValues(t, "tool kinds", toolSide.kinds, []namedValue{{value: "comment.added", file: "tool.go", line: 5}})
	assertValues(t, "tool actions", toolSide.actions, []namedValue{{value: "comment.add", file: "tool.go", line: 6}})
	assertValues(t, "operation kinds", restSide.kinds, []namedValue{{value: "task.comment.added", file: "rest.go", line: 4}})
	assertValues(t, "operation actions", restSide.actions, []namedValue{{value: "comment.create", file: "rest.go", line: 5}})

	if equalValues(toolSide.kindValues(), restSide.kindValues()) {
		t.Errorf("the two samples name kinds %v and %v; the comparison reports them as agreeing",
			toolSide.kindValues(), restSide.kindValues())
	}
	if equalValues(toolSide.actionValues(), restSide.actionValues()) {
		t.Errorf("the two samples name actions %v and %v; the comparison reports them as agreeing",
			toolSide.actionValues(), restSide.actionValues())
	}

	// The delegating operation names no kind of its own, and is reported as
	// filing one somewhere the walk stops rather than as filing none. Read
	// the other way it would be a finding against a handler that records
	// exactly what it should.
	delegating, declared := source.resolve(funcRef{dir: "sample/delegating", name: "EditComment"})
	if !declared {
		t.Fatal("the delegating sample's handler was not read")
	}
	if len(delegating.kinds) != 0 {
		t.Errorf("the delegating sample was read as naming kinds %v; the value it files is in another package", delegating.kindValues())
	}
	elsewhere := refNames(source.filedElsewhere(delegating, isKind))
	want := []string{"filed by sample/commentkit.Append"}
	if !equalValues(elsewhere, want) {
		t.Errorf("the delegating sample resolves to %v, want %v; without this it reads as a handler that files nothing", elsewhere, want)
	}
	if len(source.filedElsewhere(delegating, isAction)) != 0 {
		t.Errorf("the delegating sample resolves to %v for the audit half, which nothing in it writes",
			refNames(source.filedElsewhere(delegating, isAction)))
	}

	// Two sides filing through that same function are settled, not blocked.
	// A side that names its own value and one that delegates are not: the
	// two may well agree, and which way is exactly what is not known.
	alsoDelegating, declared := source.resolve(funcRef{dir: "sample/also-delegating", name: "runEditComment"})
	if !declared {
		t.Fatal("the second delegating sample's tool function was not read")
	}
	if !source.filedThroughTheSame(alsoDelegating, delegating, isKind) {
		t.Error("two sides filing through sample/commentkit.Append are reported as unsettled; the value they file is the same value")
	}
	if source.filedThroughTheSame(alsoDelegating, restSide, isKind) {
		t.Error("a side that delegates and a side that names its own kind are reported as settled; nothing read says the two arrive at the same name")
	}
	if source.filedThroughTheSame(alsoDelegating, delegating, isAction) {
		t.Error("the audit half is reported as settled, and neither sample writes an audit row at all")
	}
}

func assertValues(t *testing.T, label string, got, want []namedValue) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: read %v, want %v", label, got, want)
	}
	for i := range want {
		if got[i].value != want[i].value || got[i].where() != want[i].where() {
			t.Errorf("%s: read %q at %s, want %q at %s",
				label, got[i].value, got[i].where(), want[i].value, want[i].where())
		}
	}
}
