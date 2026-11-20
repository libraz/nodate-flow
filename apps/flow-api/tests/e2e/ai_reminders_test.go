package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAiReminders creates two tasks — one with a past due date and one
// without — and hits the workspace reminder endpoint. The
// overdue task must appear in the reminder list with kind "overdue"
// and a negative daysUntilDue, while the other must be absent. The
// payload carries no free-text message: clients render from kind.
func TestAiReminders(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	past := time.Now().UTC().AddDate(0, 0, -3).Format("2006-01-02")

	var overdue struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId":   tt.ProjectPublicID,
		"title":       "Ship the overdue migration",
		"description": "This should light up the reminder feed.",
		"priority":    2,
		"dueOn":       past,
	}, &overdue)
	require.NotEmpty(t, overdue.ID)

	var quiet struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "A calm task with no due date",
		"priority":  1,
	}, &quiet)

	var out struct {
		Total     int `json:"total"`
		Reminders []struct {
			TaskID       string `json:"taskId"`
			Kind         string `json:"kind"`
			DaysUntilDue int    `json:"daysUntilDue"`
		} `json:"reminders"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/reminders",
		tt.AccessToken, nil, &out)

	require.GreaterOrEqual(t, out.Total, 2)
	require.Len(t, out.Reminders, 1, "only the overdue task should produce a reminder")
	top := out.Reminders[0]
	require.Equal(t, overdue.ID, top.TaskID)
	require.Equal(t, "overdue", top.Kind)
	require.Less(t, top.DaysUntilDue, 0)

	status, raw := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/reminders",
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	require.NotContains(t, string(raw), `"message"`, "reminders must not carry a message field")
}
