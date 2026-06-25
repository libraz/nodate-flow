package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSignalDuplicateCreateIsIdempotent verifies the P3-2 / H-13 fix: a
// duplicate POST /signals (same workspace-scoped dedupe key
// workspace_id+source+external_id) must NOT mint a fresh throwaway
// public_id and must NOT write a second audit row. The server detects the
// no-op INSERT IGNORE (LastInsertId()=0) and returns the EXISTING row's
// public_id so the response is honest, while short-circuiting the spurious
// audit write.
//
// Before the fix the second POST returned a brand-new UUID matching no
// persisted row and appended a duplicate signal.create audit entry, so a
// re-emitting worker (60s tick) spammed the audit log unbounded.
func TestSignalDuplicateCreateIsIdempotent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// A fixed external_id makes the (workspace_id, source, external_id)
	// dedupe key collide on the second POST. `manual`/`manual` are
	// registered in signal_kinds/user.yaml so the kind check passes.
	body := map[string]any{
		"workspaceId": tt.WorkspacePublicID,
		"source":      "manual",
		"kind":        "manual",
		"externalId":  "dedupe-fixture-" + randomHex(6),
		"payload":     map[string]any{"tick": 1},
	}

	type signalResp struct {
		ID         string `json:"id"`
		ExternalID string `json:"externalId"`
		Source     string `json:"source"`
		Kind       string `json:"kind"`
	}

	// First create: genuine insert.
	var first signalResp
	doJSON(t, http.MethodPost, testServerURL+"/signals", tt.AccessToken, body, &first)
	require.NotEmpty(t, first.ID, "first signal create must return a public id")

	// Second create with the identical dedupe key: INSERT IGNORE no-ops.
	var second signalResp
	doJSON(t, http.MethodPost, testServerURL+"/signals", tt.AccessToken, body, &second)
	require.NotEmpty(t, second.ID, "duplicate signal create must still return a public id")

	// The duplicate must return the ORIGINAL row's public_id, not a freshly
	// minted throwaway UUID that matches no persisted row.
	require.Equal(t, first.ID, second.ID,
		"duplicate signal create must return the existing row's public id, not a new UUID")

	// Confirm the persisted row matches the returned public_id and there is
	// exactly one signals row for this dedupe key.
	var signalRowCount int
	err := testDB.QueryRow(
		`SELECT COUNT(*) FROM signals
		 WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))
		   AND source = 'manual'
		   AND external_id = ?`,
		tt.WorkspacePublicID, body["externalId"]).Scan(&signalRowCount)
	require.NoError(t, err)
	require.Equal(t, 1, signalRowCount, "duplicate POST must not create a second signals row")

	var persisted int
	err = testDB.QueryRow(
		`SELECT COUNT(*) FROM signals
		 WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))
		   AND public_id = UUID_TO_BIN(?, 0)`,
		tt.WorkspacePublicID, second.ID).Scan(&persisted)
	require.NoError(t, err)
	require.Equal(t, 1, persisted,
		"the public_id returned by the duplicate POST must match a real persisted row")

	// The audit log must record exactly one signal.create entry: the
	// duplicate must NOT spam a second spurious row.
	logs := queryAuditLogs(t, testDB, tt.WorkspacePublicID)
	var signalCreateCount int
	for _, r := range logs {
		if r.Action == "signal.create" && r.ResourceType == "signal" {
			signalCreateCount++
			// The single recorded resource id must be the real persisted
			// public id, never a throwaway from the duplicate path.
			require.Equal(t, first.ID, r.ResourcePublicID.String,
				"signal.create audit must reference the persisted row's public id")
		}
	}
	require.Equal(t, 1, signalCreateCount,
		"a duplicate signal create must not write a second signal.create audit row")
}
