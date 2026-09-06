package e2e

import (
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/signals"
)

// ghSecret is the shared HMAC secret every GitHub delivery in this file
// is signed with. Each test builds its own handler around it, so a single
// value keeps the fixtures readable without coupling them.
const ghSecret = "webhook-tenancy-fixture" //#nosec G101 -- synthetic test fixture, never a real secret

// randomRepoID returns a repository id in a range wide enough that two
// parallel tests cannot collide on the instance-wide
// (provider, external_key) unique key.
func randomRepoID() int64 {
	return 100_000_000 + rand.Int63n(800_000_000) //#nosec G404 -- test fixture identity, not a security decision
}

// githubDelivery builds a signed GitHub webhook request for the given
// repository id and body extras. Each call carries its own delivery id,
// so two calls are two distinct deliveries rather than a redelivery of
// one; a case that needs the redelivery shape names the id itself.
func githubDelivery(t *testing.T, secret string, repoID int64, issueBody string) *http.Request {
	t.Helper()
	return githubDeliveryWithID(t, secret, repoID, randomHex(8), issueBody)
}

// signalWorkspaceFor returns the workspace public id the signal with the
// given public id was filed under. It reads the row back through the
// workspace join rather than counting, so the assertion states which
// tenant received the delivery instead of merely how many rows exist —
// the suite shares one MySQL instance with other parallel tests.
func signalWorkspaceFor(t *testing.T, signalPublicID string) string {
	t.Helper()
	var ws string
	err := testDB.QueryRow(
		`SELECT BIN_TO_UUID(w.public_id, 0)
		 FROM signals s
		 JOIN workspaces w ON w.id = s.workspace_id
		 WHERE s.public_id = UUID_TO_BIN(?, 0)`,
		signalPublicID).Scan(&ws)
	require.NoError(t, err, "signal %s was not stored", signalPublicID)
	return ws
}

// TestWebhookDeliveriesRouteToTheMappedWorkspace is the tenancy
// regression for the inbound receivers. Before the mapping table was
// wired up every delivery was filed under a single configured workspace,
// so on a multi-tenant instance only one tenant could use the GitHub /
// Slack / Google integrations at all and everyone else's events either
// vanished or landed in the wrong tenant.
//
// Two tenants each claim their own repository through the admin CRUD;
// a delivery from each repository must land in its own workspace.
func TestWebhookDeliveriesRouteToTheMappedWorkspace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	alice := newTenant(t)
	bob := newTenant(t)

	// No DefaultWorkspaceID: a multi-tenant instance has no meaningful
	// default, which is the whole point of the mapping.
	deps := webhookDeps()
	deps.GhWebhookSecret = ghSecret
	handler := signals.HandleGithubWebhook(deps)

	aliceRepo := randomRepoID()
	bobRepo := randomRepoID()
	createMapping(t, alice.AccessToken, alice.WorkspacePublicID, "github", fmt.Sprint(aliceRepo), "acme/alice")
	createMapping(t, bob.AccessToken, bob.WorkspacePublicID, "github", fmt.Sprint(bobRepo), "acme/bob")

	status, fromAlice := callWebhook(t, handler, githubDelivery(t, ghSecret, aliceRepo, "no marker"))
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, alice.WorkspacePublicID, signalWorkspaceFor(t, fromAlice.ID),
		"a delivery from Alice's repository must be filed in Alice's workspace")

	status, fromBob := callWebhook(t, handler, githubDelivery(t, ghSecret, bobRepo, "no marker"))
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, bob.WorkspacePublicID, signalWorkspaceFor(t, fromBob.ID),
		"a delivery from Bob's repository must be filed in Bob's workspace, not the first-configured tenant")
}

