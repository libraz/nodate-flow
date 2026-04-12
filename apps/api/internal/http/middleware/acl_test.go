package middleware

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestWorkspaceRoleAtLeast(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		have WorkspaceRole
		min  WorkspaceRole
		want bool
	}{
		{"owner>=admin", WorkspaceRoleOwner, WorkspaceRoleAdmin, true},
		{"admin>=admin", WorkspaceRoleAdmin, WorkspaceRoleAdmin, true},
		{"member>=admin", WorkspaceRoleMember, WorkspaceRoleAdmin, false},
		{"guest>=member", WorkspaceRoleGuest, WorkspaceRoleMember, false},
		{"owner>=guest", WorkspaceRoleOwner, WorkspaceRoleGuest, true},
		{"unknown<<member", WorkspaceRole("nope"), WorkspaceRoleMember, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.have.AtLeast(tc.min); got != tc.want {
				t.Fatalf("AtLeast(%q,%q)=%v want %v", tc.have, tc.min, got, tc.want)
			}
		})
	}
}

func TestProjectRoleAtLeast(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		have ProjectRole
		min  ProjectRole
		want bool
	}{
		{"lead>=editor", ProjectRoleLead, ProjectRoleEditor, true},
		{"editor>=lead", ProjectRoleEditor, ProjectRoleLead, false},
		{"commenter>=viewer", ProjectRoleCommenter, ProjectRoleViewer, true},
		{"viewer>=commenter", ProjectRoleViewer, ProjectRoleCommenter, false},
		{"lead>=lead", ProjectRoleLead, ProjectRoleLead, true},
		{"unknown<<viewer", ProjectRole("nope"), ProjectRoleViewer, false},
		{"empty<<viewer", ProjectRole(""), ProjectRoleViewer, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.have.AtLeast(tc.min); got != tc.want {
				t.Fatalf("AtLeast(%q,%q)=%v want %v", tc.have, tc.min, got, tc.want)
			}
		})
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

func TestTaskVisibilityFilterAdmin(t *testing.T) {
	t.Parallel()
	frag, args := TaskVisibilityFilter(1, WorkspaceRoleAdmin)
	if frag != "" {
		t.Fatalf("expected empty fragment for admin, got %q", frag)
	}
	if len(args) != 0 {
		t.Fatalf("expected no args for admin, got %v", args)
	}
}

func TestTaskVisibilityFilterOwner(t *testing.T) {
	t.Parallel()
	frag, args := TaskVisibilityFilter(1, WorkspaceRoleOwner)
	if frag != "" {
		t.Fatalf("expected empty fragment for owner, got %q", frag)
	}
	if len(args) != 0 {
		t.Fatalf("expected no args for owner, got %v", args)
	}
}

func TestTaskVisibilityFilterMember(t *testing.T) {
	t.Parallel()
	frag, args := TaskVisibilityFilter(42, WorkspaceRoleMember)
	if frag == "" {
		t.Fatal("expected non-empty fragment for member")
	}
	if len(args) != 3 {
		t.Fatalf("expected 3 args for member, got %d: %v", len(args), args)
	}
	// All three args should be the userID.
	for i, a := range args {
		uid, ok := a.(uint32)
		if !ok || uid != 42 {
			t.Fatalf("args[%d]=%v want uint32(42)", i, a)
		}
	}
}

func TestTaskVisibilityFilterGuest(t *testing.T) {
	t.Parallel()
	frag, args := TaskVisibilityFilter(99, WorkspaceRoleGuest)
	if frag == "" {
		t.Fatal("expected non-empty fragment for guest")
	}
	if len(args) != 3 {
		t.Fatalf("expected 3 args for guest, got %d", len(args))
	}
}
