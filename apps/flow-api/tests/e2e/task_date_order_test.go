package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// The three dates every case in this file is built from, named for where
// they sit relative to each other. They are fixed literals rather than
// offsets from time.Now() so a run near midnight or under a different
// session timezone still compares the same pair of days.
const (
	dateOrderStart = "2031-03-10"
	dateOrderLater = "2031-03-17"
	dateOrderPrior = "2031-03-03"
)

// createTaskWithDates posts a task carrying whichever of the two dates is
// non-empty and returns the decoded response. An empty string is left out
// of the body entirely, which is how a caller says "this task has no such
// date" — sending "" would be a date the handler has to parse.
func createTaskWithDates(t *testing.T, token, projectID, title, startOn, dueOn string) (int, []byte) {
	t.Helper()
	body := map[string]any{"projectId": projectID, "title": title}
	if startOn != "" {
		body["startOn"] = startOn
	}
	if dueOn != "" {
		body["dueOn"] = dueOn
	}
	return doJSONStatus(t, http.MethodPost, testServerURL+"/tasks", token, body)
}

// taskDates is the slice of the task DTO these tests read back. The
// request body names the start date `startOn` and the response names it
// `startedOn`; both spellings appear here on purpose.
type taskDates struct {
	ID        string `json:"id"`
	DueOn     string `json:"dueOn"`
	StartedOn string `json:"startedOn"`
}

// decodeTaskDates decodes a task write response into the date slice.
func decodeTaskDates(t *testing.T, body []byte) taskDates {
	t.Helper()
	var got taskDates
	require.NoErrorf(t, json.Unmarshal(body, &got), "decode task body=%s", string(body))
	return got
}

// requireDueBeforeStart asserts a refusal landed on exactly the
// date-order code. The status alone would not distinguish it from any
// other body-shape refusal on the same route — a handler that rejected
// the request for its title, its project, or an unparseable date would
// satisfy a bare 4xx assertion just as well.
func requireDueBeforeStart(t *testing.T, status int, body []byte, label string) {
	t.Helper()
	require.Equalf(t, http.StatusBadRequest, status, "%s: unexpected status; body=%s", label, string(body))
	require.Equalf(t, "VALIDATION.BODY.DUE_BEFORE_START", decodeErrorCode(t, body),
		"%s: unexpected error code; body=%s", label, string(body))
}

// fetchTaskDates reads the stored task back through GET so an acceptance
// is proven by what was persisted, not by what the write echoed.
func fetchTaskDates(t *testing.T, token, taskID string) taskDates {
	t.Helper()
	var got taskDates
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID, token, nil, &got)
	return got
}

// TestTaskCreateEnforcesDateOrder pins the cross-field date rule on
// POST /tasks: a due date strictly earlier than the start date it is
// submitted with is refused, and every other arrangement of the pair is
// stored.
//
// The acceptances are not padding. A suite that only asserted the
// refusal would pass just as well against a handler that refused every
// request carrying dates at all, and the two boundary arrangements — the
// dates equal, and only one of them supplied — are precisely where a
// tightened comparison or a Valid check dropped from the rule would
// start refusing ordinary tasks.
func TestTaskCreateEnforcesDateOrder(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	t.Run("due before start is refused", func(t *testing.T) {
		t.Parallel()
		status, body := createTaskWithDates(t, tt.AccessToken, tt.ProjectPublicID,
			"Create with an inverted pair", dateOrderStart, dateOrderPrior)
		requireDueBeforeStart(t, status, body, "create with due before start")
	})

	t.Run("due equal to start is stored with both dates", func(t *testing.T) {
		t.Parallel()
		status, body := createTaskWithDates(t, tt.AccessToken, tt.ProjectPublicID,
			"Create started and due the same day", dateOrderStart, dateOrderStart)
		require.Equalf(t, http.StatusOK, status, "equal dates must be accepted; body=%s", string(body))

		created := decodeTaskDates(t, body)
		require.NotEmpty(t, created.ID)
		stored := fetchTaskDates(t, tt.AccessToken, created.ID)
		require.Equal(t, dateOrderStart, stored.StartedOn, "the start date must survive the write")
		require.Equal(t, dateOrderStart, stored.DueOn, "the due date must survive the write")
	})

	t.Run("due after start is accepted", func(t *testing.T) {
		t.Parallel()
		status, body := createTaskWithDates(t, tt.AccessToken, tt.ProjectPublicID,
			"Create with an ordered pair", dateOrderStart, dateOrderLater)
		require.Equalf(t, http.StatusOK, status, "an ordered pair must be accepted; body=%s", string(body))

		stored := fetchTaskDates(t, tt.AccessToken, decodeTaskDates(t, body).ID)
		require.Equal(t, dateOrderStart, stored.StartedOn)
		require.Equal(t, dateOrderLater, stored.DueOn)
	})

	t.Run("only a due date is accepted", func(t *testing.T) {
		t.Parallel()
		status, body := createTaskWithDates(t, tt.AccessToken, tt.ProjectPublicID,
			"Create with a due date alone", "", dateOrderPrior)
		require.Equalf(t, http.StatusOK, status,
			"a due date with no start date has no ordering to break; body=%s", string(body))

		stored := fetchTaskDates(t, tt.AccessToken, decodeTaskDates(t, body).ID)
		require.Equal(t, dateOrderPrior, stored.DueOn)
		require.Empty(t, stored.StartedOn, "no start date was submitted")
	})

	t.Run("only a start date is accepted", func(t *testing.T) {
		t.Parallel()
		status, body := createTaskWithDates(t, tt.AccessToken, tt.ProjectPublicID,
			"Create with a start date alone", dateOrderLater, "")
		require.Equalf(t, http.StatusOK, status,
			"a start date with no due date has no ordering to break; body=%s", string(body))

		stored := fetchTaskDates(t, tt.AccessToken, decodeTaskDates(t, body).ID)
		require.Equal(t, dateOrderLater, stored.StartedOn)
		require.Empty(t, stored.DueOn, "no due date was submitted")
	})
}

