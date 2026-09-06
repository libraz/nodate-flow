package e2e

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// Every surface that creates a task reaches the same insert, but each one
// decides on its own whether the creator also becomes an actor on the row.
// That decision is not stated anywhere a reader can see it: it is implied
// by whether the surrounding handler happens to write a task_actors row.
//
// A count alone cannot hold it — a row whose role or user changed keeps
// the count — so these assertions carry the whole tuple.

// taskActorFact is one task_actors row, addressed the way a caller sees
// it: by the actor's public id rather than the internal key.
type taskActorFact struct {
	userPublicID  string
	agentPublicID string
	kind          string
	role          string
	enabled       bool
}

// readTaskActors returns every task_actors row attached to a task,
// ordered so two runs of the same surface compare equal.
func readTaskActors(t *testing.T, taskPublicID string) []taskActorFact {
	t.Helper()
	rows, err := testDB.Query(
		`SELECT BIN_TO_UUID(u.public_id, 0), BIN_TO_UUID(ag.public_id, 0),
		        ta.kind, ta.role, ta.enabled
		   FROM task_actors ta
		   JOIN tasks t ON t.id = ta.task_id
		   LEFT JOIN users u ON u.id = ta.user_id
		   LEFT JOIN ai_agents ag ON ag.id = ta.agent_id
		  WHERE t.public_id = UUID_TO_BIN(?, 0)`,
		taskPublicID,
	)
	require.NoErrorf(t, err, "read actors of task %s", taskPublicID)
	defer func() { _ = rows.Close() }()

	out := []taskActorFact{}
	for rows.Next() {
		var (
			fact  taskActorFact
			user  sql.NullString
			agent sql.NullString
		)
		require.NoError(t, rows.Scan(&user, &agent, &fact.kind, &fact.role, &fact.enabled))
		fact.userPublicID = user.String
		fact.agentPublicID = agent.String
		out = append(out, fact)
	}
	require.NoError(t, rows.Err())
	sort.Slice(out, func(i, j int) bool {
		if out[i].userPublicID != out[j].userPublicID {
			return out[i].userPublicID < out[j].userPublicID
		}
		return out[i].role < out[j].role
	})
	return out
}

// requireNoActors asserts a surface left the task with nobody attached.
func requireNoActors(t *testing.T, taskPublicID, surface string) {
	t.Helper()
	require.Emptyf(t, readTaskActors(t, taskPublicID),
		"%s attached an actor to task %s where it attached none", surface, taskPublicID)
}

// requireSoleAssignee asserts exactly one enabled human assignee row, held
// by the named user. Naming the user and the role rather than counting is
// what makes a changed attribution visible.
func requireSoleAssignee(t *testing.T, taskPublicID, userPublicID, surface string) {
	t.Helper()
	require.Equalf(t,
		[]taskActorFact{{userPublicID: userPublicID, kind: "user", role: "assignee", enabled: true}},
		readTaskActors(t, taskPublicID),
		"%s did not leave task %s with that user as its sole assignee", surface, taskPublicID)
}

// inviteMember adds a second real user to the owner's workspace and
// returns them, so a test can distinguish "the creator" from "somebody the
// caller named".
func inviteMember(t *testing.T, owner *helpers.TestTenant) *helpers.TestTenant {
	t.Helper()
	member := newTenant(t)
	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)
	return member
}

// TestRESTCreateTaskAttribution records what POST /tasks does about
// actors, on both the branch that names them and the branch that does not.
func TestRESTCreateTaskAttribution(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	t.Run("no actors named", func(t *testing.T) {
		t.Parallel()
		tt := newTenant(t)

		var task struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     "rest create, actors omitted",
		}, &task)
		require.NotEmpty(t, task.ID)

		requireSoleAssignee(t, task.ID, tt.UserPublicID, "REST create task")
	})

	t.Run("empty actor list", func(t *testing.T) {
		t.Parallel()
		tt := newTenant(t)

		// An explicitly empty list is not the same request as an absent
		// one, and the handler branches on length rather than presence.
		var task struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     "rest create, actors empty",
			"actors":    []map[string]any{},
		}, &task)
		require.NotEmpty(t, task.ID)

		requireSoleAssignee(t, task.ID, tt.UserPublicID, "REST create task with an empty actor list")
	})

	t.Run("actors named", func(t *testing.T) {
		t.Parallel()
		owner := newTenant(t)
		member := inviteMember(t, owner)

		var task struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken, map[string]any{
			"projectId": owner.ProjectPublicID,
			"title":     "rest create, actors named",
			"actors": []map[string]any{
				{"userId": member.UserPublicID, "role": "reviewer"},
			},
		}, &task)
		require.NotEmpty(t, task.ID)

		// The named list stands on its own: the creator is not merged in,
		// and the role the caller asked for is the role that is stored.
		require.Equal(t,
			[]taskActorFact{{userPublicID: member.UserPublicID, kind: "user", role: "reviewer", enabled: true}},
			readTaskActors(t, task.ID),
			"a named actor list must be the whole list, at the role it named")
	})
}

