// Test seam for the cross-package integration tests in
// apps/presence-discord/tests/. The production code path remains
// driven by Start(); the helpers in this file let tests wire the
// emitter + debouncer + sink without opening a real Discord WS and
// without taking on the cost of standing up a fake gateway server
// that speaks Discord's protocol byte-for-byte.
//
// Everything here is exported under names that are obviously
// test-only (the trailing "ForTest" suffix). Keeping the surface
// small and documented avoids accidental production use; nothing
// here introduces new logic, all methods are thin wrappers around
// the same private state the production Start() touches.
//
// The seam exists because the integration tests sit in package
// `tests` (mirroring apps/flow-worker's tests/ layout), which has
// no white-box access to the unexported handler, sink or factory
// fields. Without this seam tests would have to either reimplement
// discordgo's WS protocol or move into package gateway, both of
// which the Phase 8 / P8-5 plan explicitly calls out as undesirable.
package gateway

import (
	"context"
	"time"

	"github.com/bwmarrin/discordgo"
)

// WireForTest installs an Emitter + Debouncer onto the gateway as if
// Start() had been called, but without opening a Discord WS. The
// caller supplies the emitter (typically pointed at an httptest
// server) and the debounce window; the debouncer is constructed with
// the supplied ctx so trailing timers respect the test's lifetime.
//
// Subsequent calls to DispatchForTest dispatch synthetic
// PresenceUpdate events through the production handler chain
// (onPresenceUpdate -> debouncer -> emitter).
//
// WireForTest does NOT touch GatewayUp / EventsTotal counters
// (Start() is what owns those); tests assert on
// EventsTotal/DebounceDroppedTotal directly because the gateway
// metrics are package-global by design.
func (g *Gateway) WireForTest(ctx context.Context, emitter *Emitter, window time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.emitter = emitter
	g.debouncer = NewDebouncer(ctx, window, emitter)
	g.sink = g.debouncer
}

// DispatchForTest is the test-only equivalent of discordgo's handler
// dispatch. It invokes the same handler the production session would
// invoke when a PresenceUpdate gateway frame arrives, exercising the
// full chain: handler -> debouncer -> emitter -> POST /signals.
//
// The session argument is left nil because the production handler
// nil-guards on s.State; tests do not need a real *discordgo.Session.
func (g *Gateway) DispatchForTest(pu *discordgo.PresenceUpdate) {
	g.onPresenceUpdate(nil, pu)
}

// StopForTest drains the wired debouncer without going through the
// full Stop() (which expects a sessionAdapter set up by Start()).
// Safe to call multiple times.
func (g *Gateway) StopForTest() {
	g.mu.Lock()
	d := g.debouncer
	g.mu.Unlock()
	if d != nil {
		d.Stop()
	}
}

// SessionFactoryForTest overrides the discordgo session factory used
// by Start(). Tests that want to exercise Start()'s session lifecycle
// (e.g. start-stop-start reconnect probing) inject a no-op adapter so
// the gateway never tries to open a real WS.
//
// The factory takes a token and must return a sessionAdapter whose
// Open() / Close() succeed; AddHandler is invoked for each registered
// handler and the returned remove func is called from Stop().
func (g *Gateway) SessionFactoryForTest(factory func(token string) (SessionAdapterForTest, error)) {
	g.newSession = func(token string) (sessionAdapter, error) {
		adapter, err := factory(token)
		if err != nil {
			return nil, err
		}
		return adapter, nil
	}
}

// SessionAdapterForTest re-exports the unexported sessionAdapter
// surface so tests can implement a fake session without leaking the
// private interface name. The method set is identical; the alias
// exists purely so the seam in SessionFactoryForTest accepts a
// caller-visible interface.
type SessionAdapterForTest interface {
	Open() error
	Close() error
	AddHandler(handler interface{}) func()
}
