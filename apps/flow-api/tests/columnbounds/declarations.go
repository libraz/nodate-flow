package columnbounds

import (
	"fmt"
	"go/ast"
	"go/parser"
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
	// Max is the declared bound.
	Max  int
	Path string
	Line int
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

// HandlerDeclarations reads every bound declared on an input field under
// one handler tree.
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

// ParseHandlerPackage reads the declared bounds out of one handler package,
// given its files keyed by path. It is exported so the control can hold the
// derivation against a source it states in full, rather than against
// whatever the tree happens to contain.
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

// walker collects the bounded wire fields reachable from one input type.
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
		bound, ok := boundOf(tag)
		if !ok {
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

// ToolDeclarations reads every bound declared in the MCP tool schemas.
func ToolDeclarations(root string) ([]Declaration, error) {
	path := filepath.Join(root, ToolsPath)
	raw, err := os.ReadFile(path) //#nosec G304,G122 -- repository path read at test time
	if err != nil {
		return nil, err
	}
	return ParseTools(filepath.ToSlash(ToolsPath), string(raw))
}

// ParseTools reads the declared bounds out of a tool source. It is exported
// so the control can drive a source it states in full through the same
// derivation the tree goes through.
//
// A tool states its arguments as a literal map of JSON Schema fragments, so
// the bounds are read where they are written: a stringSchema call carrying
// a Constraints literal with a MaxLength. Nested object schemas contribute
// their key as a prefix, the same way a nested body struct does.
func ParseTools(path, src string) ([]Declaration, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	var out []Declaration
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
		resource, _ := toolResourceOf(name)
		collectToolBounds(fset, path, name, resource, "", schema, &out)
		return true
	})
	return out, nil
}

// itemsKeyword is the JSON Schema key describing an array's member.
const itemsKeyword = "items"

// collectToolBounds walks one tool's input schema, gathering the properties
// that state a bound.
func collectToolBounds(fset *token.FileSet, path, tool, resource, prefix string, expr ast.Expr, out *[]Declaration) {
	call, ok := expr.(*ast.CallExpr)
	if ok {
		fn, isIdent := call.Fun.(*ast.Ident)
		if isIdent && fn.Name == "objectSchema" && len(call.Args) > 0 {
			collectToolBounds(fset, path, tool, resource, prefix, call.Args[0], out)
		}
		return
	}
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key := stringLiteral(kv.Key)
		if key == "" {
			continue
		}
		// `items` is the JSON Schema keyword introducing an array's member
		// rather than a name the wire carries, so it contributes no
		// segment: a member's property is spelled the same way the field
		// nested under a repeated body struct is.
		child := prefix + key + "."
		if key == itemsKeyword {
			child = prefix
		}
		switch value := kv.Value.(type) {
		case *ast.CallExpr:
			fn, isIdent := value.Fun.(*ast.Ident)
			if !isIdent {
				continue
			}
			switch fn.Name {
			case "stringSchema":
				bound, found := toolBound(value)
				if !found {
					continue
				}
				*out = append(*out, Declaration{
					Surface:  MCP,
					Scope:    path,
					Owner:    tool,
					Resource: resource,
					Section:  "body",
					Name:     prefix + key,
					Max:      bound,
					Path:     path,
					Line:     fset.Position(value.Pos()).Line,
				})
			case "objectSchema":
				if len(value.Args) > 0 {
					collectToolBounds(fset, path, tool, resource, child, value.Args[0], out)
				}
			}
		case *ast.CompositeLit:
			// An array schema written as a bare map, whose `items` entry
			// describes the member.
			collectToolBounds(fset, path, tool, resource, child, value, out)
		}
	}
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
