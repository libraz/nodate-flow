package eventbus

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	sharedbus "github.com/libraz/nodate-flow/packages/go-shared/eventbus"
	"github.com/libraz/nodate-flow/packages/go-shared/eventlog"
)

// TestBridgeDeliversEventlogAppends is the behavioural half of the
// wiring: an append made through the cross-service eventlog must reach a
// subscriber of this package, carrying the event id the subscriber needs
// to resolve the row.
func TestBridgeDeliversEventlogAppends(t *testing.T) {
	db := stubDB(t)
	eventlog.ClearHooks()
	t.Cleanup(eventlog.ClearHooks)
	resetBridge(t)

	var (
		gotType string
		gotWS   uint32
		gotID   uint64
		fired   int
	)
	handle := AddNotifyHook(func(_ context.Context, ws uint32, eventType string, eventInternalID uint64) {
		fired++
		gotWS, gotType, gotID = ws, eventType, eventInternalID
	})
	t.Cleanup(func() { RemoveNotifyHook(handle) })

	BridgeEventlog()

	if _, err := eventlog.Append(context.Background(), dbretry.AutoCommit(db), eventlog.Event{
		Type:        sharedbus.WorkspaceMemberAdded,
		WorkspaceID: 11,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	if fired != 1 {
		t.Fatalf("an eventlog append must reach eventbus subscribers, fired = %d", fired)
	}
	if gotType != "workspace.member.added" || gotWS != 11 {
		t.Fatalf("subscriber saw ws=%d type=%q", gotWS, gotType)
	}
	if gotID == 0 {
		t.Fatal("subscribers need the event id to resolve the row they were told about")
	}
}

// TestBridgeWaitsForCommit pins that the forwarded fan-out obeys the
// same commit boundary as a native append: memberkit and itemkit write
// inside transactions, and a subscriber woken before the commit reads
// its own connection and finds nothing.
func TestBridgeWaitsForCommit(t *testing.T) {
	db := stubDB(t)
	eventlog.ClearHooks()
	t.Cleanup(eventlog.ClearHooks)
	resetBridge(t)

	fired := 0
	handle := AddNotifyHook(func(context.Context, uint32, string, uint64) { fired++ })
	t.Cleanup(func() { RemoveNotifyHook(handle) })
	BridgeEventlog()

	firedInside := false
	err := dbretry.InTx(context.Background(), db, "eventbus.bridge.test", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		if _, err := eventlog.Append(ctx, tx, eventlog.Event{Type: sharedbus.ItemScheduled, WorkspaceID: 3}); err != nil {
			return err
		}
		firedInside = fired > 0
		return nil
	})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}
	if firedInside {
		t.Fatal("the forwarded fan-out ran while the transaction was still open")
	}
	if fired != 1 {
		t.Fatalf("the forwarded fan-out must run once after commit, ran %d times", fired)
	}
}

// The bridge inherits the eventbus rule: a transaction opened by hand
// has no commit boundary, so the forwarded fan-out would fire against a
// row nobody else can see. There is no test for that case because
// eventlog.Append takes a dbretry.CommitBoundary, which a bare *sql.Tx
// does not satisfy: the call does not compile.

// TestEventlogBridgeIsWired is the structural half. The defect was never
// that the forwarding was wrong — it did not exist, while every
// subscriber it feeds was implemented, tested and configurable. A test
// that only exercises BridgeEventlog would stay green with nothing in
// the binary calling it, so this asserts the process wiring itself.
//
// It parses main.go and looks for a live call expression rather than
// searching the text for the identifier. Searching text cannot tell a
// call from a mention: `// eventbus.BridgeEventlog()` satisfies a
// substring check exactly as well as the real thing, and commenting a
// line out during debugging and forgetting to restore it is a more
// likely way to lose the wiring than deleting it. Comments never reach
// the AST, so parsing removes that whole class.
func TestEventlogBridgeIsWired(t *testing.T) {
	t.Parallel()

	mainPath := filepath.Join(flowAPIModuleRoot(t), "cmd", "api", "main.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, mainPath, nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	found := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "BridgeEventlog" {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if ok && pkg.Name == "eventbus" {
				found = true
				return false
			}
			return true
		})
		if found {
			break
		}
	}

	if !found {
		t.Fatal("cmd/api/main.go must call eventbus.BridgeEventlog() from a function body: without it " +
			"every append made through the shared eventlog (itemkit, memberkit) fans out to nobody — " +
			"no realtime refresh, no notification row, no webhook delivery — and nothing reports an error")
	}
}

// resetBridge clears the one-shot registration so each test can install
// the bridge against its own subscriber.
func resetBridge(t *testing.T) {
	t.Helper()
	bridgeMu.Lock()
	bridgeHandle = nil
	bridgeMu.Unlock()
	t.Cleanup(func() {
		bridgeMu.Lock()
		bridgeHandle = nil
		bridgeMu.Unlock()
	})
}
