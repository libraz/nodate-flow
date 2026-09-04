package responseids

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Kind is which of the two shapes a finding is.
type Kind string

const (
	// Value is an internal id placed under a key of a response map.
	Value Kind = "value"
	// Whole is a row returned as itself, whose every field is marshalled.
	Whole Kind = "whole row"
)

// Finding is one response that carries an internal id.
type Finding struct {
	Kind Kind
	// Tool is the name the transport calls the handler by, empty when no
	// tool names it.
	Tool string
	// Handler is the function the response is built in.
	Handler string
	// Key is the response key the value sits under, qualified by the keys
	// of the objects it is nested under. For a whole row it is the
	// identifier returned, since no key was written.
	Key string
	// Expr is the source text of the offending expression.
	Expr string
	Path string
	Line int
}

// Location renders the finding's position for a failure message.
func (f Finding) Location() string { return fmt.Sprintf("%s:%d", f.Path, f.Line) }

// Owner renders what a failure calls the handler: the tool name when the
// transport exposes one, and the function otherwise.
func (f Finding) Owner() string {
	if f.Tool == "" {
		return f.Handler
	}
	return f.Tool
}

// Report is what one walk produced, findings and reach together. The counts
// are the half that says whether an empty finding list means anything.
type Report struct {
	Findings []Finding
	// Handlers is how many tool handlers were walked.
	Handlers int
	// Tools is how many of them a tool names.
	Tools int
	// MapValues is how many response values were inspected.
	MapValues int
}

// Walk reads the tool handlers out of the MCP tree and reports the responses
// carrying an internal id.
func Walk(root string, vocab Vocabulary) (Report, error) {
	sources := map[string]string{}
	base := filepath.Join(root, MCPRoot)
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
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
		sources[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	return ParseHandlers(vocab, sources)
}

// ParseHandlers reads the tool handlers out of a set of sources keyed by
// path. It is exported so the control can hold the derivation against a
// source it states in full, rather than against whatever the tree happens to
// contain.
//
// The sources are parsed together because a handler and the tool that names
// it live in different files as often as not, and because the response types
// a handler builds are declared beside it rather than inside it.
func ParseHandlers(vocab Vocabulary, sources map[string]string) (Report, error) {
	fset := token.NewFileSet()
	paths := make([]string, 0, len(sources))
	for path := range sources {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	files := make([]*ast.File, 0, len(paths))
	for _, path := range paths {
		file, err := parser.ParseFile(fset, path, sources[path], 0)
		if err != nil {
			return Report{}, fmt.Errorf("parse %s: %w", path, err)
		}
		files = append(files, file)
	}

	structs := map[string]*ast.StructType{}
	tools := map[string]string{}
	for _, file := range files {
		collectStructs(file, structs)
		collectTools(file, tools)
	}

	report := Report{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !isToolHandler(fn) {
				continue
			}
			report.Handlers++
			if tools[fn.Name.Name] != "" {
				report.Tools++
			}
			// The package's types are copied per handler because a handler
			// may declare its own response type inside its body, and a name
			// declared in one body says nothing about the same name in the
			// next.
			scope := make(map[string]*ast.StructType, len(structs))
			for name, st := range structs {
				scope[name] = st
			}
			w := &handlerWalk{
				fset:    fset,
				vocab:   vocab,
				tool:    tools[fn.Name.Name],
				handler: fn.Name.Name,
				structs: scope,
				locals:  map[string]*ast.StructType{},
				queried: map[string]bool{},
			}
			w.bindParams(fn.Type.Params)
			w.run(fn.Body)
			report.Findings = append(report.Findings, w.found...)
			report.MapValues += w.values
		}
	}
	return report, nil
}

// collectStructs records the struct types declared at package level, which
// is where the response shapes a handler assembles are named.
func collectStructs(file *ast.File, out map[string]*ast.StructType) {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if st, isStruct := ts.Type.(*ast.StructType); isStruct {
				out[ts.Name.Name] = st
			}
		}
	}
}

