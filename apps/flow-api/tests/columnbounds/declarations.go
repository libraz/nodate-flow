package columnbounds

import (
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

// Surface is where a bound is declared.
type Surface string

const (
	// REST is a Huma input struct under a handler tree.
	REST Surface = "REST"
	// MCP is a tool's input schema in internal/mcp.
	MCP Surface = "MCP"
)

// Declaration is one place the API states the longest string a wire field
// accepts.
type Declaration struct {
	Surface Surface
	// Scope is the repository-relative handler package for a REST field,
	// and the tool source for an MCP one. It is what a table lookup is
	// narrowed to, so two surfaces cannot borrow each other's tables.
	Scope string
	// Owner is the input type or the tool the field belongs to, named in
	// failures so two sides of a disagreement can be told apart.
	Owner string
	// Resource is the snake_case noun the owner writes, taken from its
	// name. It is empty when the name carries no write verb, which is what
	// keeps reads out of scope.
	Resource string
	// Section is "body" or "query": where on the request the field
	// travels. An MCP argument has no such split and is always "body".
	Section string
	// Name is the wire name, qualified by the names of the objects it is
	// nested under.
	Name string
	// Max is the declared bound, and zero when none is declared.
	Max int
	// Bounded reports whether the field states a length at all. A field
	// that states none carries no promise to compare against a width, so
	// the overflow and agreement derivations read only the bounded ones;
	// the absence derivation reads only the rest.
	Bounded bool
	Path    string
	Line    int
}

// RESTDeclaration builds a declaration for one wire field of a handler
// input, deriving the resource from the owner's name the way the bounded
// fields do.
//
// It is exported for the check that reads which values a field accepts.
// That check scopes itself differently — a field states a value set whether
// or not it states a length — so it collects its own fields, and this lets
// it hand them to this package's resolution instead of carrying a second
// copy of the naming rule. Max is left zero and Bounded false: nothing
// about a length, stated or absent, is claimed by building one of these.
func RESTDeclaration(scope, owner, section, name, path string, line int) Declaration {
	resource, _ := resourceOf(owner)
	return Declaration{
		Surface:  REST,
		Scope:    scope,
		Owner:    owner,
		Resource: resource,
		Section:  section,
		Name:     name,
		Path:     path,
		Line:     line,
	}
}

// Location renders the declaration's position for a failure message.
func (d Declaration) Location() string { return fmt.Sprintf("%s:%d", d.Path, d.Line) }

// Describe renders the declaration the way a failure names it.
func (d Declaration) Describe() string {
	return fmt.Sprintf("%s %s %s.%s", d.Surface, d.Owner, d.Section, d.Name)
}

// HandlerRoots are the handler trees the REST scope is read from, relative
// to the repository root. Both API binaries declare their request shapes
// the same way, so a bound stated in either one lands in the same schema.
var HandlerRoots = []string{
	filepath.Join("apps", "flow-api", "internal", "http", "handlers"),
	filepath.Join("apps", "auth-api", "internal", "http", "handlers"),
}

// ToolsPath is the file the MCP tool schemas are declared in.
var ToolsPath = filepath.Join("apps", "flow-api", "internal", "mcp", "tools.go")

// writeVerbs are the operation prefixes that name a write on a resource.
// Everything after the verb is the resource, which is what says where the
// value lands.
//
// A name outside this vocabulary carries no bound this can place: a search
// term, a token and a free-form instruction are all spelled like a field
// and none of them is stored under the name it arrives as.
var writeVerbs = []string{
	"Create", "Add", "New",
	"Patch", "Update", "Put", "Edit", "Set", "Replace",
}

// resourceOf splits an input type name into the snake_case resource it
// writes, returning false when the name carries none of the write verbs.
func resourceOf(owner string) (string, bool) {
	name := strings.TrimSuffix(owner, "Input")
	for _, verb := range writeVerbs {
		rest, found := strings.CutPrefix(name, verb)
		if !found || rest == "" {
			continue
		}
		return snake(rest), true
	}
	return "", false
}

// toolResourceOf splits a tool name into the snake_case resource it writes.
//
// A tool name is a single global identifier rather than a name inside a
// package, so only the plain `<verb>_<resource>` form is read: it names the
// resource in full. A name that qualifies the verb or the object some other
// way says less than it appears to — a tool called after the thing it reads
// from writes somewhere else entirely — and is left out rather than guessed
// at.
func toolResourceOf(name string) (string, bool) {
	verb, rest, found := strings.Cut(name, "_")
	if !found || rest == "" {
		return "", false
	}
	for _, w := range writeVerbs {
		if strings.EqualFold(w, verb) {
			return rest, true
		}
	}
	return "", false
}

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
			return "", fmt.Errorf("columnbounds: go.work not found above the working directory")
		}
		dir = parent
	}
}

