// Package enumparity derives, from the committed handler sources, the
// request fields whose accepted value set is stated on one operation and
// left open on another.
//
// A Huma input struct is the only place the API says which values a field
// takes. When the create operation states them and the update operation
// does not, the two ends of the same column disagree about what a valid
// request is, and the open end has nothing behind it but whatever the
// storage layer happens to refuse. That refusal arrives as a write
// failure, which tells the caller their input was fine and the server is
// broken, and it leaves every reader downstream holding a value no rule
// classifies.
//
// The asymmetry is what makes this derivable. Nothing here knows which
// fields are enum-backed or what their values are — it knows only that
// within one handler package, two operations describe the same wire field,
// and one of them constrains it. Whichever of the two is right, they
// cannot both be.
//
//	scope       every wire field reachable from a type named *Input under
//	            the handler trees, through its anonymous body struct or
//	            through the package-local body type it names
//	pairing     fields are the same when they share a package, a section
//	            (body or query) and a wire name. A body field and a query
//	            parameter that happen to share a name are different
//	            fields: a filter legitimately accepts values a write does
//	            not. A field nested under another carries its parent's
//	            name, so a member of an object is never paired with a
//	            top-level field spelled the same.
//	divergence  some declarations of that field carry an enum constraint
//	            and some do not.
//
// Differing value sets are not a divergence. Two operations may
// legitimately accept different subsets of a column — recording an RSVP
// through an invite cannot leave it pending, while setting one directly
// can — and both of those state their set. What no operation may do is
// state nothing.
//
// Only string-shaped fields are read. An enum constraint describes a set
// of strings, so a field of any other type is not one this can be missing.
package enumparity

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// Roots are the handler trees the scope is read from, relative to the
// repository root. Both API binaries declare their request shapes the same
// way, so a field left open in either one is the same defect.
var Roots = []string{
	filepath.Join("apps", "flow-api", "internal", "http", "handlers"),
	filepath.Join("apps", "auth-api", "internal", "http", "handlers"),
}

// Field is one wire field reachable from a Huma input struct.
type Field struct {
	// Package is the repository-relative directory the declaration sits
	// in, which is what scopes a comparison: two packages describing a
	// field of the same name are describing different resources.
	Package string
	// Owner is the input type the field was reached from, named in
	// failures so the two sides of a divergence can be told apart.
	Owner string
	// Section is "body" or "query": where on the request the field
	// travels.
	Section string
	// Name is the wire name, qualified by the names of the objects it is
	// nested under.
	Name string
	// Enum is the enum tag's value, empty when the field carries none.
	Enum string
	// Path and Line locate the declaration.
	Path string
	Line int
}

// Constrained reports whether the declaration states the values it takes.
func (f Field) Constrained() bool {
	return f.Enum != ""
}

// Location renders the declaration's position for a failure message.
func (f Field) Location() string {
	return fmt.Sprintf("%s:%d", f.Path, f.Line)
}

// Divergence is one wire field described both ways by the operations that
// write one resource.
type Divergence struct {
	Package string
	// Resource is the noun the paired operations write, taken from their
	// input type names.
	Resource string
	Section  string
	Name     string
	// With and Without are the declarations on each side, in file order.
	With    []Field
	Without []Field
}

// Sets renders the value sets the constrained side states, deduplicated,
// so a failure can quote what the open side is missing.
func (d Divergence) Sets() []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range d.With {
		if seen[f.Enum] {
			continue
		}
		seen[f.Enum] = true
		out = append(out, f.Enum)
	}
	return out
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
			return "", errors.New("enumparity: go.work not found above the working directory")
		}
		dir = parent
	}
}

// Fields reads every input field under the handler roots.
//
// Files are grouped by directory before they are read: half the inputs
// name their body as a separate type in the same package, so a file read
// on its own would see the marker field and none of the fields the request
// actually carries.
func Fields(root string, roots []string) ([]Field, error) {
	packages := map[string]map[string]string{}
	var order []string

	for _, rel := range roots {
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
			return nil, err
		}
	}
	sort.Strings(order)

	var out []Field
	for _, pkg := range order {
		fields, err := ParsePackage(pkg, packages[pkg])
		if err != nil {
			return nil, err
		}
		out = append(out, fields...)
	}
	return out, nil
}

// ParsePackage reads the input fields out of one package, given its files
// keyed by path. It is exported so the control can hold the derivation
// against a source it states in full, rather than against whatever the
// tree happens to contain.
func ParsePackage(pkg string, sources map[string]string) ([]Field, error) {
	fset := token.NewFileSet()
	paths := make([]string, 0, len(sources))
	for path := range sources {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	type decl struct {
		path   string
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
				decls = append(decls, decl{path: path, name: ts.Name.Name, strukt: st})
			}
		}
	}

	var out []Field
	for _, d := range decls {
		if !strings.HasSuffix(d.name, "Input") {
			continue
		}
		w := &walker{fset: fset, pkg: pkg, owner: d.name, structs: structs}
		w.walk(d.strukt, "", map[string]bool{d.name: true})
		out = append(out, w.found...)
	}
	return out, nil
}

