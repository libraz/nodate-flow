// Package signaljudgetests — which tasks a judge prompt is allowed to
// quote.
//
// A judge run has no actor: it starts from a signal, and the prompt it
// builds is stored in ai_invocations.prompt_redacted, which every member
// of the workspace can read. So the reader of a title that reaches the
// prompt is the whole membership, and the only tasks that can take part
// are the ones whose audience already is the whole membership.
//
// That is a property of the shipped statements, and it is asserted here
// against the shipped statements: the lookups under test are the exported
// ones the runner is wired with, seeded through the public API, so a
// title reaching the rendered prompt is a title a judge run would really
// have put there.
package signaljudgetests

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/signaljudge"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestJudgePromptQuotesOnlyTasksTheWholeWorkspaceCanRead seeds one task
// of each audience and drives every route by which a task's title can
// reach the rendered prompt: the workspace's recent tasks, a task
// attached to a calendar event, and a calendar event that is a task's own
// projection.
//
// The two link shapes are driven separately because they are separate
// branches of one UNION, and a bound present on one of them looks like a
// bound on the statement.
func TestJudgePromptQuotesOnlyTasksTheWholeWorkspaceCanRead(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tt := helpers.CreateTestTenant(t, testSrv.BaseURL)
	wsID := resolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)

	// Sentinels rather than words: every assertion below is a substring
	// search over a whole rendered prompt.
	suffix := helpers.RandomHex(8)
	titles := map[string]string{
		"public":  "JUDGE-AUDIENCE-PUBLIC-" + suffix,
		"project": "JUDGE-AUDIENCE-PROJECT-" + suffix,
		"private": "JUDGE-AUDIENCE-PRIVATE-" + suffix,
	}
	// A due date on every task so each of them can be projected onto a
	// calendar event by the from-task route below.
	const dueOn = "2026-05-17"
	taskIDs := map[string]string{}
	for visibility, title := range titles {
		taskIDs[visibility] = seedTaskWithVisibilityAndDue(t, tt, title, visibility, dueOn)
	}

	deps := fixedNowDeps(testDB)

	t.Run("the workspace's recent tasks", func(t *testing.T) {
		signal := insertWorkspaceSignal(t, testDB, wsID, "manual", map[string]any{
			"probe": "recent-tasks-audience",
		})
		pc, err := signaljudge.BuildPromptContext(ctx, deps, signal)
		require.NoError(t, err)

		require.Equal(t, []string{titles["public"]}, summaryTitles(pc.RecentTasks),
			"the recent-tasks lookup must return the public task and only the public task; "+
				"a judge run has no actor to decide who among the workspace may read the rest")
		requireOnlyThePublicTitle(t, signaljudge.RenderUserPrompt(pc), titles)
	})

	t.Run("a task attached to a calendar event", func(t *testing.T) {
		calID := seedCalendarViaAPI(t, tt, "Judge Audience Cal "+suffix)
		startAt := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
		eventPub := seedCalendarEventViaAPI(t, tt, calID,
			"judge-audience-event-"+suffix, startAt, startAt.Add(time.Hour))
		for _, id := range taskIDs {
			linkTaskToEventViaAPI(t, tt, id, eventPub)
		}
		eventInternalID := resolveCalendarEventInternalID(t, testDB, wsID, eventPub)

		signal := insertCalendarEventSignal(t, testDB, wsID, eventInternalID,
			"calendar.event_day_arrived", map[string]any{"event_id": eventPub})
		pc, err := signaljudge.BuildPromptContext(ctx, deps, signal)
		require.NoError(t, err)

		require.Equal(t, []string{titles["public"]}, summaryTitles(pc.LinkedTasks),
			"attaching a task to a calendar event does not widen its audience, so only the "+
				"public one may be named in the prompt the event's signal builds")
		requireOnlyThePublicTitle(t, signaljudge.RenderUserPrompt(pc), titles)
	})

	t.Run("a calendar event that is a task's own projection", func(t *testing.T) {
		calID := seedCalendarViaAPI(t, tt, "Judge Projection Cal "+suffix)
		// The private task's due date, projected onto the calendar. The
		// event carries the task on calendar_events.task_id, which is the
		// UNION's other branch — reached by no link row at all.
		eventPub := seedEventFromTask(t, tt, calID, taskIDs["private"])
		eventInternalID := resolveCalendarEventInternalID(t, testDB, wsID, eventPub)

		signal := insertCalendarEventSignal(t, testDB, wsID, eventInternalID,
			"calendar.event_day_arrived", map[string]any{"event_id": eventPub})
		pc, err := signaljudge.BuildPromptContext(ctx, deps, signal)
		require.NoError(t, err)

		require.Empty(t, summaryTitles(pc.LinkedTasks),
			"the only task this event projects is private, so the linked-tasks section has "+
				"nothing it may name")
		requireOnlyThePublicTitle(t, signaljudge.RenderUserPrompt(pc), titles)
	})
}

// requireOnlyThePublicTitle asserts the rendered prompt names the public
// task and neither of the narrower ones.
//
// The rendered text is checked as well as the context because the prompt
// is the artifact that is stored and read: a title dropped from
// RecentTasks but reached through some other section would be just as
// readable to the workspace.
func requireOnlyThePublicTitle(t *testing.T, rendered string, titles map[string]string) {
	t.Helper()
	require.Contains(t, rendered, titles["public"],
		"the public task's title is missing from the prompt; the lookups returned nothing and "+
			"the absences below would prove nothing")
	for _, narrower := range []string{"project", "private"} {
		require.NotContainsf(t, rendered, titles[narrower],
			"a %s task's title reached the judge prompt, which is stored in "+
				"ai_invocations.prompt_redacted and readable by every member of the workspace — "+
				"including the members that visibility keeps the task from", narrower)
	}
}

// summaryTitles projects the titles out of a prompt section, in order.
func summaryTitles(rows []signaljudge.TaskSummary) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Title)
	}
	return out
}

// seedTaskWithVisibilityAndDue creates a task through POST /tasks at the
// given audience with a due date, and returns its public id.
func seedTaskWithVisibilityAndDue(t *testing.T, tt *helpers.TestTenant, title, visibility, dueOn string) string {
	t.Helper()
	var created struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, testSrv.BaseURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId":  tt.ProjectPublicID,
		"title":      title,
		"visibility": visibility,
		"dueOn":      dueOn,
	}, &created)
	require.NotEmpty(t, created.ID, "POST /tasks returned no id for a %s task", visibility)
	return created.ID
}

// seedEventFromTask projects a task's due date onto a calendar through
// POST /calendars/{id}/events/from-task, which is what writes
// calendar_events.task_id, and returns the new event's public id.
func seedEventFromTask(t *testing.T, tt *helpers.TestTenant, calendarPublicID, taskPublicID string) string {
	t.Helper()
	var created struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost,
		testSrv.BaseURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars/"+calendarPublicID+"/events/from-task",
		tt.AccessToken, map[string]any{"taskId": taskPublicID, "timezone": "UTC"}, &created)
	require.NotEmpty(t, created.ID, "POST /events/from-task returned no id")
	return created.ID
}
