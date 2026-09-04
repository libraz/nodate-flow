package e2e

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// smartCreateLongTitleSentinel is the opening of the deliberately
// overlong title in testdata/ai/smart_create.json. It is long enough to
// identify that subtask and short enough to survive the cut.
const smartCreateLongTitleSentinel = "Overlong AAAAA"

// tasksTitleMaxLen is the width of tasks.title. Stated here rather than
// read from the tool so the test fails if the tool's bound is loosened
// past the column.
const tasksTitleMaxLen = 255

// TestMCPSmartCreateBoundsModelTitles holds the boundary between what a
// model returns and what a column accepts.
//
// The caller's own title is bounded by the tool's input schema; the
// titles in the proposal are not bounded by anything, and each one is
// written straight into tasks.title. An overlong one failed the insert
// and took the whole batch — the parent task included — down with it,
// after the LLM call had already been paid for.
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
		"the batch must survive an overlong proposed title, body=%s", string(body))

	var found bool
	for _, sub := range result.Subtasks {
		if !strings.HasPrefix(sub.Title, smartCreateLongTitleSentinel) {
			continue
		}
		found = true
		require.NotEmptyf(t, sub.ID, "the overlong subtask must have been persisted")
		require.LessOrEqualf(t, len(sub.Title), tasksTitleMaxLen,
			"the proposed title reached the column unbounded")
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
		"the fixture's overlong subtask did not come back, body=%s", string(body))
}
