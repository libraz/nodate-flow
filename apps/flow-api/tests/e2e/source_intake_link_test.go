package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

type intakeConvertResponse struct {
	Ok     bool   `json:"ok"`
	TaskID string `json:"taskId"`
}

func captureSource(t *testing.T, tt *helpers.TestTenant, signalID string) sourceIntakeResponse {
	t.Helper()
	var captured sourceIntakeResponse
	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/inbox/"+signalID+"/intake",
		tt.AccessToken, nil)
	require.Containsf(t, []int{http.StatusCreated, http.StatusOK}, status, "source capture body=%s", string(raw))
	require.NoError(t, json.Unmarshal(raw, &captured))
	return captured
}

func intakeConvertURL(tt *helpers.TestTenant, intakeItemID string) string {
	return testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/intake/" + intakeItemID + "/convert"
}

func taskInternalIDOf(t *testing.T, taskPublicID string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, testDB.QueryRow(
		`SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`, taskPublicID,
	).Scan(&id))
	return id
}

func intakeLinkState(t *testing.T, intakeItemID string) (sql.NullInt64, string) {
	t.Helper()
	var taskID sql.NullInt64
	var status string
	require.NoError(t, testDB.QueryRow(
		`SELECT task_id, triage_status FROM intake_items WHERE public_id = UUID_TO_BIN(?, 0)`, intakeItemID,
	).Scan(&taskID, &status))
	return taskID, status
}

func signalTaskID(t *testing.T, signalID string) sql.NullInt64 {
	t.Helper()
	var taskID sql.NullInt64
	require.NoError(t, testDB.QueryRow(
		`SELECT task_id FROM signals WHERE public_id = UUID_TO_BIN(?, 0)`, signalID,
	).Scan(&taskID))
	return taskID
}

