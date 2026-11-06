package e2e

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/stretchr/testify/require"
)

// TestProposalCacheRevocation drives the real orchestrator and sqlc readers
// against MySQL, with only the embedding vector supplied deterministically.
// The proposal initially quotes a private task readable through a watcher
// actor. Revoking that actor while the request text and workspace stay the
// same must invalidate the cached result and rebuild the prompt without the
// private title.
func TestProposalCacheRevocation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	caller := seedProjectRoleMember(t, owner, "editor")
	marker := "AI_CACHE_PRIVATE_CANDIDATE_" + strings.ReplaceAll(randomHex(12), "-", "")
	candidate := createTaskWithVisibility(t, owner, marker, "private")
	attachTaskUserActor(t, owner.WorkspacePublicID, candidate, caller.UserPublicID, "watcher")
	seedProposalCacheEmbedding(t, owner.WorkspacePublicID, candidate, "proposal-cache-768")

	parent := createTaskWithVisibility(t, owner, "AI_CACHE_PUBLIC_PARENT_"+randomHex(8), "public")
	status, body := doJSONStatus(t, http.MethodGet, testServerURL+"/tasks/"+parent, caller.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "caller must retain a readable public task; body=%s", string(body))

	var mu sync.Mutex
	var prompts []string
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
		promptText := prompt.String()
		mu.Lock()
		prompts = append(prompts, promptText)
		mu.Unlock()

		title := "safe refreshed proposal"
		if strings.Contains(promptText, marker) {
			title = "Draft from " + marker
		}
		content, err := json.Marshal(map[string]any{
			"suggestedAssignees": []any{},
			"subtasks": []any{map[string]any{
				"title":       title,
				"description": "",
				"priority":    "medium",
			}, map[string]any{"title": "Review result", "description": "Verify the result", "priority": "low"}},
		})
		if err != nil {
			http.Error(w, "encode response", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":8}}`, string(content))
	}))
	t.Cleanup(upstream.Close)

	llm, err := providers.New(providers.Config{
		Kind:         providers.KindOpenAICompat,
		Name:         "proposal cache revocation fixture",
		BaseURL:      upstream.URL,
		DefaultModel: "gpt-4o-mini",
		EncryptedKey: []byte("test-key"),
	}, proposalCacheTestDecryptor{})
	require.NoError(t, err)
	orchestrator := &ai.Orchestrator{
		Resolver: ai.ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
			return llm, nil
		}),
		ProposalCache: ai.NewProposalCache(time.Minute),
	}
	queries := generated.New(testDB)
	wsID := mustWorkspaceInternalID(t, owner.WorkspacePublicID)
	actorID := mustUserInternalIDForProposalCache(t, caller.UserPublicID)
	vis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole("member"))
	var embeddingCalls atomic.Int32
	embeddings := proposalCacheEmbeddingClient{model: "proposal-cache-768", calls: &embeddingCalls}

	propose := func() *ai.SmartProposal {
		t.Helper()
		proposal, err := orchestrator.ProposeSmartCreate(context.Background(), wsID,
			"Ship the release checklist", "Prepare the launch notes.", embeddings, queries, vis)
		require.NoError(t, err)
		return proposal
	}

	first := propose()
	require.Contains(t, first.Subtasks[0].Title, marker, "first provider result should reflect the quoted candidate")
	require.EqualValues(t, 1, embeddingCalls.Load())
	secondUnchanged := propose()
	require.Equal(t, first, secondUnchanged, "an unchanged proposal request should return the cached result")
	mu.Lock()
	require.Len(t, prompts, 1)
	require.Contains(t, prompts[0], marker, "the first provider prompt should include the readable private candidate")
	mu.Unlock()
	require.EqualValues(t, 1, embeddingCalls.Load(), "a validated positive cache hit must avoid embedding")

	disableTaskUserActor(t, owner.WorkspacePublicID, candidate, caller.UserPublicID, "watcher")
	status, body = doJSONStatus(t, http.MethodGet, testServerURL+"/tasks/"+parent, caller.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "unrelated public task remains readable after candidate access is revoked; body=%s", string(body))
	status, body = doJSONStatus(t, http.MethodGet, testServerURL+"/tasks/"+candidate, caller.AccessToken, nil)
	requireDenied(t, status, body, http.StatusNotFound, "WS.TASK.NOT_FOUND", "private candidate access must be revoked")

	second := propose()
	require.NotContains(t, second.Subtasks[0].Title, marker, "a cached answer derived from a now-unreadable candidate must not be returned")
	require.EqualValues(t, 2, embeddingCalls.Load(), "a stale cache envelope must follow the normal refreshed-visibility path")
	mu.Lock()
	require.Len(t, prompts, 2, "revoking a quoted candidate must make the orchestrator recompute")
	require.NotContains(t, prompts[1], marker, "recomputed prompt must use the reduced readable candidate set")
	mu.Unlock()
}

