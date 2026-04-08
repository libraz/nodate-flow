package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAiInvocationsListEmpty exercises GET /workspaces/{wsId}/ai/invocations
// for a fresh tenant that has never made an LLM call. The endpoint
// must return a non-nil empty slice (never null) so the AI reasoning
// panel can distinguish "no calls yet" from "endpoint broken".
func TestAiInvocationsListEmpty(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var out struct {
		Invocations []struct {
			ID      string `json:"id"`
			Purpose string `json:"purpose"`
		} `json:"invocations"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/invocations",
		tt.AccessToken, nil, &out)

	require.NotNil(t, out.Invocations, "invocations slice must not be nil even when empty")
	require.Empty(t, out.Invocations)
}