// TestTaskPatchEnforcesDateOrder pins the same rule on PATCH /tasks/{id}
// with both dates travelling in one body, so the endpoint is covered on
// its own rather than by inheritance from the create path.
func TestTaskPatchEnforcesDateOrder(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	t.Run("due before start is refused", func(t *testing.T) {
		t.Parallel()
		id := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Patch to an inverted pair")
		status, body := doJSONStatus(t, http.MethodPatch, testServerURL+"/tasks/"+id, tt.AccessToken,
			map[string]any{"startOn": dateOrderStart, "dueOn": dateOrderPrior})
		requireDueBeforeStart(t, status, body, "patch with due before start")
	})

	t.Run("due equal to start is stored with both dates", func(t *testing.T) {
		t.Parallel()
		id := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Patch to the same day")
		status, body := doJSONStatus(t, http.MethodPatch, testServerURL+"/tasks/"+id, tt.AccessToken,
			map[string]any{"startOn": dateOrderStart, "dueOn": dateOrderStart})
		require.Equalf(t, http.StatusOK, status, "equal dates must be accepted; body=%s", string(body))

		stored := fetchTaskDates(t, tt.AccessToken, id)
		require.Equal(t, dateOrderStart, stored.StartedOn, "the start date must survive the write")
		require.Equal(t, dateOrderStart, stored.DueOn, "the due date must survive the write")
	})

	t.Run("due after start is accepted", func(t *testing.T) {
		t.Parallel()
		id := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Patch to an ordered pair")
		status, body := doJSONStatus(t, http.MethodPatch, testServerURL+"/tasks/"+id, tt.AccessToken,
			map[string]any{"startOn": dateOrderStart, "dueOn": dateOrderLater})
		require.Equalf(t, http.StatusOK, status, "an ordered pair must be accepted; body=%s", string(body))

		stored := fetchTaskDates(t, tt.AccessToken, id)
		require.Equal(t, dateOrderStart, stored.StartedOn)
		require.Equal(t, dateOrderLater, stored.DueOn)
	})

	t.Run("one date on a task that has neither is accepted", func(t *testing.T) {
		t.Parallel()
		id := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Patch a lone due date")
		status, body := doJSONStatus(t, http.MethodPatch, testServerURL+"/tasks/"+id, tt.AccessToken,
			map[string]any{"dueOn": dateOrderPrior})
		require.Equalf(t, http.StatusOK, status,
			"a due date with no stored start date has no ordering to break; body=%s", string(body))

		stored := fetchTaskDates(t, tt.AccessToken, id)
		require.Equal(t, dateOrderPrior, stored.DueOn)
		require.Empty(t, stored.StartedOn, "the task never had a start date")
	})
}

// TestTaskPatchChecksDateOrderAgainstStoredDates is the case that
// separates PATCH's rule from POST's: the pair being checked is the body
// merged over the persisted row, not the body on its own.
//
// Each half sends exactly one date. A handler that validated the request
// body alone would see a single date, find nothing to compare it with,
// and store an inverted task one field at a time — and it would do so
// while every case above stayed green, because each of those supplies
// both dates in the same request.
func TestTaskPatchChecksDateOrderAgainstStoredDates(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	t.Run("a due date is checked against the stored start date", func(t *testing.T) {
		t.Parallel()
		status, body := createTaskWithDates(t, tt.AccessToken, tt.ProjectPublicID,
			"Task with a stored start date", dateOrderStart, "")
		require.Equalf(t, http.StatusOK, status, "setup create must succeed; body=%s", string(body))
		id := decodeTaskDates(t, body).ID

		status, body = doJSONStatus(t, http.MethodPatch, testServerURL+"/tasks/"+id, tt.AccessToken,
			map[string]any{"dueOn": dateOrderPrior})
		requireDueBeforeStart(t, status, body, "patch a due date before the stored start date")

		stored := fetchTaskDates(t, tt.AccessToken, id)
		require.Empty(t, stored.DueOn, "the refused due date must not have been written")
		require.Equal(t, dateOrderStart, stored.StartedOn, "the stored start date must be untouched")
	})

	t.Run("a start date is checked against the stored due date", func(t *testing.T) {
		t.Parallel()
		status, body := createTaskWithDates(t, tt.AccessToken, tt.ProjectPublicID,
			"Task with a stored due date", "", dateOrderStart)
		require.Equalf(t, http.StatusOK, status, "setup create must succeed; body=%s", string(body))
		id := decodeTaskDates(t, body).ID

		status, body = doJSONStatus(t, http.MethodPatch, testServerURL+"/tasks/"+id, tt.AccessToken,
			map[string]any{"startOn": dateOrderLater})
		requireDueBeforeStart(t, status, body, "patch a start date after the stored due date")

		stored := fetchTaskDates(t, tt.AccessToken, id)
		require.Empty(t, stored.StartedOn, "the refused start date must not have been written")
		require.Equal(t, dateOrderStart, stored.DueOn, "the stored due date must be untouched")
	})
}
