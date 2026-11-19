package e2e

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

func TestTaskReplyProposalDeterministicBypassAndEditorGate(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	taskID := seedPublicTask(t, owner, "Reply proposal deterministic path")
	viewer := seedProjectRoleMember(t, owner, "viewer")
	workspaceID := helpers.ResolveWorkspaceInternalID(t, testDB, owner.WorkspacePublicID)
	var invocationsBefore int
	require.NoError(t, testDB.QueryRow(`SELECT COUNT(*) FROM ai_invocations WHERE workspace_id = ?`, workspaceID).Scan(&invocationsBefore))

	post := func(token, reply string) (int, []byte) {
		t.Helper()
		return doJSONStatus(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/propose-reply", token, map[string]any{"reply": reply})
	}

	status, body := post(owner.AccessToken, "due 2035-04-05")
	require.Equal(t, http.StatusOK, status, "deterministic due proposal; body=%s", string(body))
	var dueProposal struct {
		TaskID        string `json:"taskId"`
		ExpectedState string `json:"expectedState"`
		ExpectedDueOn string `json:"expectedDueOn"`
		Proposal      struct {
			DueOn string `json:"dueOn"`
		} `json:"proposal"`
	}
	require.NoError(t, json.Unmarshal(body, &dueProposal))
	require.Equal(t, taskID, dueProposal.TaskID)
	require.Equal(t, "open", dueProposal.ExpectedState)
	require.Empty(t, dueProposal.ExpectedDueOn, "unset precondition is the empty string expected by PATCH")
	require.Equal(t, "2035-04-05", dueProposal.Proposal.DueOn)

	status, body = post(owner.AccessToken, "complete")
	require.Equal(t, http.StatusOK, status, "deterministic completion proposal; body=%s", string(body))
	var completeProposal struct {
		Proposal struct {
			Transition string `json:"transition"`
		} `json:"proposal"`
	}
	require.NoError(t, json.Unmarshal(body, &completeProposal))
	require.Equal(t, "complete", completeProposal.Proposal.Transition)

	status, body = post(viewer.AccessToken, "complete")
	requireDenied(t, status, body, http.StatusForbidden, projectACLDenied, "viewer cannot request task proposals")

	var state string
	var dueOn sql.NullTime
	require.NoError(t, testDB.QueryRow(`SELECT derived_state, due_on FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`, taskID).Scan(&state, &dueOn))
	require.Equal(t, "open", state, "proposal must not close the task")
	require.False(t, dueOn.Valid, "proposal must not persist a due date")
	var invocationsAfter int
	require.NoError(t, testDB.QueryRow(`SELECT COUNT(*) FROM ai_invocations WHERE workspace_id = ?`, workspaceID).Scan(&invocationsAfter))
	require.Equal(t, invocationsBefore, invocationsAfter, "exact commands and denied calls must bypass the LLM")
}

func TestTaskReplyProposalDoesNotInferCompletionFromTextOnlyReply(t *testing.T) {
	bootstrap(t)
	if os.Getenv("NF_FLOW_AI_MOCK") != "" && os.Getenv("NF_FLOW_AI_MOCK") != "0" && os.Getenv("NF_FLOW_AI_MOCK") != "false" {
		t.Skip("NF_FLOW_AI_MOCK is set; this test needs a per-workspace fake provider")
	}
	t.Parallel()

	owner := newTenant(t)
	taskID := seedPublicTask(t, owner, "Reply proposal model path")
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		defer r.Body.Close()
		content := `{"dueOn":null,"transition":"complete","comment":"","clarification":""}`
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":8}}`, content)
	}))
	t.Cleanup(upstream.Close)
	registerOpenAICompatProvider(t, owner, upstream.URL)

	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/tasks/"+taskID+"/propose-reply", owner.AccessToken,
		map[string]any{"reply": "完了にしてはいけません。返信を記録してください"})
	require.Equal(t, http.StatusOK, status, "freeform reply proposal; body=%s", string(body))
	var got struct {
		ExpectedDueOn string `json:"expectedDueOn"`
		Proposal      struct {
			Transition    string `json:"transition"`
			Clarification string `json:"clarification"`
		} `json:"proposal"`
	}
	require.NoError(t, json.Unmarshal(body, &got))
	require.Empty(t, got.ExpectedDueOn)
	require.Empty(t, got.Proposal.Transition, "a text-only reply cannot be turned into task completion by model output")
	require.NotEmpty(t, got.Proposal.Clarification)
	require.EqualValues(t, 1, hits.Load(), "a freeform request should make one provider call")

	var state string
	var dueOn sql.NullTime
	require.NoError(t, testDB.QueryRow(`SELECT derived_state, due_on FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`, taskID).Scan(&state, &dueOn))
	require.Equal(t, "open", state)
	require.False(t, dueOn.Valid)

	_, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, taskID)
	var audience, resourcesJSON string
	var provenanceValid bool
	require.NoError(t, testDB.QueryRow(`
		SELECT audience, CAST(input_resources_json AS CHAR), provenance_valid
		FROM ai_invocations
		WHERE workspace_id = ? AND task_id = ? AND purpose = 'task_reply_proposal'
		ORDER BY id DESC LIMIT 1`, helpers.ResolveWorkspaceInternalID(t, testDB, owner.WorkspacePublicID), taskInternalID).
		Scan(&audience, &resourcesJSON, &provenanceValid))
	require.Equal(t, "initiator", audience, "reply input is scoped to its initiator")
	require.True(t, provenanceValid, "task resource provenance is checked")
	require.Contains(t, resourcesJSON, taskID)
}
