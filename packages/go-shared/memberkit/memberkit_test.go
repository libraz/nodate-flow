package memberkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/dbretry"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

var shared = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{
	Database: "memberkit_test",
})

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func startDB(t *testing.T) *sql.DB {
	t.Helper()
	testhelpers.SkipUnlessIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	inst, err := shared.Start(ctx)
	if err != nil {
		t.Fatalf("start mysql: %v", err)
	}
	return inst.DB
}

type wsFixture struct {
	wsID    uint32
	actorID uint32
}

func seedWorkspace(t *testing.T, ctx context.Context, db *sql.DB, country string) wsFixture {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	exec := func(q string, args ...any) int64 {
		res, err := db.ExecContext(ctx, q, args...)
		if err != nil {
			t.Fatalf("seed exec %q: %v", q, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("seed last id: %v", err)
		}
		return id
	}

	var countryArg any
	if country != "" {
		countryArg = country
	}
	wsID := uint32(exec(
		`INSERT INTO workspaces (public_id, slug, name, timezone, country)
		 VALUES (?, ?, ?, 'UTC', ?)`,
		dbtype.New(), "ws-"+suffix[:10], "MemberKit Test "+suffix, countryArg,
	))
	actorID := uint32(exec(
		`INSERT INTO users (public_id, email, display_name, locale, timezone)
		 VALUES (?, ?, ?, 'en', 'UTC')`,
		dbtype.New(), "mk-actor+"+suffix+"@example.test", "MK Actor",
	))
	exec(
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role, joined_at)
		 VALUES (?, ?, ?, 'owner', NOW())`,
		dbtype.New(), wsID, actorID,
	)
	return wsFixture{wsID: wsID, actorID: actorID}
}

func seedUser(t *testing.T, ctx context.Context, db *sql.DB) uint32 {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	res, err := db.ExecContext(ctx,
		`INSERT INTO users (public_id, email, display_name, locale, timezone)
		 VALUES (?, ?, ?, 'en', 'UTC')`,
		dbtype.New(), "mk-inv+"+suffix+"@example.test", "Invitee "+suffix[:5],
	)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed user LastInsertId: %v", err)
	}
	return uint32(id)
}

func purgeWorkspace(t *testing.T, db *sql.DB, wsID uint32) {
	t.Helper()
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0")
	for _, q := range []string{
		`DELETE FROM events WHERE workspace_id = ?`,
		`DELETE FROM calendar_subscriptions WHERE workspace_id = ?`,
		`DELETE FROM calendars WHERE workspace_id = ?`,
		`DELETE FROM task_actors WHERE task_id IN (SELECT id FROM tasks WHERE workspace_id = ?)`,
		`DELETE FROM project_members WHERE project_id IN (SELECT id FROM projects WHERE workspace_id = ?)`,
		`DELETE FROM tasks WHERE workspace_id = ?`,
		`DELETE FROM projects WHERE workspace_id = ?`,
		`DELETE FROM workspace_members WHERE workspace_id = ?`,
		`DELETE FROM workspaces WHERE id = ?`,
	} {
		if _, err := db.ExecContext(ctx, q, wsID); err != nil {
			t.Logf("purge %q: %v", q, err)
		}
	}
	_, _ = db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1")
}

// withTx runs fn inside the transaction type memberkit's entry points
// take. It goes through dbretry.InTx rather than db.BeginTx because
// those entry points append to the event log and the appender only
// accepts a transaction whose commit it can wait for.
func withTx(t *testing.T, db *sql.DB, fn func(tx *dbretry.Tx)) {
	t.Helper()
	err := dbretry.InTx(context.Background(), db, "memberkit.test", nil,
		func(_ context.Context, tx *dbretry.Tx) error {
			fn(tx)
			return nil
		})
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
}

// TestAddWorkspaceMember_NewMemberCreatesCalendar verifies the
// expected side effects for a fresh member with no country set:
// member row, personal calendar, personal subscription.
func TestAddWorkspaceMember_NewMemberCreatesCalendar(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(t, ctx, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	userID := seedUser(t, ctx, db)

	var res AddWorkspaceMemberResult
	withTx(t, db, func(tx *dbretry.Tx) {
		r, err := AddWorkspaceMember(ctx, tx, AddWorkspaceMemberArgs{
			WorkspaceID:            ws.wsID,
			UserID:                 userID,
			Role:                   RoleMember,
			InvitedByUserID:        ws.actorID,
			EnsurePersonalCalendar: true,
		})
		if err != nil {
			t.Fatalf("AddWorkspaceMember: %v", err)
		}
		res = r
	})

	if !res.CreatedMember {
		t.Errorf("expected CreatedMember=true, got %+v", res)
	}
	if !res.CreatedCalendar {
		t.Errorf("expected CreatedCalendar=true")
	}

	// Verify member row exists and is enabled.
	var memberEnabled bool
	if err := db.QueryRowContext(ctx,
		`SELECT enabled FROM workspace_members WHERE workspace_id = ? AND user_id = ?`,
		ws.wsID, userID).Scan(&memberEnabled); err != nil {
		t.Fatalf("load member: %v", err)
	}
	if !memberEnabled {
		t.Error("member should be enabled")
	}

	// Verify personal calendar.
	var calCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calendars
		 WHERE workspace_id = ? AND owner_user_id = ? AND kind = 'personal' AND enabled = TRUE`,
		ws.wsID, userID).Scan(&calCount); err != nil {
		t.Fatalf("count calendars: %v", err)
	}
	if calCount != 1 {
		t.Errorf("expected 1 personal calendar, got %d", calCount)
	}

	// Verify subscription.
	var subCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calendar_subscriptions
		 WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE`,
		ws.wsID, userID).Scan(&subCount); err != nil {
		t.Fatalf("count subs: %v", err)
	}
	if subCount != 1 {
		t.Errorf("expected 1 subscription, got %d", subCount)
	}

	// Verify audit event.
	var eventCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events
		 WHERE workspace_id = ? AND type = 'workspace.member.added'`,
		ws.wsID).Scan(&eventCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventCount != 1 {
		t.Errorf("expected 1 workspace.member.added event, got %d", eventCount)
	}
}