// TestProposalCacheChildRevocationREST exercises the actual REST path for
// step proposals. Child tasks are fetched with their current visibility, so
// after revocation the handler's input changes and the refreshed response
// must omit the old child's title.
func TestProposalCacheChildRevocationREST(t *testing.T) {
	bootstrap(t)
	if aiMockEnabled() {
		t.Skip("NF_FLOW_AI_MOCK is set; this test needs a per-workspace fake provider")
	}
	t.Parallel()

	owner := newTenant(t)
	caller := seedProjectRoleMember(t, owner, "editor")
	marker := "AI_CACHE_PRIVATE_CHILD_" + strings.ReplaceAll(randomHex(12), "-", "")
	parent := createTaskWithVisibility(t, owner, "AI cache public parent "+randomHex(8), "public")
	child := createTaskWithVisibility(t, owner, marker, "private")
	attachChildTask(t, owner.WorkspacePublicID, parent, child)
	attachTaskUserActor(t, owner.WorkspacePublicID, child, caller.UserPublicID, "watcher")

	var mu sync.Mutex
	var prompts []string
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
		promptText := prompt.String()
		mu.Lock()
		prompts = append(prompts, promptText)
		mu.Unlock()
		content := `[{"title":"Step 1","description":"Verify evidence","priority":"low"},{"title":"Step 2","description":"Verify evidence","priority":"low"},{"title":"Step 3","description":"Verify evidence","priority":"low"}]`
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":8}}`, content)
	}))
	t.Cleanup(upstream.Close)
	registerOpenAICompatProvider(t, owner, upstream.URL)

	post := func() (int, []byte) {
		t.Helper()
		return doJSONStatus(t, http.MethodPost, testServerURL+"/tasks/"+parent+"/propose-steps", caller.AccessToken,
			map[string]any{"granularity": "coarse"})
	}
	status, body := post()
	require.Equal(t, http.StatusOK, status, "initial step proposal; body=%s", string(body))
	status, body = doJSONStatus(t, http.MethodGet, testServerURL+"/tasks/"+parent, caller.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "public parent stays readable; body=%s", string(body))
	status, body = doJSONStatus(t, http.MethodGet, testServerURL+"/tasks/"+child, caller.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "watcher can initially read the private child; body=%s", string(body))

	disableTaskUserActor(t, owner.WorkspacePublicID, child, caller.UserPublicID, "watcher")
	status, body = post()
	require.Equal(t, http.StatusOK, status, "proposal refresh after child revocation; body=%s", string(body))
	status, body = doJSONStatus(t, http.MethodGet, testServerURL+"/tasks/"+parent, caller.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "public parent remains readable after child revocation; body=%s", string(body))
	status, body = doJSONStatus(t, http.MethodGet, testServerURL+"/tasks/"+child, caller.AccessToken, nil)
	requireDenied(t, status, body, http.StatusNotFound, "WS.TASK.NOT_FOUND", "child actor access must be revoked")

	mu.Lock()
	require.Len(t, prompts, 2, "the changed child set must produce a fresh provider call")
	require.Contains(t, prompts[0], marker)
	require.NotContains(t, prompts[1], marker, "the second real REST prompt must not retain the revoked child")
	mu.Unlock()
}

