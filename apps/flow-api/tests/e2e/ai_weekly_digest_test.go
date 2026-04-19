package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAiWeeklyDigest creates two tasks — one with a past due date and
// one without — and hits the weekly digest endpoint. The
// overdue task must appear in overdueOpen and in the rendered
// markdown, counts must include both, and the markdown header must
// carry today's date.
func TestAiWeeklyDigest(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	past := time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")

	var overdue struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Migrate billing cron",
		"priority":  2,
		"dueOn":     past,
	}, &overdue)
	require.NotEmpty(t, overdue.ID)

	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Write onboarding copy",
		"priority":  1,
	}, &struct {
		ID string `json:"id"`
	}{})

	var out struct {
		Counts struct {
			Open    int `json:"open"`
			Waiting int `json:"waiting"`
		} `json:"counts"`
		OverdueOpen []struct {
			TaskID string `json:"taskId"`
			Title  string `json:"title"`
			Date   string `json:"date"`
		} `json:"overdueOpen"`
		CompletedThisWeek []struct {
			TaskID string `json:"taskId"`
		} `json:"completedThisWeek"`
		Markdown string `json:"markdown"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/weekly-digest",
		tt.AccessToken, nil, &out)

	require.GreaterOrEqual(t, out.Counts.Open, 2, "both created tasks should count toward open")
	require.Len(t, out.OverdueOpen, 1)
	require.Equal(t, overdue.ID, out.OverdueOpen[0].TaskID)
	require.Equal(t, past, out.OverdueOpen[0].Date)
	require.Empty(t, out.CompletedThisWeek)
	require.True(t, strings.HasPrefix(out.Markdown, "# Weekly digest"))
	require.Contains(t, out.Markdown, "Migrate billing cron")
}