// TestAddWorkspaceMember_IdempotentOnExistingEnabled verifies that
// re-adding an enabled member returns the same row without creating
// a second calendar.
func TestAddWorkspaceMember_IdempotentOnExistingEnabled(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(t, ctx, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	userID := seedUser(t, ctx, db)

	var first, second AddWorkspaceMemberResult
	withTx(t, db, func(tx *dbretry.Tx) {
		r, err := AddWorkspaceMember(ctx, tx, AddWorkspaceMemberArgs{
			WorkspaceID: ws.wsID, UserID: userID, Role: RoleMember,
			InvitedByUserID: ws.actorID, EnsurePersonalCalendar: true,
		})
		if err != nil {
			t.Fatalf("first add: %v", err)
		}
		first = r
	})
	withTx(t, db, func(tx *dbretry.Tx) {
		r, err := AddWorkspaceMember(ctx, tx, AddWorkspaceMemberArgs{
			WorkspaceID: ws.wsID, UserID: userID, Role: RoleMember,
			InvitedByUserID: ws.actorID, EnsurePersonalCalendar: true,
		})
		if err != nil {
			t.Fatalf("second add: %v", err)
		}
		second = r
	})

	if first.MemberID != second.MemberID {
		t.Errorf("member id should be stable: first=%d second=%d",
			first.MemberID, second.MemberID)
	}
	if second.CreatedMember || second.CreatedCalendar {
		t.Errorf("re-add should not create: %+v", second)
	}

	// Exactly one calendar row.
	var calCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calendars WHERE workspace_id = ? AND owner_user_id = ?`,
		ws.wsID, userID).Scan(&calCount); err != nil {
		t.Fatalf("count calendars: %v", err)
	}
	if calCount != 1 {
		t.Errorf("expected 1 calendar, got %d", calCount)
	}
}

// TestRemoveWorkspaceMember_CascadesSoftDisable verifies that
// removing a member soft-disables their subscriptions, personal
// calendars, task_actors, and project_members rows in the same
// workspace.
func TestRemoveWorkspaceMember_CascadesSoftDisable(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(t, ctx, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	userID := seedUser(t, ctx, db)

	// Add the user with a personal calendar + subscription.
	withTx(t, db, func(tx *dbretry.Tx) {
		if _, err := AddWorkspaceMember(ctx, tx, AddWorkspaceMemberArgs{
			WorkspaceID: ws.wsID, UserID: userID, Role: RoleMember,
			InvitedByUserID: ws.actorID, EnsurePersonalCalendar: true,
		}); err != nil {
			t.Fatalf("add: %v", err)
		}
	})

	// Seed one project_members + one task_actor row.
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	projRes, err := db.ExecContext(ctx,
		`INSERT INTO projects (public_id, workspace_id, name, slug, identifier)
		 VALUES (?, ?, ?, ?, ?)`,
		dbtype.New(), ws.wsID, "Remove Test", "rp-"+suffix[:10], "RT",
	)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	projID, _ := projRes.LastInsertId()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO project_members (public_id, workspace_id, project_id, user_id, role)
		 VALUES (?, ?, ?, ?, 'editor')`,
		dbtype.New(), ws.wsID, projID, userID,
	); err != nil {
		t.Fatalf("seed project_members: %v", err)
	}

	taskRes, err := db.ExecContext(ctx,
		`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title, visibility)
		 VALUES (?, ?, ?, 1, 'task', 'public')`,
		dbtype.New(), ws.wsID, projID,
	)
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
	taskID, _ := taskRes.LastInsertId()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO task_actors (public_id, workspace_id, task_id, user_id, role)
		 VALUES (?, ?, ?, ?, 'assignee')`,
		dbtype.New(), ws.wsID, taskID, userID,
	); err != nil {
		t.Fatalf("seed task_actors: %v", err)
	}

	// Remove.
	var res RemoveWorkspaceMemberResult
	withTx(t, db, func(tx *dbretry.Tx) {
		r, err := RemoveWorkspaceMember(ctx, tx, RemoveWorkspaceMemberArgs{
			WorkspaceID: ws.wsID, UserID: userID, ActorUserID: ws.actorID,
		})
		if err != nil {
			t.Fatalf("remove: %v", err)
		}
		res = r
	})
	if !res.MemberDisabled {
		t.Error("member should be disabled")
	}
	if res.PersonalCalsDisabled != 1 {
		t.Errorf("expected 1 personal calendar disabled, got %d", res.PersonalCalsDisabled)
	}
	if res.SubscriptionsDisabled != 1 {
		t.Errorf("expected 1 subscription disabled, got %d", res.SubscriptionsDisabled)
	}
	if res.TaskActorsDisabled != 1 {
		t.Errorf("expected 1 task_actor disabled, got %d", res.TaskActorsDisabled)
	}
	if res.ProjectMembersDisabled != 1 {
		t.Errorf("expected 1 project_member disabled, got %d", res.ProjectMembersDisabled)
	}

	// Cross-check via direct SELECT.
	var enabledSubs int
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calendar_subscriptions
		 WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE`,
		ws.wsID, userID).Scan(&enabledSubs)
	if enabledSubs != 0 {
		t.Errorf("expected 0 enabled subscriptions after remove, got %d", enabledSubs)
	}
}