// walker collects the wire fields reachable from one input type.
type walker struct {
	fset    *token.FileSet
	pkg     string
	owner   string
	structs map[string]*ast.StructType
	found   []Field
}

// walk reads a struct's fields and descends into the structs they name,
// whether written inline or declared beside the input in the same package.
//
// prefix carries the names of the objects a field is nested under, so a
// member of an object is never paired with a top-level field of the same
// name. The body marker field itself contributes no prefix: it has no wire
// name, because the body is the request rather than a member of it.
//
// visiting is the cycle guard. A type that refers to itself is legal Go
// and would otherwise be descended into forever.
func (w *walker) walk(st *ast.StructType, prefix string, visiting map[string]bool) {
	for _, field := range st.Fields.List {
		section, name := "", ""
		if field.Tag != nil {
			section, name = wireName(reflect.StructTag(strings.Trim(field.Tag.Value, "`")))
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

		if field.Tag == nil || name == "" || !isStringShaped(field.Type) {
			continue
		}
		tag := reflect.StructTag(strings.Trim(field.Tag.Value, "`"))
		w.found = append(w.found, Field{
			Package: w.pkg,
			Owner:   w.owner,
			Section: section,
			Name:    prefix + name,
			Enum:    tag.Get("enum"),
			Path:    w.fset.Position(field.Pos()).Filename,
			Line:    w.fset.Position(field.Pos()).Line,
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

// wireName returns where the field travels and what it is called there.
// A path parameter is deliberately not one: it identifies a resource
// rather than carrying a value, and it is spelled the same on every
// operation that names that resource.
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

// isStringShaped reports whether an enum constraint could apply to the
// field's type: a string, a pointer to one, or a slice of them.
func isStringShaped(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name == "string"
	case *ast.StarExpr:
		return isStringShaped(t.X)
	case *ast.ArrayType:
		return t.Len == nil && isStringShaped(t.Elt)
	default:
		return false
	}
}

// writeVerbs are the operation prefixes that name a write on a resource.
// Everything after the verb, up to Input, is the resource — which is what
// pairs two operations that describe the same thing.
//
// A name outside this vocabulary is out of scope rather than paired on its
// own: a package as large as tasks holds unrelated operations whose bodies
// share ordinary field names, and pairing on the name alone puts a
// free-text note beside a categorical one because both are called reason.
var writeVerbs = []string{
	"Create", "Add", "New",
	"Patch", "Update", "Put", "Edit", "Set", "Replace",
}

// resourceOf splits an input type name into the resource it writes,
// returning false when the name carries none of the write verbs.
func resourceOf(owner string) (string, bool) {
	name := strings.TrimSuffix(owner, "Input")
	for _, verb := range writeVerbs {
		if !strings.HasPrefix(name, verb) {
			continue
		}
		if rest := name[len(verb):]; rest != "" {
			return rest, true
		}
	}
	return "", false
}

// Comparisons returns the field groups this check actually reasons over:
// one wire field of one resource, described by two or more of the
// operations that write it.
//
// It is separate from Divergences because the failure mode of a derived
// check is that the derivation stops matching — a renamed suffix, a body
// type that moved packages — and then it passes because it compared
// nothing. The caller asserts on this set for that reason.
func Comparisons(fields []Field) []Divergence {
	type key struct{ pkg, resource, section, name string }
	groups := map[key]*Divergence{}
	owners := map[key]map[string]bool{}
	var order []key

	for _, f := range fields {
		resource, ok := resourceOf(f.Owner)
		if !ok {
			continue
		}
		k := key{f.Package, resource, f.Section, f.Name}
		group, seen := groups[k]
		if !seen {
			group = &Divergence{Package: f.Package, Resource: resource, Section: f.Section, Name: f.Name}
			groups[k] = group
			owners[k] = map[string]bool{}
			order = append(order, k)
		}
		owners[k][f.Owner] = true
		if f.Constrained() {
			group.With = append(group.With, f)
			continue
		}
		group.Without = append(group.Without, f)
	}

	var out []Divergence
	for _, k := range order {
		if len(owners[k]) < 2 {
			continue
		}
		out = append(out, *groups[k])
	}
	return out
}

// Divergences narrows the comparisons down to the fields described both
// ways: constrained by one operation, left open by another.
func Divergences(fields []Field) []Divergence {
	var out []Divergence
	for _, c := range Comparisons(fields) {
		if len(c.With) == 0 || len(c.Without) == 0 {
			continue
		}
		out = append(out, c)
	}
	return out
}
