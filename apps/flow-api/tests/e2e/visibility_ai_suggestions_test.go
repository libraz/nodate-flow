package e2e

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// seedSuggestionOnTask links a fresh inbox item to a task and records a
// pending AI suggestion about it whose reasoning is marker. It returns
// the inbox item's public id.
func seedSuggestionOnTask(t *testing.T, owner *helpers.TestTenant, taskID, marker string) string {
	t.Helper()
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, taskID)
	item := uuid.Must(uuid.NewV7())
	_, err := testDB.Exec(
		`INSERT INTO signals (public_id, workspace_id, task_id, source, kind, payload_json, received_at)
		 VALUES (?, ?, ?, 'manual', 'vis.probe', JSON_OBJECT(), NOW(3))`,
		item[:], wsID, taskInternalID)
	require.NoError(t, err)
	evt := uuid.Must(uuid.NewV7())
	_, err = testDB.Exec(
		`INSERT INTO events (public_id, workspace_id, type, payload_json, occurred_at)
		 VALUES (?, ?, 'ai.suggestion.proposed',
		         JSON_OBJECT('inbox_item_id', ?, 'score', 0.5, 'action', 'open', 'reasoning', ?), NOW(3))`,
		evt[:], wsID, item.String(), marker)
	require.NoError(t, err)
	return item.String()
}

// TestAiSuggestionsFollowInboxTaskVisibility holds the persisted triage
// suggestions to the task the inbox item points at: the reasoning is the
// model talking about that task, so a guest who cannot read it must not
// get the suggestion, while one about a readable task is still listed.
func TestAiSuggestionsFollowInboxTaskVisibility(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	guest := seedGuestMember(t, owner)

	suffix := uuid.NewString()
	privateMarker := "vis-sugg private " + suffix
	publicMarker := "vis-sugg public " + suffix
	seedSuggestionOnTask(t, owner, createTaskWithVisibility(t, owner, "Vis sugg private", "private"), privateMarker)
	seedSuggestionOnTask(t, owner, createTaskWithVisibility(t, owner, "Vis sugg public", "public"), publicMarker)

	list := func(token string) string {
		status, body := doJSONStatus(t, http.MethodGet,
			testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/ai/suggestions", token, nil)
		require.Equal(t, http.StatusOK, status, "suggestion list must render; body=%s", string(body))
		return string(body)
	}

	got := list(guest.AccessToken)
	assert.NotContains(t, got, privateMarker, "a suggestion about a private task must not reach a guest")
	assert.Contains(t, got, publicMarker, "a suggestion about a readable task must still be listed")
	assert.Contains(t, list(owner.AccessToken), privateMarker, "the workspace owner reads every suggestion")
}

// TestAiSuggestionReactionsRequireAnInboxItem holds apply and dismiss to
// an inbox item that exists in the workspace: an unknown id, or text that
// is not an id at all, used to be recorded as a reaction to nothing.
func TestAiSuggestionReactionsRequireAnInboxItem(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	item := seedSuggestionOnTask(t, owner,
		createTaskWithVisibility(t, owner, "Vis sugg react", "public"), "vis-sugg react "+uuid.NewString())

	base := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/ai/suggestions/"
	for _, verb := range []string{"apply", "dismiss"} {
		for _, id := range []string{uuid.NewString(), "not-an-inbox-item"} {
			status, body := doJSONStatus(t, http.MethodPost, base+id+"/"+verb, owner.AccessToken, nil)
			assert.Equal(t, http.StatusNotFound, status,
				"%s on unknown inbox item %q must be refused; body=%s", verb, id, string(body))
		}
	}

	status, body := doJSONStatus(t, http.MethodPost, base+item+"/dismiss", owner.AccessToken, nil)
	assert.Equal(t, http.StatusNoContent, status,
		"dismissing a real inbox item must still succeed; body=%s", string(body))
}
