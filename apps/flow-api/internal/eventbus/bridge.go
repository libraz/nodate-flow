package eventbus

import (
	"context"
	"sync"

	"github.com/libraz/nodate-flow/packages/go-shared/eventlog"
)

// bridgeMu guards the one-shot registration below.
var bridgeMu sync.Mutex

// bridgeHandle is the eventlog registration handle, non-nil once the
// bridge is installed.
var bridgeHandle *int

// BridgeEventlog forwards every append made through
// packages/go-shared/eventlog to this package's subscribers.
//
// Two appenders write the same `events` table: flow-api's own handlers
// go through [Append], while cross-service kits (itemkit, memberkit)
// go through eventlog because they cannot depend on flow-api's
// generated types. Without this bridge an eventlog append — a task
// scheduled onto a calendar — reaches no subscriber: no realtime
// refresh, no notification row, no webhook delivery.
//
// The bridge is in-process: it reaches appends eventlog makes inside this
// binary. Member events memberkit appends from auth-api are written by
// another process, so no hook registered here fires for them; only the
// realtime stream, which tails the table, sees those rows.
//
// Forwarding rather than registering each subscriber twice is the
// point: subscribers are added over time (the webhook worker and the
// on_event trigger both arrived after the notification fan-out), and a
// second registration list would have to be updated every time. With
// one bridge, anything that subscribes to this package is reached by
// both appenders by construction.
//
// Safe to call more than once; only the first call registers.
func BridgeEventlog() {
	bridgeMu.Lock()
	defer bridgeMu.Unlock()
	if bridgeHandle != nil {
		return
	}
	h := eventlog.RegisterHook(func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
		fireNotifyHooks(ctx, workspaceID, eventType, eventInternalID)
	})
	bridgeHandle = &h
}

// EventlogBridged reports whether [BridgeEventlog] has installed the
// forwarder. Exposed so a test can assert the wiring exists rather than
// asserting that some particular subscriber happens to be registered.
func EventlogBridged() bool {
	bridgeMu.Lock()
	defer bridgeMu.Unlock()
	return bridgeHandle != nil
}
