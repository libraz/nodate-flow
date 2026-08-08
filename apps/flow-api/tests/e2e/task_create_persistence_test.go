package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// taskRowFacts are the columns that used to be silently wrong: task_number
// was left at its 0 default and visibility was written as the empty string.
// Both are invisible through the API surface, so the assertions read the row.
type taskRowFacts struct {
	taskNumber   uint32
	visibility   string
	parentTaskID *int64
}

func readTaskRowFacts(t *testing.T, publicID string) taskRowFacts {
	t.Helper()
	var facts taskRowFacts
	err := testDB.QueryRow(
		`SELECT task_number, visibility, parent_task_id
		   FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`,
		publicID,
	).Scan(&facts.taskNumber, &facts.visibility, &facts.parentTaskID)
	require.NoErrorf(t, err, "task %s should exist", publicID)
	return facts
}

// requireWellFormedTaskRow asserts the invariants every created task row must
// satisfy regardless of which transport created it.
func requireWellFormedTaskRow(t *testing.T, publicID string) taskRowFacts {
	t.Helper()
	facts := readTaskRowFacts(t, publicID)
	require.NotZerof(t, facts.taskNumber, "task %s must carry an allocated task_number, not the column default", publicID)
	require.Equalf(t, "public", facts.visibility, "task %s must carry a real visibility enum value", publicID)
	return facts
}

// TestMCPCreateTaskPersistsRow proves the MCP create_task tool actually
// writes a task.
//
// Every existing MCP test for this tool asserted a rejection — workspace
// mismatch, insufficient scope — so the tool could fail on every successful
// call without a single test noticing. It did: the insert named the
// visibility column explicitly while leaving the Go field at its zero value,
// which MySQL rejects under STRICT_TRANS_TABLES.
func TestMCPCreateTaskPersistsRow(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	tok := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"create-task-token", []string{"read:workspace", "write:workspace"})

	body := mcpCall(t, tok, "tools/call", map[string]any{
		"name": "create_task",
		"arguments": map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     "created over mcp",
		},
	})
	created := mcpToolTextJSON[struct {
		ID string `json:"id"`
	}](t, body)
	require.NotEmpty(t, created.ID, "create_task must return the new task id, body=%s", string(body))

	facts := requireWellFormedTaskRow(t, created.ID)
	require.Nil(t, facts.parentTaskID, "a top-level task must not carry a parent")

	// The task is readable back through the same transport.
	readBody := mcpCall(t, tok, "tools/call", map[string]any{
		"name":      "get_task",
		"arguments": map[string]any{"taskId": created.ID},
	})
	fetched := mcpToolTextJSON[struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}](t, readBody)
	require.Equal(t, created.ID, fetched.ID)
	require.Equal(t, "created over mcp", fetched.Title)

	// A second task in the same project must get its own number. A constant
	// or unallocated number collides on uniq_tasks_project_id_task_number,
	// and a single-task test cannot see that.
	secondBody := mcpCall(t, tok, "tools/call", map[string]any{
		"name": "create_task",
		"arguments": map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     "created over mcp again",
		},
	})
	second := mcpToolTextJSON[struct {
		ID string `json:"id"`
	}](t, secondBody)
	require.NotEmpty(t, second.ID, "second create_task must succeed, body=%s", string(secondBody))

	secondFacts := requireWellFormedTaskRow(t, second.ID)
	require.NotEqual(t, facts.taskNumber, secondFacts.taskNumber,
		"two tasks in one project must not share a task_number")
}

// TestMCPApplyStepsCreatesEveryChild proves the MCP apply_steps tool creates
// all of its children.
//
// Three steps rather than one is the whole point: the children share a
// project, so an unallocated task_number leaves the first insert succeeding
// on the 0 default and the second colliding on
// uniq_tasks_project_id_task_number. A one-step test passes either way.
func TestMCPApplyStepsCreatesEveryChild(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	tok := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"apply-steps-token", []string{"read:workspace", "write:workspace"})

	var parent struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "parent for mcp steps",
	}, &parent)
	require.NotEmpty(t, parent.ID)

	body := mcpCall(t, tok, "tools/call", map[string]any{
		"name": "apply_steps",
		"arguments": map[string]any{
			"parentTaskId": parent.ID,
			"steps": []map[string]any{
				{"title": "mcp step one"},
				{"title": "mcp step two", "description": "with a body"},
				{"title": "mcp step three", "priority": 2},
			},
		},
	})
	result := mcpToolTextJSON[struct {
		Created []string `json:"created"`
	}](t, body)
	require.Len(t, result.Created, 3, "every step must be created, body=%s", string(body))

	requireDistinctChildRows(t, parent.ID, result.Created)
}

