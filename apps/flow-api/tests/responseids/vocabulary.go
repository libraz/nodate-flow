package responseids

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// ModelsPath is the generated model file the vocabulary is read from,
// relative to the repository root. It holds one struct per table, with the
// column's Go type and the tag saying whether the column is serialised.
var ModelsPath = filepath.Join("apps", "flow-api", "internal", "db", "generated", "models.go")

// MCPRoot is the tree the tool handlers live in.
var MCPRoot = filepath.Join("apps", "flow-api", "internal", "mcp")

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
			return "", errors.New("responseids: go.work not found above the working directory")
		}
		dir = parent
	}
}

// Vocabulary is the set of model field names that hold an internal id.
type Vocabulary map[string]bool

// Names returns the vocabulary in a stable order, for reporting.
func (v Vocabulary) Names() []string {
	out := make([]string, 0, len(v))
	for name := range v {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// InternalIDs reads the vocabulary out of the generated models.
func InternalIDs(root string) (Vocabulary, error) {
	path := filepath.Join(root, ModelsPath)
	raw, err := os.ReadFile(path) //#nosec G304,G122 -- repository path read at test time
	if err != nil {
		return nil, err
	}
	return ParseModels(filepath.ToSlash(ModelsPath), string(raw))
}

// ParseModels reads the internal-id field names out of a model source. It is
// exported so the control can drive a source it states in full through the
// same derivation the tree goes through.
//
// A field qualifies on two counts at once: its type is a surrogate key's
// spelling, and the generated tag says the column is never serialised. Type
// alone reaches the ordinary counters stored beside the ids, and the tag
// alone reaches every column held back from JSON for some other reason.
func ParseModels(path, src string) (Vocabulary, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	out := Vocabulary{}
	ast.Inspect(file, func(n ast.Node) bool {
		st, ok := n.(*ast.StructType)
		if !ok {
			return true
		}
		for _, field := range st.Fields.List {
			if field.Tag == nil || len(field.Names) == 0 {
				continue
			}
			if !withheldFromJSON(reflect.StructTag(strings.Trim(field.Tag.Value, "`"))) {
				continue
			}
			if !IsInternalIDType(field.Type) {
				continue
			}
			for _, name := range field.Names {
				out[name.Name] = true
			}
		}
		return true
	})
	return out, nil
}

// withheldFromJSON reports whether the tag says the column never reaches a
// JSON document.
func withheldFromJSON(tag reflect.StructTag) bool {
	v, ok := tag.Lookup("json")
	if !ok {
		return false
	}
	name, _, _ := strings.Cut(v, ",")
	return name == "-"
}

// unsignedIntegers are the Go spellings of a surrogate key's column.
var unsignedIntegers = map[string]bool{
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
}

// nullableIntegers are the same key where the column admits NULL. The
// generated type is signed because that is what the database/sql scanner
// offers, so the type alone does not say which of the two it is — the tag
// does, and both are read together.
var nullableIntegers = map[string]bool{
	"NullInt16": true, "NullInt32": true, "NullInt64": true,
}

// IsInternalIDType reports whether a declared type is one an internal id is
// spelled with. A public id is a UUID type and is not one of these, which is
// the whole distinction this package rests on.
func IsInternalIDType(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.Ident:
		return unsignedIntegers[t.Name]
	case *ast.StarExpr:
		return IsInternalIDType(t.X)
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		return ok && pkg.Name == "sql" && nullableIntegers[t.Sel.Name]
	default:
		return false
	}
}
