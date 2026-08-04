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

// seededActivityIDs holds the public_ids inserted by seedActivity, one
// per source leg, so callers can filter ListWorkspaceActivity output to
// exactly the rows this test seeded (CreateTestTenant itself emits
// audit_logs rows for workspace.create and project.create, so an
// unfiltered count would be misleading).
type seededActivityIDs struct {
	audit [16]byte
	ai    [16]byte
	mcp   [16]byte
}

// seedActivity inserts one row into each of the three source tables for
// a fresh tenant and returns its workspace_id together with the seeded
// public_ids. The provider row is created on demand because
// ai_invocations.provider_id is NOT NULL.
func seedActivity(t *testing.T) (uint32, seededActivityIDs) {
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

	// ai_providers row (FK target for ai_invocations). Column names follow
	// sql/flow/tables/ai_providers.sql: name (label), api_key_ciphertext
	// (VARBINARY, AES-GCM blob), api_key_prefix/api_key_suffix (NOT NULL
	// masking chars), default_model.
	provPub := uuid.Must(uuid.NewV7())
	provRes, err := testDB.ExecContext(ctx, `
		INSERT INTO ai_providers (public_id, workspace_id, kind, name, api_key_ciphertext, api_key_prefix, api_key_suffix, default_model)
		VALUES (?, ?, 'openai_compat', 'm8-prov', X'00', 'sk-test1', 'last', 'test-model')`,
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

	return wsID, seededActivityIDs{audit: auditPub, ai: aiPub, mcp: mcpPub}
}

// findSeeded looks up a row by public_id in the ListWorkspaceActivity
// result set. Returns the matching row and true when found. The view's
// public_id is sourced from the underlying audit/ai/mcp row, so each
// seeded leg has a distinct, addressable identity.
func findSeeded(rows []generated.ListWorkspaceActivityRow, want [16]byte) (generated.ListWorkspaceActivityRow, bool) {
	for _, r := range rows {
		if [16]byte(r.PublicID.UUID()) == want {
			return r, true
		}
	}
	return generated.ListWorkspaceActivityRow{}, false
}

func TestVWorkspaceActivityUnionsAllSources(t *testing.T) {
	skipIfNoIntegration(t)
	wsID, seeded := seedActivity(t)

	q := generated.New(testDB)
	rows, err := q.ListWorkspaceActivity(context.Background(), generated.ListWorkspaceActivityParams{
		WorkspaceID:  wsID,
		FilterSource: "",
		FilterSince:  sql.NullTime{},
		FilterUntil:  sql.NullTime{},
		Limit:        50,
	})
	require.NoError(t, err)

	// CreateTestTenant writes additional audit_logs rows during workspace
	// bootstrap, so the unfiltered count is intentionally not asserted.
	// Instead, look up each seeded row by its public_id and verify the
	// source label is what the view derives.
	auditRow, ok := findSeeded(rows, seeded.audit)
	require.True(t, ok, "seeded audit row missing from view output")
	auditSrc, ok := auditRow.Source.([]byte)
	require.True(t, ok, "source column should decode to []byte")
	require.Equal(t, "audit", string(auditSrc))

	aiRow, ok := findSeeded(rows, seeded.ai)
	require.True(t, ok, "seeded ai row missing from view output")
	aiSrc, ok := aiRow.Source.([]byte)
	require.True(t, ok)
	require.Equal(t, "ai", string(aiSrc))

	mcpRow, ok := findSeeded(rows, seeded.mcp)
	require.True(t, ok, "seeded mcp row missing from view output")
	mcpSrc, ok := mcpRow.Source.([]byte)
	require.True(t, ok)
	require.Equal(t, "mcp", string(mcpSrc))
}

func TestVWorkspaceActivitySourceFilter(t *testing.T) {
	skipIfNoIntegration(t)
	wsID, seeded := seedActivity(t)

	q := generated.New(testDB)
	rows, err := q.ListWorkspaceActivity(context.Background(), generated.ListWorkspaceActivityParams{
		WorkspaceID:  wsID,
		FilterSource: "audit",
		Limit:        50,
	})
	require.NoError(t, err)

	// All returned rows must carry source='audit' (filter is exact match)
	// and the seeded audit row must appear; ai / mcp seeded rows must not.
	require.NotEmpty(t, rows, "audit filter must surface at least the seeded row")
	for _, r := range rows {
		s, ok := r.Source.([]byte)
		require.True(t, ok)
		require.Equal(t, "audit", string(s),
			"filter_source='audit' must exclude all non-audit legs")
	}
	_, hasAudit := findSeeded(rows, seeded.audit)
	require.True(t, hasAudit, "seeded audit row must survive the filter")
	_, hasAI := findSeeded(rows, seeded.ai)
	require.False(t, hasAI, "seeded ai row must be excluded by filter_source='audit'")
	_, hasMCP := findSeeded(rows, seeded.mcp)
	require.False(t, hasMCP, "seeded mcp row must be excluded by filter_source='audit'")
}