// TestRemoveWorkspaceMember_ReturnsNotFoundForUnknownUser verifies
// that RemoveWorkspaceMember returns sql.ErrNoRows when the target
// membership doesn't exist at all.
func TestRemoveWorkspaceMember_ReturnsNotFoundForUnknownUser(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(t, ctx, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	bogusUser := seedUser(t, ctx, db) // exists but never joined the ws

	withTx(t, db, func(tx *dbretry.Tx) {
		_, err := RemoveWorkspaceMember(ctx, tx, RemoveWorkspaceMemberArgs{
			WorkspaceID: ws.wsID, UserID: bogusUser,
		})
		if !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("expected sql.ErrNoRows, got %v", err)
		}
	})
}

// TestUpdateMemberRole_ChangesRoleAndLogsEvent verifies that
// UpdateMemberRole updates the role column and emits the role_changed
// audit event.
func TestUpdateMemberRole_ChangesRoleAndLogsEvent(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(t, ctx, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	userID := seedUser(t, ctx, db)
	withTx(t, db, func(tx *dbretry.Tx) {
		if _, err := AddWorkspaceMember(ctx, tx, AddWorkspaceMemberArgs{
			WorkspaceID: ws.wsID, UserID: userID, Role: RoleMember,
			InvitedByUserID: ws.actorID,
		}); err != nil {
			t.Fatalf("add: %v", err)
		}
	})

	withTx(t, db, func(tx *dbretry.Tx) {
		if err := UpdateMemberRole(ctx, tx, UpdateMemberRoleArgs{
			WorkspaceID: ws.wsID, UserID: userID, NewRole: RoleAdmin,
			ActorUserID: ws.actorID,
		}); err != nil {
			t.Fatalf("update role: %v", err)
		}
	})

	var role string
	if err := db.QueryRowContext(ctx,
		`SELECT role FROM workspace_members WHERE workspace_id = ? AND user_id = ?`,
		ws.wsID, userID).Scan(&role); err != nil {
		t.Fatalf("load role: %v", err)
	}
	if role != "admin" {
		t.Errorf("expected role=admin, got %q", role)
	}

	// Verify audit event.
	var eventCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events
		 WHERE workspace_id = ? AND type = 'workspace.member.role_changed'`,
		ws.wsID).Scan(&eventCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventCount != 1 {
		t.Errorf("expected 1 role_changed event, got %d", eventCount)
	}
}

// addOwner seeds a second enabled owner so the last-owner guard does
// not trip in tests that act on a non-last owner. Returns the new
// user's internal id.
func addOwner(t *testing.T, ctx context.Context, db *sql.DB, wsID uint32) uint32 {
	t.Helper()
	uid := seedUser(t, ctx, db)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role, joined_at)
		 VALUES (?, ?, ?, 'owner', NOW())`,
		dbtype.New(), wsID, uid); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	return uid
}