// collectTools maps a handler function to the tool name the transport
// exposes it under, read off the registration literal.
func collectTools(file *ast.File, out map[string]string) {
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		ident, ok := lit.Type.(*ast.Ident)
		if !ok || ident.Name != "tool" {
			return true
		}
		name, handler := "", ""
		for _, elt := range lit.Elts {
			kv, isKV := elt.(*ast.KeyValueExpr)
			if !isKV {
				continue
			}
			key, isIdent := kv.Key.(*ast.Ident)
			if !isIdent {
				continue
			}
			switch key.Name {
			case "name":
				name = stringLiteral(kv.Value)
			case "run":
				if fn, isFn := kv.Value.(*ast.Ident); isFn {
					handler = fn.Name
				}
			}
		}
		if name != "" && handler != "" {
			out[handler] = name
		}
		return true
	})
}

// isToolHandler reports whether a function is one of the tool handlers: the
// name every one of them carries, and the untyped result the transport
// marshals without a schema in between.
func isToolHandler(fn *ast.FuncDecl) bool {
	if fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "run") {
		return false
	}
	results := fn.Type.Results
	if results == nil || len(results.List) != 2 {
		return false
	}
	first, ok := results.List[0].Type.(*ast.Ident)
	if !ok || first.Name != "any" {
		return false
	}
	second, ok := results.List[1].Type.(*ast.Ident)
	return ok && second.Name == "error"
}

// handlerWalk reads one handler.
type handlerWalk struct {
	fset    *token.FileSet
	vocab   Vocabulary
	tool    string
	handler string
	// structs are the struct types the handler can name, package-level
	// ones and the types declared in its own body.
	structs map[string]*ast.StructType
	// locals are the identifiers whose struct type the source states, which
	// is what tells a parsed argument's taskId apart from the model's.
	locals map[string]*ast.StructType
	// queried are the identifiers a statement call assigned. A row is
	// reached through one of these, and returning one whole marshals every
	// field the model has.
	queried map[string]bool
	found   []Finding
	values  int
}

// bindParams resolves the handler's parameters to the structs they name.
// The session is one of them, and it carries the caller's workspace and user
// as the counters the statements are run against.
func (w *handlerWalk) bindParams(params *ast.FieldList) {
	if params == nil {
		return
	}
	for _, param := range params.List {
		st := w.structOf(param.Type)
		if st == nil {
			continue
		}
		for _, name := range param.Names {
			w.locals[name.Name] = st
		}
	}
}

// run reads the handler body in two passes. What a local holds has to be
// known before the responses are read, because a map literal is as likely to
// sit above the assignment that explains it as below.
//
// A response map is read wherever it is written, including inside a closure,
// because a handler that runs its writes in a transaction assembles the
// answer in there. A return is not: the closure's own return goes back to
// whatever runs it rather than to the transport, and the transaction runners
// this package calls take a function returning an error.
func (w *handlerWalk) run(body *ast.BlockStmt) {
	nested := map[*ast.ReturnStmt]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		w.declare(n)
		if lit, ok := n.(*ast.FuncLit); ok {
			ast.Inspect(lit.Body, func(inner ast.Node) bool {
				if ret, isReturn := inner.(*ast.ReturnStmt); isReturn {
					nested[ret] = true
				}
				return true
			})
		}
		return true
	})
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CompositeLit:
			if isAnyMap(node.Type) {
				w.readResponse(node)
				return true
			}
			// A list-shaped tool declares a row type beside itself and fills
			// one per row, so the map the response is returned in carries a
			// slice of these and states none of their fields. The parameters
			// a statement is called with are literals too, and are not
			// these: they name a generated type from another package, which
			// resolves to nothing here.
			if st := w.structOf(node.Type); st != nil {
				w.readStructResponse(node, st)
			}
		case *ast.ReturnStmt:
			if !nested[node] {
				w.readReturn(node)
			}
		}
		return true
	})
}

