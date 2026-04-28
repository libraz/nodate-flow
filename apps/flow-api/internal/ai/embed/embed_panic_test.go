// Panic guard tests for embed.New.
//
// Wiring mistakes — passing a nil Provider or a nil *generated.Queries —
// must surface at boot rather than turning into a nil dereference on
// the first EmbedTask call. These tests pin the contract.
package embed

import (
	"strings"
	"testing"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
)

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

// TestNew_PanicsOnNilProvider locks in the constructor's nil-provider
// guard. The Queries arg is a fresh non-nil instance so we know the
// panic comes from the provider check.
func TestNew_PanicsOnNilProvider(t *testing.T) {
	t.Parallel()
	q := &generated.Queries{}
	assertPanic(t, "provider and queries must be non-nil", func() {
		_ = New(nil, q)
	})
}

// TestNew_PanicsOnNilQueries locks in the constructor's nil-queries
// guard. The Provider arg is a real MockProvider so we know the panic
// comes from the queries check.
func TestNew_PanicsOnNilQueries(t *testing.T) {
	t.Parallel()
	assertPanic(t, "provider and queries must be non-nil", func() {
		_ = New(NewMockProvider(), nil)
	})
}

// TestNew_PanicsOnBothNil covers the case where both arguments are nil.
func TestNew_PanicsOnBothNil(t *testing.T) {
	t.Parallel()
	assertPanic(t, "provider and queries must be non-nil", func() {
		_ = New(nil, nil)
	})
}