// HandlerDeclarations reads every string wire field of an input under one
// handler tree, with the bound each states or the record that it states
// none.
func HandlerDeclarations(root, rel string) ([]Declaration, error) {
	packages, order, err := readPackages(root, rel)
	if err != nil {
		return nil, err
	}

	var out []Declaration
	for _, pkg := range order {
		decls, perr := ParseHandlerPackage(pkg, packages[pkg])
		if perr != nil {
			return nil, perr
		}
		out = append(out, decls...)
	}
	return out, nil
}

// readPackages reads one handler tree's sources, grouped by directory and
// keyed by repository-relative path.
//
// The grouping is what both derivations need. Half the inputs name their
// body as a separate type in the same package, so a file read on its own
// would see the marker field and none of the fields the request actually
// carries; and a handler lives in a different file from the input it takes
// as often as not.
func readPackages(root, rel string) (map[string]map[string]string, []string, error) {
	packages := map[string]map[string]string{}
	var order []string

	base := filepath.Join(root, rel)
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
		relPath, relErr := filepath.Rel(root, path)
		if relErr != nil {
			relPath = path
		}
		relPath = filepath.ToSlash(relPath)
		pkg := filepath.ToSlash(filepath.Dir(relPath))
		if _, seen := packages[pkg]; !seen {
			packages[pkg] = map[string]string{}
			order = append(order, pkg)
		}
		packages[pkg][relPath] = string(raw)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(order)
	return packages, order, nil
}

