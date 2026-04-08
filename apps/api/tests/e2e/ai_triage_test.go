package e2e

import (
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAiTriageSuggestionLifecycle exercises the AI suggestion lifecycle
// end to end against the mock AI provider: triage proposes 3 fixture
// suggestions, GET /ai/suggestions returns 3 pending, applying one
// drops the pending count to 2.
//
// Requires NF_AI_MOCK=1 because the test server only wires the mock AI
// orchestrator when AiMock is true at router.Build time.
func TestAiTriageSuggestionLifecycle(t *testing.T) {
	bootstrap(t)
	if os.Getenv("NF_AI_MOCK") == "" {
		t.Skip("set NF_AI_MOCK=1 to run AI triage e2e tests")
	}
	t.Parallel()

	tt := newTenant(t)

	// Triage. The mock provider ignores the prompt and returns the
	// canned 3-item fixture from apps/api/testdata/ai/inbox_triage.json.
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
		Suggestions []struct {
			EventID           string `json:"eventId"`
			InboxItemID       string `json:"inboxItemId"`
			RecommendedAction string `json:"recommendedAction"`
		} `json:"suggestions"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions",
		tt.AccessToken, nil, &list)
	require.Len(t, list.Suggestions, 3, "all 3 proposed suggestions should be pending")

	// Apply one — pick the first.
	target := list.Suggestions[0].InboxItemID
	require.NotEmpty(t, target)
	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions/"+target+"/apply",
		tt.AccessToken, nil)
	require.Equalf(t, http.StatusNoContent, status, "apply body=%s", string(body))

	// List again — pending count should now be 2.
	var list2 struct {
		Suggestions []struct {
			InboxItemID string `json:"inboxItemId"`
		} `json:"suggestions"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions",
		tt.AccessToken, nil, &list2)
	require.Len(t, list2.Suggestions, 2, "applied suggestion must drop out of pending")
	for _, s := range list2.Suggestions {
		require.NotEqual(t, target, s.InboxItemID, "applied suggestion must not reappear")
	}

	// Dismiss another and verify it drops to 1.
	dismissTarget := list2.Suggestions[0].InboxItemID
	status, body = doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions/"+dismissTarget+"/dismiss",
		tt.AccessToken, nil)
	require.Equalf(t, http.StatusNoContent, status, "dismiss body=%s", string(body))

	var list3 struct {
		Suggestions []struct {
			InboxItemID string `json:"inboxItemId"`
		} `json:"suggestions"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions",
		tt.AccessToken, nil, &list3)
	require.Len(t, list3.Suggestions, 1, "dismissed suggestion must drop out of pending")
}
