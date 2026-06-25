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

	// Snooze it until an arbitrary future time.
	snoozeUntil := time.Now().Add(1 * time.Hour).Unix()
	status, _ := doJSONStatus(t, http.MethodPost,
		testServerURL+"/inbox/"+signal.ID+"/snooze?workspaceId="+tt.WorkspacePublicID,
		tt.AccessToken, map[string]any{"snoozeUntil": snoozeUntil})
	require.Equal(t, http.StatusOK, status)

	// Archive it.
	status, _ = doJSONStatus(t, http.MethodPost,
		testServerURL+"/inbox/"+signal.ID+"/archive?workspaceId="+tt.WorkspacePublicID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)
}
