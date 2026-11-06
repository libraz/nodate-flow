package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/stretchr/testify/require"
)

func skipIfTriageAIMock(t *testing.T) {
	t.Helper()
	if mock := os.Getenv("NF_FLOW_AI_MOCK"); mock != "" && mock != "0" && mock != "false" {
		t.Skip("NF_FLOW_AI_MOCK is set; this test exercises the workspace provider resolver")
	}
}

func createUnlinkedManualSignal(t *testing.T, tt *helpers.TestTenant) string {
	t.Helper()
	var signal struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/signals", tt.AccessToken, map[string]any{
		"workspaceId": tt.WorkspacePublicID,
		"source":      "manual",
		"kind":        "manual",
	}, &signal)
	require.NotEmpty(t, signal.ID)
	return signal.ID
}

func TestAiTriageWithoutEnabledProviderPersistsExecutableFallback(t *testing.T) {
	bootstrap(t)
	skipIfTriageAIMock(t)
	t.Parallel()

	tt := newTenant(t)
	signalID := createUnlinkedManualSignal(t, tt)
	workspaceID := helpers.ResolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)
	var providerCount int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM ai_providers WHERE workspace_id = ? AND enabled = TRUE`, workspaceID,
	).Scan(&providerCount))
	require.Zero(t, providerCount, "fresh workspace must have no enabled provider")

	var triage struct {
		Suggestions []struct {
			InboxItemID       string `json:"inboxItemId"`
			RecommendedAction string `json:"recommendedAction"`
		} `json:"suggestions"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/inbox/triage",
		tt.AccessToken, map[string]any{"limit": 10}, &triage)
	require.Len(t, triage.Suggestions, 1)
	require.Equal(t, signalID, triage.Suggestions[0].InboxItemID)
	require.Equal(t, "snooze", triage.Suggestions[0].RecommendedAction,
		"a fresh unlinked manual item deterministically recommends snooze")

	var persisted struct {
		Suggestions []struct {
			ProposalID          string `json:"proposalId"`
			InboxItemID         string `json:"inboxItemId"`
			Revision            int    `json:"revision"`
			RecommendedAction   string `json:"recommendedAction"`
			SourceVersionStatus string `json:"sourceVersionStatus"`
		} `json:"suggestions"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions",
		tt.AccessToken, nil, &persisted)
	require.Len(t, persisted.Suggestions, 1, "fallback proposal must persist for the requesting actor")
	suggestion := persisted.Suggestions[0]
	require.NotEmpty(t, suggestion.ProposalID)
	require.Equal(t, signalID, suggestion.InboxItemID)
	require.Equal(t, "snooze", suggestion.RecommendedAction)
	require.Equal(t, "captured", suggestion.SourceVersionStatus)

	var sourceVersion, batchID string
	require.NoError(t, testDB.QueryRow(
		`SELECT JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.source_version')),
		        JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.suggestion_batch_id'))
		   FROM events
		  WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)
		    AND type = 'ai.suggestion.proposed'`,
		workspaceID, suggestion.ProposalID,
	).Scan(&sourceVersion, &batchID))
	require.Regexp(t, `^[0-9a-f]{64}$`, sourceVersion, "proposal must retain its captured inbox source version")
	require.NotEmpty(t, batchID)
	var batchOwnerID, batchInputCount uint32
	var batchValid bool
	require.NoError(t, testDB.QueryRow(
		`SELECT user_id, input_count, provenance_valid
		   FROM ai_suggestion_batches
		  WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		workspaceID, batchID,
	).Scan(&batchOwnerID, &batchInputCount, &batchValid))
	require.Equal(t, helpers.ResolveUserInternalID(t, testDB, tt.UserPublicID), batchOwnerID,
		"the persisted empty task-reference set remains owned by the requesting actor")
	require.Zero(t, batchInputCount, "an unlinked manual item contributes no task references")
	require.True(t, batchValid, "an empty task-reference set is still a complete valid batch")

	var auditCount int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM audit_logs
		 WHERE workspace_id = ? AND action = 'inbox.ai_propose'
		   AND resource_type = 'signal' AND resource_public_id = UUID_TO_BIN(?, 0)`,
		workspaceID, signalID,
	).Scan(&auditCount))
	require.Equal(t, 1, auditCount, "a persisted fallback proposal must have its paired audit record")

	snoozeUntil := time.Now().Add(time.Hour).Unix()
	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions/"+signalID+"/execute",
		tt.AccessToken, map[string]any{
			"operationKey":     "fallback-snooze-" + randomHex(8),
			"proposalId":       suggestion.ProposalID,
			"expectedRevision": suggestion.Revision,
			"action":           "snooze",
			"snoozeUntil":      snoozeUntil,
		})
	require.Equalf(t, http.StatusOK, status, "execute fallback snooze body=%s", string(raw))
	var execution struct {
		Status      string `json:"status"`
		Action      string `json:"action"`
		InboxItemID string `json:"inboxItemId"`
	}
	require.NoError(t, json.Unmarshal(raw, &execution))
	require.Equal(t, "executed", execution.Status)
	require.Equal(t, "snooze", execution.Action)
	require.Equal(t, signalID, execution.InboxItemID)

	var inbox struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/inbox?workspaceId="+tt.WorkspacePublicID,
		tt.AccessToken, nil, &inbox)
	for _, item := range inbox.Items {
		require.NotEqual(t, signalID, item.ID, "fresh inbox reload must hide the snoozed source")
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions",
		tt.AccessToken, nil, &persisted)
	require.Empty(t, persisted.Suggestions, "executed fallback proposal must leave the pending list")
}

func TestAiTriageProviderFailuresDoNotUseDeterministicFallback(t *testing.T) {
	bootstrap(t)
	skipIfTriageAIMock(t)

	cases := []struct {
		name      string
		status    int
		body      string
		wantError string
	}{
		{
			name:      "upstream auth rejection",
			status:    http.StatusUnauthorized,
			body:      `{"error":{"message":"bad key","type":"invalid_api_key"}}`,
			wantError: "AI.PROVIDER.UPSTREAM_AUTH_REJECTED",
		},
		{
			name:      "invalid upstream response",
			status:    http.StatusOK,
			body:      `<<<not even json>>>`,
			wantError: "AI.RESPONSE.INVALID_JSON",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var hits atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(upstream.Close)

			tt := newTenant(t)
			signalID := createUnlinkedManualSignal(t, tt)
			registerOpenAICompatProvider(t, tt, upstream.URL)

			status, raw := doJSONStatus(t, http.MethodPost,
				testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/inbox/triage",
				tt.AccessToken, map[string]any{"limit": 10})
			require.Equalf(t, http.StatusBadGateway, status, "provider error must propagate; body=%s", string(raw))
			require.Truef(t, strings.Contains(string(raw), tc.wantError),
				"provider error code %q must propagate; body=%s", tc.wantError, string(raw))
			require.Greater(t, hits.Load(), int32(0), "the configured fake upstream must receive the call")

			var pending struct {
				Suggestions []struct {
					InboxItemID string `json:"inboxItemId"`
				} `json:"suggestions"`
			}
			doJSON(t, http.MethodGet,
				testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions",
				tt.AccessToken, nil, &pending)
			for _, suggestion := range pending.Suggestions {
				require.NotEqual(t, signalID, suggestion.InboxItemID,
					"upstream failures must not create a deterministic proposal")
			}
		})
	}
}
