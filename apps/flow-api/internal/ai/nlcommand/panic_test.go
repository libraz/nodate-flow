// Panic guard tests for nlcommand constructors.
//
// Constructors enforce non-nil dependencies via panic so wiring
// mistakes fail loudly at boot rather than degrading into nil
// dereference on first request.
package nlcommand

import (
	"context"
	"strings"
	"testing"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/providers"
)

// stubResolver is a non-nil WorkspaceProviderResolver used to exercise
// the extractWS nil branch of NewWorkspaceProvider; never invoked.
type stubResolver struct{}

func (stubResolver) Default(_ context.Context, _ uint32) (providers.Provider, error) {
	return nil, nil
}

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

// TestNew_PanicsOnNilProvider locks in that nlcommand.New refuses a nil
// Provider. A nil Provider would crash on the first Resolve call.
func TestNew_PanicsOnNilProvider(t *testing.T) {
	t.Parallel()
	assertPanic(t, "provider must be non-nil", func() {
		_ = New(nil, nil)
	})
}

// TestNewWorkspaceProvider_PanicsOnNilResolver verifies the resolver
// nil-check fires (constructor order: resolver first, then extractWS).
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
