package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestExportWithLensAppliesTheLensFilter holds the lens export to the
// set the lens names. The lens here is workspace-wide, which is the case
// that used to fall back to exporting the whole workspace, and its filter
// names two states and two non-adjacent priorities so a reader that drops
// either predicate exports rows nobody selected.
func TestExportWithLensAppliesTheLensFilter(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	specs := []lensParityTask{
		{title: "export open p1", priority: 1, inLens: true},
		{title: "export done p4", priority: 4, transition: "complete", inLens: true},
		{title: "export open p2", priority: 2, inLens: false},
		{title: "export waiting p1", priority: 1, transition: "block", inLens: false},
	}
	ids := make(map[string]lensParityTask, len(specs))
	expected := map[string]bool{}
	for _, spec := range specs {
		id := createParityTask(t, tt, spec)
		ids[id] = spec
		if spec.inLens {
			expected[id] = true
		}
	}

	var lens struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/lenses", tt.AccessToken, map[string]any{
		"name":      "Export lens " + randomHex(4),
		"filter":    json.RawMessage(`{"status":{"values":["open","done"]},"priority":{"values":[1,4]}}`),
		"sort":      json.RawMessage(`[]`),
		"isDefault": false,
	}, &lens)
	require.NotEmpty(t, lens.ID)

	var out struct {
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/export/tasks?format=json&lensId="+lens.ID,
		tt.AccessToken, nil, &out)

	exported := map[string]bool{}
	for _, task := range out.Tasks {
		if _, ours := ids[task.ID]; ours {
			exported[task.ID] = true
		}
	}
	require.Equal(t, sortedTitles(t, ids, expected), sortedTitles(t, ids, exported),
		"a lens export must contain exactly the tasks the lens filter selects, not the whole workspace")
}