// TestWebhookDeliveryFromUnmappedSourceIsRejected pins the decision that
// an unroutable delivery is refused rather than filed somewhere. Storing
// it under a default workspace is what let one tenant read another
// tenant's repository activity.
func TestWebhookDeliveryFromUnmappedSourceIsRejected(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	deps := webhookDeps()
	deps.GhWebhookSecret = ghSecret
	handler := signals.HandleGithubWebhook(deps)

	unmapped := randomRepoID()
	rec := httptest.NewRecorder()
	handler(rec, githubDelivery(t, ghSecret, unmapped, "no marker"))

	require.Equal(t, http.StatusNotFound, rec.Code,
		"an unmapped sender must be refused, not routed to an arbitrary workspace")
	require.Contains(t, rec.Body.String(), "INTEGRATION.MAPPING.WORKSPACE_UNRESOLVED")

	var stored int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM signals
		 WHERE source = 'github' AND JSON_EXTRACT(payload_json, '$.repository.id') = ?`,
		unmapped).Scan(&stored))
	require.Zero(t, stored, "a rejected delivery must not be persisted anywhere")
}

// TestWebhookDefaultWorkspaceFallbackIsOffOnMultiTenant covers the
// remaining escape hatch: NF_FLOW_DEFAULT_WORKSPACE_ID still exists for
// single-tenant deployments, but honouring it on an instance that hosts
// several workspaces is exactly the cross-tenant leak the mapping was
// added to close. The e2e instance always has more than one workspace,
// so configuring the fallback here must change nothing.
func TestWebhookDefaultWorkspaceFallbackIsOffOnMultiTenant(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	victim := newTenant(t)
	// A second tenant guarantees the instance is multi-tenant even if this
	// test ever runs alone.
	_ = newTenant(t)

	deps := webhookDeps()
	deps.GhWebhookSecret = ghSecret
	deps.DefaultWorkspaceID = victim.WorkspacePublicID
	handler := signals.HandleGithubWebhook(deps)

	unmapped := randomRepoID()
	rec := httptest.NewRecorder()
	handler(rec, githubDelivery(t, ghSecret, unmapped, "no marker"))

	require.Equal(t, http.StatusNotFound, rec.Code,
		"the default-workspace fallback must not apply once the instance has more than one workspace")

	var leaked int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM signals
		 WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))
		   AND source = 'github'`,
		victim.WorkspacePublicID).Scan(&leaked))
	require.Zero(t, leaked, "no delivery may be filed under the configured default on a multi-tenant instance")

	// The signals row is only one of the three places a filed delivery
	// shows up. A refused delivery must also leave no trace in the two
	// logs, or the configured tenant's timeline and audit trail would
	// report an ingestion that never belonged to it.
	events, audits := countIngestionRecords(t, victim.WorkspacePublicID)
	require.Zero(t, events, "a refused delivery must not appear on the configured tenant's timeline")
	require.Zero(t, audits, "a refused delivery must not appear in the configured tenant's audit log")
}

