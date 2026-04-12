package e2e

import (
	"database/sql"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// auditRow is a lightweight projection of a single audit_logs row
// used by assertion helpers.
type auditRow struct {
	Action           string
	ResourceType     string
	ResourcePublicID sql.NullString
	ActorUserID      sql.NullInt32
}

// queryAuditLogs returns all audit_logs rows for a workspace (identified
// by its public_id) ordered oldest-first. Using direct SQL here is
// acceptable because the audit_logs table has no read API that returns
// raw rows; the entries are produced as side-effects of other API calls.
func queryAuditLogs(t *testing.T, db *sql.DB, workspacePublicID string) []auditRow {
	t.Helper()
	rows, err := db.Query(
		`SELECT action, resource_type, BIN_TO_UUID(resource_public_id, 0), actor_user_id
		 FROM audit_logs
		 WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))
		 ORDER BY occurred_at ASC, id ASC`,
		workspacePublicID)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	var out []auditRow
	for rows.Next() {
		var r auditRow
		require.NoError(t, rows.Scan(&r.Action, &r.ResourceType, &r.ResourcePublicID, &r.ActorUserID))
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

// findAudit returns the first audit row matching action and resourceType,
// or fails the test if none is found.
func findAudit(t *testing.T, logs []auditRow, action, resourceType string) auditRow {
	t.Helper()
	for _, r := range logs {
		if r.Action == action && r.ResourceType == resourceType {
			return r
		}
	}
	t.Fatalf("audit row not found: action=%q resourceType=%q (have %d rows)", action, resourceType, len(logs))
	return auditRow{} // unreachable
}

// TestAuditWorkspaceCreate verifies that creating a workspace via the
// API produces a workspace.create audit log entry with the correct
// actor, resource type, and resource public id.
func TestAuditWorkspaceCreate(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	logs := queryAuditLogs(t, testDB, tt.WorkspacePublicID)
	row := findAudit(t, logs, "workspace.create", "workspace")

	require.True(t, row.ActorUserID.Valid, "workspace.create must record an actor")
	require.True(t, row.ResourcePublicID.Valid, "workspace.create must record a resource id")
	require.Equal(t, tt.WorkspacePublicID, row.ResourcePublicID.String,
		"resource public id must match the created workspace")
}

// TestAuditProjectCreate verifies that creating a project via the
// API produces a project.create audit log entry with the correct
// actor, resource type, and resource public id.
func TestAuditProjectCreate(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	logs := queryAuditLogs(t, testDB, tt.WorkspacePublicID)
	row := findAudit(t, logs, "project.create", "project")

	require.True(t, row.ActorUserID.Valid, "project.create must record an actor")
	require.True(t, row.ResourcePublicID.Valid, "project.create must record a resource id")
	require.Equal(t, tt.ProjectPublicID, row.ResourcePublicID.String,
		"resource public id must match the created project")
}

// TestAuditTaskLifecycle exercises the full task CRUD path and verifies
// that each mutation produces the expected audit log entry with the
// correct action, resource type, and resource public id.
func TestAuditTaskLifecycle(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Create a task.
	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "audit-test-task",
		"priority":  1,
	}, &created)
	require.NotEmpty(t, created.ID)

	// Update the task.
	var updated struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+created.ID,
		tt.AccessToken, map[string]any{"title": "audit-test-task-renamed"}, &updated)
	require.Equal(t, "audit-test-task-renamed", updated.Title)

	// Delete (disable) the task.
	status, _ := doJSONStatus(t, http.MethodDelete, testServerURL+"/tasks/"+created.ID, tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	// Verify audit entries.
	logs := queryAuditLogs(t, testDB, tt.WorkspacePublicID)

	t.Run("task.create", func(t *testing.T) {
		t.Parallel()
		row := findAudit(t, logs, "task.create", "task")
		require.True(t, row.ActorUserID.Valid, "task.create must record an actor")
		require.True(t, row.ResourcePublicID.Valid, "task.create must record a resource id")
		require.Equal(t, created.ID, row.ResourcePublicID.String,
			"resource public id must match the created task")
	})

	t.Run("task.update", func(t *testing.T) {
		t.Parallel()
		row := findAudit(t, logs, "task.update", "task")
		require.True(t, row.ActorUserID.Valid, "task.update must record an actor")
		require.True(t, row.ResourcePublicID.Valid, "task.update must record a resource id")
		require.Equal(t, created.ID, row.ResourcePublicID.String,
			"resource public id must match the updated task")
	})

	t.Run("task.delete", func(t *testing.T) {
		t.Parallel()
		row := findAudit(t, logs, "task.delete", "task")
		require.True(t, row.ActorUserID.Valid, "task.delete must record an actor")
		require.True(t, row.ResourcePublicID.Valid, "task.delete must record a resource id")
		require.Equal(t, created.ID, row.ResourcePublicID.String,
			"resource public id must match the deleted task")
	})
}

// TestAuditLoginProducesEntry verifies that a successful login produces
// an auth.login audit log entry. Because the login handler records
// WorkspaceID=0 (no workspace context), the entry is queried directly
// by actor_user_id rather than by workspace.
func TestAuditLoginProducesEntry(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Login to generate the audit entry. Register (via newTenant) does
	// not produce an auth.login audit row; only POST /auth/login does.
	var login struct {
		AccessToken string `json:"accessToken"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/auth/login", "", map[string]any{
		"email":    tt.Email,
		"password": tt.Password,
	}, &login)
	require.NotEmpty(t, login.AccessToken)

	// Auth audit entries have workspace_id=0 so we query by actor.
	var count int
	err := testDB.QueryRow(
		`SELECT COUNT(*) FROM audit_logs
		 WHERE action = 'auth.login'
		   AND actor_user_id = (SELECT id FROM users WHERE public_id = UUID_TO_BIN(?, 0))`,
		tt.UserPublicID).Scan(&count)
	require.NoError(t, err)
	require.GreaterOrEqual(t, count, 1, "auth.login must produce at least one audit entry")
}

// TestAuditNoDuplicateOnSingleOp ensures that a single create operation
// produces exactly one audit entry of its type, guarding against
// accidental double-recording.
func TestAuditNoDuplicateOnSingleOp(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	logs := queryAuditLogs(t, testDB, tt.WorkspacePublicID)

	// Count workspace.create entries. newTenant creates exactly one
	// workspace, so there must be exactly one audit row for it.
	var wsCreateCount int
	for _, r := range logs {
		if r.Action == "workspace.create" && r.ResourceType == "workspace" {
			wsCreateCount++
		}
	}
	require.Equal(t, 1, wsCreateCount,
		"single workspace creation must produce exactly 1 workspace.create audit entry")

	// Same for project.create.
	var prjCreateCount int
	for _, r := range logs {
		if r.Action == "project.create" && r.ResourceType == "project" {
			prjCreateCount++
		}
	}
	require.Equal(t, 1, prjCreateCount,
		"single project creation must produce exactly 1 project.create audit entry")
}

// TestAuditCrossTenantIsolation verifies that audit log entries from
// one tenant are not visible when querying another tenant's workspace.
func TestAuditCrossTenantIsolation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenantA := newTenant(t)
	tenantB := newTenant(t)

	// Each tenant's workspace gets its own workspace.create and
	// project.create entries. Verify that tenant B's workspace audit
	// log contains no references to tenant A's resources.
	logsB := queryAuditLogs(t, testDB, tenantB.WorkspacePublicID)
	for _, r := range logsB {
		if r.ResourcePublicID.Valid {
			require.NotEqual(t, tenantA.WorkspacePublicID, r.ResourcePublicID.String,
				"tenant B audit must not contain tenant A workspace id")
			require.NotEqual(t, tenantA.ProjectPublicID, r.ResourcePublicID.String,
				"tenant B audit must not contain tenant A project id")
		}
	}

	// And the reverse.
	logsA := queryAuditLogs(t, testDB, tenantA.WorkspacePublicID)
	for _, r := range logsA {
		if r.ResourcePublicID.Valid {
			require.NotEqual(t, tenantB.WorkspacePublicID, r.ResourcePublicID.String,
				"tenant A audit must not contain tenant B workspace id")
			require.NotEqual(t, tenantB.ProjectPublicID, r.ResourcePublicID.String,
				"tenant A audit must not contain tenant B project id")
		}
	}
}