// declare records what one statement says about the identifiers in scope.
func (w *handlerWalk) declare(n ast.Node) {
	switch node := n.(type) {
	case *ast.DeclStmt:
		gen, ok := node.Decl.(*ast.GenDecl)
		if !ok {
			return
		}
		for _, spec := range gen.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				if st, isStruct := s.Type.(*ast.StructType); isStruct {
					w.structs[s.Name.Name] = st
				}
			case *ast.ValueSpec:
				if st := w.structOf(s.Type); st != nil {
					for _, name := range s.Names {
						w.locals[name.Name] = st
					}
				}
			}
		}
	case *ast.AssignStmt:
		if len(node.Rhs) != 1 {
			return
		}
		rhs := unwrap(node.Rhs[0])
		switch value := rhs.(type) {
		case *ast.CallExpr:
			if !w.isStatementCall(value) {
				return
			}
			// A generated statement answers with an error last, and with the
			// row or rows before it when it answers with anything. One
			// destination is that error and holds no row.
			for _, lhs := range node.Lhs[:max(len(node.Lhs)-1, 0)] {
				w.markQueried(lhs)
			}
		case *ast.CompositeLit:
			if len(node.Lhs) != 1 {
				return
			}
			if st := w.structOf(value.Type); st != nil {
				w.bindLocal(node.Lhs[0], st)
			}
		case *ast.Ident:
			// A row copied under another name is still the row.
			if w.queried[value.Name] && len(node.Lhs) == 1 {
				w.markQueried(node.Lhs[0])
			}
			if st, ok := w.locals[value.Name]; ok && len(node.Lhs) == 1 {
				w.bindLocal(node.Lhs[0], st)
			}
		}
	case *ast.RangeStmt:
		// Ranging a set of rows yields a row, and the response for a list
		// is built one element at a time inside the loop.
		ident, ok := unwrap(node.X).(*ast.Ident)
		if !ok {
			return
		}
		if w.queried[ident.Name] && node.Value != nil {
			w.markQueried(node.Value)
		}
		if st, isLocal := w.locals[ident.Name]; isLocal && node.Value != nil {
			w.bindLocal(node.Value, st)
		}
	}
}

func (w *handlerWalk) markQueried(expr ast.Expr) {
	ident, ok := expr.(*ast.Ident)
	if !ok || ident.Name == "_" {
		return
	}
	w.queried[ident.Name] = true
}

func (w *handlerWalk) bindLocal(expr ast.Expr, st *ast.StructType) {
	ident, ok := expr.(*ast.Ident)
	if !ok || ident.Name == "_" {
		return
	}
	w.locals[ident.Name] = st
}

// isStatementCall reports whether a call reaches the generated statements.
// The querier is named once on the dependencies, and a transaction rebinds
// it to a local, so an identifier the querier already produced counts as
// one too.
func (w *handlerWalk) isStatementCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch base := sel.X.(type) {
	case *ast.SelectorExpr:
		return base.Sel.Name == queriesField
	case *ast.Ident:
		return base.Name == queriesField || w.queried[base.Name]
	case *ast.CallExpr:
		return w.isStatementCall(base)
	}
	return false
}

// queriesField is what the generated querier is reached through.
const queriesField = "Queries"

// structOf resolves a type expression to a struct the walk can read fields
// off: written inline, named in the package, or a pointer or slice of one.
func (w *handlerWalk) structOf(expr ast.Expr) *ast.StructType {
	switch t := expr.(type) {
	case *ast.StructType:
		return t
	case *ast.Ident:
		return w.structs[t.Name]
	case *ast.StarExpr:
		return w.structOf(t.X)
	case *ast.ArrayType:
		if t.Len != nil {
			return nil
		}
		return w.structOf(t.Elt)
	default:
		return nil
	}
}

// readResponse reads one response map, and the values nested under it that
// are not maps of their own.
func (w *handlerWalk) readResponse(lit *ast.CompositeLit) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		w.values++
		w.readValue(kv.Value, responseKey(kv.Key))
	}
}