// ParseHandlerPackage reads the string wire fields out of one handler
// package, given its files keyed by path. It is exported so the control can
// hold the derivation against a source it states in full, rather than
// against whatever the tree happens to contain.
func ParseHandlerPackage(pkg string, sources map[string]string) ([]Declaration, error) {
	fset := token.NewFileSet()
	paths := make([]string, 0, len(sources))
	for path := range sources {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	type decl struct {
		name   string
		strukt *ast.StructType
	}
	var decls []decl
	structs := map[string]*ast.StructType{}

	for _, path := range paths {
		file, err := parser.ParseFile(fset, path, sources[path], 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		for _, d := range file.Decls {
			gen, ok := d.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				structs[ts.Name.Name] = st
				decls = append(decls, decl{name: ts.Name.Name, strukt: st})
			}
		}
	}

	var out []Declaration
	for _, d := range decls {
		if !strings.HasSuffix(d.name, "Input") {
			continue
		}
		resource, _ := resourceOf(d.name)
		w := &walker{fset: fset, pkg: pkg, owner: d.name, resource: resource, structs: structs}
		w.walk(d.strukt, "", map[string]bool{d.name: true})
		out = append(out, w.found...)
	}
	return out, nil
}

// walker collects the string wire fields reachable from one input type.
type walker struct {
	fset     *token.FileSet
	pkg      string
	owner    string
	resource string
	structs  map[string]*ast.StructType
	found    []Declaration
}

// walk reads a struct's fields and descends into the structs they name,
// whether written inline or declared beside the input in the same package.
//
// prefix carries the names of the objects a field is nested under, so a
// member of an object is never taken for a column of the resource the input
// is named after. The body marker field itself contributes no prefix: it
// has no wire name, because the body is the request rather than a member of
// it.
//
// visiting is the cycle guard. A type that refers to itself is legal Go and
// would otherwise be descended into forever.
func (w *walker) walk(st *ast.StructType, prefix string, visiting map[string]bool) {
	for _, field := range st.Fields.List {
		section, name := "", ""
		var tag reflect.StructTag
		if field.Tag != nil {
			tag = reflect.StructTag(strings.Trim(field.Tag.Value, "`"))
			section, name = wireName(tag)
		}

		nested, typeName := w.structOf(field.Type)
		if nested != nil {
			if visiting[typeName] {
				continue
			}
			child := prefix
			if name != "" {
				child = prefix + name + "."
			}
			if typeName != "" {
				visiting[typeName] = true
			}
			w.walk(nested, child, visiting)
			if typeName != "" {
				delete(visiting, typeName)
			}
			continue
		}

		if name == "" || !isScalarString(field.Type) {
			continue
		}
		bound, bounded := boundOf(tag)
		if !bounded && statesValueSet(tag) {
			// A field naming the values it takes is constrained by that
			// set rather than by a length, so its silence about a length
			// says nothing: the longest value it accepts is already fixed,
			// and what remains is whether the set fits, which the check
			// that reads value sets asks. A field stating both is read as
			// it is written — the length it declares is a promise like any
			// other.
			continue
		}
		w.found = append(w.found, Declaration{
			Surface:  REST,
			Scope:    w.pkg,
			Owner:    w.owner,
			Resource: w.resource,
			Section:  section,
			Name:     prefix + name,
			Max:      bound,
			Bounded:  bounded,
			Path:     w.fset.Position(field.Pos()).Filename,
			Line:     w.fset.Position(field.Pos()).Line,
		})
	}
}

// structOf resolves a field's type to a struct the walk can descend into:
// an inline struct, a package-local struct type, or a pointer or slice of
// one. Anything else — a type from another package, a scalar — is a leaf.
func (w *walker) structOf(expr ast.Expr) (*ast.StructType, string) {
	switch t := expr.(type) {
	case *ast.StructType:
		return t, ""
	case *ast.Ident:
		if st, ok := w.structs[t.Name]; ok {
			return st, t.Name
		}
		return nil, ""
	case *ast.StarExpr:
		return w.structOf(t.X)
	case *ast.ArrayType:
		if t.Len != nil {
			return nil, ""
		}
		return w.structOf(t.Elt)
	default:
		return nil, ""
	}
}

// statesValueSet reports whether a field tag names the values the field
// accepts.
func statesValueSet(tag reflect.StructTag) bool {
	v, ok := tag.Lookup("enum")
	return ok && v != ""
}

// boundOf reads the declared length bound off a field tag.
func boundOf(tag reflect.StructTag) (int, bool) {
	v, ok := tag.Lookup("maxLength")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// wireName returns where the field travels and what it is called there. A
// path parameter is deliberately not one: it identifies a resource rather
// than carrying a value into a column of it.
func wireName(tag reflect.StructTag) (section, name string) {
	if v, ok := tag.Lookup("json"); ok {
		if n, _, _ := strings.Cut(v, ","); n != "" && n != "-" {
			return "body", n
		}
	}
	if v, ok := tag.Lookup("query"); ok && v != "" {
		return "query", v
	}
	return "", ""
}

// isScalarString reports whether the field holds one string. A slice of
// them does not land in one column, so a bound on it says something this
// cannot place.
func isScalarString(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name == "string"
	case *ast.StarExpr:
		return isScalarString(t.X)
	default:
		return false
	}
}

// ToolDeclarations reads every string property of the MCP tool schemas,
// with the bound each states or the record that it states none.
func ToolDeclarations(root string) ([]Declaration, error) {
	path := filepath.Join(root, ToolsPath)
	raw, err := os.ReadFile(path) //#nosec G304,G122 -- repository path read at test time
	if err != nil {
		return nil, err
	}
	return ParseTools(filepath.ToSlash(ToolsPath), string(raw))
}

// ParseTools reads the string properties out of a tool source. It is
// exported so the control can drive a source it states in full through the
// same derivation the tree goes through.
//
// A tool states its arguments as a tree of schema constructors, so the
// bounds are read where they are written: a stringSchema call, carrying a
// Constraints literal with a MaxLength or carrying none. An object's
// properties contribute their key as a prefix, the same way a nested body
// struct does, and an array's element schema contributes none of its own.
//
// It fails on a constructor it does not know rather than walking past it.
// What a schema position holds is a closed set of helpers in one file, and
// a walker that descends into the ones it recognises and ignores the rest
// loses coverage the day a new helper is written, without saying so: the
// properties under it stop being read, nothing turns red, and the scan
// covers less than it did. That is the failure this package refuses to
// build on anywhere else, and refusing an unknown callee costs one line
// here at the moment the helper is added, against a gap nobody sees.
func ParseTools(path, src string) ([]Declaration, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	w := &toolWalker{fset: fset, path: path}
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		ident, ok := lit.Type.(*ast.Ident)
		if !ok || ident.Name != "tool" {
			return true
		}
		name, schema := "", ast.Expr(nil)
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
				name = stringLiteral(kv.Value)
			case "inputSchema":
				schema = kv.Value
			}
		}
		if name == "" || schema == nil {
			return true
		}
		w.tool = name
		w.resource, _ = toolResourceOf(name)
		w.property("", "", schema)
		return true
	})
	if len(w.unknown) > 0 {
		return nil, fmt.Errorf("%s: %s. Every property under one of these is unread, and a "+
			"walker that steps over what it does not recognise says nothing while it stops "+
			"seeing things. Give each its line in toolWalker.property: which argument holds "+
			"the schema to descend into, or that it holds a value no length constrains",
			path, strings.Join(w.unknown, "; "))
	}
	return w.found, nil
}

