package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAiCompileLensHappyAndSad exercises POST /ai/compile-lens against
// the mock NL query fixture: a known-good prompt compiles to a
// populated Lens, an unknown prompt returns 422
// AI.NL_QUERY.UNPARSEABLE (ADR 0004).
func TestAiCompileLensHappyAndSad(t *testing.T) {
	bootstrap(t)
	if os.Getenv("NF_AI_MOCK") == "" {
		t.Skip("set NF_AI_MOCK=1 to run NL query e2e tests")
	}
	t.Parallel()

	tt := newTenant(t)
	url := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/ai/compile-lens"

	// Happy path — fixture key from testdata/ai/nl_query.json.
	var happy struct {
		Prompt string `json:"prompt"`
		Lens   struct {
			Filter  map[string]map[string]any `json:"filter"`
			Sort    []map[string]string       `json:"sort"`
			GroupBy *string                   `json:"groupBy"`
		} `json:"lens"`
	}
	doJSON(t, http.MethodPost, url, tt.AccessToken,
		map[string]any{"prompt": "show me everything blocked this week"}, &happy)
	require.Equal(t, "show me everything blocked this week", happy.Prompt)
	require.Contains(t, happy.Lens.Filter, "blocked",
		"compiled lens must carry the blocked predicate")
	require.NotEmpty(t, happy.Lens.Sort)

	// Sad path — prompt that isn't in the fixture map must 422.
	body, _ := json.Marshal(map[string]any{"prompt": "render me a poem about dragons"})
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("authorization", "Bearer "+tt.AccessToken)
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	raw, _ := io.ReadAll(resp.Body)
	require.Contains(t, string(raw), "AI.NL_QUERY.UNPARSEABLE",
		"422 body must carry the error code, got=%s", string(raw))
}