// TestProposalCacheFingerprintsInvalidatePromptInputs ensures cache hits are
// tied to the task fields, assignee, and workspace roster actually quoted in
// the prompt. Every mutation keeps the request text and actor unchanged, so
// the provider must be called again because the cached input snapshot changed.
func TestProposalCacheFingerprintsInvalidatePromptInputs(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	caller := seedProjectRoleMember(t, owner, "editor")
	nextAssignee := seedProjectRoleMember(t, owner, "editor")
	taskV1 := "AI_CACHE_FINGERPRINT_TASK_V1_" + randomHex(8)
	taskV2 := "AI_CACHE_FINGERPRINT_TASK_V2_" + randomHex(8)
	candidate := createTaskWithVisibility(t, owner, taskV1, "public")
	// Task creation automatically attaches the creator as the sole
	// assignee. Remove that row so the caller is the prompt's primary
	// assignee and the following change really changes quoted prompt data.
	disableTaskUserActor(t, owner.WorkspacePublicID, candidate, owner.UserPublicID, "assignee")
	attachTaskUserActor(t, owner.WorkspacePublicID, candidate, caller.UserPublicID, "assignee")
	seedProposalCacheEmbedding(t, owner.WorkspacePublicID, candidate, "proposal-cache-fingerprint-768")

	var mu sync.Mutex
	var prompts []string
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
		mu.Lock()
		prompts = append(prompts, prompt.String())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"suggestedAssignees\":[],\"subtasks\":[{\"title\":\"fresh proposal\",\"description\":\"\",\"priority\":\"medium\"},{\"title\":\"Review result\",\"description\":\"Verify the result\",\"priority\":\"low\"}]}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":8}}`)
	}))
	t.Cleanup(upstream.Close)
	llm, err := providers.New(providers.Config{
		Kind:         providers.KindOpenAICompat,
		Name:         "proposal cache fingerprint fixture",
		BaseURL:      upstream.URL,
		DefaultModel: "gpt-4o-mini",
		EncryptedKey: []byte("test-key"),
	}, proposalCacheTestDecryptor{})
	require.NoError(t, err)
	orchestrator := &ai.Orchestrator{
		Resolver: ai.ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
			return llm, nil
		}),
		ProposalCache: ai.NewProposalCache(time.Minute),
	}
	queries := generated.New(testDB)
	wsID := mustWorkspaceInternalID(t, owner.WorkspacePublicID)
	actorID := mustUserInternalIDForProposalCache(t, caller.UserPublicID)
	vis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole("member"))
	embeddings := proposalCacheEmbeddingClient{model: "proposal-cache-fingerprint-768"}
	propose := func() {
		t.Helper()
		_, err := orchestrator.ProposeSmartCreate(context.Background(), wsID,
			"Plan the migration", "Keep the current public request text.", embeddings, queries, vis)
		require.NoError(t, err)
	}
	promptAt := func(index int) string {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		require.Greater(t, len(prompts), index, "expected a provider call for changed prompt inputs")
		return prompts[index]
	}

	propose()
	require.Contains(t, promptAt(0), taskV1)
	require.Contains(t, promptAt(0), caller.UserPublicID, "initial prompt should include the current assignee")
	require.Equal(t, 2, strings.Count(promptAt(0), caller.UserPublicID), "initial assignee appears once in the roster and once on the candidate")

	_, candidateID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, candidate)
	_, err = testDB.Exec(`UPDATE tasks SET title = ? WHERE id = ?`, taskV2, candidateID)
	require.NoError(t, err)
	propose()
	require.Contains(t, promptAt(1), taskV2, "changed task text must invalidate the old proposal")

	disableTaskUserActor(t, owner.WorkspacePublicID, candidate, caller.UserPublicID, "assignee")
	attachTaskUserActor(t, owner.WorkspacePublicID, candidate, nextAssignee.UserPublicID, "assignee")
	propose()
	require.Contains(t, promptAt(2), nextAssignee.UserPublicID, "changed assignee must invalidate the old proposal")
	require.Equal(t, 2, strings.Count(promptAt(2), nextAssignee.UserPublicID), "new assignee appears once in the roster and once on the candidate")
	require.Equal(t, 1, strings.Count(promptAt(2), caller.UserPublicID), "old assignee should remain only in the roster")

	newRosterMember := seedGuestMember(t, owner)
	propose()
	require.Contains(t, promptAt(3), newRosterMember.UserPublicID, "workspace roster changes must invalidate the old proposal")
}

// TestProposalCacheRechecksInputsAfterProviderResponse pins the final
// authorization boundary for a cache miss. The fake provider holds its
// response after receiving a private candidate title; access is revoked
// before release, so the result must be rejected and must not seed the cache.
func TestProposalCacheRechecksInputsAfterProviderResponse(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	caller := seedProjectRoleMember(t, owner, "editor")
	marker := "AI_CACHE_INFLIGHT_PRIVATE_" + randomHex(8)
	candidate := createTaskWithVisibility(t, owner, marker, "private")
	attachTaskUserActor(t, owner.WorkspacePublicID, candidate, caller.UserPublicID, "watcher")
	seedProposalCacheEmbedding(t, owner.WorkspacePublicID, candidate, "proposal-cache-inflight-768")

	providerStarted := make(chan string, 1)
	releaseProvider := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseProvider) })
	var mu sync.Mutex
	var prompts []string
	var providerResponses []string
	var invocationRecords []ai.InvocationRecord
	type metricSample struct {
		inputTokens  int
		outputTokens int
		costMicros   int64
		err          error
	}
	var metrics []metricSample
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
		promptText := prompt.String()
		mu.Lock()
		callNumber := len(prompts) + 1
		prompts = append(prompts, promptText)
		mu.Unlock()
		if callNumber == 1 {
			providerStarted <- promptText
			select {
			case <-releaseProvider:
			case <-r.Context().Done():
				return
			}
		}
		title := "fresh safe proposal"
		if strings.Contains(promptText, marker) {
			title = "response derived from " + marker
		}
		content, err := json.Marshal(map[string]any{
			"suggestedAssignees": []any{},
			"subtasks": []any{map[string]any{
				"title":       title,
				"description": "",
				"priority":    "medium",
			}, map[string]any{"title": "Review result", "description": "Verify the result", "priority": "low"}},
		})
		if err != nil {
			http.Error(w, "encode response", http.StatusInternalServerError)
			return
		}
		mu.Lock()
		providerResponses = append(providerResponses, string(content))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":8}}`, string(content))
	}))
	t.Cleanup(upstream.Close)
	llm, err := providers.New(providers.Config{
		Kind:         providers.KindOpenAICompat,
		Name:         "proposal cache in-flight fixture",
		BaseURL:      upstream.URL,
		DefaultModel: "gpt-4o-mini",
		EncryptedKey: []byte("test-key"),
	}, proposalCacheTestDecryptor{})
	require.NoError(t, err)
	orchestrator := &ai.Orchestrator{
		Resolver: ai.ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
			return llm, nil
		}),
		ProposalCache: ai.NewProposalCache(time.Minute),
		LogInvoke: func(_ context.Context, record ai.InvocationRecord) {
			mu.Lock()
			invocationRecords = append(invocationRecords, record)
			mu.Unlock()
		},
		OnInvocation: func(_, _ string, inputTokens, outputTokens int, costMicros int64, _ time.Duration, err error) {
			mu.Lock()
			metrics = append(metrics, metricSample{
				inputTokens: inputTokens, outputTokens: outputTokens, costMicros: costMicros, err: err,
			})
			mu.Unlock()
		},
	}
	queries := generated.New(testDB)
	wsID := mustWorkspaceInternalID(t, owner.WorkspacePublicID)
	actorID := mustUserInternalIDForProposalCache(t, caller.UserPublicID)
	vis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole("member"))
	embeddings := proposalCacheEmbeddingClient{model: "proposal-cache-inflight-768"}
	propose := func() (*ai.SmartProposal, error) {
		return orchestrator.ProposeSmartCreate(context.Background(), wsID,
			"Ship the release checklist", "Prepare the launch notes.", embeddings, queries, vis)
	}
	type result struct {
		proposal *ai.SmartProposal
		err      error
	}
	completed := make(chan result, 1)
	go func() {
		proposal, callErr := propose()
		completed <- result{proposal: proposal, err: callErr}
	}()
	select {
	case prompt := <-providerStarted:
		require.Contains(t, prompt, marker, "provider should receive the currently readable private candidate")
	case <-time.After(10 * time.Second):
		t.Fatal("provider did not receive the first prompt")
	}
	disableTaskUserActor(t, owner.WorkspacePublicID, candidate, caller.UserPublicID, "watcher")
	releaseOnce.Do(func() { close(releaseProvider) })
	var first result
	select {
	case first = <-completed:
	case <-time.After(10 * time.Second):
		t.Fatal("proposal did not complete after provider release")
	}
	require.Error(t, first.err, "a proposal whose input became unreadable in flight must be rejected")
	require.Nil(t, first.proposal, "a response derived from revoked input must not reach the caller")

	second, err := propose()
	require.NoError(t, err, "the same request must recompute from the reduced readable set")
	require.NotContains(t, second.Subtasks[0].Title, marker)
	mu.Lock()
	require.Len(t, prompts, 2, "the rejected response must not populate the cache")
	require.NotContains(t, prompts[1], marker)
	require.Len(t, invocationRecords, 2)
	require.Equal(t, "error", invocationRecords[0].Status)
	require.Equal(t, "AI.PROPOSAL.INPUTS_CHANGED", invocationRecords[0].ErrorCode)
	require.Equal(t, 10, invocationRecords[0].TokensInput)
	require.Equal(t, 8, invocationRecords[0].TokensOutput)
	require.Greater(t, invocationRecords[0].CostMicros, int64(0))
	require.NotEmpty(t, invocationRecords[0].ResponseRedacted)
	require.Len(t, providerResponses, 2)
	require.Equal(t, ai.Redact(providerResponses[0]), invocationRecords[0].ResponseRedacted)
	require.Len(t, metrics, 2)
	require.Error(t, metrics[0].err)
	require.Equal(t, 10, metrics[0].inputTokens)
	require.Equal(t, 8, metrics[0].outputTokens)
	require.Greater(t, metrics[0].costMicros, int64(0))
	mu.Unlock()
}

