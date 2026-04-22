package memberkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/testhelpers"
)

var shared = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{
	Database: "memberkit_test",
})

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func startDB(t *testing.T) *sql.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("memberkit tests require MySQL; skipping in -short mode")
	}
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

func withTx(t *testing.T, db *sql.DB, fn func(tx *sql.Tx)) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() {
		if err := tx.Commit(); err != nil {
			t.Logf("commit: %v", err)
		}
	}()
	fn(tx)
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
	withTx(t, db, func(tx *sql.Tx) {
		r, err := AddWorkspaceMember(ctx, tx, AddWorkspaceMemberArgs{
			WorkspaceID:              ws.wsID,
			UserID:                   userID,
			Role:                     RoleMember,
			InvitedByUserID:          ws.actorID,
			EnsurePersonalCalendar:   true,
			SubscribeHolidayCalendar: true, // no country set → no-op
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
	if res.SubscribedHoliday {
		t.Errorf("expected SubscribedHoliday=false (no country set)")
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

// TestAddWorkspaceMember_SubscribesHolidayWhenCountrySet verifies
// that a workspace with a country gets a system holiday calendar
// auto-created on first add, and subsequent adds reuse it.
func TestAddWorkspaceMember_SubscribesHolidayWhenCountrySet(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(t, ctx, db, "JP")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	// First invitee — creates the holiday calendar.
	user1 := seedUser(t, ctx, db)
	var res1 AddWorkspaceMemberResult
	withTx(t, db, func(tx *sql.Tx) {
		r, err := AddWorkspaceMember(ctx, tx, AddWorkspaceMemberArgs{
			WorkspaceID: ws.wsID, UserID: user1, Role: RoleMember,
			InvitedByUserID:          ws.actorID,
			EnsurePersonalCalendar:   true,
			SubscribeHolidayCalendar: true,
		})
		if err != nil {
			t.Fatalf("add user1: %v", err)
		}
		res1 = r
	})
	if !res1.CreatedHolidayCal || !res1.SubscribedHoliday {
		t.Errorf("user1 should have created+subscribed holiday: %+v", res1)
	}

	// Second invitee — reuses the same calendar.
	user2 := seedUser(t, ctx, db)
	var res2 AddWorkspaceMemberResult
	withTx(t, db, func(tx *sql.Tx) {
		r, err := AddWorkspaceMember(ctx, tx, AddWorkspaceMemberArgs{
			WorkspaceID: ws.wsID, UserID: user2, Role: RoleMember,
			InvitedByUserID:          ws.actorID,
			EnsurePersonalCalendar:   true,
			SubscribeHolidayCalendar: true,
		})
		if err != nil {
			t.Fatalf("add user2: %v", err)
		}
		res2 = r
	})
	if res2.CreatedHolidayCal {
		t.Error("user2 should NOT have created another holiday calendar")
	}
	if !res2.SubscribedHoliday {
		t.Error("user2 should have subscribed to the existing holiday calendar")
	}
	if res1.HolidayCalendarID != res2.HolidayCalendarID {
		t.Errorf("holiday calendar IDs should match: user1=%d user2=%d",
			res1.HolidayCalendarID, res2.HolidayCalendarID)
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
	withTx(t, db, func(tx *sql.Tx) {
		r, err := AddWorkspaceMember(ctx, tx, AddWorkspaceMemberArgs{
			WorkspaceID: ws.wsID, UserID: userID, Role: RoleMember,
			InvitedByUserID: ws.actorID, EnsurePersonalCalendar: true,
		})
		if err != nil {
			t.Fatalf("first add: %v", err)
		}
		first = r
	})
	withTx(t, db, func(tx *sql.Tx) {
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
	withTx(t, db, func(tx *sql.Tx) {
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
	withTx(t, db, func(tx *sql.Tx) {
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

	withTx(t, db, func(tx *sql.Tx) {
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
	withTx(t, db, func(tx *sql.Tx) {
		if _, err := AddWorkspaceMember(ctx, tx, AddWorkspaceMemberArgs{
			WorkspaceID: ws.wsID, UserID: userID, Role: RoleMember,
			InvitedByUserID: ws.actorID,
		}); err != nil {
			t.Fatalf("add: %v", err)
		}
	})

	withTx(t, db, func(tx *sql.Tx) {
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

// TestUpdateMemberRole_SameRoleIsNoop verifies that passing the same
// role does not emit a redundant audit row.
func TestUpdateMemberRole_SameRoleIsNoop(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(t, ctx, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	userID := seedUser(t, ctx, db)
	withTx(t, db, func(tx *sql.Tx) {
		if _, err := AddWorkspaceMember(ctx, tx, AddWorkspaceMemberArgs{
			WorkspaceID: ws.wsID, UserID: userID, Role: RoleMember,
			InvitedByUserID: ws.actorID,
		}); err != nil {
			t.Fatalf("add: %v", err)
		}
	})

	withTx(t, db, func(tx *sql.Tx) {
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
