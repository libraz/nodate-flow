// Panic guard tests for nlquery constructors.
//
// The constructors enforce non-nil dependencies via panic so wiring
// mistakes fail loudly at boot rather than degrading into nil
// dereference during a request. These tests pin that contract so a
// future "let's just nil-check and return an error" refactor surfaces
// in CI instead of as a runtime crash on first traffic.
package nlquery

import (
	"context"
	"strings"
	"testing"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/providers"
)

// stubResolver is a non-nil WorkspaceProviderResolver used only to
// satisfy the type contract when probing the *other* nil-arg branch
// of NewWorkspaceProvider. It is never invoked.
type stubResolver struct{}

func (stubResolver) Default(_ context.Context, _ uint32) (providers.Provider, error) {
	return nil, nil
}

// assertPanic runs fn and fails the test if either no panic occurs or
// the recovered panic message does not contain the expected substring.
func assertPanic(t *testing.T, wantSubstr string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic containing %q, got none", wantSubstr)
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected string panic value, got %T: %v", r, r)
		}
		if !strings.Contains(msg, wantSubstr) {
			t.Fatalf("panic message %q missing substring %q", msg, wantSubstr)
		}
	}()
	fn()
}

// TestNew_PanicsOnNilProvider locks in that nlquery.New refuses a nil
// Provider. A nil Provider would make the very first Compile call nil
// dereference; we'd rather see this at construction time.
func TestNew_PanicsOnNilProvider(t *testing.T) {
	t.Parallel()
	assertPanic(t, "provider must be non-nil", func() {
		_ = New(nil)
	})
}

// TestNewWorkspaceProvider_PanicsOnNilResolver verifies the resolver
// nil-check fires before the extractWS check (matches the order in
// the constructor).
func TestNewWorkspaceProvider_PanicsOnNilResolver(t *testing.T) {
	t.Parallel()
	assertPanic(t, "resolver must be non-nil", func() {
		_ = NewWorkspaceProvider(nil, func(context.Context) (uint32, bool) { return 0, false })
	})
}

// TestNewWorkspaceProvider_PanicsOnNilExtractWS verifies the
// extractWS nil-check fires when the resolver is supplied.
func TestNewWorkspaceProvider_PanicsOnNilExtractWS(t *testing.T) {
	t.Parallel()
	assertPanic(t, "extractWS must be non-nil", func() {
		_ = NewWorkspaceProvider(stubResolver{}, nil)
	})
}