// TestProposalCacheRechecksWorkspaceRoleAfterProviderResponseREST holds a
// real REST proposal while the actor is an admin, then demotes the actor
// before the local provider returns. The parent is private to its creator,
// so the response proves whether the in-flight role is reused.
func TestProposalCacheRechecksWorkspaceRoleAfterProviderResponseREST(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	assertProposalRejectedAfterWorkspaceAccessChangeREST(t, "demote")
}

// TestProposalCacheRechecksMembershipAndParentLivenessAfterProviderResponseREST
// runs independent real REST requests and disables workspace membership,
// the workspace, or the parent project while the local provider is held.
func TestProposalCacheRechecksMembershipAndParentLivenessAfterProviderResponseREST(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	for _, change := range []string{"remove membership", "disable workspace", "disable project"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			assertProposalRejectedAfterWorkspaceAccessChangeREST(t, change)
		})
	}
}

func assertProposalRejectedAfterWorkspaceAccessChangeREST(t *testing.T, change string) {
	t.Helper()
	if aiMockEnabled() {
		t.Skip("NF_FLOW_AI_MOCK is set; this test needs a per-workspace fake provider")
	}

	owner := newTenant(t)
	admin := seedElevatedMember(t, owner)
	marker := "AI_CACHE_ROLE_PRIVATE_PARENT_" + randomHex(8)
	parent := createTaskWithVisibility(t, owner, marker, "private")
	providerStarted := make(chan string, 1)
	releaseProvider := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseProvider) })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		raw, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			http.Error(w, "read request", http.StatusBadRequest)
			return
		}
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if decodeErr := json.Unmarshal(raw, &request); decodeErr != nil {
			http.Error(w, "decode request", http.StatusBadRequest)
			return
		}
		var prompt strings.Builder
		for _, message := range request.Messages {
			prompt.WriteString(message.Content)
			prompt.WriteByte('\n')
		}
		providerStarted <- prompt.String()
		select {
		case <-releaseProvider:
		case <-r.Context().Done():
			return
		}
		content := `[{"title":"Step 1","description":"Inspect current work","priority":"low"},{"title":"Step 2","description":"Prepare the change","priority":"medium"},{"title":"Step 3","description":"Review the result","priority":"low"}]`
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":10000,"completion_tokens":10000}}`, content)
	}))
	t.Cleanup(upstream.Close)
	registerOpenAICompatProvider(t, owner, upstream.URL)

	type result struct {
		status int
		body   []byte
		err    error
	}
	completed := make(chan result, 1)
	go func() {
		status, body, err := sendJSONStatus(http.MethodPost,
			testServerURL+"/tasks/"+parent+"/propose-steps",
			admin.AccessToken,
			map[string]any{"granularity": "coarse"})
		completed <- result{status: status, body: body, err: err}
	}()
	select {
	case prompt := <-providerStarted:
		require.Contains(t, prompt, marker, "admin prompt must contain the private parent")
	case <-time.After(10 * time.Second):
		t.Fatal("provider did not receive the private-parent prompt")
	}

	var resultSet sql.Result
	var err error
	switch change {
	case "demote":
		resultSet, err = testDB.Exec(
			`UPDATE workspace_members wm
			 JOIN workspaces w ON w.id = wm.workspace_id
			 JOIN users u ON u.id = wm.user_id
			 SET wm.role = 'member'
			 WHERE w.public_id = UUID_TO_BIN(?, 0)
			   AND u.public_id = UUID_TO_BIN(?, 0)`,
			owner.WorkspacePublicID, admin.UserPublicID,
		)
	case "remove membership":
		resultSet, err = testDB.Exec(
			`UPDATE workspace_members wm
			 JOIN workspaces w ON w.id = wm.workspace_id
			 JOIN users u ON u.id = wm.user_id
			 SET wm.enabled = FALSE
			 WHERE w.public_id = UUID_TO_BIN(?, 0)
			   AND u.public_id = UUID_TO_BIN(?, 0)`,
			owner.WorkspacePublicID, admin.UserPublicID,
		)
	case "disable workspace":
		resultSet, err = testDB.Exec(`UPDATE workspaces SET enabled = FALSE WHERE public_id = UUID_TO_BIN(?, 0)`, owner.WorkspacePublicID)
	case "disable project":
		resultSet, err = testDB.Exec(`UPDATE projects SET enabled = FALSE WHERE public_id = UUID_TO_BIN(?, 0)`, owner.ProjectPublicID)
	default:
		t.Fatalf("unknown authorization change %q", change)
	}
	require.NoError(t, err)
	changed, err := resultSet.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), changed, "the requested authorization/liveness change must affect one row")
	releaseOnce.Do(func() { close(releaseProvider) })
	var response result
	select {
	case response = <-completed:
	case <-time.After(10 * time.Second):
		t.Fatal("REST proposal did not finish after provider release")
	}
	require.NoError(t, response.err)
	require.Equal(t, http.StatusConflict, response.status, "lost prompt access must reject the provider result; body=%s", string(response.body))
	require.Contains(t, string(response.body), "AI.PROPOSAL.INPUTS_CHANGED")
	require.NotContains(t, string(response.body), "Step 1", "rejected provider content must not reach the response")

	workspaceID, parentID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, parent)
	var status string
	var errorCode sql.NullString
	var responseRedacted sql.NullString
	var inputTokens, outputTokens sql.NullInt64
	var costEstimate sql.NullFloat64
	err = testDB.QueryRow(
		`SELECT status, error_code, response_redacted, tokens_input, tokens_output, cost_estimate
		 FROM ai_invocations
		 WHERE workspace_id = ? AND task_id = ? AND purpose = 'propose_steps'
		 ORDER BY id DESC LIMIT 1`,
		workspaceID, parentID,
	).Scan(&status, &errorCode, &responseRedacted, &inputTokens, &outputTokens, &costEstimate)
	require.NoError(t, err)
	require.Equal(t, "error", status)
	require.True(t, errorCode.Valid)
	require.Equal(t, "AI.PROPOSAL.INPUTS_CHANGED", errorCode.String)
	require.True(t, responseRedacted.Valid, "the paid provider response must be recorded even though the HTTP caller is denied")
	require.Contains(t, responseRedacted.String, "Step 1", "the rejected provider output must exist in the failed invocation record")
	require.True(t, inputTokens.Valid)
	require.EqualValues(t, 10000, inputTokens.Int64)
	require.True(t, outputTokens.Valid)
	require.EqualValues(t, 10000, outputTokens.Int64)
	require.True(t, costEstimate.Valid)
	require.Greater(t, costEstimate.Float64, float64(0), "the failed attempt must retain its locally estimated cost")
}

