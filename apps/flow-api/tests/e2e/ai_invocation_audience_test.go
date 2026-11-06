package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestAiInvocationAudienceMixedInputs exercises the real REST route and
// provider boundary with a public parent and private child. The caller is a
// watcher on the child; another ordinary workspace member can read the public
// parent but cannot read the child or either invocation's mixed prompt.
func TestAiInvocationAudienceMixedInputs(t *testing.T) {
	bootstrap(t)
	if aiMockEnabled() {
		t.Skip("NF_FLOW_AI_MOCK is set; this test needs a per-workspace fake provider")
	}
	t.Parallel()

	owner := newTenant(t)
	caller := seedProjectRoleMember(t, owner, "editor")
	reader := seedProjectRoleMember(t, owner, "viewer")

	suffix := uuid.NewString()
	successChildMarker := "AI_PRIVATE_CHILD_SUCCESS_" + strings.ReplaceAll(suffix, "-", "")
	failureChildMarker := "AI_PRIVATE_CHILD_FAILURE_" + strings.ReplaceAll(suffix, "-", "")
	responseMarker := "AI_PRIVATE_RESPONSE_" + strings.ReplaceAll(suffix, "-", "")
	successParent := createTaskWithVisibility(t, owner, "AI audience public success "+suffix, "public")
	successChild := createTaskWithVisibility(t, owner, successChildMarker, "private")
	attachChildTask(t, owner.WorkspacePublicID, successParent, successChild)
	attachTaskUserActor(t, owner.WorkspacePublicID, successChild, caller.UserPublicID, "watcher")

	failureParent := createTaskWithVisibility(t, owner, "AI audience public failure "+suffix, "public")
	failureChild := createTaskWithVisibility(t, owner, failureChildMarker, "private")
	attachChildTask(t, owner.WorkspacePublicID, failureParent, failureChild)
	attachTaskUserActor(t, owner.WorkspacePublicID, failureChild, caller.UserPublicID, "watcher")

	// Establish the ACL state independently of the invocation results.
	status, body := doJSONStatus(t, http.MethodGet, testServerURL+"/tasks/"+successParent, reader.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "the reader must read the public parent; body=%s", string(body))
	status, body = doJSONStatus(t, http.MethodGet, testServerURL+"/tasks/"+successChild, reader.AccessToken, nil)
	requireDenied(t, status, body, http.StatusNotFound, "WS.TASK.NOT_FOUND", "reader must not read the private child")
	status, body = doJSONStatus(t, http.MethodGet, testServerURL+"/tasks/"+successChild, caller.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "the watcher must read the private child; body=%s", string(body))

	var mu sync.Mutex
	var receivedPrompts []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read request", http.StatusBadRequest)
			return
		}
		var request struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			http.Error(w, "decode request", http.StatusBadRequest)
			return
		}
		var prompt strings.Builder
		for _, message := range request.Messages {
			prompt.WriteString(message.Content)
			prompt.WriteByte('\n')
		}
		promptText := prompt.String()
		mu.Lock()
		receivedPrompts = append(receivedPrompts, promptText)
		mu.Unlock()

		if strings.Contains(promptText, failureChildMarker) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"fake upstream rejected request","type":"invalid_api_key"}}`))
			return
		}

		content := fmt.Sprintf(`[{"title":"safe proposal","description":"copied %s","priority":"medium"},{"title":"Verify evidence","description":"Check the result","priority":"low"},{"title":"Review output","description":"Review the result","priority":"low"}]`, responseMarker)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":8}}`, content)
	}))
	t.Cleanup(upstream.Close)
	registerOpenAICompatProvider(t, owner, upstream.URL)

	postPropose := func(parentID string) (int, []byte) {
		t.Helper()
		return doJSONStatus(t, http.MethodPost,
			testServerURL+"/tasks/"+parentID+"/propose-steps",
			caller.AccessToken,
			map[string]any{"granularity": "coarse"},
		)
	}

	status, body = postPropose(successParent)
	require.Equal(t, http.StatusOK, status, "successful fake provider response; body=%s", string(body))
	require.Contains(t, string(body), responseMarker, "the fake provider response should be returned to its initiating caller")

	status, body = postPropose(failureParent)
	require.Equal(t, http.StatusBadGateway, status, "provider failure should be surfaced; body=%s", string(body))
	mu.Lock()
	prompts := append([]string(nil), receivedPrompts...)
	mu.Unlock()
	require.Len(t, prompts, 2, "both calls must cross the fake provider boundary")
	require.Contains(t, prompts[0], successChildMarker, "successful mixed prompt must include the private child")
	require.Contains(t, prompts[1], failureChildMarker, "failed mixed prompt must include the private child")

	workspaceFeed := func(token string) string {
		t.Helper()
		status, raw := doJSONStatus(t, http.MethodGet,
			testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/ai/invocations?limit=200",
			token, nil)
		require.Equal(t, http.StatusOK, status, "workspace invocation feed; body=%s", string(raw))
		return string(raw)
	}
	taskFeed := func(token, taskID string) string {
		t.Helper()
		status, raw := doJSONStatus(t, http.MethodGet,
			testServerURL+"/tasks/"+taskID+"/ai/invocations?limit=100",
			token, nil)
		require.Equal(t, http.StatusOK, status, "task invocation feed; body=%s", string(raw))
		return string(raw)
	}

	callerWorkspace := workspaceFeed(caller.AccessToken)
	callerSuccessTask := taskFeed(caller.AccessToken, successParent)
	callerFailureTask := taskFeed(caller.AccessToken, failureParent)
	require.Contains(t, callerWorkspace, successChildMarker, "caller workspace feed should retain the authorized successful prompt")
	require.Contains(t, callerWorkspace, responseMarker, "caller workspace feed should retain the successful response copy")
	require.Contains(t, callerWorkspace, failureChildMarker, "caller workspace feed should retain the authorized failed prompt")
	require.Contains(t, callerSuccessTask, successChildMarker, "caller success task feed should retain its authorized prompt")
	require.Contains(t, callerSuccessTask, responseMarker, "caller success task feed should retain its response copy")
	require.NotContains(t, callerSuccessTask, failureChildMarker, "success task feed must remain scoped to its subject")
	require.Contains(t, callerFailureTask, failureChildMarker, "caller failure task feed should retain its authorized prompt")
	require.NotContains(t, callerFailureTask, successChildMarker, "failure task feed must remain scoped to its subject")
	require.NotContains(t, callerFailureTask, responseMarker, "failure task feed must not contain the other call's response")

	readerWorkspace := workspaceFeed(reader.AccessToken)
	readerSuccessTask := taskFeed(reader.AccessToken, successParent)
	readerFailureTask := taskFeed(reader.AccessToken, failureParent)
	for _, location := range []struct {
		name string
		body string
	}{
		{"reader workspace feed", readerWorkspace},
		{"reader success task feed", readerSuccessTask},
		{"reader failure task feed", readerFailureTask},
	} {
		require.NotContains(t, location.body, successChildMarker, "%s must not expose a private-child prompt", location.name)
		require.NotContains(t, location.body, responseMarker, "%s must not expose a response copied from private context", location.name)
		require.NotContains(t, location.body, failureChildMarker, "%s must not expose a failed private-child prompt", location.name)
	}

	// The parent remains readable to the caller, but revoking the caller's
	// child actor access must also revoke the mixed-input invocation on both
	// feeds. Parent-only authorization would keep leaking the stored prompt.
	disableTaskUserActor(t, owner.WorkspacePublicID, successChild, caller.UserPublicID, "watcher")
	status, body = doJSONStatus(t, http.MethodGet, testServerURL+"/tasks/"+successParent, caller.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "parent remains readable after child access is revoked; body=%s", string(body))
	status, body = doJSONStatus(t, http.MethodGet, testServerURL+"/tasks/"+successChild, caller.AccessToken, nil)
	requireDenied(t, status, body, http.StatusNotFound, "WS.TASK.NOT_FOUND", "revoked child actor must not retain child read access")
	require.NotContains(t, workspaceFeed(caller.AccessToken), successChildMarker,
		"workspace feed must re-check the current ACL for every input resource")
	require.NotContains(t, taskFeed(caller.AccessToken, successParent), successChildMarker,
		"task feed must apply the same current input-resource ACL")

	// Revoke the subject parent separately. The workspace feed must stop
	// returning that call, and the task feed must be concealed by task ACL.
	setTaskVisibility(t, owner.WorkspacePublicID, failureParent, "private")
	status, body = doJSONStatus(t, http.MethodGet, testServerURL+"/tasks/"+failureParent, caller.AccessToken, nil)
	requireDenied(t, status, body, http.StatusNotFound, "WS.TASK.NOT_FOUND", "caller lost subject-parent access")
	require.NotContains(t, workspaceFeed(caller.AccessToken), failureChildMarker,
		"workspace feed must re-check current readability of the invocation subject")
	status, body = doJSONStatus(t, http.MethodGet,
		testServerURL+"/tasks/"+failureParent+"/ai/invocations?limit=100",
		caller.AccessToken, nil)
	requireDenied(t, status, body, http.StatusNotFound, "WS.TASK.NOT_FOUND", "task feed must conceal a revoked subject")
}