func countTasksTitled(t *testing.T, tt *helpers.TestTenant, title string) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND title = ?`,
		helpers.ResolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID), title,
	).Scan(&n))
	return n
}

func acceptedPayloadFor(t *testing.T, intakeItemID string) map[string]any {
	t.Helper()
	var raw []byte
	require.NoError(t, testDB.QueryRow(
		`SELECT payload_json FROM events
		  WHERE type = 'intake.item.accepted'
		    AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.intakeItemId')) = ?
		  ORDER BY id DESC LIMIT 1`, intakeItemID,
	).Scan(&raw))
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	return payload
}

func archiveSignal(t *testing.T, tt *helpers.TestTenant, signalID string) {
	t.Helper()
	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/inbox/"+signalID+"/archive?workspaceId="+tt.WorkspacePublicID,
		tt.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status, "archive body=%s", string(raw))
}

func TestSourceIntakeConvertsAfterSignalArchived(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)
	signalID := seedSourceSignal(t, tt, "manual", "manual", map[string]any{"title": "Archived source conversion"})
	captured := captureSource(t, tt, signalID)
	archiveSignal(t, tt, signalID)

	status, raw := doJSONStatus(t, http.MethodPost, intakeConvertURL(tt, captured.IntakeItemID), tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID})
	require.Equalf(t, http.StatusOK, status, "convert archived source body=%s", string(raw))
	var converted intakeConvertResponse
	require.NoError(t, json.Unmarshal(raw, &converted))
	require.NotEmpty(t, converted.TaskID)

	taskID := taskInternalIDOf(t, converted.TaskID)
	intakeTask, triage := intakeLinkState(t, captured.IntakeItemID)
	require.Equal(t, sql.NullInt64{Int64: taskID, Valid: true}, intakeTask)
	require.Equal(t, "accepted", triage)
	require.Equal(t, sql.NullInt64{Int64: taskID, Valid: true}, signalTaskID(t, signalID),
		"an archived source keeps its association with the converted task")
}

func TestMCPSourceIntakeConvertsAfterSignalArchived(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)
	signalID := seedSourceSignal(t, tt, "manual", "manual", map[string]any{"title": "Archived MCP source conversion"})
	captured := captureSource(t, tt, signalID)
	archiveSignal(t, tt, signalID)
	token := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"archived-source", []string{"read:workspace", "write:workspace"})

	converted := mcpToolTextJSON[intakeConvertResponse](t, mcpCall(t, token, "tools/call", map[string]any{
		"name":      "convert_intake_to_task",
		"arguments": map[string]any{"intakeItemId": captured.IntakeItemID, "projectId": tt.ProjectPublicID},
	}))
	require.NotEmpty(t, converted.TaskID)
	taskID := taskInternalIDOf(t, converted.TaskID)
	intakeTask, _ := intakeLinkState(t, captured.IntakeItemID)
	require.Equal(t, sql.NullInt64{Int64: taskID, Valid: true}, intakeTask)
	require.Equal(t, sql.NullInt64{Int64: taskID, Valid: true}, signalTaskID(t, signalID))
}

// TestSourceIntakeCaptureAdoptsSignalTask covers a source that is already
// attached to a task before anyone captures it: the intake joins that task
// rather than waiting in the queue for a conversion that cannot happen.
func TestSourceIntakeCaptureAdoptsSignalTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)
	const title = "Source already on a task"
	signalID := seedSourceSignal(t, tt, "manual", "manual", map[string]any{"title": title})
	taskPub := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Task the source already names")
	taskID := taskInternalIDOf(t, taskPub)
	_, err := testDB.Exec(`UPDATE signals SET task_id = ? WHERE public_id = UUID_TO_BIN(?, 0)`, taskID, signalID)
	require.NoError(t, err)

	captured := captureSource(t, tt, signalID)
	require.True(t, captured.Created)
	require.Equal(t, taskPub, captured.TaskID)
	intakeTask, triage := intakeLinkState(t, captured.IntakeItemID)
	require.Equal(t, sql.NullInt64{Int64: taskID, Valid: true}, intakeTask)
	require.Equal(t, "accepted", triage)
	payload := acceptedPayloadFor(t, captured.IntakeItemID)
	require.Equal(t, taskPub, payload["taskId"])
	require.Equal(t, signalID, payload["signalId"])
	require.Equal(t, tt.ProjectPublicID, payload["projectId"])

	status, raw := doJSONStatus(t, http.MethodPost, intakeConvertURL(tt, captured.IntakeItemID), tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID})
	require.Equalf(t, http.StatusOK, status, "convert adopted intake body=%s", string(raw))
	var converted intakeConvertResponse
	require.NoError(t, json.Unmarshal(raw, &converted))
	require.Equal(t, taskPub, converted.TaskID)
	require.Zero(t, countTasksTitled(t, tt, title), "converting an adopted intake must not create a task")
}

func TestMCPSourceIntakeConversionReplayReturnsTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)
	signalID := seedSourceSignal(t, tt, "manual", "manual", map[string]any{"title": "MCP conversion replay"})
	captured := captureSource(t, tt, signalID)
	token := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"conversion-replay", []string{"read:workspace", "write:workspace"})
	args := map[string]any{
		"name":      "convert_intake_to_task",
		"arguments": map[string]any{"intakeItemId": captured.IntakeItemID, "projectId": tt.ProjectPublicID},
	}

	first := mcpToolTextJSON[intakeConvertResponse](t, mcpCall(t, token, "tools/call", args))
	require.NotEmpty(t, first.TaskID)
	replay := mcpToolTextJSON[intakeConvertResponse](t, mcpCall(t, token, "tools/call", args))
	require.Equal(t, first.TaskID, replay.TaskID, "a repeated conversion must return the converted task")
	require.Equal(t, 1, countTasksTitled(t, tt, "MCP conversion replay"))
}

func TestSourceIntakeAcceptedPayloadMatchesAcrossTransports(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)
	restSignal := seedSourceSignal(t, tt, "manual", "manual", map[string]any{"title": "REST accepted payload"})
	mcpSignal := seedSourceSignal(t, tt, "manual", "manual", map[string]any{"title": "MCP accepted payload"})
	restIntake := captureSource(t, tt, restSignal)
	mcpIntake := captureSource(t, tt, mcpSignal)

	var restConverted intakeConvertResponse
	doJSON(t, http.MethodPost, intakeConvertURL(tt, restIntake.IntakeItemID), tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID}, &restConverted)
	token := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"payload-parity", []string{"read:workspace", "write:workspace"})
	mcpConverted := mcpToolTextJSON[intakeConvertResponse](t, mcpCall(t, token, "tools/call", map[string]any{
		"name":      "convert_intake_to_task",
		"arguments": map[string]any{"intakeItemId": mcpIntake.IntakeItemID, "projectId": tt.ProjectPublicID},
	}))

	restPayload := acceptedPayloadFor(t, restIntake.IntakeItemID)
	mcpPayload := acceptedPayloadFor(t, mcpIntake.IntakeItemID)
	require.Equal(t, payloadKeys(restPayload, "via"), payloadKeys(mcpPayload, "via"),
		"REST and MCP conversions must describe the accepted intake with the same keys")
	require.Equal(t, mcpConverted.TaskID, mcpPayload["taskId"])
	require.Equal(t, mcpSignal, mcpPayload["signalId"])
	require.Equal(t, tt.ProjectPublicID, mcpPayload["projectId"])
	require.Equal(t, restConverted.TaskID, restPayload["taskId"])
}

func payloadKeys(payload map[string]any, ignore string) []string {
	keys := make([]string, 0, len(payload))
	for k := range payload {
		if k != ignore {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// TestSourceIntakeLinkRechecksTargetInsideTransaction holds the intake row
// locked after the link request has passed its early checks, changes the
// target under it, and expects the committed link to see the change.
func TestSourceIntakeLinkRechecksTargetInsideTransaction(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	cases := []struct {
		name     string
		status   int
		code     string
		asEditor bool
		mutate   func(tx *sql.Tx, owner *helpers.TestTenant, taskID int64, editorPublicID string) error
	}{
		{
			name:   "disabled task",
			status: http.StatusNotFound,
			code:   "WS.TASK.NOT_FOUND",
			mutate: func(tx *sql.Tx, _ *helpers.TestTenant, taskID int64, _ string) error {
				_, err := tx.Exec(`UPDATE tasks SET enabled = FALSE WHERE id = ?`, taskID)
				return err
			},
		},
		{
			name:     "editor role revoked",
			status:   http.StatusForbidden,
			code:     "WS.PROJECT.ACCESS_DENIED",
			asEditor: true,
			mutate: func(tx *sql.Tx, owner *helpers.TestTenant, _ int64, editorPublicID string) error {
				_, err := tx.Exec(
					`UPDATE project_members SET role = 'viewer'
					  WHERE project_id = (SELECT id FROM projects WHERE public_id = UUID_TO_BIN(?, 0))
					    AND user_id = (SELECT id FROM users WHERE public_id = UUID_TO_BIN(?, 0))`,
					owner.ProjectPublicID, editorPublicID)
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			owner := newTenant(t)
			caller := owner
			if tc.asEditor {
				caller = inviteAndJoinWorkspace(t, owner)
				addProjectRoleMember(t, owner, owner.ProjectPublicID, caller.UserPublicID, "editor")
			}
			signalID := seedSourceSignal(t, owner, "manual", "manual", map[string]any{"title": "Link target recheck"})
			captured := captureSource(t, caller, signalID)
			taskPub := createTask(t, owner.AccessToken, owner.ProjectPublicID, "Link target under change")
			taskID := taskInternalIDOf(t, taskPub)

			lockTx, err := testDB.BeginTx(context.Background(), nil)
			require.NoError(t, err)
			defer func() { _ = lockTx.Rollback() }()
			var lockedIntake int64
			require.NoError(t, lockTx.QueryRow(
				`SELECT id FROM intake_items WHERE public_id = UUID_TO_BIN(?, 0) FOR UPDATE`, captured.IntakeItemID,
			).Scan(&lockedIntake))

			type result struct {
				status int
				body   []byte
				err    error
			}
			resultCh := make(chan result, 1)
			go func() {
				status, body, err := sendJSONStatus(http.MethodPost,
					testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/inbox/"+signalID+"/intake",
					caller.AccessToken, map[string]any{"existingTaskId": taskPub})
				resultCh <- result{status: status, body: body, err: err}
			}()
			// The test user cannot read lock waits, so give the request time to reach the held row.
			select {
			case res := <-resultCh:
				t.Fatalf("link request finished before the intake lock was released: status=%d body=%s", res.status, string(res.body))
			case <-time.After(300 * time.Millisecond):
			}

			require.NoError(t, tc.mutate(lockTx, owner, taskID, caller.UserPublicID))
			require.NoError(t, lockTx.Commit())
			var res result
			select {
			case res = <-resultCh:
			case <-time.After(10 * time.Second):
				t.Fatal("link request did not finish after the intake lock was released")
			}
			require.NoError(t, res.err)
			require.Equalf(t, tc.status, res.status, "link body=%s", string(res.body))
			require.Equal(t, tc.code, decodeErrorCode(t, res.body))

			intakeTask, triage := intakeLinkState(t, captured.IntakeItemID)
			require.False(t, intakeTask.Valid, "a refused link must leave the intake unlinked")
			require.Equal(t, "pending", triage)
			require.False(t, signalTaskID(t, signalID).Valid, "a refused link must leave the signal unlinked")
		})
	}
}

func TestSourceIntakeLinkLeavesTriagedItemsClosed(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)
	const title = "Rejected source"
	signalID := seedSourceSignal(t, tt, "manual", "manual", map[string]any{"title": title})
	captured := captureSource(t, tt, signalID)
	doJSON(t, http.MethodPatch,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/intake/"+captured.IntakeItemID,
		tt.AccessToken, map[string]any{"status": "rejected"}, nil)
	taskPub := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Task offered to a rejected source")

	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/inbox/"+signalID+"/intake",
		tt.AccessToken, map[string]any{"existingTaskId": taskPub})
	require.Equalf(t, http.StatusConflict, status, "link rejected intake body=%s", string(raw))
	require.Equal(t, "WS.INTAKE.ALREADY_TRIAGED", decodeErrorCode(t, raw))

	status, raw = doJSONStatus(t, http.MethodPost, intakeConvertURL(tt, captured.IntakeItemID), tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID})
	require.Equalf(t, http.StatusConflict, status, "convert rejected intake body=%s", string(raw))
	require.Equal(t, "WS.INTAKE.ALREADY_TRIAGED", decodeErrorCode(t, raw))

	intakeTask, triage := intakeLinkState(t, captured.IntakeItemID)
	require.False(t, intakeTask.Valid)
	require.Equal(t, "rejected", triage, "a rejected intake must stay rejected")
	require.False(t, signalTaskID(t, signalID).Valid)
	require.Zero(t, countTasksTitled(t, tt, title))
}

// TestSourceIntakeAcceptedTriageStillConverts pins the documented triage
// flow: an item accepted without a task is forwarded to conversion.
func TestSourceIntakeAcceptedTriageStillConverts(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)
	signalID := seedSourceSignal(t, tt, "manual", "manual", map[string]any{"title": "Accepted before conversion"})
	captured := captureSource(t, tt, signalID)
	doJSON(t, http.MethodPatch,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/intake/"+captured.IntakeItemID,
		tt.AccessToken, map[string]any{"status": "accepted"}, nil)

	status, raw := doJSONStatus(t, http.MethodPost, intakeConvertURL(tt, captured.IntakeItemID), tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID})
	require.Equalf(t, http.StatusOK, status, "convert accepted intake body=%s", string(raw))
	intakeTask, _ := intakeLinkState(t, captured.IntakeItemID)
	require.True(t, intakeTask.Valid)
}