// TestProposalCacheDoesNotTrustStaleWorkspaceElevation reuses the same
// elevated visibility bind after the actor's database role is demoted. The
// next proposal must refresh workspace authority before looking up the cache
// or ranking task titles.
func TestProposalCacheDoesNotTrustStaleWorkspaceElevation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	caller := seedProjectRoleMember(t, owner, "editor")
	marker := "AI_CACHE_STALE_ELEVATION_" + randomHex(8)
	candidate := createTaskWithVisibility(t, owner, marker, "private")
	seedProposalCacheEmbedding(t, owner.WorkspacePublicID, candidate, "proposal-cache-stale-elevation-768")
	_, err := testDB.Exec(
		`UPDATE workspace_members wm
		 JOIN workspaces w ON w.id = wm.workspace_id
		 JOIN users u ON u.id = wm.user_id
		 SET wm.role = 'admin'
		 WHERE w.public_id = UUID_TO_BIN(?, 0)
		   AND u.public_id = UUID_TO_BIN(?, 0)`,
		owner.WorkspacePublicID, caller.UserPublicID,
	)
	require.NoError(t, err)

	var mu sync.Mutex
	var prompts []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		raw, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			http.Error(w, "read request", http.StatusBadRequest)
			return
		}
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if decodeErr := json.Unmarshal(raw, &request); decodeErr != nil {
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
		content := `{"suggestedAssignees":[],"subtasks":[{"title":"fresh proposal","description":"","priority":"medium"},{"title":"Review result","description":"Verify the result","priority":"low"}]}`
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":8}}`, content)
	}))
	t.Cleanup(upstream.Close)
	llm, err := providers.New(providers.Config{
		Kind:         providers.KindOpenAICompat,
		Name:         "proposal cache stale elevation fixture",
		BaseURL:      upstream.URL,
		DefaultModel: "gpt-4o-mini",
		EncryptedKey: []byte("test-key"),
	}, proposalCacheTestDecryptor{})
	require.NoError(t, err)
	orchestrator := &ai.Orchestrator{
		Resolver: ai.ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
			return llm, nil
		}),
		ProposalCache: ai.NewProposalCache(time.Minute),
	}
	wsID := mustWorkspaceInternalID(t, owner.WorkspacePublicID)
	actorID := mustUserInternalIDForProposalCache(t, caller.UserPublicID)
	staleElevated := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole("admin"))
	queries := generated.New(testDB)
	embeddings := proposalCacheEmbeddingClient{model: "proposal-cache-stale-elevation-768"}
	propose := func() error {
		_, callErr := orchestrator.ProposeSmartCreate(context.Background(), wsID,
			"Plan the release", "Use the current request text.", embeddings, queries, staleElevated)
		return callErr
	}

	require.NoError(t, propose())
	mu.Lock()
	require.Len(t, prompts, 1)
	require.Contains(t, prompts[0], marker, "admin prompt may include the private candidate")
	mu.Unlock()
	_, err = testDB.Exec(
		`UPDATE workspace_members wm
		 JOIN workspaces w ON w.id = wm.workspace_id
		 JOIN users u ON u.id = wm.user_id
		 SET wm.role = 'member'
		 WHERE w.public_id = UUID_TO_BIN(?, 0)
		   AND u.public_id = UUID_TO_BIN(?, 0)`,
		owner.WorkspacePublicID, caller.UserPublicID,
	)
	require.NoError(t, err)

	require.NoError(t, propose(), "a still-authorized member should receive a proposal rebuilt from their current readable set")
	mu.Lock()
	require.Len(t, prompts, 2, "a role demotion must invalidate the elevated cache entry")
	require.NotContains(t, prompts[1], marker, "the refreshed member prompt must omit the private candidate")
	mu.Unlock()
}