// TestApplyStepsCreatesEveryChild is the REST counterpart of
// TestMCPApplyStepsCreatesEveryChild and asserts the same facts, so the two
// transports cannot drift apart again on the columns they share.
func TestApplyStepsCreatesEveryChild(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var parent struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "parent for rest steps",
	}, &parent)
	require.NotEmpty(t, parent.ID)

	// The request body is the MCP one, field for field. Priority used to
	// be required here and optional there, so the two transports could
	// not be sent the same steps — and a client that omitted it got a
	// 422 for leaving out a field whose column has a default.
	var applied struct {
		Created []string `json:"created"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+parent.ID+"/apply-steps",
		tt.AccessToken, map[string]any{
			"steps": []map[string]any{
				{"title": "rest step one"},
				{"title": "rest step two", "description": "with a body"},
				{"title": "rest step three", "priority": 2},
			},
		}, &applied)
	require.Len(t, applied.Created, 3, "every step must be created")

	requireDistinctChildRows(t, parent.ID, applied.Created)
}

// TestApplySmartCreatesParentAndSubtasks covers the REST apply-smart path,
// which creates a parent plus every accepted subtask in one transaction.
//
// This route needs no AI: /propose-smart returns the proposal and the client
// posts back the set it accepted, so the persistence half is testable on its
// own. Three subtasks rather than one, for the same reason as apply-steps.
func TestApplySmartCreatesParentAndSubtasks(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// ApplySmartInput and ApplySmartSubtask both declare priority without
	// omitempty, so it is required at each level.
	var applied struct {
		TaskID     string   `json:"taskId"`
		SubtaskIDs []string `json:"subtaskIds"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/tasks/apply-smart",
		tt.AccessToken, map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     "parent for apply-smart",
			"priority":  0,
			"subtasks": []map[string]any{
				{"title": "smart subtask one", "priority": 0},
				{"title": "smart subtask two", "description": "with a body", "priority": 1},
				{"title": "smart subtask three", "priority": 2},
			},
		}, &applied)
	require.NotEmpty(t, applied.TaskID)
	require.Len(t, applied.SubtaskIDs, 3, "every accepted subtask must be created")

	requireDistinctChildRows(t, applied.TaskID, applied.SubtaskIDs)
}

// TestMCPSmartCreateTaskCreatesParentAndSubtasks covers the MCP
// smart_create_task tool, which asks the LLM for a breakdown and persists
// the parent plus each proposed subtask.
//
// Unlike the REST route this one goes through the orchestrator, so it runs
// against the deterministic mock provider and the smart_create fixture. The
// fixture proposes three subtasks; anything less would not exercise a second
// insert into the same project.
func TestMCPSmartCreateTaskCreatesParentAndSubtasks(t *testing.T) {
	bootstrap(t)
	requireAIMock(t)
	t.Parallel()

	tt := newTenant(t)
	tok := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"smart-create-token", []string{"read:workspace", "write:workspace"})

	body := mcpCall(t, tok, "tools/call", map[string]any{
		"name": "smart_create_task",
		"arguments": map[string]any{
			"projectId":   tt.ProjectPublicID,
			"title":       "ship the deterministic fixture",
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

	require.NotEmpty(t, result.ID, "smart_create_task must return the parent id, body=%s", string(body))
	require.GreaterOrEqual(t, len(result.Subtasks), 2,
		"the mock proposal must yield at least two subtasks, body=%s", string(body))

	childIDs := make([]string, 0, len(result.Subtasks))
	for _, sub := range result.Subtasks {
		require.NotEmptyf(t, sub.ID, "proposed subtask %q must have been persisted", sub.Title)
		childIDs = append(childIDs, sub.ID)
	}
	requireDistinctChildRows(t, result.ID, childIDs)
}

// requireDistinctChildRows asserts each child is a well-formed row parented
// to the given task, and that no two children share a task_number.
func requireDistinctChildRows(t *testing.T, parentPublicID string, childPublicIDs []string) {
	t.Helper()

	var parentInternalID int64
	require.NoError(t, testDB.QueryRow(
		`SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`, parentPublicID,
	).Scan(&parentInternalID))

	parentFacts := requireWellFormedTaskRow(t, parentPublicID)

	seen := map[uint32]string{}
	for _, childID := range childPublicIDs {
		facts := requireWellFormedTaskRow(t, childID)
		require.NotNilf(t, facts.parentTaskID, "child %s must be parented", childID)
		require.Equalf(t, parentInternalID, *facts.parentTaskID,
			"child %s must be parented to the requested task", childID)
		require.NotEqualf(t, parentFacts.taskNumber, facts.taskNumber,
			"child %s must not reuse the parent's task_number", childID)
		if prev, dup := seen[facts.taskNumber]; dup {
			t.Fatalf("children %s and %s share task_number %d", prev, childID, facts.taskNumber)
		}
		seen[facts.taskNumber] = childID
	}
}
