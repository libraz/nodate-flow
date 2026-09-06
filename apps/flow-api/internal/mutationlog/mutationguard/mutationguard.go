// Package mutationguard is the static half of the mutation log.
//
// [mutationlog] can only make the pair of rows one function; it cannot
// stop a new call site from reaching past that function to
// eventbus.Append, or from recording the audit row alone. Nothing about
// a handler's signature says it changes workspace state, so the property
// "every change leaves both traces" is not expressible in the type
// system and a convention written in a doc comment is read by whoever
// already knew it.
//
// The checks here are therefore driven off the go/ast call graph and off
// the operation registry each transport already declares, so an
// operation added later either routes through the recorder or fails the
// build. They are assembled into tests by the package under guard rather
// than run from here: a guard that lives beside the code it guards is
// one a reader of that code finds.
//
// Nothing in this package is specific to one transport. [Analysis]
// answers structural questions about any package directory;
// [HumaOperations] reads the HTTP registry, and an MCP or CLI registry
// reader would sit beside it.
package mutationguard

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Finding is one violation, positioned so the message names the line to
// change rather than the rule that was broken.
type Finding struct {
	File    string
	Line    int
	Message string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s", f.File, f.Line, f.Message)
}

// Analysis is one parsed package directory.
type Analysis struct {
	// Dir is the directory that was read.
	Dir string
	// Files are the non-test Go files in it, relative to Dir and
	// sorted, so a message names the file the way a reader refers to it.
	Files []string

	fset  *token.FileSet
	trees map[string]*ast.File
	graph map[string]map[string]bool

	plainCallsOnly bool
}

// Option adjusts how a package is analysed.
type Option func(*Analysis)

// PlainCallsOnly restricts the call graph to plain identifier calls,
// leaving method calls out of it.
//
// Use it where the recorder is reached as a package-level function
// rather than through a field. Recording the selector's last segment
// makes an unrelated method of the same name satisfy a reachability
// check, which is a real widening for a package whose recorder is never
// reached that way — and a package that does not need the wider graph
// should not carry its blind spot.
func PlainCallsOnly() Option {
	return func(a *Analysis) { a.plainCallsOnly = true }
}

// Load parses every non-test Go file in dir.
//
// Test files are excluded because the guard's subject is the shipped
// code path: a helper that a test alone reaches cannot record a change
// a user made.
func Load(dir string, opts ...Option) (*Analysis, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("mutationguard: read %s: %w", dir, err)
	}
	a := &Analysis{
		Dir:   dir,
		fset:  token.NewFileSet(),
		trees: map[string]*ast.File{},
	}
	for _, opt := range opts {
		opt(a)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, perr := parser.ParseFile(a.fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if perr != nil {
			return nil, fmt.Errorf("mutationguard: parse %s: %w", name, perr)
		}
		a.Files = append(a.Files, name)
		a.trees[name] = parsed
	}
	sort.Strings(a.Files)
	if len(a.Files) == 0 {
		return nil, fmt.Errorf("mutationguard: %s holds no non-test Go file; the guard would be looking at nothing", dir)
	}
	a.buildGraph()
	return a, nil
}

// buildGraph records the intra-package call graph keyed by the name of
// the enclosing top-level function.
//
// Both plain calls (helper()) and selector calls (deps.X.Record()) are
// recorded under the final identifier. A handler reaches the recorder
// through a field on its dependency bundle, so a graph that only saw
// plain identifiers would report every HTTP handler as reaching nothing.
// Recording the selector's last segment means an unrelated method with
// the same name would also satisfy a reachability check, which is why a
// guarded package also bans the imports that could supply one.
//
// Function literals are walked as part of the function that declares
// them: an operation handler is a closure returned by its constructor,
// and the constructor is the name the registry points at.
func (a *Analysis) buildGraph() {
	a.graph = map[string]map[string]bool{}
	for _, name := range a.Files {
		for _, decl := range a.trees[name].Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			callees := a.graph[fn.Name.Name]
			if callees == nil {
				callees = map[string]bool{}
				a.graph[fn.Name.Name] = callees
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch f := call.Fun.(type) {
				case *ast.Ident:
					callees[f.Name] = true
				case *ast.SelectorExpr:
					if !a.plainCallsOnly {
						callees[f.Sel.Name] = true
					}
				}
				return true
			})
		}
	}
}

// Reaches reports whether any function reachable from entry is in
// targets.
func (a *Analysis) Reaches(entry string, targets map[string]bool) bool {
	visited := map[string]bool{}
	var walk func(string) bool
	walk = func(fn string) bool {
		if targets[fn] {
			return true
		}
		if visited[fn] {
			return false
		}
		visited[fn] = true
		for callee := range a.graph[fn] {
			if walk(callee) {
				return true
			}
		}
		return false
	}
	return walk(entry)
}