// TestAiInvocationAudienceAllowsTaskSafeRowsAndHonorsRevocation tests the
// task audience through both feeds. It is run after the ai_invocations
// audience migration has been generated and applied.
func TestAiInvocationAudienceAllowsTaskSafeRowsAndHonorsRevocation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	reader := seedProjectRoleMember(t, owner, "viewer")
	parent := createTaskWithVisibility(t, owner, "AI task audience public "+uuid.NewString(), "public")
	workspaceID, taskID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, parent)
	provider := helpers.SeedAgent(t, testDB, owner.WorkspacePublicID, helpers.SeedAgentOptions{}).ProviderID
	marker := "AI_TASK_SAFE_INVOCATION_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	publicID := uuid.Must(uuid.NewV7())
	parentUUID, err := uuid.Parse(parent)
	require.NoError(t, err)
	result, err := testDB.Exec(`
		INSERT INTO ai_invocations (
			public_id, workspace_id, provider_id, user_id, task_id, audience,
			input_resources_json, input_count, provenance_valid,
			purpose, model, prompt_redacted, status, invoked_at
		) VALUES (?, ?, ?, NULL, ?, 'task', JSON_ARRAY(JSON_OBJECT('kind', 'task', 'publicId', ?)), 1, TRUE,
			'propose_priority', 'test-model', ?, 'ok', ?)`,
		publicID[:], workspaceID, provider, taskID, parent, marker, time.Now().UTC())
	require.NoError(t, err)
	invocationID, err := result.LastInsertId()
	require.NoError(t, err)
	_, err = testDB.Exec(`INSERT INTO ai_invocations_inputs (invocation_id, task_public_id) VALUES (?, ?)`, uint32(invocationID), parentUUID[:]) //nolint:gosec // test-scoped invocation id fits uint32
	require.NoError(t, err)

	invalidMarker := "AI_INVALID_INVOCATION_PROVENANCE_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	invalidPublicID := uuid.Must(uuid.NewV7())
	invalidResult, err := testDB.Exec(`
		INSERT INTO ai_invocations (
			public_id, workspace_id, provider_id, user_id, task_id, audience,
			input_resources_json, input_count, provenance_valid,
			purpose, model, prompt_redacted, status, invoked_at
		) VALUES (?, ?, ?, NULL, ?, 'task', JSON_ARRAY(JSON_OBJECT('kind', 'task', 'publicId', ?)), 1, FALSE,
			'propose_priority', 'test-model', ?, 'ok', ?)`,
		invalidPublicID[:], workspaceID, provider, taskID, parent, invalidMarker, time.Now().UTC())
	require.NoError(t, err)
	invalidInvocationID, err := invalidResult.LastInsertId()
	require.NoError(t, err)

	deletedResource := createTaskWithVisibility(t, owner, "AI deleted provenance resource "+uuid.NewString(), "public")
	_, deletedResourceID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, deletedResource)
	var deletedResourcePublicID []byte
	require.NoError(t, testDB.QueryRow(`SELECT public_id FROM tasks WHERE id = ?`, deletedResourceID).Scan(&deletedResourcePublicID))
	deletedResourceUUID, err := uuid.FromBytes(deletedResourcePublicID)
	require.NoError(t, err)
	deletedMarker := "AI_DELETED_INVOCATION_RESOURCE_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	deletedInvocationID := uuid.Must(uuid.NewV7())
	result, err = testDB.Exec(`
		INSERT INTO ai_invocations (
			public_id, workspace_id, provider_id, user_id, task_id, audience,
			input_resources_json, input_count, provenance_valid,
			purpose, model, prompt_redacted, status, invoked_at
		) VALUES (?, ?, ?, NULL, ?, 'task', JSON_ARRAY(JSON_OBJECT('kind', 'task', 'publicId', ?)), 1, TRUE,
			'propose_priority', 'test-model', ?, 'ok', ?)`,
		deletedInvocationID[:], workspaceID, provider, taskID, deletedResourceUUID.String(), deletedMarker, time.Now().UTC())
	require.NoError(t, err)
	deletedInvocationRowID, err := result.LastInsertId()
	require.NoError(t, err)
	_, err = testDB.Exec(`INSERT INTO ai_invocations_inputs (invocation_id, task_public_id) VALUES (?, ?)`, uint32(deletedInvocationRowID), deletedResourcePublicID) //nolint:gosec // test-scoped invocation id fits uint32
	require.NoError(t, err)
	_, err = testDB.Exec(`UPDATE tasks SET enabled = FALSE WHERE id = ?`, deletedResourceID)
	require.NoError(t, err)
	var readerUserID uint32
	require.NoError(t, testDB.QueryRow(`SELECT id FROM users WHERE public_id = UUID_TO_BIN(?, 0)`, reader.UserPublicID).Scan(&readerUserID))
	_, err = generated.New(testDB).ListAiInvocationsForWorkspace(context.Background(), generated.ListAiInvocationsForWorkspaceParams{
		WorkspaceID: workspaceID,
		ActorUserID: int64(readerUserID),
		Limit:       200,
	})
	require.NoError(t, err, "direct workspace invocation query should report the database error hidden by the HTTP 500")

	workspaceURL := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/ai/invocations?limit=200"
	taskURL := testServerURL + "/tasks/" + parent + "/ai/invocations?limit=100"
	status, body := doJSONStatus(t, http.MethodGet, workspaceURL, reader.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "task audience row is visible in workspace feed; body=%s", string(body))
	require.Contains(t, string(body), marker)
	status, body = doJSONStatus(t, http.MethodGet, taskURL, reader.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "task audience row is visible in task feed; body=%s", string(body))
	require.Contains(t, string(body), marker)
	require.NotContains(t, string(body), invalidMarker, "task feed must fail closed on invalid input provenance")
	require.NotContains(t, string(body), deletedMarker, "task feed must fail closed on deleted input provenance")
	status, body = doJSONStatus(t, http.MethodGet, workspaceURL, reader.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, string(body), invalidMarker, "workspace feed must fail closed on invalid input provenance")
	require.NotContains(t, string(body), deletedMarker, "workspace feed must fail closed on deleted input provenance")

	// Each hidden value is discoverable by a rightful reader once its
	// provenance becomes valid again. This keeps the negative assertions
	// grounded in data that would otherwise appear in the same feed.
	_, err = testDB.Exec(`INSERT INTO ai_invocations_inputs (invocation_id, task_public_id) VALUES (?, ?)`, uint32(invalidInvocationID), parentUUID[:]) //nolint:gosec // test-scoped invocation id fits uint32
	require.NoError(t, err)
	var ownerUserID uint32
	require.NoError(t, testDB.QueryRow(`SELECT id FROM users WHERE public_id = UUID_TO_BIN(?, 0)`, owner.UserPublicID).Scan(&ownerUserID))
	_, err = testDB.Exec(`UPDATE ai_invocations SET user_id = ?, audience = 'initiator', provenance_valid = TRUE WHERE id = ?`, ownerUserID, uint32(invalidInvocationID)) //nolint:gosec // test-scoped invocation id fits uint32
	require.NoError(t, err)
	_, err = testDB.Exec(`UPDATE tasks SET enabled = TRUE WHERE id = ?`, deletedResourceID)
	require.NoError(t, err)
	status, body = doJSONStatus(t, http.MethodGet, workspaceURL, owner.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "rightful owner feed: %s", string(body))
	require.Contains(t, string(body), invalidMarker, "valid provenance should make the formerly invalid marker discoverable to its initiator")
	require.Contains(t, string(body), deletedMarker, "restoring the source task should make the formerly dangling marker discoverable")

	setTaskVisibility(t, owner.WorkspacePublicID, parent, "private")
	status, body = doJSONStatus(t, http.MethodGet, workspaceURL, reader.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, string(body), marker, "task audience is revoked when the referenced task is no longer readable")
	status, body = doJSONStatus(t, http.MethodGet, taskURL, reader.AccessToken, nil)
	requireDenied(t, status, body, http.StatusNotFound, "WS.TASK.NOT_FOUND", "task feed must conceal the revoked task")
}

