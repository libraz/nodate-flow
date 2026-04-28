package middleware

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// The canonical role / visibility tests live in
// apps/flow-api/internal/acl/acl_test.go. The wrapper tests below only
// verify that the chi-side context plumbing exposes the same shape via
// the re-exported aliases.

func TestWorkspaceRoleAlias(t *testing.T) {
	t.Parallel()
	if !WorkspaceRoleOwner.AtLeast(WorkspaceRoleAdmin) {
		t.Fatal("alias WorkspaceRole.AtLeast not wired to acl package")
	}
	if WorkspaceRoleMember.AtLeast(WorkspaceRoleAdmin) {
		t.Fatal("alias WorkspaceRole.AtLeast inverted")
	}
}

func TestProjectRoleAlias(t *testing.T) {
	t.Parallel()
	if !ProjectRoleLead.AtLeast(ProjectRoleEditor) {
		t.Fatal("alias ProjectRole.AtLeast not wired to acl package")
	}
}

func TestActorFromContext(t *testing.T) {
	t.Parallel()
	if _, ok := ActorFromContext(context.Background()); ok {
		t.Fatal("expected no actor in empty context")
	}
	ctx := WithActor(context.Background(), 42)
	id, ok := ActorFromContext(ctx)
	if !ok || id != 42 {
		t.Fatalf("got id=%d ok=%v want 42 true", id, ok)
	}
}

func TestWorkspaceFromContext(t *testing.T) {
	t.Parallel()
	if _, ok := WorkspaceFromContext(context.Background()); ok {
		t.Fatal("expected no workspace in empty context")
	}
	pub := uuid.New()
	ctx := context.WithValue(context.Background(), ctxKeyWorkspaceID, uint32(7))
	ctx = context.WithValue(ctx, ctxKeyWorkspaceIDPublic, pub)
	ctx = context.WithValue(ctx, ctxKeyWorkspaceRole, WorkspaceRoleAdmin)
	ws, ok := WorkspaceFromContext(ctx)
	if !ok || ws.ID != 7 || ws.PublicID != pub || ws.Role != WorkspaceRoleAdmin {
		t.Fatalf("unexpected ws=%+v ok=%v", ws, ok)
	}
}

func TestProjectFromContext(t *testing.T) {
	t.Parallel()
	if _, ok := ProjectFromContext(context.Background()); ok {
		t.Fatal("expected no project in empty context")
	}
	pub := uuid.New()
	ctx := context.WithValue(context.Background(), ctxKeyProjectID, uint32(11))
	ctx = context.WithValue(ctx, ctxKeyProjectIDPublic, pub)
	ctx = context.WithValue(ctx, ctxKeyProjectRole, ProjectRoleEditor)
	prj, ok := ProjectFromContext(ctx)
	if !ok || prj.ID != 11 || prj.PublicID != pub || prj.Role != ProjectRoleEditor {
		t.Fatalf("unexpected prj=%+v ok=%v", prj, ok)
	}
}

// TestProjectFromContext_InvalidRoleReportsAbsent verifies that a
// corrupted role value (e.g. an unknown enum string written by a
// stale schema migration or a manual DB edit) is treated as a
// server-side invariant violation — the context lookup returns
// ok=false so callers surface 500 INTERNAL.UNEXPECTED rather than
// silently falling through to a permissive default role or
// producing a misleading 403.
func TestProjectFromContext_InvalidRoleReportsAbsent(t *testing.T) {
	t.Parallel()
	pub := uuid.New()
	ctx := context.WithValue(context.Background(), ctxKeyProjectID, uint32(11))
	ctx = context.WithValue(ctx, ctxKeyProjectIDPublic, pub)
	ctx = context.WithValue(ctx, ctxKeyProjectRole, ProjectRole("not_a_real_role"))
	if _, ok := ProjectFromContext(ctx); ok {
		t.Fatal("ProjectFromContext returned ok=true for an unknown role string")
	}
}

// TestProjectFromContext_ElevatedIsValid verifies that the elevated
// marker (empty string) is accepted as valid — workspace owners /
// admins reach a project without a per-project role and the
// middleware records that with [ProjectRoleElevated].
func TestProjectFromContext_ElevatedIsValid(t *testing.T) {
	t.Parallel()
	pub := uuid.New()
	ctx := context.WithValue(context.Background(), ctxKeyProjectID, uint32(11))
	ctx = context.WithValue(ctx, ctxKeyProjectIDPublic, pub)
	ctx = context.WithValue(ctx, ctxKeyProjectRole, ProjectRoleElevated)
	prj, ok := ProjectFromContext(ctx)
	if !ok {
		t.Fatal("expected ok=true for ProjectRoleElevated marker")
	}
	if prj.Role != ProjectRoleElevated {
		t.Fatalf("expected role=elevated, got %q", prj.Role)
	}
}

func TestTaskVisibilityFilterDelegation(t *testing.T) {
	t.Parallel()
	// Admin sees everything -> empty fragment.
	frag, args := TaskVisibilityFilter(1, WorkspaceRoleAdmin)
	if frag != "" || len(args) != 0 {
		t.Fatalf("admin: got frag=%q args=%v", frag, args)
	}
	// Member -> non-empty fragment with three bound args.
	frag, args = TaskVisibilityFilter(42, WorkspaceRoleMember)
	if frag == "" || len(args) != 3 {
		t.Fatalf("member: got frag=%q args=%v", frag, args)
	}
}