// HasFunc reports whether the package declares a top-level function of
// this name, so a registry entry naming a function that does not exist
// is reported as such rather than as an unreachable one.
func (a *Analysis) HasFunc(name string) bool {
	_, ok := a.graph[name]
	return ok
}

// Centralized reports every reference to one of the qualified functions
// in qualified, made anywhere outside owner.
//
// qualified names are written as they appear in source ("eventbus.Append",
// "eventbus.AppendBestEffort"). A reference counts whether it is called
// or merely taken as a value, and comments do not count — which is what
// this check has over a textual search in either direction.
//
// owner may be empty, meaning no file in the package may reference them.
// When owner is named, it must itself reference every entry: a guard
// whose owner has stopped calling the thing it owns has silently become
// a check on nothing.
func (a *Analysis) Centralized(owner string, qualified []string) []Finding {
	var out []Finding
	seenInOwner := map[string]bool{}
	for _, name := range a.Files {
		refs := a.qualifiedRefs(name, qualified)
		if name == owner {
			for q := range refs {
				seenInOwner[q] = true
			}
			continue
		}
		for q, positions := range refs {
			for _, pos := range positions {
				msg := fmt.Sprintf("references %s directly; go through the mutation recorder so the audit row cannot go missing", q)
				if owner != "" {
					msg = fmt.Sprintf("references %s directly; %s is the only file allowed to, so the audit row cannot go missing", q, owner)
				}
				out = append(out, Finding{File: name, Line: pos, Message: msg})
			}
		}
	}
	if owner != "" {
		for _, q := range qualified {
			if !seenInOwner[q] {
				out = append(out, Finding{
					File:    owner,
					Line:    0,
					Message: fmt.Sprintf("no longer references %s, so nothing in this package does; either the guard is checking a rule that no longer applies or the call moved somewhere it is not watched", q),
				})
			}
		}
	}
	sortFindings(out)
	return out
}

// qualifiedRefs returns, per qualified name, the lines of file that
// reference it.
func (a *Analysis) qualifiedRefs(file string, qualified []string) map[string][]int {
	want := map[string]bool{}
	for _, q := range qualified {
		want[q] = true
	}
	out := map[string][]int{}
	ast.Inspect(a.trees[file], func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		name := pkg.Name + "." + sel.Sel.Name
		if want[name] {
			out[name] = append(out[name], a.fset.Position(sel.Pos()).Line)
		}
		return true
	})
	return out
}

// Imports reports every import of one of the banned paths.
//
// Banning the import is what makes a name-based reachability check
// sound: with the audit recorder unreachable from the package, a call to
// Record can only be the mutation log's.
func (a *Analysis) Imports(banned []string) []Finding {
	bannedSet := map[string]bool{}
	for _, b := range banned {
		bannedSet[b] = true
	}
	var out []Finding
	for _, name := range a.Files {
		for _, imp := range a.trees[name].Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if !bannedSet[path] {
				continue
			}
			out = append(out, Finding{
				File: name,
				Line: a.fset.Position(imp.Pos()).Line,
				Message: fmt.Sprintf("imports %s; a package that records its changes through the mutation log must not be able to write half of one",
					path),
			})
		}
	}
	sortFindings(out)
	return out
}

// LiteralSpec describes the shape a recorded mutation has to have.
type LiteralSpec struct {
	// TypeName is the composite literal's type as written at the call
	// site, e.g. "mutationlog.Mutation".
	TypeName string
	// Gates are the recorder entry points, keyed by the method or
	// function name a call site uses.
	Gates map[string]bool
	// Required are the fields every literal must set.
	Required []string
	// EventOptionalGates are the gates whose event row another writer
	// owns. A literal passed to one of them must not name the event
	// kind — it would not be appended — and every other gate must.
	EventOptionalGates map[string]bool
	// EventField is the field naming the event kind.
	EventField string
}

