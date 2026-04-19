package e2e

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestAiAgentPauseNotFound exercises the kill-switch route.
// It only verifies the 404 branch (unknown agentId) because creating
// a real ai_agents row requires the full ai_providers / ai_models
// dependency chain, which is out of scope for this smoke test. The
// happy path is covered by unit tests against the handler directly.
func TestAiAgentPauseNotFound(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// A well-formed but non-existent agent public id.
	unknown := uuid.Must(uuid.NewV7()).String()

	url := testServerURL + "/workspaces/" + tt.WorkspacePublicID +
		"/ai/agents/" + unknown + "/pause"
	status, body := doJSONStatus(t, http.MethodPost, url, tt.AccessToken, map[string]any{
		"paused": true,
	})
	require.Equalf(t, http.StatusNotFound, status, "body=%s", string(body))
	require.Contains(t, string(body), "AI.AGENT.NOT_FOUND")
}