// TestRESTApplyStepsAttribution records what POST
// /tasks/{id}/apply-steps does about actors on the children it creates.
func TestRESTApplyStepsAttribution(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var parent struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "parent for rest step attribution",
	}, &parent)

	var applied struct {
		Created []string `json:"created"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+parent.ID+"/apply-steps",
		tt.AccessToken, map[string]any{
			"steps": []map[string]any{
				{"title": "rest attribution step one"},
				{"title": "rest attribution step two"},
			},
		}, &applied)
	require.Len(t, applied.Created, 2)

	// The parent was made through POST /tasks, so it carries the creator;
	// asserting it here keeps the two halves of the same request visible
	// side by side.
	requireSoleAssignee(t, parent.ID, tt.UserPublicID, "REST create task")
	for _, child := range applied.Created {
		requireNoActors(t, child, "REST apply-steps")
	}
}

// TestRESTApplySmartAttribution records what POST
// /workspaces/{wsId}/tasks/apply-smart does about actors, for the parent
// and for subtasks, with and without a named assignee.
func TestRESTApplySmartAttribution(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := inviteMember(t, owner)

	t.Run("nobody named", func(t *testing.T) {
		t.Parallel()

		var applied struct {
			TaskID     string   `json:"taskId"`
			SubtaskIDs []string `json:"subtaskIds"`
		}
		doJSON(t, http.MethodPost,
			testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/tasks/apply-smart",
			owner.AccessToken, map[string]any{
				"projectId": owner.ProjectPublicID,
				"title":     "apply-smart parent, nobody named",
				"priority":  0,
				"subtasks": []map[string]any{
					{"title": "apply-smart subtask, nobody named", "priority": 0},
				},
			}, &applied)
		require.NotEmpty(t, applied.TaskID)
		require.Len(t, applied.SubtaskIDs, 1)

		requireNoActors(t, applied.TaskID, "REST apply-smart parent")
		requireNoActors(t, applied.SubtaskIDs[0], "REST apply-smart subtask")
	})

	t.Run("assignees named", func(t *testing.T) {
		t.Parallel()

		// The parent takes a list and each subtask takes at most one, so
		// the two levels are named through different fields.
		var applied struct {
			TaskID     string   `json:"taskId"`
			SubtaskIDs []string `json:"subtaskIds"`
		}
		doJSON(t, http.MethodPost,
			testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/tasks/apply-smart",
			owner.AccessToken, map[string]any{
				"projectId":       owner.ProjectPublicID,
				"title":           "apply-smart parent, assignees named",
				"priority":        0,
				"assigneeUserIds": []string{member.UserPublicID},
				"subtasks": []map[string]any{
					{"title": "apply-smart subtask, assignee named", "priority": 0, "assigneeUserId": member.UserPublicID},
					{"title": "apply-smart subtask, no assignee", "priority": 0},
				},
			}, &applied)
		require.NotEmpty(t, applied.TaskID)
		require.Len(t, applied.SubtaskIDs, 2)

		requireSoleAssignee(t, applied.TaskID, member.UserPublicID, "REST apply-smart parent with a named assignee")
		requireSoleAssignee(t, applied.SubtaskIDs[0], member.UserPublicID, "REST apply-smart subtask with a named assignee")
		requireNoActors(t, applied.SubtaskIDs[1], "REST apply-smart subtask without a named assignee")
	})
}

// TestRESTIntakeConvertAttribution records what POST
// /workspaces/{wsId}/intake/{id}/convert does about actors on the task it
// produces.
func TestRESTIntakeConvertAttribution(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var item struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/intake",
		tt.AccessToken, map[string]any{"title": "intake item for rest conversion"}, &item)
	require.NotEmpty(t, item.ID)

	var converted struct {
		TaskID string `json:"taskId"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/intake/"+item.ID+"/convert",
		tt.AccessToken, map[string]any{"projectId": tt.ProjectPublicID}, &converted)
	require.NotEmpty(t, converted.TaskID)

	requireNoActors(t, converted.TaskID, "REST intake convert")
}

