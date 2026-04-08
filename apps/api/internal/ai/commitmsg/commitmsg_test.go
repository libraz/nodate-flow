package commitmsg

import (
	"strings"
	"testing"
)

func TestPropose_FeatWithScope(t *testing.T) {
	t.Parallel()
	p := Propose([]Change{
		{Path: "apps/web/src/features/auth/login.tsx", Status: StatusAdded},
		{Path: "apps/web/src/features/auth/api.ts", Status: StatusModified},
	}, "Add login form")
	if p.Type != "feat" {
		t.Fatalf("expected feat, got %q", p.Type)
	}
	if p.Scope != "web" {
		t.Fatalf("expected scope=web, got %q", p.Scope)
	}
	if !strings.HasPrefix(p.Full, "feat(web): ") {
		t.Fatalf("unexpected full: %q", p.Full)
	}
}

func TestPropose_DocsOnly(t *testing.T) {
	t.Parallel()
	p := Propose([]Change{
		{Path: "docs/adr/0006.md", Status: StatusAdded},
		{Path: "README.md", Status: StatusModified},
	}, "")
	if p.Type != "docs" {
		t.Fatalf("expected docs, got %q", p.Type)
	}
}

func TestPropose_TestOnly(t *testing.T) {
	t.Parallel()
	p := Propose([]Change{
		{Path: "apps/api/internal/ai/judge/judge_test.go", Status: StatusAdded},
		{Path: "apps/web/e2e/realtime.spec.ts", Status: StatusAdded},
	}, "")
	if p.Type != "test" {
		t.Fatalf("expected test, got %q", p.Type)
	}
}

func TestPropose_EmptyReturnsChore(t *testing.T) {
	t.Parallel()
	p := Propose(nil, "")
	if p.Type != "chore" || p.Full != "chore: no changes" {
		t.Fatalf("unexpected: %+v", p)
	}
}

func TestPropose_MixedScopeDropped(t *testing.T) {
	t.Parallel()
	p := Propose([]Change{
		{Path: "apps/api/internal/foo.go", Status: StatusModified},
		{Path: "apps/web/src/bar.tsx", Status: StatusModified},
		{Path: "packages/sdk/src/baz.ts", Status: StatusModified},
	}, "cross-stack change")
	if p.Scope != "" {
		t.Fatalf("expected empty scope for mixed stacks, got %q", p.Scope)
	}
}

func TestPropose_DeletionHeavyBecomesFix(t *testing.T) {
	t.Parallel()
	p := Propose([]Change{
		{Path: "apps/api/internal/legacy/a.go", Status: StatusDeleted},
		{Path: "apps/api/internal/legacy/b.go", Status: StatusDeleted},
		{Path: "apps/api/internal/legacy/c.go", Status: StatusDeleted},
		{Path: "apps/api/internal/legacy/d.go", Status: StatusModified},
	}, "")
	if p.Type != "fix" {
		t.Fatalf("expected fix for deletion-heavy change, got %q", p.Type)
	}
}

func TestPropose_SummaryBecomesSubject(t *testing.T) {
	t.Parallel()
	p := Propose([]Change{
		{Path: "apps/api/main.go", Status: StatusModified},
	}, "Upgrade Huma to v2.")
	if p.Subject != "upgrade Huma to v2" {
		t.Fatalf("unexpected subject: %q", p.Subject)
	}
}
