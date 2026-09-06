package timeboxes

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// timeboxRecordGates are the recorder entry points a timebox handler
// records through, keyed by the method name a call site writes.
var timeboxRecordGates = map[string]bool{
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
	// kindConst is the event-kind constant as written, or empty when the
	// kind is chosen at run time.
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
		if !ok || !timeboxRecordGates[sel.Sel.Name] {
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

// timeboxWrites is every timebox operation that changes something, with
// the pair of names it files the change under and the payload keys that
// make the two rows readable on their own.
//
// UpdateStatus names no kind here: it picks one of three from the target
// status, and the constants it picks from are in its own body.
//
// linksTask marks the writes whose subject is a task as much as a
// timebox. Those records have to set TaskID, because events.task_id is
// what the task's own timeline selects on and a record without it is
// filed against the timebox alone.
var timeboxWrites = []struct {
	file        string
	fn          string
	action      string
	kind        string
	payloadKeys []string
	linksTask   bool
}{
	{"handlers.go", "Create", "timebox.create", "TimeboxCreated", []string{"timeboxId", "name"}, false},
	{"handlers.go", "Update", "timebox.update", "TimeboxUpdated", []string{"timeboxId", "name"}, false},
	{"handlers.go", "UpdateStatus", "timebox.status", "", []string{"timeboxId", "status"}, false},
	{"handlers.go", "Delete", "timebox.delete", "TimeboxArchived", []string{"timeboxId"}, false},
	{"handlers.go", "AddTask", "timebox.task.add", "TimeboxTaskAdded", []string{"timeboxId", "taskId"}, true},
	{"handlers.go", "RemoveTask", "timebox.task.remove", "TimeboxTaskRemoved", []string{"timeboxId", "taskId"}, true},
}

// TestTimeboxWritesRecordBothHalves holds every timebox write to one
// record naming both halves of the trail.
//
// The payload is checked because it is stored as both the event payload
// and the audit metadata: two descriptions of one change drift, and a
// reader comparing the tables then cannot tell which is stale.
func TestTimeboxWritesRecordBothHalves(t *testing.T) {
	t.Parallel()

	for _, w := range timeboxWrites {
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
			if !site.fields["EventType"] {
				t.Errorf("line %d: names no event kind, so the change reaches no timeline", site.line)
			}
			if w.kind != "" && site.kindConst != w.kind {
				t.Errorf("line %d: event kind %q, want the constant %s; a kind written any other way is one both transports cannot spell once",
					site.line, site.kindConst, w.kind)
			}
			if got := site.strings["ResourceType"]; got != "timebox" {
				t.Errorf("line %d: resource type %q, want %q", site.line, got, "timebox")
			}
			for _, field := range []string{"ResourceID", "CallSite"} {
				if !site.fields[field] {
					t.Errorf("line %d: names no %s, so the record cannot be found by the query that looks for it", site.line, field)
				}
			}
			if w.linksTask && !site.fields["TaskID"] {
				t.Errorf("line %d: the change targets a task and names no TaskID; `events` links a task through that column alone, "+
					"so the row reaches the timebox timeline and never the task's own", site.line)
			}
			for _, key := range w.payloadKeys {
				if !site.payload[key] {
					t.Errorf("line %d: the payload carries no %s; `events` holds no resource column, so a payload that "+
						"does not name the subject describes a change to nothing in particular", site.line, key)
				}
			}
		})
	}
}

// TestTimeboxWritesRecordUnconditionally holds each record to the
// handler's own statement list.
//
// This is the half that keeps the two rows together. A condition around
// one half and not the other puts the change on the timeline while the
// audit query an administrator runs finds nothing, and a condition
// around the single record still hides the change from both tables on
// the requests it excludes.
func TestTimeboxWritesRecordUnconditionally(t *testing.T) {
	t.Parallel()

	for _, w := range timeboxWrites {
		t.Run(w.fn, func(t *testing.T) {
			t.Parallel()

			sites := recordSitesIn(t, w.file, w.fn)
			if len(sites) != 1 {
				t.Fatalf("want one record for %s, found %d", w.fn, len(sites))
			}
			if sites[0].topLevel < 0 {
				t.Errorf("line %d: %s records inside a branch; one table then holds the change and the other does not",
					sites[0].line, w.fn)
			}
		})
	}
}

// TestTimeboxWritesRecordNothingWhenNoRowChanged pairs the check above.
//
// Delete matches only a timebox that is still live and RemoveTask only a
// link row that exists, so both statements match nothing on a second
// call and the affected-row count is the only thing that knows. A record
// written above that guard is an event and an audit row for a change no
// row carries — which is what a success path proving the record exists
// cannot rule out on its own.
func TestTimeboxWritesRecordNothingWhenNoRowChanged(t *testing.T) {
	t.Parallel()

	for _, fn := range []string{"Delete", "RemoveTask"} {
		t.Run(fn, func(t *testing.T) {
			t.Parallel()

			guard := affectedRowGuardIndex(t, "handlers.go", fn)
			if guard < 0 {
				t.Fatalf("%s answers on the affected-row count, and no `if <count> == 0 { return }` was found; "+
					"without it the handler reports a change it may not have made", fn)
			}
			sites := recordSitesIn(t, "handlers.go", fn)
			if len(sites) != 1 {
				t.Fatalf("want one record for %s, found %d", fn, len(sites))
			}
			if sites[0].topLevel <= guard {
				t.Errorf("line %d: %s records at or above the affected-row guard, so the record is written on the path "+
					"that changed nothing as well as the path that changed something", sites[0].line, fn)
			}
		})
	}
}
