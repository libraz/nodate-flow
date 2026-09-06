package lenses

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// lensRecordGates are the recorder entry points a lens handler records
// through, keyed by the method name a call site writes.
var lensRecordGates = map[string]bool{
	"Record":        true,
	"RecordStrict":  true,
	"RecordInTx":    true,
	"RecordTxAudit": true,
}

// recordSite is one mutation literal handed to one of those entry
// points, together with where the handler asks for it.
//
// It is read out of the source because both halves of a record land in
// the database and a package test has no database. What a package test
// can hold is the shape of what the handler asks for and the place it
// asks from, and both failures this guards against live there: a change
// recorded in one table only, and a record reached on a path that
// changed no row.
type recordSite struct {
	line int
	// topLevel is the index of the record's statement in the handler
	// body, or -1 when the call sits inside a branch. A record nested in
	// a condition is written on some requests and not others, and
	// nothing downstream can tell a change that was not recorded from a
	// change that did not happen.
	topLevel int
	fields   map[string]bool
	strings  map[string]string
	// kindConst is the event-kind constant as written, so a test can say
	// which kind is filed without the string being restated here.
	kindConst string
	payload   map[string]bool
}

// handlerBody returns the closure a handler constructor hands the
// router. The constructor's own body is a single return, so the
// statements a request runs are the closure's.
func handlerBody(t *testing.T, file, fn string) (*ast.BlockStmt, *token.FileSet) {
	t.Helper()

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	var decl *ast.FuncDecl
	for _, d := range parsed.Decls {
		f, ok := d.(*ast.FuncDecl)
		if ok && f.Name.Name == fn && f.Body != nil {
			decl = f
			break
		}
	}
	if decl == nil {
		t.Fatalf("%s declares no %s", file, fn)
	}
	var body *ast.BlockStmt
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		if body != nil {
			return false
		}
		if lit, ok := n.(*ast.FuncLit); ok {
			body = lit.Body
			return false
		}
		return true
	})
	if body == nil {
		t.Fatalf("%s.%s returns no handler closure", file, fn)
	}
	return body, fset
}

