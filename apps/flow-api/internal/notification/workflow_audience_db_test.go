package notification

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

func TestWorkflowNotificationsReachCurrentNamedReaderOnce(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	db := helpers.StartShared(t).DB
	fx := seedFanoutFixture(t, db)
	observer := addMember(t, db, fx.wsID, "workflow-observer")
	_, err := db.ExecContext(context.Background(), `INSERT INTO task_actors (public_id, workspace_id, task_id, user_id, role) VALUES (?, ?, ?, ?, 'assignee')`, types.New(), fx.wsID, fx.taskInternalID, observer)
	require.NoError(t, err)
	var recipient types.PublicID
	require.NoError(t, db.QueryRowContext(context.Background(), `SELECT public_id FROM users WHERE id = ?`, fx.recipientUserID).Scan(&recipient))
	f := NewFanout(db, generated.New(db))
	for _, kind := range []string{"task.submission.created", "task.review.decided", "task.handoff.requested", "task.handoff.decided"} {
		t.Run(kind, func(t *testing.T) {
			payload := `{"targetUserPublicId":"` + recipient.String() + `"}`
			eventID := appendTaskEvent(t, db, fx, kind, payload)
			f.fanout(context.Background(), fx.wsID, kind, eventID)
			f.fanout(context.Background(), fx.wsID, kind, eventID)
			require.Equal(t, map[uint32]int{fx.recipientUserID: 1}, rowsForEvent(t, db, fx.wsID, eventID))
			for _, absentTarget := range []string{`{}`, `{"targetUserPublicId":""}`} {
				untargetedID := appendTaskEvent(t, db, fx, kind, absentTarget)
				f.fanout(context.Background(), fx.wsID, kind, untargetedID)
				require.Empty(t, rowsForEvent(t, db, fx.wsID, untargetedID), "a deleted or missing recipient must not fall back to other task actors")
			}
			_, err := db.ExecContext(context.Background(), `UPDATE tasks SET visibility = 'private' WHERE id = ?`, fx.taskInternalID)
			require.NoError(t, err)
			deniedID := appendTaskEvent(t, db, fx, kind, payload)
			f.fanout(context.Background(), fx.wsID, kind, deniedID)
			require.Empty(t, rowsForEvent(t, db, fx.wsID, deniedID), "a recipient who lost task access must not receive workflow evidence")
			_, err = db.ExecContext(context.Background(), `UPDATE tasks SET visibility = 'public' WHERE id = ?`, fx.taskInternalID)
			require.NoError(t, err)
		})
	}
}
