package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApplyStepsChildrenInheritParentVisibility holds REST apply-steps to
// the derived-task rule: a step is the parent's plan written out, so it
// carries the parent's audience instead of the public create default.
func TestApplyStepsChildrenInheritParentVisibility(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	for _, visibility := range []string{"private", "project"} {
		parentID := createTaskWithVisibility(t, owner, "Vis steps parent "+visibility, visibility)

		var applied struct {
			Created []string `json:"created"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks/"+parentID+"/apply-steps",
			owner.AccessToken, map[string]any{
				"steps": []map[string]any{{"title": "step one"}, {"title": "step two"}},
			}, &applied)
		require.Len(t, applied.Created, 2, "every step must be created")

		for _, childID := range applied.Created {
			var got string
			require.NoError(t, testDB.QueryRow(
				`SELECT visibility FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`, childID,
			).Scan(&got))
			assert.Equal(t, visibility, got,
				"a step applied to a %s parent must not be wider than its parent", visibility)
		}
	}
}