// TestUpdateMemberRole_SelfModifyRejected verifies that an actor cannot
// change their own role: ErrSelfModify is returned and the role is
// unchanged.
func TestUpdateMemberRole_SelfModifyRejected(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(t, ctx, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	withTx(t, db, func(tx *dbretry.Tx) {
		err := UpdateMemberRole(ctx, tx, UpdateMemberRoleArgs{
			WorkspaceID: ws.wsID, UserID: ws.actorID, NewRole: RoleAdmin,
			ActorUserID: ws.actorID,
		})
		if !errors.Is(err, ErrSelfModify) {
			t.Fatalf("expected ErrSelfModify, got %v", err)
		}
	})

	var role string
	_ = db.QueryRowContext(ctx,
		`SELECT role FROM workspace_members WHERE workspace_id = ? AND user_id = ?`,
		ws.wsID, ws.actorID).Scan(&role)
	if role != "owner" {
		t.Errorf("role should be unchanged owner, got %q", role)
	}
}

// TestUpdateMemberRole_DemoteLastOwnerRejected verifies that demoting
// the only owner returns ErrLastOwner.
func TestUpdateMemberRole_DemoteLastOwnerRejected(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(t, ctx, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	// A different actor demotes the sole owner.
	otherOwner := addOwner(t, ctx, db, ws.wsID)
	// Now there are two owners; remove the second so actorID is the
	// last owner, then have otherOwner attempt the demote.
	if _, err := db.ExecContext(ctx,
		`UPDATE workspace_members SET enabled = FALSE WHERE workspace_id = ? AND user_id = ?`,
		ws.wsID, otherOwner); err != nil {
		t.Fatalf("disable second owner: %v", err)
	}

	withTx(t, db, func(tx *dbretry.Tx) {
		err := UpdateMemberRole(ctx, tx, UpdateMemberRoleArgs{
			WorkspaceID: ws.wsID, UserID: ws.actorID, NewRole: RoleAdmin,
			ActorUserID: otherOwner,
		})
		if !errors.Is(err, ErrLastOwner) {
			t.Fatalf("expected ErrLastOwner, got %v", err)
		}
	})
}

// TestUpdateMemberRole_DemoteNonLastOwnerOK verifies that demoting an
// owner while another owner remains succeeds.
func TestUpdateMemberRole_DemoteNonLastOwnerOK(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(t, ctx, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	secondOwner := addOwner(t, ctx, db, ws.wsID)

	withTx(t, db, func(tx *dbretry.Tx) {
		if err := UpdateMemberRole(ctx, tx, UpdateMemberRoleArgs{
			WorkspaceID: ws.wsID, UserID: secondOwner, NewRole: RoleAdmin,
			ActorUserID: ws.actorID,
		}); err != nil {
			t.Fatalf("expected demote of non-last owner to succeed, got %v", err)
		}
	})

	var role string
	_ = db.QueryRowContext(ctx,
		`SELECT role FROM workspace_members WHERE workspace_id = ? AND user_id = ?`,
		ws.wsID, secondOwner).Scan(&role)
	if role != "admin" {
		t.Errorf("expected role=admin, got %q", role)
	}
}

// TestRemoveWorkspaceMember_SelfModifyRejected verifies that an actor
// cannot remove themselves.
func TestRemoveWorkspaceMember_SelfModifyRejected(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(t, ctx, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	withTx(t, db, func(tx *dbretry.Tx) {
		_, err := RemoveWorkspaceMember(ctx, tx, RemoveWorkspaceMemberArgs{
			WorkspaceID: ws.wsID, UserID: ws.actorID, ActorUserID: ws.actorID,
		})
		if !errors.Is(err, ErrSelfModify) {
			t.Fatalf("expected ErrSelfModify, got %v", err)
		}
	})

	var enabled bool
	_ = db.QueryRowContext(ctx,
		`SELECT enabled FROM workspace_members WHERE workspace_id = ? AND user_id = ?`,
		ws.wsID, ws.actorID).Scan(&enabled)
	if !enabled {
		t.Error("actor membership should still be enabled")
	}
}

// TestRemoveWorkspaceMember_LastOwnerRejected verifies that removing the
// only owner returns ErrLastOwner and leaves the member enabled.
func TestRemoveWorkspaceMember_LastOwnerRejected(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(t, ctx, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	// A separate admin actor attempts to remove the sole owner.
	adminActor := seedUser(t, ctx, db)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role, joined_at)
		 VALUES (?, ?, ?, 'admin', NOW())`,
		dbtype.New(), ws.wsID, adminActor); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	withTx(t, db, func(tx *dbretry.Tx) {
		_, err := RemoveWorkspaceMember(ctx, tx, RemoveWorkspaceMemberArgs{
			WorkspaceID: ws.wsID, UserID: ws.actorID, ActorUserID: adminActor,
		})
		if !errors.Is(err, ErrLastOwner) {
			t.Fatalf("expected ErrLastOwner, got %v", err)
		}
	})

	var enabled bool
	_ = db.QueryRowContext(ctx,
		`SELECT enabled FROM workspace_members WHERE workspace_id = ? AND user_id = ?`,
		ws.wsID, ws.actorID).Scan(&enabled)
	if !enabled {
		t.Error("last owner should still be enabled after blocked removal")
	}
}

// TestRemoveWorkspaceMember_NonLastOwnerOK verifies that removing an
// owner while another owner remains succeeds.
func TestRemoveWorkspaceMember_NonLastOwnerOK(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(t, ctx, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	secondOwner := addOwner(t, ctx, db, ws.wsID)

	withTx(t, db, func(tx *dbretry.Tx) {
		res, err := RemoveWorkspaceMember(ctx, tx, RemoveWorkspaceMemberArgs{
			WorkspaceID: ws.wsID, UserID: secondOwner, ActorUserID: ws.actorID,
		})
		if err != nil {
			t.Fatalf("expected removal of non-last owner to succeed, got %v", err)
		}
		if !res.MemberDisabled {
			t.Error("second owner should be disabled")
		}
	})
}

// TestUpdateMemberRole_SameRoleIsNoop verifies that passing the same
// role does not emit a redundant audit row.
func TestUpdateMemberRole_SameRoleIsNoop(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(t, ctx, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	userID := seedUser(t, ctx, db)
	withTx(t, db, func(tx *dbretry.Tx) {
		if _, err := AddWorkspaceMember(ctx, tx, AddWorkspaceMemberArgs{
			WorkspaceID: ws.wsID, UserID: userID, Role: RoleMember,
			InvitedByUserID: ws.actorID,
		}); err != nil {
			t.Fatalf("add: %v", err)
		}
	})

	withTx(t, db, func(tx *dbretry.Tx) {
		if err := UpdateMemberRole(ctx, tx, UpdateMemberRoleArgs{
			WorkspaceID: ws.wsID, UserID: userID, NewRole: RoleMember,
		}); err != nil {
			t.Fatalf("same-role update: %v", err)
		}
	})

	var eventCount int
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events
		 WHERE workspace_id = ? AND type = 'workspace.member.role_changed'`,
		ws.wsID).Scan(&eventCount)
	if eventCount != 0 {
		t.Errorf("same-role update should not emit event, got %d", eventCount)
	}
}