// TestMCPCreateTaskAttribution records what the create_task tool does
// about actors. It is the tool that answers the same request POST /tasks
// answers, so the two are worth reading together.
func TestMCPCreateTaskAttribution(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	tok := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"create-task-attribution-token", []string{"read:workspace", "write:workspace"})

	body := mcpCall(t, tok, "tools/call", map[string]any{
		"name": "create_task",
		"arguments": map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     "mcp create, attribution baseline",
		},
	})
	created := mcpToolTextJSON[struct {
		ID string `json:"id"`
	}](t, body)
	require.NotEmptyf(t, created.ID, "create_task must return the new task id, body=%s", string(body))

	requireNoActors(t, created.ID, "MCP create_task")
}

// TestMCPApplyStepsAttribution records what the apply_steps tool does
// about actors on the children it creates.
func TestMCPApplyStepsAttribution(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	tok := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"apply-steps-attribution-token", []string{"read:workspace", "write:workspace"})

	var parent struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "parent for mcp step attribution",
	}, &parent)

	body := mcpCall(t, tok, "tools/call", map[string]any{
		"name": "apply_steps",
		"arguments": map[string]any{
			"parentTaskId": parent.ID,
			"steps": []map[string]any{
				{"title": "mcp attribution step one"},
				{"title": "mcp attribution step two"},
			},
		},
	})
	result := mcpToolTextJSON[struct {
		Created []string `json:"created"`
	}](t, body)
	require.Lenf(t, result.Created, 2, "every step must be created, body=%s", string(body))

	for _, child := range result.Created {
		requireNoActors(t, child, "MCP apply_steps")
	}
}

// TestMCPSmartCreateTaskAttribution records what the smart_create_task
// tool does about actors, for the parent and for the subtasks the model
// proposed. Nothing in the tool's arguments names an actor, so this is the
// only branch it has.
func TestMCPSmartCreateTaskAttribution(t *testing.T) {
	bootstrap(t)
	requireAIMock(t)
	t.Parallel()

	tt := newTenant(t)
	tok := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"smart-create-attribution-token", []string{"read:workspace", "write:workspace"})

	body := mcpCall(t, tok, "tools/call", map[string]any{
		"name": "smart_create_task",
		"arguments": map[string]any{
			"projectId":   tt.ProjectPublicID,
			"title":       "mcp smart create, attribution baseline",
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
	require.NotEmptyf(t, result.ID, "smart_create_task must return the parent id, body=%s", string(body))
	require.NotEmptyf(t, result.Subtasks, "the mock proposal must yield subtasks, body=%s", string(body))

	requireNoActors(t, result.ID, "MCP smart_create_task parent")
	for _, sub := range result.Subtasks {
		requireNoActors(t, sub.ID, "MCP smart_create_task subtask")
	}
}

// TestMCPConvertIntakeToTaskAttribution records what the
// convert_intake_to_task tool does about actors, alongside the REST
// conversion it mirrors.
func TestMCPConvertIntakeToTaskAttribution(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	tok := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"convert-intake-attribution-token", []string{"read:workspace", "write:workspace"})

	var item struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/intake",
		tt.AccessToken, map[string]any{"title": "intake item for mcp conversion"}, &item)
	require.NotEmpty(t, item.ID)

	body := mcpCall(t, tok, "tools/call", map[string]any{
		"name": "convert_intake_to_task",
		"arguments": map[string]any{
			"intakeItemId": item.ID,
			"projectId":    tt.ProjectPublicID,
		},
	})
	converted := mcpToolTextJSON[struct {
		TaskID string `json:"taskId"`
	}](t, body)
	require.NotEmptyf(t, converted.TaskID, "convert_intake_to_task must return the task id, body=%s", string(body))

	requireNoActors(t, converted.TaskID, "MCP convert_intake_to_task")
}

// TestCSVImportAttribution records what the CSV importer does about
// actors. It is the one create path whose acting user is not the person
// making the request but whoever queued the job, and it still attaches
// nobody: importing a file says who wanted the rows, not who will do them.
func TestCSVImportAttribution(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const importedTitle = "csv import attribution baseline"
	_, jobID := createImportJob(ctx, t, tt, "csv",
		map[string]any{"csv": "title,description,priority\n" + importedTitle + ",,0\n"})
	runImportWorkerFor(ctx, t, newImportWorker(t), jobID)

	var imported string
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT BIN_TO_UUID(t.public_id, 0)
		   FROM tasks t
		   JOIN projects p ON p.id = t.project_id
		  WHERE p.public_id = UUID_TO_BIN(?, 0) AND t.title = ?`,
		tt.ProjectPublicID, importedTitle,
	).Scan(&imported))

	requireNoActors(t, imported, "CSV import")
}
