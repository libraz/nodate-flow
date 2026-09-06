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

func seedWorkspace(ctx context.Context, t *testing.T, db *sql.DB, country string) wsFixture {
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
	//#nosec G115 -- test-scoped LastInsertId fits uint32
	wsID := uint32(exec(
		`INSERT INTO workspaces (public_id, slug, name, timezone, country)
		 VALUES (?, ?, ?, 'UTC', ?)`,
		dbtype.New(), "ws-"+suffix[:10], "MemberKit Test "+suffix, countryArg,
	))
	//#nosec G115 -- test-scoped LastInsertId fits uint32
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

func seedUser(ctx context.Context, t *testing.T, db *sql.DB) uint32 {
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
	return uint32(id) //#nosec G115 -- test-scoped LastInsertId fits uint32
}

func purgeWorkspace(t *testing.T, db *sql.DB, wsID uint32) {
	t.Helper()
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0")
	for _, q := range []string{
		`DELETE FROM events WHERE workspace_id = ?`,
		`DELETE FROM calendar_subscriptions WHERE workspace_id = ?`,
		`DELETE FROM calendar_members WHERE workspace_id = ?`,
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
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	userID := seedUser(ctx, t, db)

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

	// Verify the ownership grant. The subscription above is a display
	// preference; this row is what ListCalendarsForUser drives off, so
	// without it the calendar counted above is unreachable.
	var grantRole string
	if err := db.QueryRowContext(ctx,
		`SELECT cm.role FROM calendar_members cm
		 INNER JOIN calendars c ON c.id = cm.calendar_id
		 WHERE cm.workspace_id = ? AND cm.user_id = ?
		   AND c.kind = 'personal' AND c.owner_user_id = ?
		   AND cm.enabled = TRUE`,
		ws.wsID, userID, userID).Scan(&grantRole); err != nil {
		t.Fatalf("load personal calendar grant: %v", err)
	}
	if grantRole != "owner" {
		t.Errorf("expected owner grant on own personal calendar, got %q", grantRole)
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
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	userID := seedUser(ctx, t, db)

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

// TestAddWorkspaceMember_GrantsAccessToPreexistingCalendar verifies
// that the branch which reuses an already-present personal calendar
// still writes the grant. Rows created before the grant was part of the
// add path have a calendar and no membership, and no creation path will
// ever run for that user again — the next add has to converge them.
func TestAddWorkspaceMember_GrantsAccessToPreexistingCalendar(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	userID := seedUser(ctx, t, db)

	// A personal calendar with no calendar_members row: the state the
	// old add path left behind.
	calRes, err := db.ExecContext(ctx,
		`INSERT INTO calendars (public_id, workspace_id, kind, name, color, owner_user_id)
		 VALUES (?, ?, 'personal', 'Orphan', '#4285F4', ?)`,
		dbtype.New(), ws.wsID, userID)
	if err != nil {
		t.Fatalf("seed orphan calendar: %v", err)
	}
	calID, _ := calRes.LastInsertId()

	var res AddWorkspaceMemberResult
	withTx(t, db, func(tx *dbretry.Tx) {
		r, err := AddWorkspaceMember(ctx, tx, AddWorkspaceMemberArgs{
			WorkspaceID: ws.wsID, UserID: userID, Role: RoleMember,
			InvitedByUserID: ws.actorID, EnsurePersonalCalendar: true,
		})
		if err != nil {
			t.Fatalf("add: %v", err)
		}
		res = r
	})

	if res.CreatedCalendar {
		t.Error("existing calendar should be reused, not recreated")
	}
	if res.PersonalCalendarID != uint32(calID) { //#nosec G115 -- test-scoped LastInsertId fits uint32
		t.Errorf("expected the seeded calendar %d, got %d", calID, res.PersonalCalendarID)
	}

	var grantRole string
	if err := db.QueryRowContext(ctx,
		`SELECT role FROM calendar_members
		 WHERE calendar_id = ? AND user_id = ? AND enabled = TRUE`,
		calID, userID).Scan(&grantRole); err != nil {
		t.Fatalf("load grant on pre-existing calendar: %v", err)
	}
	if grantRole != "owner" {
		t.Errorf("expected owner grant, got %q", grantRole)
	}
}

// TestAddWorkspaceMember_ReAddKeepsGrantColorAndRole verifies that the
// grant written on the first add survives a second one. The re-add
// re-enables the row rather than rewriting it, so a colour or role
// assigned since is not silently reset by an unrelated join.
func TestAddWorkspaceMember_ReAddKeepsGrantColorAndRole(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	userID := seedUser(ctx, t, db)
	add := func() {
		withTx(t, db, func(tx *dbretry.Tx) {
			if _, err := AddWorkspaceMember(ctx, tx, AddWorkspaceMemberArgs{
				WorkspaceID: ws.wsID, UserID: userID, Role: RoleMember,
				InvitedByUserID: ws.actorID, EnsurePersonalCalendar: true,
			}); err != nil {
				t.Fatalf("add: %v", err)
			}
		})
	}
	add()

	if _, err := db.ExecContext(ctx,
		`UPDATE calendar_members SET member_color = '#FF00FF', role = 'manager'
		 WHERE workspace_id = ? AND user_id = ?`,
		ws.wsID, userID); err != nil {
		t.Fatalf("recolour grant: %v", err)
	}

	add()

	var (
		grantCount int
		role       string
		color      string
	)
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*), MIN(role), MIN(member_color) FROM calendar_members
		 WHERE workspace_id = ? AND user_id = ?`,
		ws.wsID, userID).Scan(&grantCount, &role, &color); err != nil {
		t.Fatalf("load grant: %v", err)
	}
	if grantCount != 1 {
		t.Errorf("expected 1 grant after re-add, got %d", grantCount)
	}
	if role != "manager" || color != "#FF00FF" {
		t.Errorf("re-add should not rewrite the grant: role=%q color=%q", role, color)
	}
}

// TestRemoveWorkspaceMember_CascadesSoftDisable verifies that
// removing a member soft-disables their subscriptions, personal
// calendars, task_actors, and project_members rows in the same
// workspace.
func TestRemoveWorkspaceMember_CascadesSoftDisable(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	userID := seedUser(ctx, t, db)

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

// seedSharedCalendar inserts a calendar owned by the workspace's admin
// and grants userID access to it at the given role, the shape a member
// gets when somebody adds them to a calendar that is not their own.
// Returns the calendar's internal id.
func seedSharedCalendar(ctx context.Context, t *testing.T, db *sql.DB, ws wsFixture, userID uint32, name, role string) uint32 {
	t.Helper()
	calRes, err := db.ExecContext(ctx,
		`INSERT INTO calendars (public_id, workspace_id, kind, name, color, owner_user_id)
		 VALUES (?, ?, 'personal', ?, '#34A853', ?)`,
		dbtype.New(), ws.wsID, name, ws.actorID)
	if err != nil {
		t.Fatalf("seed shared calendar %q: %v", name, err)
	}
	calID, err := calRes.LastInsertId()
	if err != nil {
		t.Fatalf("seed shared calendar LastInsertId: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO calendar_members
		 (public_id, workspace_id, calendar_id, user_id, role, member_color)
		 VALUES (?, ?, ?, ?, ?, '#34A853')`,
		dbtype.New(), ws.wsID, calID, userID, role); err != nil {
		t.Fatalf("seed grant on %q: %v", name, err)
	}
	return uint32(calID) //#nosec G115 -- test-scoped LastInsertId fits uint32
}

// countEnabledGrants returns how many live calendar_members rows a user
// holds in a workspace.
func countEnabledGrants(ctx context.Context, t *testing.T, db *sql.DB, wsID, userID uint32) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calendar_members
		 WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE`,
		wsID, userID).Scan(&n); err != nil {
		t.Fatalf("count enabled grants: %v", err)
	}
	return n
}

// grantEnabled reports whether the user's grant on one calendar is live.
func grantEnabled(ctx context.Context, t *testing.T, db *sql.DB, calID, userID uint32) bool {
	t.Helper()
	var enabled bool
	if err := db.QueryRowContext(ctx,
		`SELECT enabled FROM calendar_members WHERE calendar_id = ? AND user_id = ?`,
		calID, userID).Scan(&enabled); err != nil {
		t.Fatalf("load grant on calendar %d: %v", calID, err)
	}
	return enabled
}

// TestRemoveWorkspaceMember_DisablesCalendarGrants verifies that the
// removal revokes the per-calendar grants the workspace membership
// implied, both on the member's own personal calendar and on a calendar
// somebody else shared with them.
func TestRemoveWorkspaceMember_DisablesCalendarGrants(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	userID := seedUser(ctx, t, db)
	withTx(t, db, func(tx *dbretry.Tx) {
		if _, err := AddWorkspaceMember(ctx, tx, AddWorkspaceMemberArgs{
			WorkspaceID: ws.wsID, UserID: userID, Role: RoleMember,
			InvitedByUserID: ws.actorID, EnsurePersonalCalendar: true,
		}); err != nil {
			t.Fatalf("add: %v", err)
		}
	})
	sharedCal := seedSharedCalendar(ctx, t, db, ws, userID, "Shared", "editor")

	if got := countEnabledGrants(ctx, t, db, ws.wsID, userID); got != 2 {
		t.Fatalf("expected 2 live grants before removal (personal + shared), got %d", got)
	}

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

	if res.CalendarGrantsDisabled != 2 {
		t.Errorf("expected 2 calendar grants disabled, got %d", res.CalendarGrantsDisabled)
	}
	if got := countEnabledGrants(ctx, t, db, ws.wsID, userID); got != 0 {
		t.Errorf("expected no live grants after removal, got %d", got)
	}
	if grantEnabled(ctx, t, db, sharedCal, userID) {
		t.Error("the grant on the shared calendar should not survive the removal")
	}
}

// TestRemoveWorkspaceMember_ReAddDoesNotRestoreSharedGrants verifies
// what the removal buys: rejoining the workspace hands back the personal
// calendar the add path provisions, and nothing else. A grant an
// administrator revoked by hand while the user was still a member stays
// revoked, and so does one they merely held.
func TestRemoveWorkspaceMember_ReAddDoesNotRestoreSharedGrants(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	userID := seedUser(ctx, t, db)
	add := func() {
		withTx(t, db, func(tx *dbretry.Tx) {
			if _, err := AddWorkspaceMember(ctx, tx, AddWorkspaceMemberArgs{
				WorkspaceID: ws.wsID, UserID: userID, Role: RoleMember,
				InvitedByUserID: ws.actorID, EnsurePersonalCalendar: true,
			}); err != nil {
				t.Fatalf("add: %v", err)
			}
		})
	}
	add()

	held := seedSharedCalendar(ctx, t, db, ws, userID, "Held", "editor")
	revoked := seedSharedCalendar(ctx, t, db, ws, userID, "Revoked", "editor")
	if _, err := db.ExecContext(ctx,
		`UPDATE calendar_members SET enabled = FALSE WHERE calendar_id = ? AND user_id = ?`,
		revoked, userID); err != nil {
		t.Fatalf("revoke grant by hand: %v", err)
	}

	withTx(t, db, func(tx *dbretry.Tx) {
		if _, err := RemoveWorkspaceMember(ctx, tx, RemoveWorkspaceMemberArgs{
			WorkspaceID: ws.wsID, UserID: userID, ActorUserID: ws.actorID,
		}); err != nil {
			t.Fatalf("remove: %v", err)
		}
	})

	add()

	if grantEnabled(ctx, t, db, held, userID) {
		t.Error("rejoining should not hand back a calendar the member merely used to reach")
	}
	if grantEnabled(ctx, t, db, revoked, userID) {
		t.Error("rejoining should not undo a grant an administrator revoked")
	}

	// The personal calendar is the one grant the add path is meant to
	// (re)provision, so exactly one live grant is the expected end state.
	if got := countEnabledGrants(ctx, t, db, ws.wsID, userID); got != 1 {
		t.Errorf("expected only the personal-calendar grant after rejoining, got %d live grants", got)
	}
}

// TestRemoveWorkspaceMember_ReturnsNotFoundForUnknownUser verifies
// that RemoveWorkspaceMember returns sql.ErrNoRows when the target
// membership doesn't exist at all.
func TestRemoveWorkspaceMember_ReturnsNotFoundForUnknownUser(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	bogusUser := seedUser(ctx, t, db) // exists but never joined the ws

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
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	userID := seedUser(ctx, t, db)
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
func addOwner(ctx context.Context, t *testing.T, db *sql.DB, wsID uint32) uint32 {
	t.Helper()
	uid := seedUser(ctx, t, db)
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
	ws := seedWorkspace(ctx, t, db, "")
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
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	// A different actor demotes the sole owner.
	otherOwner := addOwner(ctx, t, db, ws.wsID)
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
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	secondOwner := addOwner(ctx, t, db, ws.wsID)

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
	ws := seedWorkspace(ctx, t, db, "")
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
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	// A separate admin actor attempts to remove the sole owner.
	adminActor := seedUser(ctx, t, db)
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
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	secondOwner := addOwner(ctx, t, db, ws.wsID)

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
	ws := seedWorkspace(ctx, t, db, "")
	t.Cleanup(func() { purgeWorkspace(t, db, ws.wsID) })

	userID := seedUser(ctx, t, db)
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
