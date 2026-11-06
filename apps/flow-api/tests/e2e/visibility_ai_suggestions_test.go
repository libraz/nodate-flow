package e2e

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// seedSuggestionOnTask records a proposal in a complete actor-owned batch
// containing the linked task. The batch is visible only to actor while its
// captured task inputs remain readable.
func seedSuggestionOnTask(t *testing.T, owner, actor *helpers.TestTenant, taskID, marker string) string {
	t.Helper()
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, taskID)
	actorID := helpers.ResolveUserInternalID(t, testDB, actor.UserPublicID)
	var taskPublicID string
	require.NoError(t, testDB.QueryRow(
		`SELECT BIN_TO_UUID(public_id, 0) FROM tasks WHERE workspace_id = ? AND id = ?`, wsID, taskInternalID,
	).Scan(&taskPublicID))
	batchPublicID := uuid.Must(uuid.NewV7())
	batchResult, err := testDB.Exec(`
INSERT INTO ai_suggestion_batches (public_id, workspace_id, user_id, input_count, provenance_valid)
VALUES (?, ?, ?, 1, TRUE)`, batchPublicID[:], wsID, actorID)
	require.NoError(t, err)
	batchID, err := batchResult.LastInsertId()
	require.NoError(t, err)
	_, err = testDB.Exec(`
INSERT INTO ai_suggestion_batches_inputs (batch_id, task_public_id)
VALUES (?, UUID_TO_BIN(?, 0))`, batchID, taskPublicID)
	require.NoError(t, err)
	item := uuid.Must(uuid.NewV7())
	_, err = testDB.Exec(
		`INSERT INTO signals (public_id, workspace_id, task_id, source, kind, payload_json, received_at)
		 VALUES (?, ?, ?, 'manual', 'vis.probe', JSON_OBJECT(), NOW(3))`,
		item[:], wsID, taskInternalID)
	require.NoError(t, err)
	evt := uuid.Must(uuid.NewV7())
	_, err = testDB.Exec(
		`INSERT INTO events (public_id, workspace_id, actor_user_id, type, payload_json, occurred_at)
		 VALUES (?, ?, ?, 'ai.suggestion.proposed',
		         JSON_OBJECT('inbox_item_id', ?, 'score', 0.5, 'action', 'open', 'reasoning', ?, 'suggestion_batch_id', ?), NOW(3))`,
		evt[:], wsID, actorID, item.String(), marker, batchPublicID.String())
	require.NoError(t, err)
	return item.String()
}

func seedLegacySuggestionOnTask(t *testing.T, owner, actor *helpers.TestTenant, taskID, marker string) {
	t.Helper()
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, taskID)
	actorID := helpers.ResolveUserInternalID(t, testDB, actor.UserPublicID)
	item := uuid.Must(uuid.NewV7())
	_, err := testDB.Exec(
		`INSERT INTO signals (public_id, workspace_id, task_id, source, kind, payload_json, received_at)
		 VALUES (?, ?, ?, 'manual', 'vis.legacy', JSON_OBJECT(), NOW(3))`,
		item[:], wsID, taskInternalID)
	require.NoError(t, err)
	evt := uuid.Must(uuid.NewV7())
	_, err = testDB.Exec(
		`INSERT INTO events (public_id, workspace_id, actor_user_id, type, payload_json, occurred_at)
		 VALUES (?, ?, ?, 'ai.suggestion.proposed',
		         JSON_OBJECT('inbox_item_id', ?, 'score', 0.5, 'action', 'open', 'reasoning', ?), NOW(3))`,
		evt[:], wsID, actorID, item.String(), marker)
	require.NoError(t, err)
}

// TestAiSuggestionsFollowInboxTaskVisibility holds the persisted triage
// suggestions to the task the inbox item points at: the reasoning is the
// model talking about that task, so a guest who cannot read it must not
// get the suggestion, while one about a readable task is still listed.
func TestAiSuggestionsFollowInboxTaskVisibility(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	initiator := seedProjectRoleMember(t, owner, "editor")
	guest := seedGuestMember(t, owner)

	suffix := uuid.NewString()
	privateMarker := "vis-sugg private " + suffix
	publicMarker := "vis-sugg public " + suffix
	projectMarker := "vis-sugg project " + suffix
	legacyMarker := "vis-sugg legacy " + suffix
	privateTask := createTaskWithVisibility(t, owner, "Vis sugg private", "private")
	attachTaskUserActor(t, owner.WorkspacePublicID, privateTask, initiator.UserPublicID, "watcher")
	publicTask := createTaskWithVisibility(t, owner, "Vis sugg public", "public")
	projectTask := createTaskWithVisibility(t, owner, "Vis sugg project", "project")
	seedSuggestionOnTask(t, owner, initiator, privateTask, privateMarker)
	seedSuggestionOnTask(t, owner, initiator, publicTask, publicMarker)
	seedSuggestionOnTask(t, owner, initiator, projectTask, projectMarker)
	seedLegacySuggestionOnTask(t, owner, initiator, publicTask, legacyMarker)

	list := func(actor *helpers.TestTenant) string {
		status, body := doJSONStatus(t, http.MethodGet,
			testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/ai/suggestions", actor.AccessToken, nil)
		require.Equal(t, http.StatusOK, status, "suggestion list must render; body=%s", string(body))
		return string(body)
	}

	initiatorList := list(initiator)
	assert.Contains(t, initiatorList, privateMarker, "the initiating task actor reads its own private-task proposal")
	assert.Contains(t, initiatorList, publicMarker, "the initiator reads its public-task proposal")
	assert.Contains(t, initiatorList, projectMarker, "the project member reads its project-task proposal")
	assert.NotContains(t, initiatorList, legacyMarker, "batchless legacy proposals stay hidden until retriage")

	assert.NotContains(t, list(guest), privateMarker, "a different actor cannot read the private-task batch")
	assert.NotContains(t, list(guest), publicMarker, "a different actor cannot read even a public-task batch")
	assert.NotContains(t, list(guest), projectMarker, "a different actor cannot read the project-task batch")
	assert.NotContains(t, list(owner), privateMarker, "workspace elevation cannot bypass the proposal actor boundary")
	assert.NotContains(t, list(owner), publicMarker, "workspace elevation cannot bypass the proposal actor boundary")
	assert.NotContains(t, list(owner), projectMarker, "workspace elevation cannot bypass the proposal actor boundary")
	assert.NotContains(t, list(owner), legacyMarker, "batchless legacy proposals stay hidden from elevated readers")

	_, privateTaskInternalID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, privateTask)
	_, err := testDB.Exec(`UPDATE tasks SET created_by_user_id = NULL WHERE id = ?`, privateTaskInternalID)
	require.NoError(t, err)
	disableTaskUserActor(t, owner.WorkspacePublicID, privateTask, initiator.UserPublicID, "watcher")
	afterRevocation := list(initiator)
	assert.NotContains(t, afterRevocation, privateMarker, "the initiating actor loses a proposal after losing a captured task")
	assert.Contains(t, afterRevocation, publicMarker, "public input remains readable after private access is revoked")
	assert.Contains(t, afterRevocation, projectMarker, "project input remains readable after private access is revoked")
}