// TestGithubTaskMarkerCannotCrossWorkspaces covers the authorisation half
// of the mapping: anyone who can write into a GitHub issue body controls
// the `tnk:<uuid>` marker, so the task it names must be resolvable only
// inside the workspace that owns the sending repository.
func TestGithubTaskMarkerCannotCrossWorkspaces(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	attacker := newTenant(t)
	victim := newTenant(t)

	victimTask := createTaskForAgent(t, victim, "Victim private task")

	deps := webhookDeps()
	deps.GhWebhookSecret = ghSecret
	handler := signals.HandleGithubWebhook(deps)

	attackerRepo := randomRepoID()
	createMapping(t, attacker.AccessToken, attacker.WorkspacePublicID, "github", fmt.Sprint(attackerRepo), "acme/attacker")

	status, out := callWebhook(t, handler,
		githubDelivery(t, ghSecret, attackerRepo, "closes tnk:"+victimTask))
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, attacker.WorkspacePublicID, signalWorkspaceFor(t, out.ID))

	var linked int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM signals s
		 JOIN tasks tk ON tk.id = s.task_id
		 WHERE s.public_id = UUID_TO_BIN(?, 0)
		   AND tk.public_id = UUID_TO_BIN(?, 0)`,
		out.ID, victimTask).Scan(&linked))
	require.Zero(t, linked,
		"a marker naming another workspace's task must not attach the signal to it")

	var subjectType string
	require.NoError(t, testDB.QueryRow(
		`SELECT subject_type FROM signals WHERE public_id = UUID_TO_BIN(?, 0)`,
		out.ID).Scan(&subjectType))
	require.Equal(t, "workspace", subjectType,
		"an unresolvable marker must leave the signal workspace-scoped rather than claiming a task subject")
}

// ---- integration source mapping CRUD ---------------------------------------

// mappingResponse is the DTO returned by the integration-mappings routes.
type mappingResponse struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	ExternalKey string `json:"externalKey"`
	Label       string `json:"label"`
	Enabled     bool   `json:"enabled"`
}

// createMapping claims an external source for a workspace through the
// admin REST surface and returns the created row.
func createMapping(t *testing.T, token, workspaceID, provider, externalKey, label string) mappingResponse {
	t.Helper()
	var out mappingResponse
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+workspaceID+"/integration-mappings",
		token,
		map[string]any{"provider": provider, "externalKey": externalKey, "label": label},
		&out)
	require.NotEmpty(t, out.ID)
	return out
}

// TestIntegrationMappingCrudIsWorkspaceScoped exercises the CRUD an
// operator needs in order to run the routing table at all, and the two
// boundaries that keep it from becoming a cross-tenant lever: a source
// belongs to exactly one workspace, and a mapping id from another tenant
// is invisible.
func TestIntegrationMappingCrudIsWorkspaceScoped(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	other := newTenant(t)

	repoID := fmt.Sprint(randomRepoID())
	created := createMapping(t, owner.AccessToken, owner.WorkspacePublicID, "github", repoID, "acme/widgets")
	require.Equal(t, "github", created.Provider)
	require.Equal(t, repoID, created.ExternalKey)
	require.True(t, created.Enabled)

	var list struct {
		Total    int64             `json:"total"`
		Mappings []mappingResponse `json:"mappings"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/integration-mappings",
		owner.AccessToken, nil, &list)
	require.Len(t, list.Mappings, 1, "the workspace owns exactly the mapping it just created")
	require.Equal(t, created.ID, list.Mappings[0].ID)

	// Another workspace cannot claim the same source: an inbound delivery
	// can only belong to one tenant.
	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+other.WorkspacePublicID+"/integration-mappings",
		other.AccessToken,
		map[string]any{"provider": "github", "externalKey": repoID, "label": "acme/stolen"})
	require.Equal(t, http.StatusConflict, status, "body=%s", string(raw))
	require.Contains(t, string(raw), "INTEGRATION.MAPPING.SOURCE_ALREADY_MAPPED")

	// The other tenant's list stays empty: the failed claim wrote nothing.
	var otherList struct {
		Mappings []mappingResponse `json:"mappings"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+other.WorkspacePublicID+"/integration-mappings",
		other.AccessToken, nil, &otherList)
	require.Empty(t, otherList.Mappings)

	// A malformed key for the chosen provider is refused up front, because
	// a mapping that can never match is indistinguishable from no mapping.
	status, raw = doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/integration-mappings",
		owner.AccessToken,
		map[string]any{"provider": "github", "externalKey": "acme/widgets", "label": "by name"})
	require.Equal(t, http.StatusBadRequest, status, "body=%s", string(raw))
	require.Contains(t, string(raw), "INTEGRATION.MAPPING.EXTERNAL_KEY_INVALID")

	// Patching another tenant's mapping is a 404, not a 403: cross-tenant
	// existence must not leak.
	status, raw = doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+other.WorkspacePublicID+"/integration-mappings/"+created.ID,
		other.AccessToken, map[string]any{"label": "renamed by a stranger"})
	require.Equal(t, http.StatusNotFound, status, "body=%s", string(raw))
	require.Contains(t, string(raw), "INTEGRATION.MAPPING.NOT_FOUND")

	// ... and neither is deleting it.
	status, raw = doJSONStatus(t, http.MethodDelete,
		testServerURL+"/workspaces/"+other.WorkspacePublicID+"/integration-mappings/"+created.ID,
		other.AccessToken, nil)
	require.Equal(t, http.StatusNotFound, status, "body=%s", string(raw))

	// The owner may pause routing without releasing the claim.
	var patched mappingResponse
	doJSON(t, http.MethodPatch,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/integration-mappings/"+created.ID,
		owner.AccessToken, map[string]any{"enabled": false}, &patched)
	require.False(t, patched.Enabled)

	// Deleting releases the source so another workspace can map it.
	doJSON(t, http.MethodDelete,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/integration-mappings/"+created.ID,
		owner.AccessToken, nil, nil)
	reclaimed := createMapping(t, other.AccessToken, other.WorkspacePublicID, "github", repoID, "acme/reclaimed")
	require.NotEqual(t, created.ID, reclaimed.ID)
}

// TestDisabledIntegrationMappingStopsRouting pins what `enabled=false`
// means on the delivery path: paused, not merely hidden from the list.
func TestDisabledIntegrationMappingStopsRouting(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	deps := webhookDeps()
	deps.GhWebhookSecret = ghSecret
	handler := signals.HandleGithubWebhook(deps)

	repoID := randomRepoID()
	mapping := createMapping(t, tt.AccessToken, tt.WorkspacePublicID, "github", fmt.Sprint(repoID), "acme/paused")

	status, accepted := callWebhook(t, handler, githubDelivery(t, ghSecret, repoID, "no marker"))
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, tt.WorkspacePublicID, signalWorkspaceFor(t, accepted.ID))

	// The routed delivery is the control for the refusal below: it fixes
	// what one accepted delivery records, so the unchanged counts after
	// the pause mean the second delivery was stopped rather than that the
	// handler records nothing at all.
	events, audits := countIngestionRecords(t, tt.WorkspacePublicID)
	require.Equal(t, 1, events, "a routed delivery appends one event")
	require.Equal(t, 1, audits, "a routed delivery writes one audit row")

	doJSON(t, http.MethodPatch,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/integration-mappings/"+mapping.ID,
		tt.AccessToken, map[string]any{"enabled": false}, nil)

	rec := httptest.NewRecorder()
	handler(rec, githubDelivery(t, ghSecret, repoID, "no marker"))
	require.Equal(t, http.StatusNotFound, rec.Code,
		"a paused mapping must stop routing deliveries")
	require.True(t, strings.Contains(rec.Body.String(), "INTEGRATION.MAPPING.WORKSPACE_UNRESOLVED"))

	events, audits = countIngestionRecords(t, tt.WorkspacePublicID)
	require.Equal(t, 1, events, "a delivery a paused mapping refused must reach neither log")
	require.Equal(t, 1, audits, "a delivery a paused mapping refused must reach neither log")
}
