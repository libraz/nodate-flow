package memberkit

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// addAdmin seeds a workspace member at the admin role and returns their
// internal user id.
func addAdmin(t *testing.T, ctx context.Context, db *sql.DB, wsID uint32) uint32 {
	t.Helper()
	uid := seedUser(t, ctx, db)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role, joined_at)
		 VALUES (?, ?, ?, 'admin', NOW())`,
		dbtype.New(), wsID, uid); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	return uid
}

func memberRole(t *testing.T, ctx context.Context, db *sql.DB, wsID, uid uint32) string {
	t.Helper()
	var role string
	if err := db.QueryRowContext(ctx,
		`SELECT role FROM workspace_members WHERE workspace_id = ? AND user_id = ?`,
		wsID, uid).Scan(&role); err != nil {
		t.Fatalf("read role: %v", err)
	}
	return role
}

// TestUpdateMemberRole_AdminCannotDemoteOwner closes the mirror image of
// the escalation guard. Blocking an admin from granting the owner role
// says nothing about what they may do to someone who already holds it:
// an admin could demote a co-owner to member, and the demoted owner
// loses the owner-only DELETE /workspaces/{wsId} in the process. Two
// owners are seeded so the last-owner guard is indifferent and the only
// thing that can refuse is the rank check.
func TestUpdateMemberRole_AdminCannotDemoteOwner(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(t, ctx, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	secondOwner := addOwner(t, ctx, db, ws.wsID)
	admin := addAdmin(t, ctx, db, ws.wsID)

	withTx(t, db, func(tx *sql.Tx) {
		err := UpdateMemberRole(ctx, tx, UpdateMemberRoleArgs{
			WorkspaceID: ws.wsID, UserID: secondOwner, NewRole: RoleMember,
			ActorUserID: admin,
		})
		if !errors.Is(err, ErrRoleEscalation) {
			t.Fatalf("expected ErrRoleEscalation when an admin demotes an owner, got %v", err)
		}
	})

	if got := memberRole(t, ctx, db, ws.wsID, secondOwner); got != "owner" {
		t.Errorf("the owner must keep their role after a blocked demote, got %q", got)
	}
}

// TestRemoveWorkspaceMember_AdminCannotRemoveOwner is the second step of
// the same attack: having failed (or succeeded) at demoting, an admin
// must not be able to remove an owner outright. A second owner is
// present so ErrLastOwner cannot be what refuses this.
func TestRemoveWorkspaceMember_AdminCannotRemoveOwner(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(t, ctx, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	secondOwner := addOwner(t, ctx, db, ws.wsID)
	admin := addAdmin(t, ctx, db, ws.wsID)

	withTx(t, db, func(tx *sql.Tx) {
		_, err := RemoveWorkspaceMember(ctx, tx, RemoveWorkspaceMemberArgs{
			WorkspaceID: ws.wsID, UserID: secondOwner, ActorUserID: admin,
		})
		if !errors.Is(err, ErrRoleEscalation) {
			t.Fatalf("expected ErrRoleEscalation when an admin removes an owner, got %v", err)
		}
	})

	var enabled bool
	if err := db.QueryRowContext(ctx,
		`SELECT enabled FROM workspace_members WHERE workspace_id = ? AND user_id = ?`,
		ws.wsID, secondOwner).Scan(&enabled); err != nil {
		t.Fatalf("read membership: %v", err)
	}
	if !enabled {
		t.Error("the owner must still be a member after a blocked removal")
	}
}

// TestUpdateMemberRole_OwnerMayDemoteCoOwner keeps the guard from
// becoming "owners are untouchable": equal ranks pass, so an owner can
// still demote a co-owner while another owner remains.
func TestUpdateMemberRole_OwnerMayDemoteCoOwner(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(t, ctx, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	secondOwner := addOwner(t, ctx, db, ws.wsID)

	withTx(t, db, func(tx *sql.Tx) {
		if err := UpdateMemberRole(ctx, tx, UpdateMemberRoleArgs{
			WorkspaceID: ws.wsID, UserID: secondOwner, NewRole: RoleMember,
			ActorUserID: ws.actorID,
		}); err != nil {
			t.Fatalf("an owner must be able to demote a co-owner, got %v", err)
		}
	})

	if got := memberRole(t, ctx, db, ws.wsID, secondOwner); got != "member" {
		t.Errorf("expected role=member after the demote, got %q", got)
	}
}

// TestUpdateMemberRole_AdminMayStillManageMembers guards the other
// direction of over-correction: the rank check must not stop an admin
// from doing their job on members at or below their own rank.
func TestUpdateMemberRole_AdminMayStillManageMembers(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(t, ctx, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	admin := addAdmin(t, ctx, db, ws.wsID)
	target := seedUser(t, ctx, db)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role, joined_at)
		 VALUES (?, ?, ?, 'member', NOW())`,
		dbtype.New(), ws.wsID, target); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	withTx(t, db, func(tx *sql.Tx) {
		if err := UpdateMemberRole(ctx, tx, UpdateMemberRoleArgs{
			WorkspaceID: ws.wsID, UserID: target, NewRole: RoleGuest,
			ActorUserID: admin,
		}); err != nil {
			t.Fatalf("an admin must still manage ordinary members, got %v", err)
		}
	})

	if got := memberRole(t, ctx, db, ws.wsID, target); got != "guest" {
		t.Errorf("expected role=guest after the admin's change, got %q", got)
	}
}
