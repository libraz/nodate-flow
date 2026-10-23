package nlcommand

import (
	"context"
	"errors"
	"testing"
	"time"
)

type ctxWS struct{}

// admittingProvider stands in for WorkspaceProvider: it admits the workspace
// carried in ctx unless guardErr is set, and counts upstream calls.
type admittingProvider struct {
	guardErr error
	calls    int
}

func (p *admittingProvider) Admit(ctx context.Context) (uint32, error) {
	if p.guardErr != nil {
		return 0, p.guardErr
	}
	return ctx.Value(ctxWS{}).(uint32), nil
}

func (p *admittingProvider) ResolveCommand(context.Context, string, []ToolSpec) ([]byte, error) {
	p.calls++
	return []byte(`{"tool":"list_tasks","args":{"filter":{"a":1}},"confidence":0.9}`), nil
}

func TestResolverCacheIsWorkspaceScoped(t *testing.T) {
	t.Parallel()
	p := &admittingProvider{}
	r := New(p, nil)
	r.Cache = NewCache(time.Hour)

	for _, ws := range []uint32{1, 2, 1} {
		if _, err := r.Resolve(context.WithValue(context.Background(), ctxWS{}, ws), "show tasks"); err != nil {
			t.Fatal(err)
		}
	}
	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (one per workspace, third served from workspace 1's entry)", p.calls)
	}
}

func TestResolverCacheHitStillPassesGuard(t *testing.T) {
	t.Parallel()
	p := &admittingProvider{}
	r := New(p, nil)
	r.Cache = NewCache(time.Hour)
	ctx := context.WithValue(context.Background(), ctxWS{}, uint32(1))
	if _, err := r.Resolve(ctx, "show tasks"); err != nil {
		t.Fatal(err)
	}

	p.guardErr = ErrBudgetExceeded
	if _, err := r.Resolve(ctx, "show tasks"); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded on a cached prompt", err)
	}
}

func TestResolverCachedToolCallIsNotShared(t *testing.T) {
	t.Parallel()
	p := &admittingProvider{}
	r := New(p, nil)
	r.Cache = NewCache(time.Hour)
	ctx := context.WithValue(context.Background(), ctxWS{}, uint32(1))

	first, _ := r.Resolve(ctx, "show tasks")
	first.Args["filter"].(map[string]any)["a"] = 99
	first.Args["extra"] = true

	second, _ := r.Resolve(ctx, "show tasks")
	if _, ok := second.Args["extra"]; ok || second.Args["filter"].(map[string]any)["a"] != float64(1) {
		t.Fatalf("mutation leaked into the cache: %+v", second.Args)
	}
}
