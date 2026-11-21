package e2e

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// smartCreateLongTitleSentinel is the opening of the column-wide title in
// testdata/ai/smart_create.json. It is long enough to identify that subtask.
const smartCreateLongTitleSentinel = "Bounded AAAAA"

// tasksTitleMaxLen is the width of tasks.title in characters. Stated here
// rather than read from the tool so the test fails if the tool's bound is
// loosened past the column.
const tasksTitleMaxLen = 500

// TestMCPSmartCreateBoundsModelTitles holds the boundary between what a
// model returns and what a column accepts.
//
// The caller's own title is bounded by the tool's input schema and the
// proposal's titles by the smart-create output contract, which refuses one
// wider than tasks.title. A title exactly at that width, ending in
// multi-byte characters, must pass the contract and reach the column whole.
func TestMCPSmartCreateBoundsModelTitles(t *testing.T) {
	bootstrap(t)
	requireAIMock(t)
	t.Parallel()

	tt := newTenant(t)
	tok := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"smart-create-bound-token", []string{"read:workspace", "write:workspace"})

	body := mcpCall(t, tok, "tools/call", map[string]any{
		"name": "smart_create_task",
		"arguments": map[string]any{
			"projectId":   tt.ProjectPublicID,
			"title":       "bound the titles the model returns",
			"description": "used to drive the mock provider",
		},
	})
	result := mcpToolTextJSON[struct {
		ID       string `json:"id"`
		Subtasks []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"subtasks"`
	}](t, body)

	require.NotEmptyf(t, result.ID,
		"the batch must survive a column-wide proposed title, body=%s", string(body))

	var found bool
	for _, sub := range result.Subtasks {
		if !strings.HasPrefix(sub.Title, smartCreateLongTitleSentinel) {
			continue
		}
		found = true
		require.NotEmptyf(t, sub.ID, "the column-wide subtask must have been persisted")
		// Equal rather than at-most: the fixture's title is exactly the
		// column width, so anything shorter means it was cut.
		require.Equalf(t, tasksTitleMaxLen, utf8.RuneCountInString(sub.Title),
			"a title at the column width was shortened")
		require.Truef(t, utf8.ValidString(sub.Title),
			"the cut severed a multi-byte character: %q", sub.Title)

		var stored string
		require.NoError(t, testDB.QueryRow(
			`SELECT title FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`, sub.ID,
		).Scan(&stored))
		require.Equalf(t, sub.Title, stored,
			"the row stores something other than what the tool reported")
	}
	require.Truef(t, found,
		"the fixture's column-wide subtask did not come back, body=%s", string(body))
}
