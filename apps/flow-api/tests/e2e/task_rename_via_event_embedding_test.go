package e2e

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A task and the calendar event that projects it share one title, so a
// rename entered on the calendar side lands in tasks.title. Search is
// served from the stored embedding, and the embedding is composed from
// the title and the description together — so a rename that commits
// without refreshing it leaves the task retrievable only under the text
// that is no longer on screen.
//
// The mock embedder derives its vector from a hash of that composed
// text, so two tasks carrying the same title and body have identical
// vectors and cosine 1.0, while unrelated text falls far below the
// duplicate threshold. That makes retrieval-by-text directly assertable:
// a probe task carrying the new title is a duplicate of the renamed task
// exactly when the renamed task's stored embedding holds the new text.
const (
	renameProbeBody = "Ship the reconciliation report to the finance team every Monday."
	renameOldTitle  = "Quarterly revenue rollup"
	renameNewTitle  = "Warehouse humidity sensor sweep"
)

// duplicateScoreFor returns the score GET /tasks/{id}/duplicates gives
// one candidate, and whether the candidate ranked at all. A candidate
// below the workspace's low threshold is not returned, which is itself
// the answer to "does the source's stored text match this one".
func duplicateScoreFor(t *testing.T, token, sourceID, candidateID string) (float64, bool) {
	t.Helper()
	var out struct {
		Source     string `json:"source"`
		Candidates []struct {
			TaskID string  `json:"taskId"`
			Score  float64 `json:"score"`
		} `json:"candidates"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks/"+sourceID+"/duplicates", token, nil, &out)
	require.Equal(t, sourceID, out.Source)
	for _, c := range out.Candidates {
		if c.TaskID == candidateID {
			return c.Score, true
		}
	}
	return 0, false
}

// TestRenamingATaskThroughItsEventMovesItsSearchText renames a task by
// patching the calendar event that projects it, and asserts the task is
// retrievable by the title it now carries rather than the one it used to.
func TestRenamingATaskThroughItsEventMovesItsSearchText(t *testing.T) {
	bootstrap(t)
	requireAIMock(t)
	t.Parallel()

	tt := newTenant(t)

	create := func(title, description string) string {
		var resp struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
			"projectId":   tt.ProjectPublicID,
			"title":       title,
			"description": description,
		}, &resp)
		require.NotEmpty(t, resp.ID)
		return resp.ID
	}

	taskID := create(renameOldTitle, renameProbeBody)

	// Two probes carrying the same body and the two titles the task is
	// moving between. Each one is a duplicate of the task exactly while
	// the task's stored embedding holds that probe's title.
	oldTextProbe := create(renameOldTitle, renameProbeBody)
	newTextProbe := create(renameNewTitle, renameProbeBody)

	// Before the rename the task reads as the old title, which is what
	// makes the assertions after it a change rather than a coincidence.
	score, ranked := duplicateScoreFor(t, tt.AccessToken, taskID, oldTextProbe)
	require.True(t, ranked, "the task must start out retrievable by the title it was created with")
	require.InDelta(t, 1.0, score, 1e-4)
	_, ranked = duplicateScoreFor(t, tt.AccessToken, taskID, newTextProbe)
	require.False(t, ranked, "the task must not start out retrievable by a title it has never held")

	calID := createCalendarMut(t, tt, "Rename projection")
	var projected struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars/"+calID+"/events/from-task",
		tt.AccessToken, map[string]any{"taskId": taskID, "timezone": "UTC"}, &projected)
	require.NotEmpty(t, projected.ID)

	// The rename is entered on the calendar side. itemkit propagates it
	// onto the task, which is the write the embedding has to follow.
	doJSON(t, http.MethodPatch,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars/"+calID+"/events/"+projected.ID,
		tt.AccessToken, map[string]any{"title": renameNewTitle}, nil)

	var task struct {
		Title string `json:"title"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID, tt.AccessToken, nil, &task)
	require.Equal(t, renameNewTitle, task.Title,
		"the event rename must reach the task before the embedding can be asked about it")

	score, ranked = duplicateScoreFor(t, tt.AccessToken, taskID, newTextProbe)
	require.True(t, ranked,
		"renaming through the calendar event left the task's stored embedding on its old title: "+
			"it is not retrievable by the text it now carries")
	require.InDelta(t, 1.0, score, 1e-4,
		"the refreshed embedding must be composed from the new title and the task's own body")

	_, ranked = duplicateScoreFor(t, tt.AccessToken, taskID, oldTextProbe)
	require.False(t, ranked,
		"the task is still retrievable under the title it was renamed away from, which is text "+
			"nobody can see on it any more")
}

// TestRenamingATaskThroughItsEventMovesItsSearchTextMCP is the same
// property on the agent surface. update_calendar_event is a second
// implementation of the event-side rename rather than a wrapper over the
// REST handler, so it carries the refresh — or fails to — on its own.
func TestRenamingATaskThroughItsEventMovesItsSearchTextMCP(t *testing.T) {
	bootstrap(t)
	requireAIMock(t)
	t.Parallel()

	tt := newTenant(t)

	var tokenResp struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/me/mcp-tokens",
		tt.AccessToken, map[string]any{
			"name":   "event-rename",
			"scopes": []string{"read:workspace", "write:workspace"},
		}, &tokenResp)
	require.True(t, strings.HasPrefix(tokenResp.Token, "mcp_"))

	create := func(title, description string) string {
		var resp struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
			"projectId":   tt.ProjectPublicID,
			"title":       title,
			"description": description,
		}, &resp)
		require.NotEmpty(t, resp.ID)
		return resp.ID
	}

	taskID := create(renameOldTitle, renameProbeBody)
	oldTextProbe := create(renameOldTitle, renameProbeBody)
	newTextProbe := create(renameNewTitle, renameProbeBody)

	score, ranked := duplicateScoreFor(t, tt.AccessToken, taskID, oldTextProbe)
	require.True(t, ranked, "the task must start out retrievable by the title it was created with")
	require.InDelta(t, 1.0, score, 1e-4)
	_, ranked = duplicateScoreFor(t, tt.AccessToken, taskID, newTextProbe)
	require.False(t, ranked, "the task must not start out retrievable by a title it has never held")

	calID := createCalendarMut(t, tt, "Rename projection")
	var projected struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars/"+calID+"/events/from-task",
		tt.AccessToken, map[string]any{"taskId": taskID, "timezone": "UTC"}, &projected)
	require.NotEmpty(t, projected.ID)

	mcpCall(t, tokenResp.Token, "tools/call", map[string]any{
		"name": "update_calendar_event",
		"arguments": map[string]any{
			"eventId": projected.ID,
			"title":   renameNewTitle,
		},
	})

	var task struct {
		Title string `json:"title"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID, tt.AccessToken, nil, &task)
	require.Equal(t, renameNewTitle, task.Title,
		"the event rename must reach the task before the embedding can be asked about it")

	score, ranked = duplicateScoreFor(t, tt.AccessToken, taskID, newTextProbe)
	require.True(t, ranked,
		"renaming through the calendar event left the task's stored embedding on its old title: "+
			"it is not retrievable by the text it now carries")
	require.InDelta(t, 1.0, score, 1e-4,
		"the refreshed embedding must be composed from the new title and the task's own body")

	_, ranked = duplicateScoreFor(t, tt.AccessToken, taskID, oldTextProbe)
	require.False(t, ranked,
		"the task is still retrievable under the title it was renamed away from, which is text "+
			"nobody can see on it any more")
}