// Literals checks every mutation literal handed to a recorder entry
// point.
//
// Returns the findings and the number of literals examined. A caller
// that finds zero should fail: a literal check that matched nothing
// passes for the wrong reason, and it passes forever.
//
// A value assembled somewhere other than the call site is skipped; the
// runtime check inside the recorder is what covers that shape.
func (a *Analysis) Literals(spec LiteralSpec) ([]Finding, int) {
	var out []Finding
	found := 0
	for _, name := range a.Files {
		ast.Inspect(a.trees[name], func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			gate := calleeName(call.Fun)
			if !spec.Gates[gate] {
				return true
			}
			lit := literalArg(call, spec.TypeName)
			if lit == nil {
				return true
			}
			found++
			keys := compositeKeys(lit)
			line := a.fset.Position(call.Pos()).Line
			for _, req := range spec.Required {
				if !keys[req] {
					out = append(out, Finding{File: name, Line: line, Message: fmt.Sprintf(
						"%s literal has no %s; name it, or the change is recorded without the field that identifies it",
						gate, req)})
				}
			}
			if spec.EventField == "" {
				return true
			}
			switch {
			case spec.EventOptionalGates[gate] && keys[spec.EventField]:
				out = append(out, Finding{File: name, Line: line, Message: fmt.Sprintf(
					"%s literal sets %s, which it will not append; use an entry point that appends the event if this change needs one of its own",
					gate, spec.EventField)})
			case !spec.EventOptionalGates[gate] && !keys[spec.EventField]:
				out = append(out, Finding{File: name, Line: line, Message: fmt.Sprintf(
					"%s literal has no %s; name the event kind, or use the entry point for a change whose event a shared helper already appended",
					gate, spec.EventField)})
			}
			return true
		})
	}
	sortFindings(out)
	return out, found
}

// Operation is one registered HTTP operation.
type Operation struct {
	// ID is the OperationID as declared, which is what an OpenAPI
	// consumer and a failing message both name it by.
	ID string
	// Method is the HTTP method constant's last segment as written
	// ("MethodPost"), or the literal string when written as one.
	Method string
	// Handler is the function the registration hands the router, which
	// is where a call-graph check has to be entered.
	Handler string
	// File and Line locate the registration.
	File string
	Line int
}

// Mutating reports whether this operation may change workspace state.
//
// GET and HEAD are the exceptions, and they are exceptions by contract
// rather than by convention: a GET that changes something is already a
// defect independent of what it records.
func (o Operation) Mutating() bool {
	switch o.Method {
	case "MethodGet", "GET", "MethodHead", "HEAD":
		return false
	}
	return true
}

// HumaOperations reads the operation registry out of the source.
//
// Reading the source rather than reflecting on the built router is what
// makes the result usable: registration stores a closure, so reflection
// can only report the wrapper, while the registration site names the
// constructor a call graph can be entered at.
func (a *Analysis) HumaOperations() []Operation {
	var out []Operation
	for _, name := range a.Files {
		ast.Inspect(a.trees[name], func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 3 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Register" {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "huma" {
				return true
			}
			lit, ok := call.Args[1].(*ast.CompositeLit)
			if !ok {
				return true
			}
			op := Operation{File: name, Line: a.fset.Position(call.Pos()).Line}
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
				case "OperationID":
					if v, ok := kv.Value.(*ast.BasicLit); ok && v.Kind == token.STRING {
						op.ID = strings.Trim(v.Value, `"`)
					}
				case "Method":
					switch v := kv.Value.(type) {
					case *ast.SelectorExpr:
						op.Method = v.Sel.Name
					case *ast.BasicLit:
						op.Method = strings.Trim(v.Value, `"`)
					}
				}
			}
			op.Handler = calleeName(call.Args[2])
			out = append(out, op)
			return true
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// calleeName returns the identifier a call expression is entered at:
// the function for a plain call, the selector's last segment for a
// method call, and the callee of a call used as an argument
// (Create(deps) hands the router what Create returns, and Create is
// what the source can be walked from).
func calleeName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	case *ast.CallExpr:
		return calleeName(v.Fun)
	}
	return ""
}

// literalArg returns the composite literal of the named type passed to
// a call, or nil when the argument is not written inline.
func literalArg(call *ast.CallExpr, typeName string) *ast.CompositeLit {
	for _, arg := range call.Args {
		lit, ok := arg.(*ast.CompositeLit)
		if !ok {
			continue
		}
		if typeExprName(lit.Type) == typeName {
			return lit
		}
	}
	return nil
}

// typeExprName renders a composite literal's type the way it is written.
func typeExprName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return typeExprName(v.X) + "." + v.Sel.Name
	}
	return ""
}

// compositeKeys returns the field names a keyed composite literal sets.
func compositeKeys(lit *ast.CompositeLit) map[string]bool {
	out := map[string]bool{}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if id, ok := kv.Key.(*ast.Ident); ok {
			out[id.Name] = true
		}
	}
	return out
}

// SortedKeys returns a map's keys in order, for a message that reads the
// same on every run.
func SortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortFindings(f []Finding) {
	sort.Slice(f, func(i, j int) bool {
		if f[i].File != f[j].File {
			return f[i].File < f[j].File
		}
		return f[i].Line < f[j].Line
	})
}
