package e2e

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

func TestMCPSourceIntakeConversionPreservesSignalLink(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tenant := newTenant(t)
	signalID := seedSourceSignal(t, tenant, "manual", "manual", map[string]any{
		"title": "Convert source through MCP",
		"body":  "The captured source body must remain on the new task.",
	})
	var captured sourceIntakeResponse
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tenant.WorkspacePublicID+"/inbox/"+signalID+"/intake",
		tenant.AccessToken, nil, &captured)
	require.NotEmpty(t, captured.IntakeItemID)
	token := mintMCPToken(t, tenant.AccessToken, tenant.WorkspacePublicID,
		"source-conversion", []string{"read:workspace", "write:workspace"})

	response := mcpCall(t, token, "tools/call", map[string]any{
		"name": "convert_intake_to_task",
		"arguments": map[string]any{
			"intakeItemId": captured.IntakeItemID,
			"projectId":    tenant.ProjectPublicID,
		},
	})
	converted := mcpToolTextJSON[struct {
		TaskID string `json:"taskId"`
	}](t, response)
	require.NotEmpty(t, converted.TaskID)

	var taskInternalID int64
	require.NoError(t, testDB.QueryRow(
		`SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`, converted.TaskID,
	).Scan(&taskInternalID))
	var intakeTaskID, signalTaskID sql.NullInt64
	require.NoError(t, testDB.QueryRow(
		`SELECT task_id FROM intake_items WHERE public_id = UUID_TO_BIN(?, 0)`, captured.IntakeItemID,
	).Scan(&intakeTaskID))
	require.NoError(t, testDB.QueryRow(
		`SELECT task_id FROM signals WHERE public_id = UUID_TO_BIN(?, 0)`, signalID,
	).Scan(&signalTaskID))
	require.Equal(t, sql.NullInt64{Int64: taskInternalID, Valid: true}, intakeTaskID)
	require.Equal(t, sql.NullInt64{Int64: taskInternalID, Valid: true}, signalTaskID,
		"the original signal must point to the MCP-created task")

	attached := sourceSignalAttachedEvents(t, signalID)
	require.Len(t, attached, 1)
	require.Equal(t, taskInternalID, attached[0].TaskID.Int64)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(attached[0].Payload, &payload))
	require.Equal(t, signalID, payload["signalId"])
	require.Equal(t, converted.TaskID, payload["taskId"])
	require.Equal(t, "manual", payload["source"])

	var auditCount int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM audit_logs
		  WHERE action = 'signal.attach'
		    AND resource_type = 'signal'
		    AND resource_public_id = UUID_TO_BIN(?, 0)`, signalID,
	).Scan(&auditCount))
	require.Equal(t, 1, auditCount, "the link event and its audit record must commit together")
}

func TestMCPSourceIntakeConversionAdoptsSignalTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tenant := newTenant(t)
	signalID := seedSourceSignal(t, tenant, "manual", "manual", map[string]any{
		"title": "Conflicting source conversion",
	})
	var captured sourceIntakeResponse
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tenant.WorkspacePublicID+"/inbox/"+signalID+"/intake",
		tenant.AccessToken, nil, &captured)
	otherTaskID := createTask(t, tenant.AccessToken, tenant.ProjectPublicID, "Existing source destination")
	var otherTaskInternalID int64
	require.NoError(t, testDB.QueryRow(
		`SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`, otherTaskID,
	).Scan(&otherTaskInternalID))
	_, err := testDB.Exec(
		`UPDATE signals SET task_id = ? WHERE public_id = UUID_TO_BIN(?, 0)`, otherTaskInternalID, signalID,
	)
	require.NoError(t, err)
	token := mintMCPToken(t, tenant.AccessToken, tenant.WorkspacePublicID,
		"source-conflict", []string{"read:workspace", "write:workspace"})

	body := mcpCall(t, token, "tools/call", map[string]any{
		"name": "convert_intake_to_task",
		"arguments": map[string]any{
			"intakeItemId": captured.IntakeItemID,
			"projectId":    tenant.ProjectPublicID,
		},
	})
	adopted := mcpToolTextJSON[struct {
		TaskID string `json:"taskId"`
	}](t, body)
	require.Equal(t, otherTaskID, adopted.TaskID,
		"a source that already names a task must convert to that task")
	var intakeTaskID sql.NullInt64
	require.NoError(t, testDB.QueryRow(
		`SELECT task_id FROM intake_items WHERE public_id = UUID_TO_BIN(?, 0)`, captured.IntakeItemID,
	).Scan(&intakeTaskID))
	require.Equal(t, sql.NullInt64{Int64: otherTaskInternalID, Valid: true}, intakeTaskID)
	var count int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND title = 'Conflicting source conversion'`,
		helpers.ResolveWorkspaceInternalID(t, testDB, tenant.WorkspacePublicID),
	).Scan(&count))
	require.Zero(t, count, "an adopted source must not create another task")
	require.Empty(t, sourceSignalAttachedEvents(t, signalID))
}

func TestMCPSourceIntakeConversionRollsBackWhenSignalEventFails(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tenant := newTenant(t)
	signalID := seedSourceSignal(t, tenant, "manual", "manual", map[string]any{
		"title": "Rollback MCP source conversion",
	})
	var captured sourceIntakeResponse
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tenant.WorkspacePublicID+"/inbox/"+signalID+"/intake",
		tenant.AccessToken, nil, &captured)
	installSignalAttachedFailure(t, signalID)
	token := mintMCPToken(t, tenant.AccessToken, tenant.WorkspacePublicID,
		"source-event-failure", []string{"read:workspace", "write:workspace"})

	body := mcpCall(t, token, "tools/call", map[string]any{
		"name": "convert_intake_to_task",
		"arguments": map[string]any{
			"intakeItemId": captured.IntakeItemID,
			"projectId":    tenant.ProjectPublicID,
		},
	})
	require.Equal(t, "MCP.TOOL.EXECUTION_FAILED", mcpErrorCode(t, body),
		"a failed source event must fail the transaction")
	var count int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND title = 'Rollback MCP source conversion'`,
		helpers.ResolveWorkspaceInternalID(t, testDB, tenant.WorkspacePublicID),
	).Scan(&count))
	require.Zero(t, count)
	var intakeTaskID, signalTaskID sql.NullInt64
	require.NoError(t, testDB.QueryRow(
		`SELECT task_id FROM intake_items WHERE public_id = UUID_TO_BIN(?, 0)`, captured.IntakeItemID,
	).Scan(&intakeTaskID))
	require.NoError(t, testDB.QueryRow(
		`SELECT task_id FROM signals WHERE public_id = UUID_TO_BIN(?, 0)`, signalID,
	).Scan(&signalTaskID))
	require.False(t, intakeTaskID.Valid)
	require.False(t, signalTaskID.Valid)
	require.Empty(t, sourceSignalAttachedEvents(t, signalID))
}