// toolWalker collects the string properties of one tool source, and the
// schema constructors it met and could not read.
type toolWalker struct {
	fset     *token.FileSet
	path     string
	tool     string
	resource string
	found    []Declaration
	unknown  []string
}

// property reads one schema expression: a tool's whole input schema, the
// value of a named property, or the element schema of an array.
//
// prefix is the path of the objects the expression sits under, and key the
// name of the property it is the value of — empty for an input schema, and
// the array's own name for an element schema, because an array's element is
// spelled the way a field nested under a repeated body struct is.
//
// The dispatch is the whole reach of this package into the tool schemas:
// every constructor gets a line, including the ones there is nothing to
// descend into, so that "this holds no string" is written down as a decision
// rather than left as the absence of a case.
func (w *toolWalker) property(prefix, key string, expr ast.Expr) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		// Not a call, so not a constructor whose name could be unknown: a
		// literal, an identifier holding a shared fragment, or the nil an
		// argument-free tool passes. None of them declares a length here.
		return
	}
	fn, isIdent := call.Fun.(*ast.Ident)
	if !isIdent {
		w.note(call, exprText(call.Fun))
		return
	}
	switch fn.Name {
	case "stringSchema":
		w.stringProperty(prefix, key, call)
	case "objectSchema":
		// The first argument is the properties map. Each of its keys is a
		// wire name, and it deepens the path by one segment.
		if len(call.Args) > 0 {
			w.properties(prefix+segment(key), call.Args[0])
		}
	case "arraySchema":
		// The second argument is the element schema. An array contributes
		// no segment of its own beyond its key: a member of the element
		// object is named as though it hung off the array directly.
		if len(call.Args) > 1 {
			w.element(prefix, key, call.Args[1])
		}
	case "intSchema", "boolSchema":
		// A number and a boolean hold no string, so there is nothing to
		// collect and nowhere to descend.
	default:
		w.note(call, fn.Name)
	}
}

// properties reads a properties map, which is where a schema names the wire
// fields under it.
func (w *toolWalker) properties(prefix string, expr ast.Expr) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if name := stringLiteral(kv.Key); name != "" {
			w.property(prefix, name, kv.Value)
		}
	}
}

