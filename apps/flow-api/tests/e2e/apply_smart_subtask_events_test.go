package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// A subtask created through apply-smart is a task in its own right, so its
// history has to start where every other task's does. tasks.derived_state
// is read from the event log, and an event addressed to the wrong row is
// not in that task's history at all.
//
// Nothing on the request path says whether the row was written: the append
// is best-effort, so a refused INSERT is logged and the response is
// unchanged. That is what makes the events table the only place this can
// be asserted.

// taskCreatedEvents returns the task.created events addressed to a task,
// found through events.task_id rather than through the payload. The column
// is the assertion: it is a foreign key onto tasks.id, so an event
// appended under any other id is not reachable from the row it describes.
func taskCreatedEvents(t *testing.T, taskPublicID string) []map[string]any {
	t.Helper()

	rows, err := testDB.Query(
		`SELECT e.payload_json
		   FROM events e
		   JOIN tasks t ON t.id = e.task_id
		  WHERE t.public_id = UUID_TO_BIN(?, 0) AND e.type = 'task.created'`,
		taskPublicID,
	)
	require.NoErrorf(t, err, "read task.created events of task %s", taskPublicID)
	defer func() { _ = rows.Close() }()

	out := []map[string]any{}
	for rows.Next() {
		var raw []byte
		require.NoError(t, rows.Scan(&raw))
		var payload map[string]any
		require.NoErrorf(t, json.Unmarshal(raw, &payload),
			"task.created payload of task %s is not an object: %s", taskPublicID, string(raw))
		out = append(out, payload)
	}
	require.NoError(t, rows.Err())
	return out
}

// requireOwnCreatedEvent asserts a task has exactly one task.created event
// addressed to its own row, and that the payload describes that same task.
// Matching the payload too is what keeps the ids aligned: an event carried
// out of a batch under a sibling's internal id would still be reachable
// from a task, just not from the one it names.
func requireOwnCreatedEvent(t *testing.T, taskPublicID, wantTitle, surface string) {
	t.Helper()

	events := taskCreatedEvents(t, taskPublicID)
	require.Lenf(t, events, 1,
		"%s left task %s without exactly one task.created event of its own", surface, taskPublicID)
	require.Equalf(t, taskPublicID, events[0]["taskId"],
		"%s addressed task %s with an event naming another task", surface, taskPublicID)
	require.Equalf(t, wantTitle, events[0]["title"],
		"%s recorded a title the task %s was never stored under", surface, taskPublicID)
}

// TestRESTApplySmartSubtaskCreatedEvents holds that every task apply-smart
// creates gets its own creation event, subtasks included. The parent is
// asserted alongside them so the two halves of the same request are read
// together: a path that emits for the parent and drops the children looks
// identical from the response.
func TestRESTApplySmartSubtaskCreatedEvents(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	const (
		parentTitle = "apply-smart parent, creation events"
		firstTitle  = "apply-smart subtask one, creation events"
		secondTitle = "apply-smart subtask two, creation events"
	)

	// Two subtasks rather than one: with a single child, an event
	// addressed to the parent by mistake and one addressed to the child
	// are the same count, and a batch is where the ids get crossed.
	var applied struct {
		TaskID     string   `json:"taskId"`
		SubtaskIDs []string `json:"subtaskIds"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/tasks/apply-smart",
		tt.AccessToken, map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     parentTitle,
			"priority":  0,
			"subtasks": []map[string]any{
				{"title": firstTitle, "priority": 0},
				{"title": secondTitle, "priority": 0},
			},
		}, &applied)
	require.NotEmpty(t, applied.TaskID)
	require.Len(t, applied.SubtaskIDs, 2)

	requireOwnCreatedEvent(t, applied.TaskID, parentTitle, "REST apply-smart parent")
	requireOwnCreatedEvent(t, applied.SubtaskIDs[0], firstTitle, "REST apply-smart first subtask")
	requireOwnCreatedEvent(t, applied.SubtaskIDs[1], secondTitle, "REST apply-smart second subtask")
}

// TestRESTApplyStepsChildCreatedEvents holds the same rule on the sibling
// batch path. apply-steps carries its children's internal ids out of the
// transaction already; pinning it here is what keeps the two paths from
// diverging again, since only one of them was ever wrong.
func TestRESTApplyStepsChildCreatedEvents(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	const (
		firstStep  = "apply-steps child one, creation events"
		secondStep = "apply-steps child two, creation events"
	)

	var parent struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "parent for apply-steps creation events",
	}, &parent)
	require.NotEmpty(t, parent.ID)

	var applied struct {
		Created []string `json:"created"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+parent.ID+"/apply-steps",
		tt.AccessToken, map[string]any{
			"steps": []map[string]any{
				{"title": firstStep},
				{"title": secondStep},
			},
		}, &applied)
	require.Len(t, applied.Created, 2)

	requireOwnCreatedEvent(t, applied.Created[0], firstStep, "REST apply-steps first child")
	requireOwnCreatedEvent(t, applied.Created[1], secondStep, "REST apply-steps second child")
}