// TestProposalCacheRejectsRemovedMembershipForInputlessSteps holds stale
// visibility across a workspace membership removal. The steps prompt has no
// child or embedding inputs, so workspace access itself must gate the hit.
func TestProposalCacheRejectsRemovedMembershipForInputlessSteps(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	wsID := mustWorkspaceInternalID(t, tenant.WorkspacePublicID)
	actorID := mustUserInternalIDForProposalCache(t, tenant.UserPublicID)
	vis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole("owner"))
	var calls int
	var callsMu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		callsMu.Lock()
		calls++
		callsMu.Unlock()
		content := `[{"title":"Step 1","description":"Check","priority":"low"},{"title":"Step 2","description":"Build","priority":"low"},{"title":"Step 3","description":"Review","priority":"low"}]`
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":8}}`, content)
	}))
	t.Cleanup(upstream.Close)
	llm, err := providers.New(providers.Config{
		Kind:         providers.KindOpenAICompat,
		Name:         "proposal cache missing membership fixture",
		BaseURL:      upstream.URL,
		DefaultModel: "gpt-4o-mini",
		EncryptedKey: []byte("test-key"),
	}, proposalCacheTestDecryptor{})
	require.NoError(t, err)
	orchestrator := &ai.Orchestrator{
		Resolver: ai.ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
			return llm, nil
		}),
		Queries:       generated.New(testDB),
		ProposalCache: ai.NewProposalCache(time.Minute),
	}
	propose := func() error {
		_, callErr := orchestrator.ProposeSteps(context.Background(), wsID,
			"Prepare the weekly report", "Collect the current work.", ai.GranularityCoarse,
			nil, nil, nil, vis)
		return callErr
	}
	require.NoError(t, propose())
	callsMu.Lock()
	require.Equal(t, 1, calls)
	callsMu.Unlock()
	_, err = testDB.Exec(
		`UPDATE workspace_members wm
		 JOIN workspaces w ON w.id = wm.workspace_id
		 JOIN users u ON u.id = wm.user_id
		 SET wm.enabled = FALSE
		 WHERE w.public_id = UUID_TO_BIN(?, 0)
		   AND u.public_id = UUID_TO_BIN(?, 0)`,
		tenant.WorkspacePublicID, tenant.UserPublicID,
	)
	require.NoError(t, err)

	err = propose()
	require.Error(t, err, "workspace removal must refuse a cached result even with no task input references")
	callsMu.Lock()
	require.Equal(t, 1, calls, "the removed member must not reach the provider again")
	callsMu.Unlock()
}

