package e2e

import (
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTaskDuplicatesProposal creates three tasks — two with nearly
// identical titles + descriptions and one clearly different — and
// verifies GET /tasks/{id}/duplicates ranks the twin above the
// unrelated task. The mock embedder is deterministic (splitmix64 from
// sha256), so vectors for identical text are identical and cosine is
// 1.0, while unrelated text falls well below the 0.75 low threshold.
func TestTaskDuplicatesProposal(t *testing.T) {
	bootstrap(t)
	if os.Getenv("NF_FLOW_AI_MOCK") == "" {
		t.Skip("set NF_AI_MOCK=1 to run duplicate-detection e2e tests")
	}
	t.Parallel()

	tt := newTenant(t)

	create := func(title, description string) string {
		var resp struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
			"projectId":   tt.ProjectPublicID,
			"title":       title,
			"description": description,
			"priority":    1,
		}, &resp)
		require.NotEmpty(t, resp.ID)
		return resp.ID
	}

	sourceID := create("Investigate login bug", "Users cannot log in after password reset.")
	// Identical text → cosine 1.0, must classify as duplicate.
	twinID := create("Investigate login bug", "Users cannot log in after password reset.")
	// Unrelated text → cosine well below 0.75.
	_ = create("Update billing invoice PDF template", "Redesign the quarterly invoice layout.")

	var out struct {
		Source     string `json:"source"`
		Model      string `json:"model"`
		Candidates []struct {
			TaskID         string  `json:"taskId"`
			Title          string  `json:"title"`
			Score          float64 `json:"score"`
			Classification string  `json:"classification"`
		} `json:"candidates"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks/"+sourceID+"/duplicates",
		tt.AccessToken, nil, &out)

	require.Equal(t, sourceID, out.Source)
	require.Equal(t, "mock-768", out.Model)
	require.NotEmpty(t, out.Candidates, "at least the identical twin must rank")
	top := out.Candidates[0]
	require.Equal(t, twinID, top.TaskID, "identical-text twin must rank first")
	require.Equal(t, "duplicate", top.Classification)
	require.InDelta(t, 1.0, top.Score, 1e-4, "cosine of identical vectors should be 1.0")
}
