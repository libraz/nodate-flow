package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestDiscordSignalSourceRoundTrip is a regression test: every
// enforcement layer (DB ENUM, Huma validation, OpenAPI schema) has to
// carry "discord", or the presence-discord gateway's POST /signals
// returns 422 before reaching the handler. This test drives a full
// round-trip through the real HTTP router against a testcontainer MySQL
// to confirm:
//
//  1. POST /signals with source="discord" and kind="discord.presence"
//     returns 200 (not 422).
//  2. The response body carries the expected source, kind, and subjectType.
//  3. A `signals` row with the returned public_id actually exists in the
//     correct workspace.
//
// The service-token harness is used because the presence-discord gateway
// authenticates via a static service token, not a user JWT (the same
// path the presence-discord emitter takes in production).
func TestDiscordSignalSourceRoundTrip(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	h := newSignalsServiceTokenHarness(t)

	// Emit a discord.presence signal that mirrors what emitter.go sends.
	// SubjectID is a plausible Discord user snowflake translated to a
	// workspace-user public_id; the handler accepts any non-empty UUID v7
	// string for subjectType=user, so we use the tenant's own user id to
	// keep the fixture self-contained.
	status, raw := postSignal(t, h.baseURL, serviceTokenFixture, map[string]any{
		"workspaceId": h.tenant.WorkspacePublicID,
		"source":      "discord",
		"kind":        "discord.presence",
		"subjectType": "user",
		"subjectId":   h.tenant.UserPublicID,
		"payload":     map[string]any{"status": "online", "activities": []string{}},
	})
	require.Equalf(t, http.StatusOK, status,
		"POST /signals with source=discord returned %d (expected 200); body=%s", status, string(raw))

	var signal struct {
		ID          string `json:"id"`
		Source      string `json:"source"`
		Kind        string `json:"kind"`
		SubjectType string `json:"subjectType"`
	}
	require.NoError(t, json.Unmarshal(raw, &signal), "response body is not valid JSON: %s", string(raw))
	require.NotEmpty(t, signal.ID, "discord signal response has empty id")
	require.Equal(t, "discord", signal.Source, "unexpected source in response")
	require.Equal(t, "discord.presence", signal.Kind, "unexpected kind in response")
	require.Equal(t, "user", signal.SubjectType, "unexpected subjectType in response")

	// Confirm the row landed in the correct workspace via direct SQL.
	wsPub, err := types.Parse(h.tenant.WorkspacePublicID)
	require.NoError(t, err, "WorkspacePublicID is not a valid UUID")
	sigPub, err := types.Parse(signal.ID)
	require.NoError(t, err, "signal ID in response is not a valid UUID")

	var count int
	row := testDB.QueryRow(
		`SELECT COUNT(*) FROM signals s
		   INNER JOIN workspaces w ON w.id = s.workspace_id
		  WHERE w.public_id = ? AND s.public_id = ? AND s.source = 'discord'`,
		wsPub,
		sigPub,
	)
	require.NoError(t, row.Scan(&count))
	require.Equal(t, 1, count, "discord signal row not persisted in the expected workspace")
}

// TestDiscordSignalSourceCrossTenant verifies that a discord signal
// belonging to tenant A is not visible when querying via tenant B's
// workspace id. The row-level isolation guarantee is enforced by the
// workspace_id column on the signals table rather than application logic,
// so this test acts as a canary if that scoping is ever removed.
func TestDiscordSignalSourceCrossTenant(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	h := newSignalsServiceTokenHarness(t)

	// Create a second independent tenant on the same server.
	tenantB := helpers.CreateTestTenant(t, h.baseURL)

	// Post a discord signal scoped to tenant A's workspace.
	status, raw := postSignal(t, h.baseURL, serviceTokenFixture, map[string]any{
		"workspaceId": h.tenant.WorkspacePublicID,
		"source":      "discord",
		"kind":        "discord.presence",
		"subjectType": "user",
		"subjectId":   h.tenant.UserPublicID,
		"payload":     map[string]any{"status": "online"},
	})
	require.Equalf(t, http.StatusOK, status,
		"POST /signals tenant A returned %d; body=%s", status, string(raw))

	var signal struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(raw, &signal))
	require.NotEmpty(t, signal.ID)

	// Resolve internal ids for both workspaces.
	wsPubA, err := types.Parse(h.tenant.WorkspacePublicID)
	require.NoError(t, err)
	wsPubB, err := types.Parse(tenantB.WorkspacePublicID)
	require.NoError(t, err)
	sigPub, err := types.Parse(signal.ID)
	require.NoError(t, err)

	// The row must appear in workspace A.
	var countA int
	rowA := testDB.QueryRow(
		`SELECT COUNT(*) FROM signals s
		   INNER JOIN workspaces w ON w.id = s.workspace_id
		  WHERE w.public_id = ? AND s.public_id = ?`,
		wsPubA, sigPub,
	)
	require.NoError(t, rowA.Scan(&countA))
	require.Equal(t, 1, countA, "discord signal not found in workspace A")

	// The same public_id must NOT appear in workspace B.
	var countB int
	rowB := testDB.QueryRow(
		`SELECT COUNT(*) FROM signals s
		   INNER JOIN workspaces w ON w.id = s.workspace_id
		  WHERE w.public_id = ? AND s.public_id = ?`,
		wsPubB, sigPub,
	)
	require.NoError(t, rowB.Scan(&countB))
	require.Equal(t, 0, countB, "discord signal from workspace A is visible in workspace B (cross-tenant leak)")
}