// element reads an array's element schema.
//
// An element that is itself a string is not collected. The array holds many
// of them and they do not land in one column, which is the same reading the
// handler walk gives a slice of strings: a bound on it says something no
// column width answers.
func (w *toolWalker) element(prefix, key string, expr ast.Expr) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return
	}
	if fn, isIdent := call.Fun.(*ast.Ident); isIdent && fn.Name == "stringSchema" {
		return
	}
	w.property(prefix, key, expr)
}

// stringProperty records one string property with the bound it states or
// the record that it states none.
func (w *toolWalker) stringProperty(prefix, key string, call *ast.CallExpr) {
	bound, bounded := toolBound(call)
	if !bounded && toolStatesValueSet(call) {
		// The same reading as a field tag naming its values: the set fixes
		// the longest value, so the silence about a length claims nothing.
		return
	}
	w.found = append(w.found, Declaration{
		Surface:  MCP,
		Scope:    w.path,
		Owner:    w.tool,
		Resource: w.resource,
		Section:  "body",
		Name:     prefix + key,
		Max:      bound,
		Bounded:  bounded,
		Path:     w.path,
		Line:     w.fset.Position(call.Pos()).Line,
	})
}

// note records a constructor the walk cannot read, with where it was met.
func (w *toolWalker) note(call *ast.CallExpr, name string) {
	at := w.fset.Position(call.Pos())
	w.unknown = append(w.unknown,
		fmt.Sprintf("%s is called in a schema position at %s:%d and this walk does not know it",
			name, w.path, at.Line))
}

// segment renders a property name as the path segment its members hang off,
// and nothing for the schema at the top, which is the request rather than a
// member of it.
func segment(key string) string {
	if key == "" {
		return ""
	}
	return key + "."
}

// exprText renders a callee that is not a plain identifier, so a failure can
// name it.
func exprText(expr ast.Expr) string {
	var out strings.Builder
	if err := printer.Fprint(&out, token.NewFileSet(), expr); err != nil {
		return "a callee this could not render"
	}
	return out.String()
}

// toolStatesValueSet reports whether a stringSchema call's Constraints
// argument names the values the property accepts.
func toolStatesValueSet(call *ast.CallExpr) bool {
	for _, arg := range call.Args[1:] {
		lit, ok := arg.(*ast.CompositeLit)
		if !ok {
			continue
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, isIdent := kv.Key.(*ast.Ident); isIdent && key.Name == "Enum" {
				return true
			}
		}
	}
	return false
}

// toolBound reads a MaxLength off a stringSchema call's Constraints
// argument.
func toolBound(call *ast.CallExpr) (int, bool) {
	for _, arg := range call.Args[1:] {
		lit, ok := arg.(*ast.CompositeLit)
		if !ok {
			continue
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != "MaxLength" {
				continue
			}
			inner, ok := kv.Value.(*ast.CallExpr)
			if !ok || len(inner.Args) != 1 {
				continue
			}
			if n, err := strconv.Atoi(literalText(inner.Args[0])); err == nil && n > 0 {
				return n, true
			}
		}
	}
	return 0, false
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

// literalText returns the source text of a basic literal.
func literalText(expr ast.Expr) string {
	basic, ok := expr.(*ast.BasicLit)
	if !ok {
		return ""
	}
	return basic.Value
}

// snake converts a Go identifier or a camelCase wire name to the spelling a
// column carries. Runs of capitals are one word, so IconURL and iconUrl
// both reach icon_url.
func snake(name string) string {
	runes := []rune(name)
	var out strings.Builder
	for i, r := range runes {
		if !isUpper(r) {
			out.WriteRune(r)
			continue
		}
		prevLower := i > 0 && !isUpper(runes[i-1]) && runes[i-1] != '_'
		nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
		if i > 0 && (prevLower || nextLower) {
			out.WriteByte('_')
		}
		out.WriteRune(r - 'A' + 'a')
	}
	return out.String()
}

func isUpper(r rune) bool { return r >= 'A' && r <= 'Z' }
