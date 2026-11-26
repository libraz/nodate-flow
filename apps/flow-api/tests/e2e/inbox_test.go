package e2e

import (
	"context"
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

// TestInboxExcludesSchedulerTicks verifies that a calendar-source signal,
// which only the judge consumes, stays out of both inbox feeds and their
// totals while an ordinary signal in the same workspace still lists.
func TestInboxExcludesSchedulerTicks(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var manual struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/signals", tt.AccessToken, map[string]any{
		"workspaceId": tt.WorkspacePublicID,
		"source":      "manual",
		"kind":        "manual",
	}, &manual)
	require.NotEmpty(t, manual.ID)

	type feed struct {
		Total int64 `json:"total"`
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	workspaceURL := testServerURL + "/inbox?workspaceId=" + tt.WorkspacePublicID
	var workspaceBefore, crossBefore feed
	doJSON(t, http.MethodGet, workspaceURL, tt.AccessToken, nil, &workspaceBefore)
	doJSON(t, http.MethodGet, testServerURL+"/inbox", tt.AccessToken, nil, &crossBefore)

	// The worker posts ticks with a service token; the row is what matters here.
	ctx := context.Background()
	res, err := testDB.ExecContext(ctx, `
		INSERT INTO signals (public_id, workspace_id, source, kind, payload_json, received_at, subject_type)
		VALUES (UUID_TO_BIN(UUID(), 0), ?, 'calendar', 'calendar.event_day_arrived', JSON_OBJECT(), NOW(3), 'workspace')`,
		internalWorkspaceID(t, testDB, tt.WorkspacePublicID))
	require.NoError(t, err)
	tickInternalID, err := res.LastInsertId()
	require.NoError(t, err)
	var tickID string
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT BIN_TO_UUID(public_id, 0) FROM signals WHERE id = ?`, tickInternalID).Scan(&tickID))

	var workspaceAfter, crossAfter feed
	doJSON(t, http.MethodGet, workspaceURL, tt.AccessToken, nil, &workspaceAfter)
	doJSON(t, http.MethodGet, testServerURL+"/inbox", tt.AccessToken, nil, &crossAfter)

	require.Contains(t, inboxItemIDs(workspaceAfter.Items), manual.ID)
	require.NotContains(t, inboxItemIDs(workspaceAfter.Items), tickID,
		"a scheduler tick must not list in the workspace Inbox")
	require.NotContains(t, inboxItemIDs(crossAfter.Items), tickID,
		"a scheduler tick must not list in the cross-workspace Inbox")
	require.Equal(t, workspaceBefore.Total, workspaceAfter.Total)
	require.Equal(t, crossBefore.Total, crossAfter.Total)
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
