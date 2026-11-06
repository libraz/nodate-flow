package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/stretchr/testify/require"
)

// TestAiInboxTriageOutputProtocol exercises the caller through the real HTTP
// server and MySQL, with a local OpenAI-compatible server standing in only for
// the external model API. Every decoded response must produce one invocation
// record with actual usage, while rejected outputs must not create proposal
// events or provenance batches.
func TestAiInboxTriageOutputProtocol(t *testing.T) {
	bootstrap(t)
	if aiMockEnabled() {
		t.Skip("NF_FLOW_AI_MOCK is set; this test needs a per-workspace fake provider")
	}

	tests := []struct {
		name              string
		content           func(signalID string) any
		finishReason      string
		wantStatus        int
		wantErrorCode     string
		wantInvocation    string
		wantEvents        int
		wantBatches       int
		wantResponseMatch string
		cacheReplay       bool
		linkedTask        bool
		wantAction        string
	}{
		{
			name:              "incomplete response",
			content:           func(signalID string) any { return inboxTriageSuggestionJSON(signalID) },
			finishReason:      "length",
			wantStatus:        http.StatusBadGateway,
			wantErrorCode:     "AI.RESPONSE.GENERATION_INCOMPLETE",
			wantInvocation:    "error",
			wantResponseMatch: "inboxItemId",
		},
		{
			name:           "provider refusal",
			content:        func(string) any { return nil },
			finishReason:   "content_filter",
			wantStatus:     http.StatusUnprocessableEntity,
			wantErrorCode:  "AI.RESPONSE.MODEL_REFUSED",
			wantInvocation: "blocked",
		},
		{
			name:              "invalid JSON",
			content:           func(string) any { return `[{"inboxItemId":` },
			finishReason:      "stop",
			wantStatus:        http.StatusBadGateway,
			wantErrorCode:     "AI.RESPONSE.INVALID_JSON",
			wantInvocation:    "error",
			wantResponseMatch: "inboxItemId",
		},
		{
			name: "schema-invalid suggestion",
			content: func(signalID string) any {
				return fmt.Sprintf(`[{"inboxItemId":%q,"score":0.9,"recommendedAction":"snooze","reasoning":"Review the item.","unexpected":"extra"}]`, signalID)
			},
			finishReason:      "stop",
			wantStatus:        http.StatusBadGateway,
			wantErrorCode:     "AI.RESPONSE.SCHEMA_MISMATCH",
			wantInvocation:    "error",
			wantResponseMatch: "unexpected",
		},
		{
			name: "suggestion references an uncaptured inbox item",
			content: func(string) any {
				return inboxTriageSuggestionJSON("00000000-0000-7000-8000-000000000001")
			},
			finishReason:      "stop",
			wantStatus:        http.StatusBadGateway,
			wantErrorCode:     "AI.RESPONSE.SCHEMA_MISMATCH",
			wantInvocation:    "error",
			wantResponseMatch: "inboxItemId",
		},
		{
			name:              "open action requires a readable linked task",
			content:           func(signalID string) any { return inboxTriageSuggestionJSON(signalID) },
			finishReason:      "stop",
			wantStatus:        http.StatusBadGateway,
			wantErrorCode:     "AI.RESPONSE.SCHEMA_MISMATCH",
			wantInvocation:    "error",
			wantResponseMatch: "recommendedAction",
		},
		{
			name:           "valid empty result",
			content:        func(string) any { return `[]` },
			finishReason:   "stop",
			wantStatus:     http.StatusOK,
			wantInvocation: "ok",
		},
		{
			name: "valid unlinked suggestion may snooze",
			content: func(signalID string) any {
				return inboxTriageSuggestionJSONForAction(signalID, "snooze")
			},
			finishReason:   "stop",
			wantStatus:     http.StatusOK,
			wantInvocation: "ok",
			wantEvents:     1,
			wantBatches:    1,
			wantAction:     "snooze",
		},
		{
			name:           "valid linked suggestion and actor-private cache replay",
			content:        func(signalID string) any { return inboxTriageSuggestionJSON(signalID) },
			finishReason:   "stop",
			wantStatus:     http.StatusOK,
			wantInvocation: "ok",
			wantEvents:     1,
			wantBatches:    1,
			cacheReplay:    true,
			linkedTask:     true,
			wantAction:     "open",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var providerCalls atomic.Int32
			var signalID string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				providerCalls.Add(1)
				defer r.Body.Close()

				content := tc.content(signalID)
				body, err := json.Marshal(map[string]any{
					"choices": []any{map[string]any{
						"message":       map[string]any{"role": "assistant", "content": content},
						"finish_reason": tc.finishReason,
					}},
					"usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 100},
				})
				if err != nil {
					http.Error(w, "encode fixture", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			}))
			t.Cleanup(upstream.Close)

			tt := newTenant(t)
			if tc.linkedTask {
				signalID = createLinkedManualSignal(t, tt)
			} else {
				signalID = createUnlinkedManualSignal(t, tt)
			}
			registerOpenAICompatProvider(t, tt, upstream.URL)
			workspaceID := helpers.ResolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)
			triageURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/inbox/triage"

			status, body := doJSONStatus(t, http.MethodPost, triageURL, tt.AccessToken, map[string]any{"limit": 10})
			if tc.wantErrorCode != "" {
				require.Equalf(t, tc.wantStatus, status, "triage error; body=%s", string(body))
				require.Equal(t, tc.wantErrorCode, decodeErrorCode(t, body))
			} else {
				require.Equalf(t, tc.wantStatus, status, "triage success; body=%s", string(body))
				var result struct {
					Suggestions []struct {
						InboxItemID       string `json:"inboxItemId"`
						RecommendedAction string `json:"recommendedAction"`
					} `json:"suggestions"`
				}
				require.NoError(t, json.Unmarshal(body, &result))
				if tc.wantEvents == 0 {
					require.Empty(t, result.Suggestions)
				} else {
					require.Len(t, result.Suggestions, 1)
					require.Equal(t, signalID, result.Suggestions[0].InboxItemID)
					require.Equal(t, tc.wantAction, result.Suggestions[0].RecommendedAction)
				}
			}
			require.EqualValues(t, 1, providerCalls.Load(), "one decoded provider response must not trigger another call")

			var invocations int
			require.NoError(t, testDB.QueryRow(
				`SELECT COUNT(*) FROM ai_invocations WHERE workspace_id = ? AND purpose = 'propose_inbox_triage'`, workspaceID,
			).Scan(&invocations))
			require.Equal(t, 1, invocations, "one provider response must produce exactly one invocation outcome")

			var invocationStatus, errorCode, response string
			var inputTokens, outputTokens int
			var costMicros int64
			require.NoError(t, testDB.QueryRow(
				`SELECT status, COALESCE(error_code, ''), COALESCE(response_redacted, ''),
				        tokens_input, tokens_output,
				        CAST(ROUND(cost_estimate * 1000000) AS SIGNED)
				   FROM ai_invocations
				  WHERE workspace_id = ? AND purpose = 'propose_inbox_triage'`, workspaceID,
			).Scan(&invocationStatus, &errorCode, &response, &inputTokens, &outputTokens, &costMicros))
			require.Equal(t, tc.wantInvocation, invocationStatus)
			require.Equal(t, tc.wantErrorCode, errorCode)
			require.Equal(t, 100, inputTokens, "input token usage must come from the decoded provider response")
			require.Equal(t, 100, outputTokens, "output token usage must come from the decoded provider response")
			require.EqualValues(t, 75, costMicros, "cost must be derived from actual provider usage")
			if tc.wantResponseMatch != "" {
				require.Contains(t, response, tc.wantResponseMatch, "the decoded response must be retained in the redacted invocation record")
			}

			var eventCount, batchCount int
			require.NoError(t, testDB.QueryRow(
				`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = 'ai.suggestion.proposed'`, workspaceID,
			).Scan(&eventCount))
			require.Equal(t, tc.wantEvents, eventCount, "rejected output must not append suggestion events")
			require.NoError(t, testDB.QueryRow(
				`SELECT COUNT(*) FROM ai_suggestion_batches WHERE workspace_id = ?`, workspaceID,
			).Scan(&batchCount))
			require.Equal(t, tc.wantBatches, batchCount, "rejected output must not create suggestion provenance batches")

			if tc.cacheReplay {
				var pending struct {
					Suggestions []struct {
						ProposalID  string `json:"proposalId"`
						InboxItemID string `json:"inboxItemId"`
					} `json:"suggestions"`
				}
				doJSON(t, http.MethodGet,
					testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions",
					tt.AccessToken, nil, &pending)
				require.Len(t, pending.Suggestions, 1)
				require.Equal(t, signalID, pending.Suggestions[0].InboxItemID)
				require.NotEmpty(t, pending.Suggestions[0].ProposalID)

				var replay struct {
					Suggestions []struct {
						InboxItemID       string `json:"inboxItemId"`
						RecommendedAction string `json:"recommendedAction"`
					} `json:"suggestions"`
				}
				doJSON(t, http.MethodPost, triageURL, tt.AccessToken, map[string]any{"limit": 10}, &replay)
				require.Len(t, replay.Suggestions, 1)
				require.Equal(t, signalID, replay.Suggestions[0].InboxItemID)
				require.Equal(t, "open", replay.Suggestions[0].RecommendedAction)
				require.EqualValues(t, 1, providerCalls.Load(), "validated actor-private cache replay must avoid another paid call")
				require.NoError(t, testDB.QueryRow(
					`SELECT COUNT(*) FROM ai_invocations WHERE workspace_id = ? AND purpose = 'propose_inbox_triage'`, workspaceID,
				).Scan(&invocations))
				require.Equal(t, 1, invocations, "a cache replay must not create a provider invocation")
				require.NoError(t, testDB.QueryRow(
					`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = 'ai.suggestion.proposed'`, workspaceID,
				).Scan(&eventCount))
				require.Equal(t, 1, eventCount, "a cache replay must not append a duplicate proposal")
				var sourceVersion, batchID string
				require.NoError(t, testDB.QueryRow(
					`SELECT JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.source_version')),
					        JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.suggestion_batch_id'))
					   FROM events
				  WHERE workspace_id = ? AND type = 'ai.suggestion.proposed'`, workspaceID,
				).Scan(&sourceVersion, &batchID))
				require.Regexp(t, `^[0-9a-f]{64}$`, sourceVersion, "the proposal must retain its captured inbox input version")
				var batchOwnerID uint32
				var provenanceValid bool
				require.NoError(t, testDB.QueryRow(
					`SELECT user_id, provenance_valid FROM ai_suggestion_batches
					  WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`, workspaceID, batchID,
				).Scan(&batchOwnerID, &provenanceValid))
				require.Equal(t, helpers.ResolveUserInternalID(t, testDB, tt.UserPublicID), batchOwnerID,
					"the proposal batch must remain owned by the requesting actor")
				require.True(t, provenanceValid, "the suggestion batch must record complete input provenance")
			}
		})
	}
}

func inboxTriageSuggestionJSON(signalID string) string {
	return inboxTriageSuggestionJSONForAction(signalID, "open")
}

func inboxTriageSuggestionJSONForAction(signalID, action string) string {
	return fmt.Sprintf(`[{"inboxItemId":%q,"score":0.9,"recommendedAction":%q,"reasoning":"Review the item."}]`, signalID, action)
}

func createLinkedManualSignal(t *testing.T, tt *helpers.TestTenant) string {
	t.Helper()
	taskID := createTaskWithVisibility(t, tt, "AI_TRIAGE_LINKED_"+randomHex(8), "public")
	workspaceID := helpers.ResolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)
	taskWorkspaceID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)
	require.Equal(t, workspaceID, taskWorkspaceID)
	signalID := uuid.Must(uuid.NewV7())
	_, err := testDB.Exec(`
INSERT INTO signals (public_id, workspace_id, task_id, source, kind, payload_json, received_at)
VALUES (?, ?, ?, 'manual', 'manual', JSON_OBJECT(), NOW(3))`, signalID[:], workspaceID, taskInternalID)
	require.NoError(t, err)
	return signalID.String()
}
