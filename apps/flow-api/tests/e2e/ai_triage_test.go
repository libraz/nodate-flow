package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// seedTriageFixtureItems inserts an inbox item for every id the mock's
// triage fixture names. The suggestion list only replays suggestions about
// items that exist in the workspace, so the canned ids must be real ones.
// An item the fixture recommends opening is linked to a task the actor can
// read, because the triage contract refuses "open" for any other item.
func seedTriageFixtureItems(t *testing.T, tt *helpers.TestTenant) {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/ai/inbox_triage.json")
	require.NoError(t, err)
	var fixture []struct {
		InboxItemID       string `json:"inboxItemId"`
		RecommendedAction string `json:"recommendedAction"`
	}
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.NotEmpty(t, fixture)

	wsID := helpers.ResolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)
	for _, f := range fixture {
		id := uuid.MustParse(f.InboxItemID)
		var taskID any
		subjectType := "workspace"
		if f.RecommendedAction == "open" {
			taskPublicID := createTaskWithVisibility(t, tt, "Triage open target", "public")
			_, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskPublicID)
			taskID = taskInternalID
			subjectType = "task"
		}
		_, err := testDB.Exec(
			`INSERT INTO signals (public_id, workspace_id, task_id, source, kind, payload_json, received_at, subject_type, subject_id)
			 VALUES (?, ?, ?, 'manual', 'manual', JSON_OBJECT(), NOW(3), ?, ?)`,
			id[:], wsID, taskID, subjectType, taskID)
		require.NoError(t, err)
	}
}

// TestAiTriageSuggestionLifecycle exercises the AI suggestion lifecycle
// end to end against the mock AI provider: triage proposes 3 fixture
// suggestions, GET /ai/suggestions returns 3 pending, executing one
// drops the pending count to 2, and dismissing another drops it to 1.
//
// Requires NF_FLOW_AI_MOCK=1 because the test server only wires the mock AI
// orchestrator when AiMock is true at router.Build time.
func TestAiTriageSuggestionLifecycle(t *testing.T) {
	bootstrap(t)
	if os.Getenv("NF_FLOW_AI_MOCK") == "" {
		t.Skip("set NF_FLOW_AI_MOCK=1 to run AI triage e2e tests")
	}
	t.Parallel()

	tt := newTenant(t)
	seedTriageFixtureItems(t, tt)

	// Triage. The mock provider ignores the prompt and returns the
	// canned 3-item fixture from apps/flow-api/testdata/ai/inbox_triage.json.
	var triage struct {
		Suggestions []struct {
			InboxItemID       string  `json:"inboxItemId"`
			Score             float32 `json:"score"`
			RecommendedAction string  `json:"recommendedAction"`
		} `json:"suggestions"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/inbox/triage",
		tt.AccessToken, map[string]any{"limit": 10}, &triage)
	require.Len(t, triage.Suggestions, 3, "mock fixture must yield 3 suggestions")

	// List pending suggestions.
	var list struct {
		Suggestions []commandSuggestion `json:"suggestions"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions",
		tt.AccessToken, nil, &list)
	require.Len(t, list.Suggestions, 3, "all 3 proposed suggestions should be pending")

	// Execute the archive recommendation.
	target := suggestionForAction(t, list.Suggestions, "archive")
	status, body := executeSuggestion(t, tt, target, "triage-archive-"+randomHex(8), "archive")
	require.Equalf(t, http.StatusOK, status, "execute body=%s", string(body))

	// List again — pending count should now be 2.
	var list2 struct {
		Suggestions []commandSuggestion `json:"suggestions"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions",
		tt.AccessToken, nil, &list2)
	require.Len(t, list2.Suggestions, 2, "executed suggestion must drop out of pending")
	for _, s := range list2.Suggestions {
		require.NotEqual(t, target.InboxItemID, s.InboxItemID, "executed suggestion must not reappear")
	}

	// Dismiss another and verify it drops to 1.
	dismissTarget := suggestionForAction(t, list2.Suggestions, "snooze")
	status, body = doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions/"+dismissTarget.InboxItemID+"/decision",
		tt.AccessToken, map[string]any{
			"proposalId":       dismissTarget.ProposalID,
			"expectedRevision": dismissTarget.Revision,
			"state":            "dismissed",
		})
	require.Equalf(t, http.StatusOK, status, "dismiss body=%s", string(body))

	var list3 struct {
		Suggestions []commandSuggestion `json:"suggestions"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions",
		tt.AccessToken, nil, &list3)
	require.Len(t, list3.Suggestions, 1, "dismissed suggestion must drop out of pending")
}
