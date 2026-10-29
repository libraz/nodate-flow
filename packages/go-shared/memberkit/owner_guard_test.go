package memberkit

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/dbretry"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// addAdmin seeds a workspace member at the admin role and returns their
// internal user id.
func addAdmin(ctx context.Context, t *testing.T, db *sql.DB, wsID uint32) uint32 {
	t.Helper()
	uid := seedUser(ctx, t, db)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role, joined_at)
		 VALUES (?, ?, ?, 'admin', NOW())`,
		dbtype.New(), wsID, uid); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	return uid
}

func memberRole(ctx context.Context, t *testing.T, db *sql.DB, wsID, uid uint32) string {
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
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	secondOwner := addOwner(ctx, t, db, ws.wsID)
	admin := addAdmin(ctx, t, db, ws.wsID)

	withTx(t, db, func(tx *dbretry.Tx) {
		err := UpdateMemberRole(ctx, tx, UpdateMemberRoleArgs{
			WorkspaceID: ws.wsID, UserID: secondOwner, NewRole: RoleMember,
			ActorUserID: admin,
		})
		if !errors.Is(err, ErrRoleEscalation) {
			t.Fatalf("expected ErrRoleEscalation when an admin demotes an owner, got %v", err)
		}
	})

	if got := memberRole(ctx, t, db, ws.wsID, secondOwner); got != "owner" {
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
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	secondOwner := addOwner(ctx, t, db, ws.wsID)
	admin := addAdmin(ctx, t, db, ws.wsID)

	withTx(t, db, func(tx *dbretry.Tx) {
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
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	secondOwner := addOwner(ctx, t, db, ws.wsID)

	withTx(t, db, func(tx *dbretry.Tx) {
		if err := UpdateMemberRole(ctx, tx, UpdateMemberRoleArgs{
			WorkspaceID: ws.wsID, UserID: secondOwner, NewRole: RoleMember,
			ActorUserID: ws.actorID,
		}); err != nil {
			t.Fatalf("an owner must be able to demote a co-owner, got %v", err)
		}
	})

	if got := memberRole(ctx, t, db, ws.wsID, secondOwner); got != "member" {
		t.Errorf("expected role=member after the demote, got %q", got)
	}
}

// TestUpdateMemberRole_AdminMayStillManageMembers guards the other
// direction of over-correction: the rank check must not stop an admin
// from doing their job on members at or below their own rank.
func TestUpdateMemberRole_AdminMayStillManageMembers(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	admin := addAdmin(ctx, t, db, ws.wsID)
	target := seedUser(ctx, t, db)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role, joined_at)
		 VALUES (?, ?, ?, 'member', NOW())`,
		dbtype.New(), ws.wsID, target); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	withTx(t, db, func(tx *dbretry.Tx) {
		if err := UpdateMemberRole(ctx, tx, UpdateMemberRoleArgs{
			WorkspaceID: ws.wsID, UserID: target, NewRole: RoleGuest,
			ActorUserID: admin,
		}); err != nil {
			t.Fatalf("an admin must still manage ordinary members, got %v", err)
		}
	})

	if got := memberRole(ctx, t, db, ws.wsID, target); got != "guest" {
		t.Errorf("expected role=guest after the admin's change, got %q", got)
	}
}

// ownerCount reads the enabled owners of a workspace outside any
// transaction.
func ownerCount(ctx context.Context, t *testing.T, db *sql.DB, wsID uint32) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM workspace_members WHERE workspace_id = ? AND role = 'owner' AND enabled = TRUE`,
		wsID).Scan(&n); err != nil {
		t.Fatalf("count owners: %v", err)
	}
	return n
}

// runCrossOwnerRace has the two owners of a workspace act on each other
// at once: the first transaction applies its change and holds its commit
// until the second has started its own, so a guard that counts owners
// from a snapshot lets both through.
func runCrossOwnerRace(t *testing.T, db *sql.DB, first, second func(ctx context.Context, tx *dbretry.Tx) error) (firstErr, secondErr error) {
	t.Helper()
	ctx := context.Background()
	racerCtx := ctx
	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)

	firstErr = dbretry.InTx(ctx, db, "memberkit.test.first", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		if err := first(ctx, tx); err != nil {
			return err
		}
		go func() {
			secondDone <- dbretry.InTx(racerCtx, db, "memberkit.test.second", nil, func(ctx context.Context, tx *dbretry.Tx) error {
				close(secondStarted)
				return second(ctx, tx)
			})
		}()
		<-secondStarted
		// Long enough for the second transaction to reach the owner
		// rows before this one commits.
		time.Sleep(300 * time.Millisecond)
		return nil
	})
	return firstErr, <-secondDone
}

// TestUpdateMemberRole_CrossDemoteLeavesOneOwner holds the last-owner
// guard under concurrency: two owners demoting each other at once must
// not both succeed, because the workspace would be left with no owner.
func TestUpdateMemberRole_CrossDemoteLeavesOneOwner(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })
	ownerA := ws.actorID
	ownerB := addOwner(ctx, t, db, ws.wsID)

	firstErr, secondErr := runCrossOwnerRace(t, db,
		func(ctx context.Context, tx *dbretry.Tx) error {
			return UpdateMemberRole(ctx, tx, UpdateMemberRoleArgs{WorkspaceID: ws.wsID, UserID: ownerB, NewRole: RoleAdmin, ActorUserID: ownerA})
		},
		func(ctx context.Context, tx *dbretry.Tx) error {
			return UpdateMemberRole(ctx, tx, UpdateMemberRoleArgs{WorkspaceID: ws.wsID, UserID: ownerA, NewRole: RoleAdmin, ActorUserID: ownerB})
		})

	if firstErr != nil {
		t.Fatalf("first demote: %v", firstErr)
	}
	if !errors.Is(secondErr, ErrLastOwner) {
		t.Fatalf("second demote: want ErrLastOwner, got %v", secondErr)
	}
	if n := ownerCount(ctx, t, db, ws.wsID); n != 1 {
		t.Fatalf("owners after the race: want 1, got %d", n)
	}
}

// TestRemoveWorkspaceMember_CrossRemoveLeavesOneOwner is the removal
// counterpart.
func TestRemoveWorkspaceMember_CrossRemoveLeavesOneOwner(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })
	ownerA := ws.actorID
	ownerB := addOwner(ctx, t, db, ws.wsID)

	firstErr, secondErr := runCrossOwnerRace(t, db,
		func(ctx context.Context, tx *dbretry.Tx) error {
			_, err := RemoveWorkspaceMember(ctx, tx, RemoveWorkspaceMemberArgs{WorkspaceID: ws.wsID, UserID: ownerB, ActorUserID: ownerA})
			return err
		},
		func(ctx context.Context, tx *dbretry.Tx) error {
			_, err := RemoveWorkspaceMember(ctx, tx, RemoveWorkspaceMemberArgs{WorkspaceID: ws.wsID, UserID: ownerA, ActorUserID: ownerB})
			return err
		})

	if firstErr != nil {
		t.Fatalf("first remove: %v", firstErr)
	}
	if !errors.Is(secondErr, ErrLastOwner) {
		t.Fatalf("second remove: want ErrLastOwner, got %v", secondErr)
	}
	if n := ownerCount(ctx, t, db, ws.wsID); n != 1 {
		t.Fatalf("owners after the race: want 1, got %d", n)
	}
}