// readStructResponse reads one literal of a declared struct, under the names
// its fields reach the wire by.
func (w *handlerWalk) readStructResponse(lit *ast.CompositeLit, st *ast.StructType) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		field, isIdent := kv.Key.(*ast.Ident)
		if !isIdent {
			continue
		}
		key, reaches := wireName(st, field.Name)
		if !reaches {
			continue
		}
		w.values++
		w.readValue(kv.Value, key)
	}
}

// wireName returns the name a struct field is serialised under, and whether
// it is serialised at all.
//
// A field the marshaller cannot see carries nothing to a caller: an
// unexported one is skipped whatever it holds, and a tag can say the same of
// an exported one. Both are how a handler keeps a counter it needs for its
// own bookkeeping — the parent id a subtask is created under, the row a
// second statement is run against — and holding those against a response
// would report work that never leaves the process.
func wireName(st *ast.StructType, field string) (string, bool) {
	if field == "" || !isUpper(rune(field[0])) {
		return "", false
	}
	for _, f := range st.Fields.List {
		for _, ident := range f.Names {
			if ident.Name != field || f.Tag == nil {
				continue
			}
			tag := reflect.StructTag(strings.Trim(f.Tag.Value, "`"))
			v, ok := tag.Lookup("json")
			if !ok {
				continue
			}
			name, _, _ := strings.Cut(v, ",")
			if name == "-" {
				return "", false
			}
			if name != "" {
				return name, true
			}
		}
	}
	return field, true
}

// isUpper reports whether a rune is an ASCII capital, which is what makes a
// Go field visible outside its package and therefore to the marshaller.
func isUpper(r rune) bool { return r >= 'A' && r <= 'Z' }

// readValue looks for an internal id under one response key.
//
// It follows what a value can be built out of and still be the field it
// started as — a conversion, a formatting call, a member of a list — and
// stops at anything else. A helper's result is the helper's, and a nested
// response map is read on its own, so neither is descended into here.
func (w *handlerWalk) readValue(expr ast.Expr, key string) {
	switch value := expr.(type) {
	case *ast.ParenExpr:
		w.readValue(value.X, key)
	case *ast.UnaryExpr:
		w.readValue(value.X, key)
	case *ast.StarExpr:
		w.readValue(value.X, key)
	case *ast.BinaryExpr:
		w.readValue(value.X, key)
		w.readValue(value.Y, key)
	case *ast.SelectorExpr:
		w.readSelector(value, key)
	case *ast.CallExpr:
		if !isLaundering(value.Fun) {
			return
		}
		for _, arg := range value.Args {
			w.readValue(arg, key)
		}
	case *ast.CompositeLit:
		// A response map and a declared struct are each read where they are
		// written, so descending into one from here would report it twice.
		if isAnyMap(value.Type) || w.structOf(value.Type) != nil {
			return
		}
		for _, elt := range value.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				w.readValue(kv.Value, key+"."+responseKey(kv.Key))
				continue
			}
			w.readValue(elt, key+"[]")
		}
	}
}

// readSelector decides whether one field reference is an internal id or
// something else spelled the same.
//
// Two readings answer that, and the nearer one wins. Where the base resolves
// to a struct the sources state — the tool's parsed arguments, the response
// shape declared beside the handler, the session the transport hands in —
// the field's own declared type says what it holds, which is what tells the
// arguments' taskId, a public string, apart from the column of the same
// name. Where the base is something this cannot resolve, the derived
// vocabulary answers on the field name alone.
func (w *handlerWalk) readSelector(sel *ast.SelectorExpr, key string) {
	name := sel.Sel.Name
	if base, ok := sel.X.(*ast.Ident); ok {
		if st, known := w.locals[base.Name]; known {
			field, found := fieldType(st, name)
			if found && !IsInternalIDType(field) {
				return
			}
			if found {
				w.report(sel, key)
				return
			}
		}
	}
	if !w.vocab[name] {
		return
	}
	w.report(sel, key)
}