// recordSitesIn returns every record the named handler asks for, in
// source order.
func recordSitesIn(t *testing.T, file, fn string) []recordSite {
	t.Helper()

	body, fset := handlerBody(t, file, fn)
	topLevel := map[ast.Node]int{}
	for i, stmt := range body.List {
		topLevel[stmt] = i
	}

	var out []recordSite
	ast.Inspect(body, func(n ast.Node) bool {
		stmt, ok := n.(*ast.ExprStmt)
		if !ok {
			return true
		}
		call, ok := stmt.X.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !lensRecordGates[sel.Sel.Name] {
			return true
		}
		line := fset.Position(call.Pos()).Line
		lit := mutationLiteralArg(call)
		if lit == nil {
			t.Fatalf("%s:%d: %s is handed a mutation assembled elsewhere; written at the call site it is also what the "+
				"package guard reads, and away from it nothing checks the shape", file, line, sel.Sel.Name)
		}
		index, isTopLevel := topLevel[ast.Node(stmt)]
		if !isTopLevel {
			index = -1
		}
		site := recordSite{
			line:     line,
			topLevel: index,
			fields:   map[string]bool{},
			strings:  map[string]string{},
			payload:  map[string]bool{},
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			site.fields[key.Name] = true
			if s, ok := literalString(kv.Value); ok {
				site.strings[key.Name] = s
			}
			if key.Name == "EventType" {
				if kindSel, ok := kv.Value.(*ast.SelectorExpr); ok {
					site.kindConst = kindSel.Sel.Name
				}
			}
			if key.Name != "Payload" {
				continue
			}
			payload, ok := kv.Value.(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, entry := range payload.Elts {
				pair, ok := entry.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if s, ok := literalString(pair.Key); ok {
					site.payload[s] = true
				}
			}
		}
		out = append(out, site)
		return true
	})
	return out
}

// mutationLiteralArg returns the mutationlog.Mutation written inline at
// a call, or nil when none was.
func mutationLiteralArg(call *ast.CallExpr) *ast.CompositeLit {
	for _, arg := range call.Args {
		lit, ok := arg.(*ast.CompositeLit)
		if !ok {
			continue
		}
		sel, ok := lit.Type.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		pkg, ok := sel.X.(*ast.Ident)
		if ok && pkg.Name == "mutationlog" && sel.Sel.Name == "Mutation" {
			return lit
		}
	}
	return nil
}

// literalString returns the value of a string literal expression.
func literalString(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

// affectedRowGuardIndex returns the position, among the handler's own
// statements, of the `if <count> == 0 { return ... }` that answers not
// found, or -1 when the handler has none.
//
// The count is what the statement returns, so it is the only thing that
// knows whether the write matched a row. A record written above this
// guard describes a change to a row that was not there.
func affectedRowGuardIndex(t *testing.T, file, fn string) int {
	t.Helper()

	body, _ := handlerBody(t, file, fn)
	for i, stmt := range body.List {
		ifStmt, ok := stmt.(*ast.IfStmt)
		if !ok {
			continue
		}
		cond, ok := ifStmt.Cond.(*ast.BinaryExpr)
		if !ok || cond.Op != token.EQL {
			continue
		}
		if _, ok := cond.X.(*ast.Ident); !ok {
			continue
		}
		if zero, ok := cond.Y.(*ast.BasicLit); !ok || zero.Value != "0" {
			continue
		}
		if len(ifStmt.Body.List) == 0 {
			continue
		}
		if _, returns := ifStmt.Body.List[len(ifStmt.Body.List)-1].(*ast.ReturnStmt); !returns {
			continue
		}
		return i
	}
	return -1
}

// lensWrites is every lens operation that changes something, with the
// pair of names it files the change under and the payload keys that make
// the two rows readable on their own.
var lensWrites = []struct {
	file        string
	fn          string
	action      string
	kind        string
	payloadKeys []string
}{
	{"crud.go", "Create", "lens.create", "LensCreated", []string{"lensId", "name"}},
	{"crud.go", "Update", "lens.update", "LensUpdated", []string{"lensId"}},
	{"crud.go", "Delete", "lens.delete", "LensArchived", []string{"lensId"}},
	{"public.go", "Publish", "lens.publish", "LensShared", []string{"lensId"}},
	{"public.go", "Unpublish", "lens.unpublish", "LensUnshared", []string{"lensId"}},
}

// TestLensWritesRecordBothHalves holds every lens write to one record
// naming both halves of the trail.
//
// A lens that appears on no timeline and a lens whose creation no audit
// query can find are the same defect seen from two tables, and the table
// that did get a row reads as a complete answer to whoever queries it.
func TestLensWritesRecordBothHalves(t *testing.T) {
	t.Parallel()

	for _, w := range lensWrites {
		t.Run(w.fn, func(t *testing.T) {
			t.Parallel()

			sites := recordSitesIn(t, w.file, w.fn)
			if len(sites) != 1 {
				t.Fatalf("want one record for %s, found %d", w.fn, len(sites))
			}
			site := sites[0]

			if got := site.strings["AuditAction"]; got != w.action {
				t.Errorf("line %d: audit action %q, want %q", site.line, got, w.action)
			}
			if got := site.kindConst; got != w.kind {
				t.Errorf("line %d: event kind %q, want the constant %s; a kind written any other way is one both transports cannot spell once",
					site.line, got, w.kind)
			}
			if got := site.strings["ResourceType"]; got != "lens" {
				t.Errorf("line %d: resource type %q, want %q", site.line, got, "lens")
			}
			for _, field := range []string{"ResourceID", "CallSite"} {
				if !site.fields[field] {
					t.Errorf("line %d: names no %s, so the record cannot be found by the query that looks for it", site.line, field)
				}
			}
			for _, key := range w.payloadKeys {
				if !site.payload[key] {
					t.Errorf("line %d: the payload carries no %s; `events` holds no resource column, so a payload that "+
						"does not name the lens describes a change to nothing in particular", site.line, key)
				}
			}
		})
	}
}

// TestLensWritesRecordUnconditionally holds each record to the handler's
// own statement list.
//
// A record nested in a condition is recorded on some requests and not
// others, and nothing downstream can tell the difference between a
// change that was not recorded and a change that did not happen.
func TestLensWritesRecordUnconditionally(t *testing.T) {
	t.Parallel()

	for _, w := range lensWrites {
		t.Run(w.fn, func(t *testing.T) {
			t.Parallel()

			sites := recordSitesIn(t, w.file, w.fn)
			if len(sites) != 1 {
				t.Fatalf("want one record for %s, found %d", w.fn, len(sites))
			}
			if sites[0].topLevel < 0 {
				t.Errorf("line %d: %s records inside a branch; a change recorded on some requests and not others is "+
					"indistinguishable from one that did not happen", sites[0].line, w.fn)
			}
		})
	}
}

// TestLensWritesRecordNothingWhenNoRowChanged pairs the check above.
//
// Delete, Publish and Unpublish all guard on `enabled` or on the share
// state, so the statement matches nothing when the lens is already gone
// or the share has already been taken down, and the affected-row count
// is the only thing that knows. A record written above that guard is an
// event and an audit row for a change no row carries — which is what a
// success path proving the record exists cannot rule out on its own.
func TestLensWritesRecordNothingWhenNoRowChanged(t *testing.T) {
	t.Parallel()

	for _, w := range []struct{ file, fn string }{
		{"crud.go", "Delete"},
		{"public.go", "Publish"},
		{"public.go", "Unpublish"},
	} {
		t.Run(w.fn, func(t *testing.T) {
			t.Parallel()

			guard := affectedRowGuardIndex(t, w.file, w.fn)
			if guard < 0 {
				t.Fatalf("%s answers on the affected-row count, and no `if <count> == 0 { return }` was found; "+
					"without it the handler reports a change it may not have made", w.fn)
			}
			sites := recordSitesIn(t, w.file, w.fn)
			if len(sites) != 1 {
				t.Fatalf("want one record for %s, found %d", w.fn, len(sites))
			}
			if sites[0].topLevel <= guard {
				t.Errorf("line %d: %s records at or above the affected-row guard, so the record is written on the path "+
					"that changed nothing as well as the path that changed something", sites[0].line, w.fn)
			}
		})
	}
}
