// View-level test for v_workspace_activity (audit item M8).
// Asserts that audit_logs / ai_invocations / mcp_invocations are
// projected through a single ListWorkspaceActivity query and that
// the source filter narrows to a single leg.
package sqlviews

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	generated "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// seedActivity inserts one row into each of the three source tables for
// a fresh tenant and returns its workspace_id. The provider row is
// created on demand because ai_invocations.provider_id is NOT NULL.
func seedActivity(t *testing.T) uint32 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	tt := helpers.CreateTestTenant(t, testSrv.BaseURL)
	t.Cleanup(func() {
		helpers.CleanupTenant(t, tt)
		helpers.PurgeWorkspace(t, testDB, tt.WorkspacePublicID)
	})

	var wsID, userID uint32
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0)`,
		tt.WorkspacePublicID).Scan(&wsID))
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM users WHERE public_id = UUID_TO_BIN(?, 0)`,
		tt.UserPublicID).Scan(&userID))

	// ai_providers row (FK target for ai_invocations).
	provPub := uuid.Must(uuid.NewV7())
	provRes, err := testDB.ExecContext(ctx, `
		INSERT INTO ai_providers (public_id, workspace_id, kind, display_name, encrypted_api_key, model_default)
		VALUES (?, ?, 'openai_compat', 'm8-prov', '', 'test-model')`,
		provPub[:], wsID)
	require.NoError(t, err)
	provID64, err := provRes.LastInsertId()
	require.NoError(t, err)
	providerID := uint32(provID64) //nolint:gosec // test-scoped LastInsertId fits uint32

	now := time.Now().UTC()

	// audit_logs leg.
	auditPub := uuid.Must(uuid.NewV7())
	_, err = testDB.ExecContext(ctx, `
		INSERT INTO audit_logs (public_id, workspace_id, actor_user_id, action, resource_type, occurred_at)
		VALUES (?, ?, ?, 'workspace.update', 'workspace', ?)`,
		auditPub[:], wsID, userID, now.Add(-3*time.Second))
	require.NoError(t, err)

	// ai_invocations leg.
	aiPub := uuid.Must(uuid.NewV7())
	_, err = testDB.ExecContext(ctx, `
		INSERT INTO ai_invocations (public_id, workspace_id, provider_id, user_id, purpose, model, prompt_redacted, status, invoked_at)
		VALUES (?, ?, ?, ?, 'propose_tasks', 'test-model', '[redacted]', 'ok', ?)`,
		aiPub[:], wsID, providerID, userID, now.Add(-2*time.Second))
	require.NoError(t, err)

	// mcp_invocations leg.
	mcpPub := uuid.Must(uuid.NewV7())
	_, err = testDB.ExecContext(ctx, `
		INSERT INTO mcp_invocations (public_id, workspace_id, user_id, tool_name, arguments_redacted_json, status, invoked_at)
		VALUES (?, ?, ?, 'create_task', '{}', 'ok', ?)`,
		mcpPub[:], wsID, userID, now.Add(-1*time.Second))
	require.NoError(t, err)

	return wsID
}

func TestVWorkspaceActivityUnionsAllSources(t *testing.T) {
	skipIfNoIntegration(t)
	wsID := seedActivity(t)

	q := generated.New(testDB)
	rows, err := q.ListWorkspaceActivity(context.Background(), generated.ListWorkspaceActivityParams{
		WorkspaceID:  wsID,
		FilterSource: "",
		FilterSince:  sql.NullTime{},
		FilterUntil:  sql.NullTime{},
		Limit:        50,
	})
	require.NoError(t, err)
	require.Len(t, rows, 3, "view should surface one row per source table")

	got := map[string]bool{}
	for _, r := range rows {
		s, ok := r.Source.([]byte)
		require.True(t, ok, "source column should decode to []byte")
		got[string(s)] = true
	}
	require.True(t, got["audit"], "audit leg missing")
	require.True(t, got["ai"], "ai leg missing")
	require.True(t, got["mcp"], "mcp leg missing")
}

func TestVWorkspaceActivitySourceFilter(t *testing.T) {
	skipIfNoIntegration(t)
	wsID := seedActivity(t)

	q := generated.New(testDB)
	rows, err := q.ListWorkspaceActivity(context.Background(), generated.ListWorkspaceActivityParams{
		WorkspaceID:  wsID,
		FilterSource: "audit",
		Limit:        50,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1, "filter_source='audit' should narrow to the audit leg only")
	s, ok := rows[0].Source.([]byte)
	require.True(t, ok)
	require.Equal(t, "audit", string(s))
}