// A private task whose creator was deleted has neither a current creator
// nor an actor grant. SQL's three-valued logic must treat that missing grant
// as unreadable in both invocation feeds.
func TestAiInvocationAudienceFailsClosedForPrivateTaskWithoutCreator(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	reader := seedProjectRoleMember(t, owner, "viewer")
	parent := createTaskWithVisibility(t, owner, "AI orphan ACL parent "+uuid.NewString(), "public")
	orphan := createTaskWithVisibility(t, owner, "AI orphan ACL source "+uuid.NewString(), "private")
	workspaceID, parentID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, parent)
	_, orphanID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, orphan)
	_, err := testDB.Exec(`UPDATE tasks SET created_by_user_id = NULL WHERE id = ?`, orphanID)
	require.NoError(t, err)
	_, err = testDB.Exec(`DELETE FROM task_actors WHERE task_id = ?`, orphanID)
	require.NoError(t, err)

	// Confirm the fixture really has no private-task access path before the
	// invocation copies that task's identity into its provenance.
	status, body := doJSONStatus(t, http.MethodGet, testServerURL+"/tasks/"+orphan, reader.AccessToken, nil)
	requireDenied(t, status, body, http.StatusNotFound, "WS.TASK.NOT_FOUND", "the reader must not access the private task without a creator or actor grant")

	providerID := helpers.SeedAgent(t, testDB, owner.WorkspacePublicID, helpers.SeedAgentOptions{}).ProviderID
	marker := "AI_NULL_CREATOR_INVOCATION_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	invocationPublicID := uuid.Must(uuid.NewV7())
	orphanPublicID, err := uuid.Parse(orphan)
	require.NoError(t, err)
	result, err := testDB.Exec(`
		INSERT INTO ai_invocations (
			public_id, workspace_id, provider_id, user_id, task_id, audience,
			input_resources_json, input_count, provenance_valid,
			purpose, model, prompt_redacted, status, invoked_at
		) VALUES (?, ?, ?, NULL, ?, 'task', JSON_ARRAY(JSON_OBJECT('kind', 'task', 'publicId', ?)), 1, TRUE,
			'propose_priority', 'test-model', ?, 'ok', ?)`,
		invocationPublicID[:], workspaceID, providerID, parentID, orphan, marker, time.Now().UTC())
	require.NoError(t, err)
	invocationID, err := result.LastInsertId()
	require.NoError(t, err)
	_, err = testDB.Exec(`INSERT INTO ai_invocations_inputs (invocation_id, task_public_id) VALUES (?, ?)`, uint32(invocationID), orphanPublicID[:]) //nolint:gosec // test-scoped invocation id fits uint32
	require.NoError(t, err)

	workspaceURL := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/ai/invocations?limit=200"
	parentURL := testServerURL + "/tasks/" + parent + "/ai/invocations?limit=100"
	for _, url := range []string{workspaceURL, parentURL} {
		status, body = doJSONStatus(t, http.MethodGet, url, reader.AccessToken, nil)
		require.Equal(t, http.StatusOK, status, "feed %s: %s", url, string(body))
		require.NotContains(t, string(body), marker, "a dangling NULL-creator ACL must not expose private input provenance through %s", url)
	}
	attachTaskUserActor(t, owner.WorkspacePublicID, orphan, owner.UserPublicID, "watcher")
	status, body = doJSONStatus(t, http.MethodGet, workspaceURL, owner.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "rightful task actor can read the invocation once granted access; body=%s", string(body))
	require.Contains(t, string(body), marker, "the private marker must be discoverable by a reader with an explicit current grant")
}

