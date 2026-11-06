package agentruntime_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/agentruntime"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

type fixedOnEventAgents []agentruntime.OnEventAgentMatch

func (q fixedOnEventAgents) ListOnEventAgentsFor(context.Context, uint32, string) ([]agentruntime.OnEventAgentMatch, error) {
	return q, nil
}

func (q fixedOnEventAgents) ListOnEventAgentsForEvent(context.Context, uint32, uint64) ([]agentruntime.OnEventAgentMatch, error) {
	return q, nil
}

// TestMySQLQueueRejectsReplayAfterAck is the persistent half of
// TestEventTriggerDedupesBySourceEventID: the finished agent_runs row
// keeps its dedupe key, so an event replayed after its run was acked
// does not run again.
func TestMySQLQueueRejectsReplayAfterAck(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	db := helpers.StartShared(t).DB
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	wsPub := uuid.Must(uuid.NewV7())
	slug := "replay-" + uuid.NewString()[:8]
	res, err := db.ExecContext(ctx,
		`INSERT INTO workspaces (public_id, slug, name) VALUES (?, ?, ?)`, wsPub[:], slug, slug)
	require.NoError(t, err)
	wsID64, err := res.LastInsertId()
	require.NoError(t, err)
	wsID := uint32(wsID64) //#nosec G115 -- AUTO_INCREMENT LastInsertId in a test seed, fits uint32
	agent := helpers.SeedAgent(t, db, wsPub.String(), helpers.SeedAgentOptions{})
	res, err = db.ExecContext(ctx, `
		INSERT INTO events (public_id, workspace_id, type, payload_json, occurred_at)
		VALUES (UUID_TO_BIN(UUID(), 0), ?, 'test.synthetic.source', JSON_OBJECT(), NOW(3))`, wsID)
	require.NoError(t, err)
	eventID64, err := res.LastInsertId()
	require.NoError(t, err)
	eventID := uint64(eventID64) //#nosec G115 -- AUTO_INCREMENT LastInsertId is non-negative

	queue := agentruntime.NewMySQLQueue(db)
	queue.PollInterval = 20 * time.Millisecond
	dispatch := func() {
		trigger := &agentruntime.EventTrigger{
			Queries: fixedOnEventAgents{{ID: agent.AgentID, WorkspaceID: wsID}},
			Queue:   queue,
		}
		trigger.NotifyHook()(ctx, wsID, "test.synthetic.source", eventID)
		require.NoError(t, trigger.Shutdown(ctx))
	}

	dispatch()
	claimCtx, claimCancel := context.WithTimeout(ctx, 5*time.Second)
	defer claimCancel()
	run, err := queue.Claim(claimCtx)
	require.NoError(t, err)
	require.Equal(t, eventID, run.Job.SourceEventID)
	require.NoError(t, queue.Ack(ctx, run.DedupeKey))

	dispatch()
	var rows int
	var status string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*), MAX(status) FROM agent_runs WHERE agent_id = ?`, agent.AgentID).Scan(&rows, &status))
	require.Equal(t, 1, rows, "an event replayed after its run finished must not run again")
	require.Equal(t, "succeeded", status)
}
