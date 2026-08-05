package eventbus

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
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

	if _, err := eventlog.Append(context.Background(), db, eventlog.Event{
		Type:        "workspace.member.added",
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
	err := dbretry.InTx(context.Background(), db, "eventbus.bridge.test", nil, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := eventlog.Append(ctx, tx, eventlog.Event{Type: "item.scheduled", WorkspaceID: 3}); err != nil {
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

// TestBridgeRefusedInHandRolledTx mirrors the eventbus rule: a
// transaction opened by hand has no commit boundary, so the fan-out is
// refused rather than fired against a row nobody else can see.
func TestBridgeRefusedInHandRolledTx(t *testing.T) {
	db := stubDB(t)
	eventlog.ClearHooks()
	t.Cleanup(eventlog.ClearHooks)
	resetBridge(t)

	fired := 0
	handle := AddNotifyHook(func(context.Context, uint32, string, uint64) { fired++ })
	t.Cleanup(func() { RemoveNotifyHook(handle) })
	BridgeEventlog()

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err := eventlog.Append(ctx, tx, eventlog.Event{Type: "item.scheduled", WorkspaceID: 3}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if fired != 0 {
		t.Fatalf("fan-out must be refused without a commit boundary, fired %d times", fired)
	}
}

// TestEventlogBridgeIsWired is the structural half. The defect was never
// that the forwarding was wrong — it did not exist, while every
// subscriber it feeds was implemented, tested and configurable. A test
// that only exercises BridgeEventlog would stay green with nothing in
// the binary calling it, so this asserts the process wiring itself.
func TestEventlogBridgeIsWired(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile(filepath.Join(flowAPIModuleRoot(t), "cmd", "api", "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !strings.Contains(string(src), "eventbus.BridgeEventlog()") {
		t.Fatal("cmd/api/main.go must call eventbus.BridgeEventlog(): without it every append made " +
			"through the shared eventlog (itemkit, memberkit) fans out to nobody — no realtime " +
			"refresh, no notification row, no webhook delivery — and nothing reports an error")
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
