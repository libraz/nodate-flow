package sqlviews

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	generated "github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// TestSnoozedSignalsLeaveOnlyTheInbox pins snooze as an inbox concept: a
// signal whose received_at lies in the future is hidden from the inbox but
// still listed with its task and among the unattached signals.
func TestSnoozedSignalsLeaveOnlyTheInbox(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	tt := helpers.CreateTestTenant(t, testSrv.BaseURL)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tt.WorkspacePublicID) })
	var task struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, testSrv.BaseURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Snoozed signal host",
	}, &task)

	wsID, ok := helpers.WorkspaceInternalID(t, testDB, tt.WorkspacePublicID)
	require.True(t, ok)
	taskPublicID, err := types.Parse(task.ID)
	require.NoError(t, err)
	var taskID int32
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE public_id = ?`, taskPublicID).Scan(&taskID))

	q := generated.New(testDB)
	snoozedUntil := time.Now().UTC().Add(time.Hour)
	insert := func(taskRef sql.NullInt32) types.PublicID {
		pid := types.New()
		_, err := q.InsertSignal(ctx, generated.InsertSignalParams{
			PublicID:    pid,
			WorkspaceID: wsID,
			TaskID:      taskRef,
			Source:      generated.SignalsSourceManual,
			Kind:        "note",
			PayloadJson: json.RawMessage(`{}`),
			ReceivedAt:  snoozedUntil,
			SubjectType: generated.SignalsSubjectTypeWorkspace,
		})
		require.NoError(t, err)
		return pid
	}
	attached := insert(sql.NullInt32{Int32: taskID, Valid: true})
	unattached := insert(sql.NullInt32{})

	forTask, err := q.ListSignalsForTask(ctx, generated.ListSignalsForTaskParams{
		WorkspaceID:  wsID,
		TaskPublicID: sql.NullString{String: string(taskPublicID[:]), Valid: true},
		IsElevated:   1,
		Limit:        50,
	})
	require.NoError(t, err)
	var forTaskIDs []types.PublicID
	for _, r := range forTask {
		forTaskIDs = append(forTaskIDs, r.PublicID)
	}
	require.Contains(t, forTaskIDs, attached, "task detail lists the task's snoozed signal")

	loose, err := q.ListUnattachedSignals(ctx, generated.ListUnattachedSignalsParams{WorkspaceID: wsID, Limit: 50})
	require.NoError(t, err)
	var looseIDs []types.PublicID
	for _, r := range loose {
		looseIDs = append(looseIDs, r.PublicID)
	}
	require.Contains(t, looseIDs, unattached, "unattached listing includes a snoozed signal")

	inboxTotal, err := q.CountInbox(ctx, wsID)
	require.NoError(t, err)
	require.Zero(t, inboxTotal, "the inbox hides both snoozed signals")
}
