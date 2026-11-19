package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

func TestTaskStepsInstructionReturnsOneCachedProposalWithoutApplying(t *testing.T) {
	bootstrap(t)
	if mock := os.Getenv("NF_FLOW_AI_MOCK"); mock != "" && mock != "0" && mock != "false" {
		t.Skip("requires a per-workspace test provider")
	}
	t.Parallel()

	owner := newTenant(t)
	taskID := seedPublicTask(t, owner, "Release preparation")
	var hits atomic.Int32
	var sawInstruction atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for _, message := range request.Messages {
			if strings.Contains(message.Content, `{"instruction":"Include an operational rollback step"}`) {
				sawInstruction.Store(true)
			}
		}
		content := `[{"title":"Prepare rollback","description":"","priority":"high"},{"title":"Verify the build","description":"","priority":"medium"},{"title":"Share the checklist","description":"","priority":"low"}]`
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}]}`, content)
	}))
	t.Cleanup(upstream.Close)
	registerOpenAICompatProvider(t, owner, upstream.URL)

	path := testServerURL + "/tasks/" + taskID + "/propose-steps"
	body := map[string]any{"granularity": "coarse", "instruction": "Include an operational rollback step"}
	status, first := doJSONStatus(t, http.MethodPost, path, owner.AccessToken, body)
	require.Equalf(t, http.StatusOK, status, "body=%s", first)
	var proposal struct {
		ParentTaskID string `json:"parentTaskId"`
		Steps        []struct {
			Title string `json:"title"`
		} `json:"steps"`
	}
	require.NoError(t, json.Unmarshal(first, &proposal))
	require.Equal(t, taskID, proposal.ParentTaskID)
	require.Len(t, proposal.Steps, 3)
	require.Equal(t, "Prepare rollback", proposal.Steps[0].Title)

	status, replay := doJSONStatus(t, http.MethodPost, path, owner.AccessToken, body)
	require.Equalf(t, http.StatusOK, status, "body=%s", replay)
	require.JSONEq(t, string(first), string(replay))
	require.EqualValues(t, 1, hits.Load())
	require.True(t, sawInstruction.Load())

	workspaceID, parentID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, taskID)
	var children int
	require.NoError(t, testDB.QueryRow(`SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND parent_task_id = ?`, workspaceID, parentID).Scan(&children))
	require.Zero(t, children)
}
