package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestAiSuggestionBatchAudienceKeepsMixedPrivateContextActorScoped exercises
// real triage, proposal-list, edit, and workspace-timeline HTTP paths. A
// private task is readable by the initiating member and included in the model
// batch; its title must not escape through a proposal for an unrelated inbox
// item, even to an elevated workspace owner.
func TestAiSuggestionBatchAudienceKeepsMixedPrivateContextActorScoped(t *testing.T) {
	bootstrap(t)
	if aiMockEnabled() {
		t.Skip("NF_FLOW_AI_MOCK is set; this test needs a per-workspace fake provider")
	}
	t.Parallel()

	owner := newTenant(t)
	initiator := seedProjectRoleMember(t, owner, "editor")
	reader := seedProjectRoleMember(t, owner, "viewer")
	marker := "AI_PROPOSAL_PRIVATE_INPUT_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	privateTask := createTaskWithVisibility(t, owner, marker, "private")
	attachTaskUserActor(t, owner.WorkspacePublicID, privateTask, initiator.UserPublicID, "watcher")

	workspaceID := helpers.ResolveWorkspaceInternalID(t, testDB, owner.WorkspacePublicID)
	_, privateTaskID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, privateTask)
	privateSignalID := uuid.Must(uuid.NewV7())
	proposalItemID := uuid.Must(uuid.NewV7())
	for _, signal := range []struct {
		id     uuid.UUID
		taskID any
	}{
		{id: privateSignalID, taskID: privateTaskID},
		{id: proposalItemID},
	} {
		_, err := testDB.Exec(`
INSERT INTO signals (public_id, workspace_id, task_id, source, kind, payload_json, received_at)
VALUES (?, ?, ?, 'manual', 'manual', JSON_OBJECT(), NOW(3))`, signal.id[:], workspaceID, signal.taskID)
		require.NoError(t, err)
	}

	status, body := doJSONStatus(t, http.MethodGet, testServerURL+"/tasks/"+privateTask, initiator.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "initiator fixture can read its private task; body=%s", string(body))
	status, body = doJSONStatus(t, http.MethodGet, testServerURL+"/tasks/"+privateTask, reader.AccessToken, nil)
	requireDenied(t, status, body, http.StatusNotFound, "WS.TASK.NOT_FOUND", "reader cannot read the private task")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read request", http.StatusBadRequest)
			return
		}
		var request struct {
			Messages []struct {
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
		if !strings.Contains(prompt.String(), marker) {
			http.Error(w, "private task was not in the authenticated input snapshot", http.StatusBadRequest)
			return
		}
		content := fmt.Sprintf(`[{"inboxItemId":%q,"score":0.9,"recommendedAction":"snooze","reasoning":%q}]`, proposalItemID.String(), "Private context: "+marker)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":8}}`, content)
	}))
	t.Cleanup(upstream.Close)
	registerOpenAICompatProvider(t, owner, upstream.URL)

	baseline := workspaceTimelineTotal(t, owner.WorkspacePublicID, reader)
	elevatedBaseline := workspaceTimelineTotal(t, owner.WorkspacePublicID, owner)
	status, body = doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/inbox/triage",
		initiator.AccessToken, map[string]any{"limit": 20})
	require.Equal(t, http.StatusOK, status, "triage uses the real fake-provider boundary; body=%s", string(body))

	var initiatorList struct {
		Suggestions []struct {
			ProposalID  string `json:"proposalId"`
			InboxItemID string `json:"inboxItemId"`
			Reasoning   string `json:"reasoning"`
		} `json:"suggestions"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/ai/suggestions",
		initiator.AccessToken, nil, &initiatorList)
	require.Len(t, initiatorList.Suggestions, 1)
	require.Equal(t, proposalItemID.String(), initiatorList.Suggestions[0].InboxItemID)
	require.Contains(t, initiatorList.Suggestions[0].Reasoning, marker,
		"the authorized initiator can read a proposal based on its captured input")

	var readerList struct {
		Suggestions []json.RawMessage `json:"suggestions"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/ai/suggestions",
		reader.AccessToken, nil, &readerList)
	require.Empty(t, readerList.Suggestions, "a different actor must not receive mixed-batch proposal output")
	foreignEditStatus, foreignEditBody := doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/ai/suggestions/"+proposalItemID.String()+"/decision",
		reader.AccessToken, map[string]any{
			"proposalId": initiatorList.Suggestions[0].ProposalID, "expectedRevision": 0,
			"state": "edited", "action": "open", "reasoning": "FOREIGN_ACTOR_EDIT_MARKER",
		})
	require.Equal(t, http.StatusConflict, foreignEditStatus,
		"guessing another actor's proposal id fails closed; body=%s", string(foreignEditBody))
	require.NotContains(t, string(foreignEditBody), marker)
	foreignExecuteStatus, foreignExecuteBody := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/ai/suggestions/"+proposalItemID.String()+"/execute",
		reader.AccessToken, map[string]any{
			"operationKey": "foreign-proposal-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			"proposalId":   initiatorList.Suggestions[0].ProposalID, "expectedRevision": 0, "action": "open",
		})
	require.Equal(t, http.StatusConflict, foreignExecuteStatus,
		"another actor cannot execute a proposal by public id; body=%s", string(foreignExecuteBody))
	require.NotContains(t, string(foreignExecuteBody), marker)
	require.Equal(t, baseline, workspaceTimelineTotal(t, owner.WorkspacePublicID, reader),
		"taskless proposed events are excluded from both the reader's timeline page and total")
	require.NotContains(t, workspaceTimelineBody(t, owner.WorkspacePublicID, reader), marker,
		"the reader's timeline page must not include mixed-input reasoning")
	var elevatedList struct {
		Suggestions []json.RawMessage `json:"suggestions"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/ai/suggestions",
		owner.AccessToken, nil, &elevatedList)
	require.Empty(t, elevatedList.Suggestions, "workspace elevation cannot bypass the initiating actor boundary")
	require.Equal(t, elevatedBaseline, workspaceTimelineTotal(t, owner.WorkspacePublicID, owner),
		"workspace elevation does not expose actor-private proposal timeline rows")
	// The workspace owner may read the private task's ordinary task.created
	// event. Check proposal events specifically; their reasoning stays personal.
	var elevatedTimeline struct {
		Events []struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		} `json:"events"`
	}
	require.NoError(t, json.Unmarshal([]byte(workspaceTimelineBody(t, owner.WorkspacePublicID, owner)), &elevatedTimeline))
	for _, event := range elevatedTimeline.Events {
		if event.Type == "ai.suggestion.proposed" || event.Type == "ai.suggestion.edited" {
			require.NotContains(t, string(event.Payload), marker, "elevated readers cannot see another actor's proposal reasoning")
		}
	}

	editMarker := "AI_PROPOSAL_PRIVATE_EDIT_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	suggestion := initiatorList.Suggestions[0]
	status, body = doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/ai/suggestions/"+suggestion.InboxItemID+"/decision",
		initiator.AccessToken, map[string]any{
			"proposalId": suggestion.ProposalID, "expectedRevision": 0, "state": "edited",
			"action": "archive", "reasoning": editMarker,
		})
	require.Equal(t, http.StatusOK, status, "actor-private edit should persist; body=%s", string(body))
	require.Equal(t, baseline, workspaceTimelineTotal(t, owner.WorkspacePublicID, reader),
		"edited reasoning remains absent from the other actor's page and count")
	require.NotContains(t, workspaceTimelineBody(t, owner.WorkspacePublicID, reader), editMarker)
	require.Equal(t, elevatedBaseline, workspaceTimelineTotal(t, owner.WorkspacePublicID, owner),
		"workspace elevation does not expose actor-private edited timeline rows")
	require.NotContains(t, workspaceTimelineBody(t, owner.WorkspacePublicID, owner), editMarker)
	require.Contains(t, workspaceTimelineBody(t, owner.WorkspacePublicID, initiator), editMarker,
		"the initiating actor retains its own edited reason")

	_, err := testDB.Exec(`UPDATE tasks SET created_by_user_id = NULL WHERE workspace_id = ? AND id = ?`, workspaceID, privateTaskID)
	require.NoError(t, err, "simulate creator deletion, which nulls the task creator FK")
	disableTaskUserActor(t, owner.WorkspacePublicID, privateTask, initiator.UserPublicID, "watcher")
	var afterRevocation struct {
		Suggestions []json.RawMessage `json:"suggestions"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/ai/suggestions",
		initiator.AccessToken, nil, &afterRevocation)
	require.Empty(t, afterRevocation.Suggestions,
		"an initiator that loses access to any captured task loses the batch output too")
}

func workspaceTimelineTotal(t *testing.T, workspacePublicID string, as *helpers.TestTenant) int64 {
	t.Helper()
	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+workspacePublicID+"/timeline?limit=200",
		as.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "workspace timeline must render; body=%s", string(body))
	var response struct {
		Body struct {
			Total int64 `json:"total"`
		} `json:"body"`
		Total int64 `json:"total"`
	}
	require.NoError(t, json.Unmarshal(body, &response))
	if response.Body.Total != 0 {
		return response.Body.Total
	}
	return response.Total
}
