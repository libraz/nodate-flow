package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/stretchr/testify/require"
)

func TestAiTaskDigestEmptySelectionNeedsNoProvider(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	var out struct {
		Summary   string `json:"summary"`
		TaskCount int    `json:"taskCount"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tenant.WorkspacePublicID+"/ai/task-digest",
		tenant.AccessToken,
		map[string]any{"taskIds": []string{}},
		&out,
	)
	require.Empty(t, out.Summary)
	require.Zero(t, out.TaskCount)
}

func TestAiTaskDigestAuthorizesDeduplicatesAndScopesCache(t *testing.T) {
	bootstrap(t)
	if aiMockEnabled() {
		t.Skip("NF_FLOW_AI_MOCK is set; this test needs a per-workspace fake provider")
	}
	t.Parallel()

	owner := newTenant(t)
	member := seedWorkspaceMemberTenant(t, owner)
	publicA := createTaskForDigest(t, owner, "AI_DIGEST_PUBLIC_A_"+randomHex(6), "AI_DIGEST_DESCRIPTION_SECRET_"+randomHex(6), "2099-03-07", "public")
	publicB := createTaskForDigest(t, owner, "AI_DIGEST_PUBLIC_B_"+randomHex(6), "", "", "public")
	private := createTaskWithVisibility(t, owner, "AI_DIGEST_PRIVATE_"+randomHex(6), "private")

	var calls atomic.Int32
	var mu sync.Mutex
	var prompts []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read request", http.StatusBadRequest)
			return
		}
		var request struct {
			Messages []struct {
				Role    string `json:"role"`
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
		mu.Lock()
		prompts = append(prompts, prompt.String())
		mu.Unlock()
		calls.Add(1)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w,
			`{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":8}}`,
			`{"summary":"Two selected tasks are open."}`,
		)
	}))
	t.Cleanup(upstream.Close)
	registerOpenAICompatProvider(t, owner, upstream.URL)

	digestURL := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/ai/task-digest"
	status, body := doJSONStatus(t, http.MethodPost, digestURL, member.AccessToken, map[string]any{
		"taskIds": []string{publicA, private},
	})
	requireDenied(t, status, body, http.StatusNotFound, "WS.TASK.NOT_FOUND", "a digest containing an unreadable task")
	require.Zero(t, calls.Load(), "authorization must finish for every task before any provider call")

	tooMany := make([]string, 26)
	for i := range tooMany {
		tooMany[i] = publicA
	}
	status, body = doJSONStatus(t, http.MethodPost, digestURL, owner.AccessToken, map[string]any{"taskIds": tooMany})
	require.Equalf(t, http.StatusUnprocessableEntity, status, "request cap must be enforced before generation; body=%s", string(body))
	require.Zero(t, calls.Load(), "an over-limit request must not reach the provider")

	request := map[string]any{"taskIds": []string{publicA, publicB, publicA}}
	var result struct {
		Summary   string `json:"summary"`
		TaskCount int    `json:"taskCount"`
	}
	doJSON(t, http.MethodPost, digestURL, owner.AccessToken, request, &result)
	require.Equal(t, "Two selected tasks are open.", result.Summary)
	require.Equal(t, 2, result.TaskCount, "duplicate IDs should be collapsed")

	// A same-actor retry with the same selected facts should reuse the
	// proposal cache. The cache scope also includes the actor, so the
	// member's request below must make a separate provider call.
	doJSON(t, http.MethodPost, digestURL, owner.AccessToken, request, &result)
	require.Equal(t, int32(1), calls.Load(), "same actor and snapshot should not call the provider twice")

	var memberResult struct {
		Summary   string `json:"summary"`
		TaskCount int    `json:"taskCount"`
	}
	doJSON(t, http.MethodPost, digestURL, member.AccessToken,
		map[string]any{"taskIds": []string{publicA}}, &memberResult)
	require.Equal(t, int32(2), calls.Load(), "another actor must not reuse the first actor's private cached result")

	mu.Lock()
	seenPrompts := append([]string(nil), prompts...)
	mu.Unlock()
	require.Len(t, seenPrompts, 2)
	require.Contains(t, seenPrompts[0], "AI_DIGEST_PUBLIC_A_")
	require.Contains(t, seenPrompts[0], "AI_DIGEST_PUBLIC_B_")
	require.Contains(t, seenPrompts[0], "2099-03-07")
	require.NotContains(t, seenPrompts[0], "AI_DIGEST_DESCRIPTION_SECRET_")
	require.NotContains(t, seenPrompts[0], "AI_DIGEST_PRIVATE_")
	require.Equal(t, 1, strings.Count(seenPrompts[0], "AI_DIGEST_PUBLIC_A_"), "duplicate task IDs must appear once in the prompt")

	workspaceID := helpers.ResolveWorkspaceInternalID(t, testDB, owner.WorkspacePublicID)
	var audience, resourcesJSON string
	var inputCount int64
	require.NoError(t, testDB.QueryRow(
		`SELECT audience, CAST(input_resources_json AS CHAR), input_count
		   FROM ai_invocations
		  WHERE workspace_id = ? AND purpose = 'task_digest'
		  ORDER BY id DESC LIMIT 1`, workspaceID,
	).Scan(&audience, &resourcesJSON, &inputCount))
	require.Equal(t, "initiator", audience)
	require.EqualValues(t, 1, inputCount, "the member digest should retain one task resource reference")
	var resources []struct {
		Kind     string `json:"kind"`
		PublicID string `json:"publicId"`
	}
	require.NoError(t, json.Unmarshal([]byte(resourcesJSON), &resources))
	require.Equal(t, []struct {
		Kind     string `json:"kind"`
		PublicID string `json:"publicId"`
	}{{Kind: "task", PublicID: publicA}}, resources)
}

func createTaskForDigest(t *testing.T, tenant *helpers.TestTenant, title, description, dueOn, visibility string) string {
	t.Helper()
	body := map[string]any{
		"projectId":   tenant.ProjectPublicID,
		"title":       title,
		"description": description,
		"visibility":  visibility,
	}
	if dueOn != "" {
		body["dueOn"] = dueOn
	}
	var out struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tenant.AccessToken, body, &out)
	require.NotEmpty(t, out.ID)
	return out.ID
}
