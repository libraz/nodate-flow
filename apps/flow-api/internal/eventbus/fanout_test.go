package eventbus

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	sharedbus "github.com/libraz/nodate-flow/packages/go-shared/eventbus"
	"github.com/libraz/nodate-flow/packages/go-shared/eventlog"
)

// TestWithoutFanout pins that a WithoutFanout context silences every
// notify hook on both appenders, and that a plain context still fires.
func TestWithoutFanout(t *testing.T) {
	db := stubDB(t)
	eventlog.ClearHooks()
	t.Cleanup(eventlog.ClearHooks)
	resetBridge(t)

	var fired atomic.Int32
	handle := AddNotifyHook(func(context.Context, uint32, string, uint64) { fired.Add(1) })
	t.Cleanup(func() { RemoveNotifyHook(handle) })
	BridgeEventlog()

	appendNative := func(ctx context.Context) error {
		return Append(ctx, dbretry.AutoCommit(db), Event{Type: TaskCreated, WorkspaceID: 81})
	}
	appendBridged := func(ctx context.Context) error {
		_, err := eventlog.Append(ctx, dbretry.AutoCommit(db), eventlog.Event{Type: sharedbus.WorkspaceMemberAdded, WorkspaceID: 82})
		return err
	}
	paths := map[string]func(context.Context) error{
		"append":         appendNative,
		"eventlogBridge": appendBridged,
	}

	for name, write := range paths {
		t.Run(name+"/suppressed", func(t *testing.T) {
			fired.Store(0)
			if err := write(WithoutFanout(context.Background())); err != nil {
				t.Fatalf("append: %v", err)
			}
			if n := fired.Load(); n != 0 {
				t.Fatalf("hooks fired %d times under WithoutFanout", n)
			}
		})
		t.Run(name+"/control", func(t *testing.T) {
			fired.Store(0)
			if err := write(context.Background()); err != nil {
				t.Fatalf("append: %v", err)
			}
			if fired.Load() < 1 {
				t.Fatal("control: a plain context must fire the hook")
			}
		})
	}
}
