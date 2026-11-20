package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type prioritySuggestion struct {
	TaskID          string   `json:"taskId"`
	Reasons         []string `json:"reasons"`
	DependentCount  int64    `json:"dependentCount"`
	SuggestedPrio   int32    `json:"suggestedPriority"`
	CurrentPriority int32    `json:"currentPriority"`
}

func listPrioritySuggestions(t *testing.T, token, wsID string) (map[string]prioritySuggestion, []byte) {
	t.Helper()
	status, raw := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+wsID+"/ai/priority-suggestions", token, nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var out struct {
		Suggestions []prioritySuggestion `json:"suggestions"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/workspaces/"+wsID+"/ai/priority-suggestions", token, nil, &out)
	byID := map[string]prioritySuggestion{}
	for _, s := range out.Suggestions {
		byID[s.TaskID] = s
	}
	return byID, raw
}

// TestPrioritySuggestionsCarryReasonCodes pins that suggestions expose
// machine-readable reason codes, never a free-text reason.
func TestPrioritySuggestionsCarryReasonCodes(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	day := func(offset int) string { return time.Now().UTC().AddDate(0, 0, offset).Format("2006-01-02") }
	mk := func(title, due string) string {
		var out struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
			"projectId": tt.ProjectPublicID, "title": title, "dueOn": due,
		}, &out)
		require.NotEmpty(t, out.ID)
		return out.ID
	}
	overdue := mk("prio overdue", day(-5))
	week := mk("prio week", day(5))
	fortnight := mk("prio fortnight", day(12))

	got, raw := listPrioritySuggestions(t, tt.AccessToken, tt.WorkspacePublicID)
	require.NotContains(t, string(raw), `"reason":`, "free-text reason was replaced by reasons")

	require.Contains(t, got[overdue].Reasons, "overdue")
	require.Contains(t, got[week].Reasons, "due_this_week")
	require.Contains(t, got[fortnight].Reasons, "due_this_fortnight")
	for _, id := range []string{overdue, week, fortnight} {
		require.NotNil(t, got[id].Reasons, "reasons is a list, never null")
	}
}

// TestPriorityManyDependents pins that a task blocking three others is
// reported with many_dependents and the count, while one blocking a
// single task is not.
func TestPriorityManyDependents(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	blocker := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Bottleneck")
	minor := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Minor blocker")
	for i, title := range []string{"Dependent A", "Dependent B", "Dependent C"} {
		dep := createTask(t, tt.AccessToken, tt.ProjectPublicID, title)
		doJSON(t, http.MethodPost, testServerURL+"/tasks/"+dep+"/dependencies", tt.AccessToken,
			map[string]any{"toTaskId": blocker, "kind": "blocks"}, nil)
		if i == 0 {
			doJSON(t, http.MethodPost, testServerURL+"/tasks/"+dep+"/dependencies", tt.AccessToken,
				map[string]any{"toTaskId": minor, "kind": "blocks"}, nil)
		}
	}

	got, _ := listPrioritySuggestions(t, tt.AccessToken, tt.WorkspacePublicID)
	require.Contains(t, got[blocker].Reasons, "many_dependents")
	require.Equal(t, int64(3), got[blocker].DependentCount)
	require.NotContains(t, got[minor].Reasons, "many_dependents")
	require.Equal(t, int64(1), got[minor].DependentCount)
}
