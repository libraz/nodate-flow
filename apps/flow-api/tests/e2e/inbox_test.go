package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestInboxLifecycle verifies that a manually-injected signal surfaces
// in /inbox and can be archived and snoozed.
func TestInboxLifecycle(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Inject a signal without a task binding — the inbox view should
	// still surface it as a workspace-scoped item.
	var signal struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/signals", tt.AccessToken, map[string]any{
		"workspaceId": tt.WorkspacePublicID,
		"source":      "manual",
		"kind":        "manual",
	}, &signal)
	require.NotEmpty(t, signal.ID)

	// List the inbox and find our signal.
	var list struct {
		Total int64 `json:"total"`
		Items []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"items"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/inbox?workspaceId="+tt.WorkspacePublicID,
		tt.AccessToken, nil, &list)
	require.GreaterOrEqual(t, list.Total, int64(1))
	foundID := ""
	for _, it := range list.Items {
		if it.ID == signal.ID {
			foundID = it.ID
			break
		}
	}
	require.Equal(t, signal.ID, foundID, "injected signal must appear in inbox")
	var crossWorkspaceBefore struct {
		Total int64 `json:"total"`
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/inbox", tt.AccessToken, nil, &crossWorkspaceBefore)
	require.Contains(t, inboxItemIDs(crossWorkspaceBefore.Items), signal.ID)

	// Snooze it until an arbitrary future time.
	snoozeUntil := time.Now().Add(1 * time.Hour).Unix()
	status, _ := doJSONStatus(t, http.MethodPost,
		testServerURL+"/inbox/"+signal.ID+"/snooze?workspaceId="+tt.WorkspacePublicID,
		tt.AccessToken, map[string]any{"snoozeUntil": snoozeUntil})
	require.Equal(t, http.StatusOK, status)

	var workspaceSnoozed struct {
		Total int64 `json:"total"`
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/inbox?workspaceId="+tt.WorkspacePublicID,
		tt.AccessToken, nil, &workspaceSnoozed)
	require.Equal(t, list.Total-1, workspaceSnoozed.Total)
	require.NotContains(t, inboxItemIDs(workspaceSnoozed.Items), signal.ID,
		"a snoozed item must stay out of the workspace Inbox after a fresh read")

	var crossWorkspaceSnoozed struct {
		Total int64 `json:"total"`
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/inbox", tt.AccessToken, nil, &crossWorkspaceSnoozed)
	require.Equal(t, crossWorkspaceBefore.Total-1, crossWorkspaceSnoozed.Total)
	require.NotContains(t, inboxItemIDs(crossWorkspaceSnoozed.Items), signal.ID,
		"a snoozed item must stay out of the cross-workspace Inbox after a fresh read")

	// The snooze deadline is stored in received_at. Move that eligibility
	// value into the past directly so resurfacing is tested without a sleep.
	_, err := testDB.Exec(
		`UPDATE signals
		    SET received_at = DATE_SUB(UTC_TIMESTAMP(3), INTERVAL 1 SECOND)
		  WHERE public_id = UUID_TO_BIN(?, 0)`, signal.ID,
	)
	require.NoError(t, err)
	var resurfaced struct {
		Total int64 `json:"total"`
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/inbox?workspaceId="+tt.WorkspacePublicID,
		tt.AccessToken, nil, &resurfaced)
	require.Equal(t, list.Total, resurfaced.Total)
	require.Contains(t, inboxItemIDs(resurfaced.Items), signal.ID,
		"a due snooze must resurface in Inbox")
	var crossWorkspaceResurfaced struct {
		Total int64 `json:"total"`
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/inbox", tt.AccessToken, nil, &crossWorkspaceResurfaced)
	require.Equal(t, crossWorkspaceBefore.Total, crossWorkspaceResurfaced.Total)
	require.Contains(t, inboxItemIDs(crossWorkspaceResurfaced.Items), signal.ID,
		"a due snooze must resurface in the cross-workspace Inbox")

	// Archive it.
	status, _ = doJSONStatus(t, http.MethodPost,
		testServerURL+"/inbox/"+signal.ID+"/archive?workspaceId="+tt.WorkspacePublicID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)
}

func inboxItemIDs(items []struct {
	ID string `json:"id"`
}) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}