// report records one internal id reaching a response key.
func (w *handlerWalk) report(sel *ast.SelectorExpr, key string) {
	w.found = append(w.found, Finding{
		Kind:    Value,
		Tool:    w.tool,
		Handler: w.handler,
		Key:     key,
		Expr:    w.text(sel),
		Path:    w.fset.Position(sel.Pos()).Filename,
		Line:    w.fset.Position(sel.Pos()).Line,
	})
}

// readReturn refuses a row handed back as itself. Nothing names a field
// here, so nothing can be spelled right: the marshaller emits the model.
func (w *handlerWalk) readReturn(ret *ast.ReturnStmt) {
	if len(ret.Results) == 0 {
		return
	}
	ident, ok := unwrap(ret.Results[0]).(*ast.Ident)
	if !ok || !w.queried[ident.Name] {
		return
	}
	w.found = append(w.found, Finding{
		Kind:    Whole,
		Tool:    w.tool,
		Handler: w.handler,
		Key:     ident.Name,
		Expr:    w.text(ret.Results[0]),
		Path:    w.fset.Position(ret.Pos()).Filename,
		Line:    w.fset.Position(ret.Pos()).Line,
	})
}

// text renders an expression the way the source writes it.
func (w *handlerWalk) text(expr ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, w.fset, expr); err != nil {
		return "<unprintable>"
	}
	return buf.String()
}

// fieldType returns a struct's declared type for one field name.
func fieldType(st *ast.StructType, name string) (ast.Expr, bool) {
	for _, field := range st.Fields.List {
		for _, ident := range field.Names {
			if ident.Name == name {
				return field.Type, true
			}
		}
	}
	return nil, false
}

// isAnyMap reports whether a composite literal's type is the untyped map
// every tool response is assembled in.
func isAnyMap(expr ast.Expr) bool {
	m, ok := expr.(*ast.MapType)
	if !ok {
		return false
	}
	key, ok := m.Key.(*ast.Ident)
	if !ok || key.Name != "string" {
		return false
	}
	switch value := m.Value.(type) {
	case *ast.Ident:
		return value.Name == "any"
	case *ast.InterfaceType:
		return value.Methods == nil || len(value.Methods.List) == 0
	}
	return false
}

// laundering are the calls that carry a value through unchanged: a numeric
// conversion, and the two packages a number is turned into a string with. An
// id passed through one of these is still the id.
var laundering = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true, "string": true, "any": true,
}

// launderingPackages are the packages whose calls render a value rather than
// derive a new one.
var launderingPackages = map[string]bool{"strconv": true, "fmt": true}

// isLaundering reports whether a call's result is still whatever was handed
// to it.
func isLaundering(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.Ident:
		return laundering[f.Name]
	case *ast.SelectorExpr:
		pkg, ok := f.X.(*ast.Ident)
		return ok && launderingPackages[pkg.Name]
	}
	return false
}

// unwrap strips the wrappers that do not change what an expression refers
// to.
func unwrap(expr ast.Expr) ast.Expr {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return unwrap(e.X)
	case *ast.UnaryExpr:
		return unwrap(e.X)
	case *ast.StarExpr:
		return unwrap(e.X)
	default:
		return expr
	}
}

// responseKey renders the key a value sits under. A key that is not a
// literal is rendered as written, since the response still carries whatever
// it evaluates to.
func responseKey(expr ast.Expr) string {
	if s := stringLiteral(expr); s != "" {
		return s
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return "?"
}

// stringLiteral returns the value of a string literal expression, or the
// empty string when the expression is not one.
func stringLiteral(expr ast.Expr) string {
	basic, ok := expr.(*ast.BasicLit)
	if !ok || basic.Kind != token.STRING {
		return ""
	}
	unquoted, err := strconv.Unquote(basic.Value)
	if err != nil {
		return ""
	}
	return unquoted
}
