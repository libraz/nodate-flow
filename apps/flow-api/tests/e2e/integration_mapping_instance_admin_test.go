package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/signals"
)

// TestIntegrationMappingWritesRequireInstanceAdmin keeps global inbound
// source claims behind the instance operator boundary while preserving the
// existing workspace membership/admin gate and read access.
func TestIntegrationMappingWritesRequireInstanceAdmin(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	trustedAdmin := seedElevatedMember(t, owner)
	grantInstanceAdmin(t, testDB, trustedAdmin.UserPublicID)
	attacker := newTenant(t)

	trustedRepo := randomRepoID()
	created := createMapping(t, trustedAdmin.AccessToken, owner.WorkspacePublicID,
		"github", fmt.Sprint(trustedRepo), "acme/trusted")

	// Workspace administrators can still list mappings without an instance
	// admin grant.
	var ownerList struct {
		Mappings []mappingResponse `json:"mappings"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/integration-mappings",
		owner.AccessToken, nil, &ownerList)
	require.Len(t, ownerList.Mappings, 1)
	require.Equal(t, created.ID, ownerList.Mappings[0].ID)

	var attackerList struct {
		Mappings []mappingResponse `json:"mappings"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+attacker.WorkspacePublicID+"/integration-mappings",
		attacker.AccessToken, nil, &attackerList)
	require.Empty(t, attackerList.Mappings,
		"ordinary workspace admins retain read/list access to their workspace mappings")

	// A signed delivery for a source bound by the trusted operator continues
	// to route into the authorized workspace.
	deps := webhookDeps()
	deps.GhWebhookSecret = ghSecret
	webhook := signals.HandleGithubWebhook(deps)
	status, routed := callWebhook(t, webhook, githubDelivery(t, ghSecret, trustedRepo, "no marker"))
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, owner.WorkspacePublicID, signalWorkspaceFor(t, routed.ID))

	// An unrelated workspace admin cannot claim an unclaimed source by
	// presenting its provider-side identifier alone.
	unclaimedRepo := randomRepoID()
	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+attacker.WorkspacePublicID+"/integration-mappings",
		attacker.AccessToken,
		map[string]any{"provider": "github", "externalKey": fmt.Sprint(unclaimedRepo), "label": "acme/preclaimed"})
	require.Equal(t, http.StatusForbidden, status, "body=%s", string(raw))
	require.Contains(t, string(raw), "AUTH.PERMISSION.INSTANCE_ADMIN_REQUIRED")

	// The denied claim leaves no route. A correctly signed provider delivery
	// is refused rather than filed in the attacker's workspace or elsewhere.
	rec := httptest.NewRecorder()
	webhook(rec, githubDelivery(t, ghSecret, unclaimedRepo, "sensitive issue body"))
	require.Equal(t, http.StatusNotFound, rec.Code, "body=%s", rec.Body.String())
	require.Contains(t, rec.Body.String(), "INTEGRATION.MAPPING.WORKSPACE_UNRESOLVED")
	var stored int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM signals
		 WHERE source = 'github' AND JSON_EXTRACT(payload_json, '$.repository.id') = ?`,
		unclaimedRepo).Scan(&stored))
	require.Zero(t, stored, "a denied claim must not route or persist a signed delivery")

	// Workspace admins retain their workspace floor but cannot modify an
	// existing global routing claim without instance operator authority.
	status, raw = doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/integration-mappings/"+created.ID,
		owner.AccessToken, map[string]any{"label": "owner changed it"})
	require.Equal(t, http.StatusForbidden, status, "body=%s", string(raw))
	require.Contains(t, string(raw), "AUTH.PERMISSION.INSTANCE_ADMIN_REQUIRED")

	status, raw = doJSONStatus(t, http.MethodDelete,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/integration-mappings/"+created.ID,
		owner.AccessToken, nil)
	require.Equal(t, http.StatusForbidden, status, "body=%s", string(raw))
	require.Contains(t, string(raw), "AUTH.PERMISSION.INSTANCE_ADMIN_REQUIRED")

	// A trusted instance admin who also has workspace admin access can still
	// update and release the source binding.
	var patched mappingResponse
	doJSON(t, http.MethodPatch,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/integration-mappings/"+created.ID,
		trustedAdmin.AccessToken, map[string]any{"label": "acme/renamed"}, &patched)
	require.Equal(t, "acme/renamed", patched.Label)
	doJSON(t, http.MethodDelete,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/integration-mappings/"+created.ID,
		trustedAdmin.AccessToken, nil, nil)
}