// TestProposalCacheRejectsPromptSnapshotRace pauses the cache snapshot query
// after the prompt's candidate and assignee reads, then changes a quoted title
// or assignee. The stale prompt must never reach the provider; a fresh retry
// must use the changed task data.
func TestProposalCacheRejectsPromptSnapshotRace(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	for _, changedInput := range []string{"candidate title", "candidate assignee"} {
		t.Run(changedInput, func(t *testing.T) {
			t.Parallel()
			assertProposalCacheRejectsPromptSnapshotRace(t, changedInput)
		})
	}
}

func assertProposalCacheRejectsPromptSnapshotRace(t *testing.T, changedInput string) {
	t.Helper()

	tenant := newTenant(t)
	candidateTitle := "AI_CACHE_CAPTURED_TITLE_" + randomHex(8)
	updatedTitle := "AI_CACHE_UPDATED_TITLE_" + randomHex(8)
	candidate := createTaskWithVisibility(t, tenant, candidateTitle, "public")
	var nextAssignee *helpers.TestTenant
	if changedInput == "candidate assignee" {
		nextAssignee = seedProjectRoleMember(t, tenant, "editor")
	}
	seedProposalCacheEmbedding(t, tenant.WorkspacePublicID, candidate, "proposal-cache-snapshot-race-768")
	entered := make(chan struct{})
	release := make(chan struct{})
	queries := &proposalCacheSnapshotBarrierReader{
		Queries: generated.New(testDB),
		entered: entered,
		release: release,
	}
	var mu sync.Mutex
	var prompts []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		raw, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			http.Error(w, "read request", http.StatusBadRequest)
			return
		}
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if decodeErr := json.Unmarshal(raw, &request); decodeErr != nil {
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
		content := `{"suggestedAssignees":[],"subtasks":[{"title":"fresh proposal","description":"","priority":"medium"},{"title":"Review result","description":"Verify the result","priority":"low"}]}`
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":8}}`, content)
	}))
	t.Cleanup(upstream.Close)
	llm, err := providers.New(providers.Config{
		Kind:         providers.KindOpenAICompat,
		Name:         "proposal cache snapshot race fixture",
		BaseURL:      upstream.URL,
		DefaultModel: "gpt-4o-mini",
		EncryptedKey: []byte("test-key"),
	}, proposalCacheTestDecryptor{})
	require.NoError(t, err)
	orchestrator := &ai.Orchestrator{
		Resolver: ai.ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
			return llm, nil
		}),
		ProposalCache: ai.NewProposalCache(time.Minute),
	}
	wsID := mustWorkspaceInternalID(t, tenant.WorkspacePublicID)
	actorID := mustUserInternalIDForProposalCache(t, tenant.UserPublicID)
	vis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole("owner"))
	embeddings := proposalCacheEmbeddingClient{model: "proposal-cache-snapshot-race-768"}
	propose := func() error {
		_, callErr := orchestrator.ProposeSmartCreate(context.Background(), wsID,
			"Plan the deployment", "Use the current request.", embeddings, queries, vis)
		return callErr
	}
	completed := make(chan error, 1)
	go func() { completed <- propose() }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("proposal did not reach the cache input snapshot read")
	}
	if changedInput == "candidate title" {
		_, candidateID := helpers.AgentTaskInternalIDs(t, testDB, tenant.WorkspacePublicID, candidate)
		_, err = testDB.Exec(`UPDATE tasks SET title = ? WHERE id = ?`, updatedTitle, candidateID)
		require.NoError(t, err)
	} else {
		disableTaskUserActor(t, tenant.WorkspacePublicID, candidate, tenant.UserPublicID, "assignee")
		attachTaskUserActor(t, tenant.WorkspacePublicID, candidate, nextAssignee.UserPublicID, "assignee")
	}
	close(release)
	select {
	case err = <-completed:
	case <-time.After(10 * time.Second):
		t.Fatal("proposal did not finish after releasing the snapshot read")
	}
	require.Error(t, err, "a changed captured input between prompt build and snapshot must fail before provider use")
	mu.Lock()
	require.Empty(t, prompts, "stale captured prompt must not reach the LLM")
	mu.Unlock()

	require.NoError(t, propose(), "a fresh request should retry from the current task row")
	mu.Lock()
	require.Len(t, prompts, 1)
	if changedInput == "candidate title" {
		require.Contains(t, prompts[0], updatedTitle)
		require.NotContains(t, prompts[0], candidateTitle)
	} else {
		require.Contains(t, prompts[0], nextAssignee.UserPublicID, "fresh prompt must include the new candidate assignee")
		require.Equal(t, 2, strings.Count(prompts[0], nextAssignee.UserPublicID), "assignee appears in the candidate history and roster")
	}
	mu.Unlock()
}

