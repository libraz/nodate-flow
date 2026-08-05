package eventbus

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// eventWriters are the helpers that put a row in the events log. Passing
// a transaction to any of them makes that transaction responsible for
// the fan-out boundary. ApplyTransitionTx is in the list because it
// appends the transition event itself.
var eventWriters = map[string]bool{
	"Append":             true,
	"AppendJudgeEvent":   true,
	"AppendReverseEvent": true,
	"AppendBestEffort":   true,
	"ApplyTransitionTx":  true,
}

// TestNoHandRolledTxAppendsEvents rejects the pairing that made
// realtime updates, in-app notifications and webhook deliveries go
// missing for agent-driven writes: a transaction opened with a bare
// BeginTx that then appends an event.
//
// The fan-out has to wait for the commit, and dbretry.InTx is what
// carries the commit boundary (via the collector on the context it
// passes down). A transaction opened by hand has no boundary, so the
// append can only either fire the hooks against a row nobody else can
// see yet or skip them — and both lose the delivery. The same write
// over a path that used InTx delivered fine, which is why this was
// reported as "webhooks work from the web app but not from my agent".
//
// Opening a transaction by hand stays perfectly fine as long as no
// event is appended in it, so the check is the pairing rather than
// BeginTx alone: an allowlist of the dozens of innocuous transactions
// would go stale immediately and hide the one that matters.
func TestNoHandRolledTxAppendsEvents(t *testing.T) {
	t.Parallel()

	root := flowAPIModuleRoot(t)
	var offenders []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			// Unparseable means uncompilable: the build rejects it and
			// this guard has nothing useful to say about it.
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			handRolled := handRolledTxNames(fn.Body)
			if len(handRolled) == 0 {
				continue
			}
			for _, use := range eventWriterUses(fn.Body, handRolled) {
				offenders = append(offenders, fmt.Sprintf("%s:%d %s passes hand-rolled %s to %s",
					rel, fset.Position(use.pos).Line, fn.Name.Name, use.txName, use.callee))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}

	if len(offenders) > 0 {
		t.Fatalf("a transaction that appends events must be opened with dbretry.InTx so the fan-out "+
			"fires after the commit:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// handRolledTxNames returns the identifiers in body that were assigned
// from a BeginTx call. The closure parameter dbretry.InTx hands out is
// never assigned this way, so InTx bodies do not match.
func handRolledTxNames(body *ast.BlockStmt) map[string]bool {
	names := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "BeginTx" {
			return true
		}
		if len(assign.Lhs) == 0 {
			return true
		}
		if ident, ok := assign.Lhs[0].(*ast.Ident); ok && ident.Name != "_" {
			names[ident.Name] = true
		}
		return true
	})
	return names
}

// writerUse is one call that hands a hand-rolled transaction to an
// event writer.
type writerUse struct {
	pos    token.Pos
	txName string
	callee string
}

// eventWriterUses finds calls in body that pass one of txNames to a
// helper listed in [eventWriters].
func eventWriterUses(body *ast.BlockStmt, txNames map[string]bool) []writerUse {
	var uses []writerUse
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !eventWriters[sel.Sel.Name] {
			return true
		}
		for _, arg := range call.Args {
			ident, ok := arg.(*ast.Ident)
			if ok && txNames[ident.Name] {
				uses = append(uses, writerUse{pos: call.Lparen, txName: ident.Name, callee: sel.Sel.Name})
			}
		}
		return true
	})
	return uses
}
