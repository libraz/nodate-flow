package db

import (
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestGeneratedIDFieldsAreNotSerialized requires every generated field
// whose name ends in ID to be either a public identifier or excluded
// from JSON.
//
// The two-tier id scheme only holds if the internal integer never
// reaches a response body. sqlc decides that per column, from the
// `json:"-"` override list in sql/sqlc.yaml, and that list is written by
// hand — so it drifts the moment a schema change adds a foreign key
// nobody remembers to enumerate. It had already drifted by six columns
// (agent_id, actor_agent_id, model_id, from_task_id, to_task_id,
// share_id, recurrence_parent_id), each of which serialised a row's
// sequence position straight into any handler that returned the
// generated struct.
//
// The rule is decidable from the name because sqlc's naming is
// mechanical: a column ending in _id becomes a field ending in ID. What
// distinguishes a legitimate one is its type — types.PublicID is the
// UUID v7 that is *meant* to be public. Anything else ending in ID is an
// internal surrogate key and has to carry `json:"-"`.
//
// The one exception is derived from the schema rather than listed here:
// a foreign key into a master / enumeration table names an id that is
// documented as safe to publish. See masterForeignKeyColumns.
//
// This reads the generated source rather than reflecting over the
// packages so it can cover all three generated trees, including
// auth-api's, which lives in a different module and cannot be imported
// from here.
func TestGeneratedIDFieldsAreNotSerialized(t *testing.T) {
	t.Parallel()

	exempt := masterForeignKeyColumns(t)
	var offenders []string

	for _, f := range walkGeneratedFields(t) {
		// Params types are the arguments a query takes, built by server
		// code and never marshalled to a client, so the serialisation
		// rule does not reach them. They also carry fields sqlc cannot
		// tag at all: a sqlc.arg() bound through a CAST is a named
		// parameter rather than a column, and column overrides do not
		// apply to it.
		if strings.HasSuffix(f.Struct, "Params") {
			continue
		}
		if !ast.IsExported(f.Name) || !strings.HasSuffix(f.Name, "ID") {
			continue
		}
		if !isIntegerType(f.Type) || hasJSONExcludeTag(f.Tag) {
			continue
		}
		if _, ok := exempt[normalizeIdentifier(f.Name)]; ok {
			continue
		}
		offenders = append(offenders, fmt.Sprintf("%s:%d %s.%s", f.Path, f.Line, f.Struct, f.Name))
	}

	if len(offenders) > 0 {
		t.Fatalf("an internal surrogate key must not reach a JSON response; add the column to "+
			"the json:\"-\" override list in sql/sqlc.yaml and re-run make gen-sqlc:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// integerIDTypes are the Go types sqlc emits for an INT UNSIGNED
// surrogate key, nullable or not. The type is what makes the rule
// precise: a public identifier is a types.PublicID (or, where an
// override is still missing, a string), and neither is a sequence
// position. An integer field whose name ends in ID is one.
var integerIDTypes = map[string]bool{
	"uint32":           true,
	"uint64":           true,
	"int32":            true,
	"int64":            true,
	"sql.NullInt16":    true,
	"sql.NullInt32":    true,
	"sql.NullInt64":    true,
	"sql.NullByte":     true,
	"generated.NullID": false,
}

func isIntegerType(expr ast.Expr) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	switch t := expr.(type) {
	case *ast.Ident:
		return integerIDTypes[t.Name]
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		if !ok {
			return false
		}
		return integerIDTypes[pkg.Name+"."+t.Sel.Name]
	default:
		return false
	}
}

func hasJSONExcludeTag(tag *ast.BasicLit) bool {
	if tag == nil {
		return false
	}
	// The literal arrives quoted; strip the backticks before reading it
	// as a struct tag.
	raw := strings.Trim(tag.Value, "`")
	return reflect.StructTag(raw).Get("json") == "-"
}

// generatedRoots returns every sqlc output directory in the repository.
func generatedRoots(t *testing.T) []string {
	t.Helper()

	repo, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	roots := []string{
		filepath.Join(repo, "apps", "flow-api", "internal", "db", "generated"),
		filepath.Join(repo, "apps", "auth-api", "internal", "db", "generated"),
	}
	for _, r := range roots {
		if _, err := os.Stat(r); err != nil {
			t.Fatalf("expected generated sources at %s: %v", r, err)
		}
	}
	return roots
}

func relativeTo(t *testing.T, path string) string {
	t.Helper()

	repo, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(repo, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}