// TestAiInvocationMixedStepProvenanceUsesPublicReferences verifies the
// persisted provenance shape after the schema/query generator has run.
func TestAiInvocationMixedStepProvenanceUsesPublicReferences(t *testing.T) {
	bootstrap(t)
	if aiMockEnabled() {
		t.Skip("NF_FLOW_AI_MOCK is set; this test needs a per-workspace fake provider")
	}
	t.Parallel()

	owner := newTenant(t)
	caller := seedProjectRoleMember(t, owner, "editor")
	parent := createTaskWithVisibility(t, owner, "AI provenance public parent "+uuid.NewString(), "public")
	child := createTaskWithVisibility(t, owner, "AI provenance private child "+uuid.NewString(), "private")
	attachChildTask(t, owner.WorkspacePublicID, parent, child)
	attachTaskUserActor(t, owner.WorkspacePublicID, child, caller.UserPublicID, "watcher")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"[{\"title\":\"Step 1\",\"description\":\"Verify evidence\",\"priority\":\"low\"},{\"title\":\"Step 2\",\"description\":\"Verify evidence\",\"priority\":\"low\"},{\"title\":\"Step 3\",\"description\":\"Verify evidence\",\"priority\":\"low\"}]"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	t.Cleanup(upstream.Close)
	registerOpenAICompatProvider(t, owner, upstream.URL)
	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/tasks/"+parent+"/propose-steps",
		caller.AccessToken, map[string]any{"granularity": "coarse"})
	require.Equal(t, http.StatusOK, status, "fake provider call; body=%s", string(body))

	_, parentInternalID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, parent)
	rows, err := testDB.Query(`
		SELECT audience, CAST(input_resources_json AS CHAR), input_count, provenance_valid,
			(SELECT COUNT(*) FROM ai_invocations_inputs i WHERE i.invocation_id = ai_invocations.id)
		FROM ai_invocations
		WHERE workspace_id = ? AND task_id = ? AND purpose = 'propose_steps'
		ORDER BY id DESC LIMIT 1`,
		mustWorkspaceInternalID(t, owner.WorkspacePublicID), parentInternalID)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next(), "the provider call must have an invocation row")
	var audience, resourcesJSON string
	var inputCount, normalizedCount int64
	var provenanceValid bool
	require.NoError(t, rows.Scan(&audience, &resourcesJSON, &inputCount, &provenanceValid, &normalizedCount))
	require.Equal(t, "initiator", audience, "mixed-context ProposeSteps must default to its initiating user")
	require.True(t, provenanceValid, "all task references from the provider input should be validated")
	require.EqualValues(t, 2, inputCount, "the parent and child references should be represented")
	require.Equal(t, inputCount, normalizedCount, "normalized references must match the metadata count")
	var resources []struct {
		Kind     string `json:"kind"`
		PublicID string `json:"publicId"`
	}
	require.NoError(t, json.Unmarshal([]byte(resourcesJSON), &resources))
	resourceIDs := make(map[string]bool, len(resources))
	for _, resource := range resources {
		require.Equal(t, "task", resource.Kind)
		require.NotEmpty(t, resource.PublicID)
		resourceIDs[resource.PublicID] = true
	}
	require.True(t, resourceIDs[parent], "parent public UUID is provenance")
	require.True(t, resourceIDs[child], "private child public UUID is provenance")
	_, childInternalID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, child)
	require.False(t, resourceIDs[fmt.Sprint(parentInternalID)], "internal parent id must not be persisted as provenance")
	require.False(t, resourceIDs[fmt.Sprint(childInternalID)], "internal child id must not be persisted as provenance")
}

