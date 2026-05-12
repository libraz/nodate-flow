// Audit-log landing regression for the immediate-delete contract.
//
// Background. The deletion pipeline is single-step: the workspaces (or
// users) row is hard-DELETEd before the audit recorder runs. The
// workspace-scoped audit_logs table has FK CASCADE on workspace_id, so
// recording an entry that names the just-deleted workspace would either
// fail outright (FK violation against a row that no longer exists in the
// same transaction view) or be CASCADE-wiped immediately by the same
// DELETE that removed the workspace. The instance_audit_logs table has
// FK SET NULL on target_workspace_id and survives the delete.
//
// Fix. The three delete handlers
//
//   - DELETE /workspaces/{wsId}                   audit "workspace.delete"
//   - DELETE /admin/workspaces/{wsId}             audit "admin.workspace.delete"
//   - DELETE /admin/users/{userId}                audit "admin.user.delete"
//
// must omit Entry.WorkspaceID so the recorder routes the entry to
// instance_audit_logs (the WorkspaceID==0 branch in audit.Recorder.Record).
//
// What this file pins. For each of the three actions, after a successful
// delete the audit row MUST land in instance_audit_logs with the right
// action / actor / target public id, and there MUST be zero rows in the
// workspace-scoped audit_logs table for the same action by the same
// actor (defensive: if anyone re-introduces a WorkspaceID on these
// entries pointing at a different surviving workspace, this catches it).
//
// This test must FAIL if anyone reverts the WorkspaceID omission in
//
//   - apps/auth-api/internal/http/handlers/workspace/delete.go    (Record block)
//   - apps/auth-api/internal/http/handlers/admin/delete_workspace.go (Record block)
//   - apps/auth-api/internal/http/handlers/admin/delete_user.go      (Record block)
package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// countInstanceAuditLogs returns the number of instance_audit_logs rows
// matching the (action, actor_user_id, target_resource_public_id) tuple.
// The target id is supplied as the textual UUID v7 form; the column is
// BINARY(16) so we hex-encode in the query the same way the rest of the
// e2e suite does (UUID_TO_BIN(?, 0)).
func countInstanceAuditLogs(t *testing.T, db *sql.DB, action string, actorID uint32, targetPublicID string) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*)
		   FROM instance_audit_logs
		  WHERE action = ?
		    AND actor_user_id = ?
		    AND target_resource_public_id = UUID_TO_BIN(?, 0)`,
		action, actorID, targetPublicID).Scan(&n)
	require.NoError(t, err)
	return n
}

// countWorkspaceAuditLogs returns the number of audit_logs rows for the
// given action by the given actor. We deliberately do NOT scope by
// workspace_id — the FK CASCADE means rows targeting the just-deleted
// workspace are gone anyway, so the only way to land here is if a future
// regression points the entry at some OTHER surviving workspace. That
// would be a real bug; pin it at zero.
func countWorkspaceAuditLogs(t *testing.T, db *sql.DB, action string, actorID uint32) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*)
		   FROM audit_logs
		  WHERE action = ?
		    AND actor_user_id = ?`,
		action, actorID).Scan(&n)
	require.NoError(t, err)
	return n
}

// TestOwnerDeleteWorkspaceAuditLandsInInstanceTable pins the audit
// landing for the owner self-service workspace delete. The handler must
// omit WorkspaceID so the recorder writes to instance_audit_logs (FK SET
// NULL) rather than audit_logs (FK CASCADE on the just-deleted ws).
func TestOwnerDeleteWorkspaceAuditLandsInInstanceTable(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	actorUID := internalUserID(t, testDB, tt.UserPublicID)

	// Single-step destructive delete (the action under test).
	var out adminDeleteOutput
	doJSON(t, http.MethodDelete,
		fmt.Sprintf("%s/workspaces/%s", testServerURL, tt.WorkspacePublicID),
		tt.AccessToken, map[string]any{"confirm": true}, &out)
	require.Truef(t, out.Deleted, "owner delete must report deleted=true; got %+v", out)

	require.Equalf(t, 1,
		countInstanceAuditLogs(t, testDB, "workspace.delete", actorUID, tt.WorkspacePublicID),
		"owner workspace.delete audit must land in instance_audit_logs exactly once "+
			"(actor=%d ws=%s); regression suspect: WorkspaceID was set on the audit Entry, "+
			"routing it to FK-CASCADE audit_logs which wipes the row instantly",
		actorUID, tt.WorkspacePublicID)

	require.Equalf(t, 0,
		countWorkspaceAuditLogs(t, testDB, "workspace.delete", actorUID),
		"owner workspace.delete must NOT touch the workspace-scoped audit_logs table "+
			"for the actor (regression suspect: WorkspaceID was set to a surviving "+
			"workspace id by mistake)")
}

// TestAdminDeleteWorkspaceAuditLandsInInstanceTable mirrors the owner
// case for the admin force-delete endpoint. Same FK reasoning, distinct
// action name.
func TestAdminDeleteWorkspaceAuditLandsInInstanceTable(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	target := newTenant(t)
	admin := newTenant(t)
	grantInstanceAdmin(t, testDB, admin.UserPublicID)
	adminUID := internalUserID(t, testDB, admin.UserPublicID)

	out := adminDeleteWorkspace(t, admin.AccessToken, target.WorkspacePublicID)
	require.Truef(t, out.Deleted, "admin workspace delete must report deleted=true; got %+v", out)

	require.Equalf(t, 1,
		countInstanceAuditLogs(t, testDB, "admin.workspace.delete", adminUID, target.WorkspacePublicID),
		"admin.workspace.delete audit must land in instance_audit_logs exactly once "+
			"(actor=%d ws=%s); regression suspect: WorkspaceID was set on the audit Entry, "+
			"routing it to FK-CASCADE audit_logs",
		adminUID, target.WorkspacePublicID)

	require.Equalf(t, 0,
		countWorkspaceAuditLogs(t, testDB, "admin.workspace.delete", adminUID),
		"admin.workspace.delete must NOT touch the workspace-scoped audit_logs table "+
			"for the actor")
}

// TestAdminDeleteUserAuditLandsInInstanceTable pins the user-delete
// audit. Unlike the workspace cases, audit_logs.workspace_id has no
// natural value here (the action has no workspace context), so this
// test additionally guarantees that no future change tries to scope the
// user-delete audit to some derived workspace id.
func TestAdminDeleteUserAuditLandsInInstanceTable(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	// Target user is freshly invited (the bootstrap admin can't self-delete).
	target := newTenant(t)
	admin := newTenant(t)
	grantInstanceAdmin(t, testDB, admin.UserPublicID)
	adminUID := internalUserID(t, testDB, admin.UserPublicID)

	out := adminDeleteUser(t, admin.AccessToken, target.UserPublicID)
	require.Truef(t, out.Deleted, "admin user delete must report deleted=true; got %+v", out)

	require.Equalf(t, 1,
		countInstanceAuditLogs(t, testDB, "admin.user.delete", adminUID, target.UserPublicID),
		"admin.user.delete audit must land in instance_audit_logs exactly once "+
			"(actor=%d user=%s); regression suspect: WorkspaceID was set on the audit Entry, "+
			"routing it to FK-CASCADE audit_logs (where there is no natural workspace anyway)",
		adminUID, target.UserPublicID)

	require.Equalf(t, 0,
		countWorkspaceAuditLogs(t, testDB, "admin.user.delete", adminUID),
		"admin.user.delete must NOT touch the workspace-scoped audit_logs table "+
			"for the actor (the action has no workspace context)")
}