type proposalCacheSnapshotBarrierReader struct {
	*generated.Queries
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *proposalCacheSnapshotBarrierReader) GetProposalCacheWorkspaceAccess(
	ctx context.Context,
	arg generated.GetProposalCacheWorkspaceAccessParams,
) (interface{}, error) {
	return r.Queries.GetProposalCacheWorkspaceAccess(ctx, arg)
}

func (r *proposalCacheSnapshotBarrierReader) ListProposalCacheTaskInputs(
	ctx context.Context,
	arg generated.ListProposalCacheTaskInputsParams,
) ([]generated.ListProposalCacheTaskInputsRow, error) {
	blocked := false
	r.once.Do(func() {
		blocked = true
		close(r.entered)
	})
	if blocked {
		select {
		case <-r.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return r.Queries.ListProposalCacheTaskInputs(ctx, arg)
}

type proposalCacheTestDecryptor struct{}

func (proposalCacheTestDecryptor) Decrypt(blob []byte) ([]byte, error) {
	return append([]byte(nil), blob...), nil
}

type proposalCacheEmbeddingClient struct {
	model string
	calls *atomic.Int32
}

func (c proposalCacheEmbeddingClient) EmbedQuery(context.Context, uint32, string) ([]float32, error) {
	if c.calls != nil {
		c.calls.Add(1)
	}
	vector := make([]float32, embed.Dim)
	vector[0] = 1
	return vector, nil
}

func (c proposalCacheEmbeddingClient) Model() string { return c.model }

func seedProposalCacheEmbedding(t *testing.T, workspacePublicID, taskPublicID, model string) {
	t.Helper()
	workspaceID, taskID := helpers.AgentTaskInternalIDs(t, testDB, workspacePublicID, taskPublicID)
	vector := make([]float32, embed.Dim)
	vector[0] = 1
	contentHash := fmt.Sprintf("%x", sha256.Sum256([]byte("proposal cache fixture")))
	err := generated.New(testDB).UpsertTaskEmbedding(context.Background(), generated.UpsertTaskEmbeddingParams{
		TaskID:         taskID,
		WorkspaceID:    workspaceID,
		Model:          model,
		Dim:            uint16(embed.Dim),
		StringToVector: embed.Encode(vector),
		ContentHash:    contentHash,
	})
	require.NoError(t, err)
}

func mustUserInternalIDForProposalCache(t *testing.T, userPublicID string) uint32 {
	t.Helper()
	var userID uint32
	require.NoError(t, testDB.QueryRow(`SELECT id FROM users WHERE public_id = UUID_TO_BIN(?, 0)`, userPublicID).Scan(&userID))
	return userID
}