// TestMCPProposePriorityInvocationRetainsDeletedTaskProvenance verifies that
// a single-task MCP prompt keeps public provenance after its subject FK is
// cleared by physical deletion. Otherwise the row becomes task-less and an
// initiator-only check can no longer revalidate the task content it stores.
func TestMCPProposePriorityInvocationRetainsDeletedTaskProvenance(t *testing.T) {
	bootstrap(t)
	if aiMockEnabled() {
		t.Skip("NF_FLOW_AI_MOCK is set; this test needs a per-workspace fake provider")
	}
	t.Parallel()

	owner := newTenant(t)
	caller := seedProjectRoleMember(t, owner, "editor")
	taskMarker := "AI_MCP_PRIORITY_TASK_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	taskPublicID := createTaskWithVisibility(t, owner, taskMarker, "public")
	workspaceID, taskID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, taskPublicID)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"priority\":\"high\",\"reason\":\"test\"}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":8}}`))
	}))
	t.Cleanup(upstream.Close)
	registerOpenAICompatProvider(t, owner, upstream.URL)

	var tokenResp struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/me/mcp-tokens",
		caller.AccessToken,
		map[string]any{"name": "priority provenance", "scopes": []string{"read:workspace", "write:workspace"}},
		&tokenResp)
	require.NotEmpty(t, tokenResp.Token)
	callBody := mcpCall(t, tokenResp.Token, "tools/call", map[string]any{
		"name":      "propose_priority",
		"arguments": map[string]any{"taskId": taskPublicID},
	})
	priorityResult := mcpToolTextJSON[struct {
		Priority int32 `json:"priority"`
	}](t, callBody)
	require.EqualValues(t, 3, priorityResult.Priority, "the real MCP tool path should reach the fake provider")

	var invocationID uint32
	var taskFK, actorFK sql.NullInt32
	var audience, resourcesJSON string
	var inputCount uint32
	var provenanceValid bool
	require.NoError(t, testDB.QueryRow(`
		SELECT id, task_id, user_id, audience, CAST(input_resources_json AS CHAR), input_count, provenance_valid
		FROM ai_invocations
		WHERE workspace_id = ? AND task_id = ? AND purpose = 'propose_priority'
		ORDER BY id DESC LIMIT 1`, workspaceID, taskID).Scan(
		&invocationID, &taskFK, &actorFK, &audience, &resourcesJSON, &inputCount, &provenanceValid))
	require.True(t, taskFK.Valid, "the invocation starts with a subject task FK")
	require.True(t, actorFK.Valid, "the MCP request must be attributed to its initiating user")
	require.Equal(t, "initiator", audience, "adding provenance must not widen task invocation audience")
	require.True(t, provenanceValid)
	require.Equal(t, uint32(1), inputCount)
	var resources []struct {
		Kind     string `json:"kind"`
		PublicID string `json:"publicId"`
	}
	require.NoError(t, json.Unmarshal([]byte(resourcesJSON), &resources))
	require.Equal(t, []struct {
		Kind     string `json:"kind"`
		PublicID string `json:"publicId"`
	}{{Kind: "task", PublicID: taskPublicID}}, resources)
	var normalizedCount int64
	require.NoError(t, testDB.QueryRow(`SELECT COUNT(*) FROM ai_invocations_inputs WHERE invocation_id = ?`, invocationID).Scan(&normalizedCount))
	require.EqualValues(t, 1, normalizedCount)

	workspaceURL := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/ai/invocations?limit=200"
	status, body := doJSONStatus(t, http.MethodGet, workspaceURL, caller.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "initiator can inspect the current task invocation; body=%s", string(body))
	require.Contains(t, string(body), taskMarker)

	_, err := testDB.Exec(`DELETE FROM tasks WHERE id = ?`, taskID)
	require.NoError(t, err, "physical task deletion should clear the subject FK but retain normalized provenance")
	var subjectCount int64
	require.NoError(t, testDB.QueryRow(`SELECT COUNT(*) FROM ai_invocations WHERE id = ? AND task_id IS NOT NULL`, invocationID).Scan(&subjectCount))
	require.Zero(t, subjectCount)
	require.NoError(t, testDB.QueryRow(`SELECT COUNT(*) FROM ai_invocations_inputs WHERE invocation_id = ?`, invocationID).Scan(&normalizedCount))
	require.EqualValues(t, 1, normalizedCount,
		"the normalized public task reference must survive task deletion")
	status, body = doJSONStatus(t, http.MethodGet, workspaceURL, caller.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "workspace invocation feed; body=%s", string(body))
	require.NotContains(t, string(body), taskMarker, "the orphaned input reference must fail closed after SET NULL")
}

func aiMockEnabled() bool {
	switch os.Getenv("NF_FLOW_AI_MOCK") {
	case "", "0", "false":
		return false
	default:
		return true
	}
}

func attachChildTask(t *testing.T, workspacePublicID, parentPublicID, childPublicID string) {
	t.Helper()
	workspaceID, parentID := helpers.AgentTaskInternalIDs(t, testDB, workspacePublicID, parentPublicID)
	childWorkspaceID, childID := helpers.AgentTaskInternalIDs(t, testDB, workspacePublicID, childPublicID)
	require.Equal(t, workspaceID, childWorkspaceID)
	_, err := testDB.Exec(`UPDATE tasks SET parent_task_id = ? WHERE id = ?`, parentID, childID)
	require.NoError(t, err)
}

func attachTaskUserActor(t *testing.T, workspacePublicID, taskPublicID, userPublicID, role string) {
	t.Helper()
	workspaceID, taskID := helpers.AgentTaskInternalIDs(t, testDB, workspacePublicID, taskPublicID)
	var userID uint32
	require.NoError(t, testDB.QueryRow(
		`SELECT id FROM users WHERE public_id = UUID_TO_BIN(?, 0)`, userPublicID,
	).Scan(&userID))
	actorID := uuid.Must(uuid.NewV7())
	_, err := testDB.Exec(`
		INSERT INTO task_actors (public_id, workspace_id, task_id, user_id, kind, role, enabled)
		VALUES (?, ?, ?, ?, 'user', ?, TRUE)`, actorID[:], workspaceID, taskID, userID, role)
	require.NoError(t, err)
}

func disableTaskUserActor(t *testing.T, workspacePublicID, taskPublicID, userPublicID, role string) {
	t.Helper()
	_, taskID := helpers.AgentTaskInternalIDs(t, testDB, workspacePublicID, taskPublicID)
	var userID uint32
	require.NoError(t, testDB.QueryRow(
		`SELECT id FROM users WHERE public_id = UUID_TO_BIN(?, 0)`, userPublicID,
	).Scan(&userID))
	result, err := testDB.Exec(`UPDATE task_actors SET enabled = FALSE WHERE task_id = ? AND user_id = ? AND role = ?`, taskID, userID, role)
	require.NoError(t, err)
	affected, err := result.RowsAffected()
	require.NoError(t, err)
	require.EqualValues(t, 1, affected)
}

func setTaskVisibility(t *testing.T, workspacePublicID, taskPublicID, visibility string) {
	t.Helper()
	_, taskID := helpers.AgentTaskInternalIDs(t, testDB, workspacePublicID, taskPublicID)
	result, err := testDB.Exec(`UPDATE tasks SET visibility = ? WHERE id = ?`, visibility, taskID)
	require.NoError(t, err)
	affected, err := result.RowsAffected()
	require.NoError(t, err)
	require.EqualValues(t, 1, affected)
}

func mustWorkspaceInternalID(t *testing.T, workspacePublicID string) uint32 {
	t.Helper()
	var workspaceID uint32
	require.NoError(t, testDB.QueryRow(
		`SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0)`, workspacePublicID,
	).Scan(&workspaceID))
	return workspaceID
}
