// Pre-flight idle skip against a real MySQL: the agent's own run
// bookkeeping must never count as new activity.
package signaljudgetests

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

func insertPreflightEvent(t *testing.T, db *sql.DB, wsID uint32, kind string, actorAgent sql.NullInt32, at time.Time) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO events (public_id, workspace_id, actor_agent_id, type, payload_json, occurred_at)
		VALUES (?, ?, ?, ?, JSON_OBJECT(), ?)`,
		types.New(), wsID, actorAgent, kind, at.UTC())
	require.NoError(t, err)
}

func TestPreFlightSkipsWhenOnlyTheAgentsOwnEventsFollowTheLastRun(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := helpers.CreateTestTenant(t, testSrv.BaseURL)
	wsID := resolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})

	// Placed after everything tenant and agent setup wrote, so only the events
	// this test inserts can fall after the last run.
	lastRun := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	_, err := testDB.ExecContext(context.Background(), `
		INSERT INTO agent_runs (public_id, workspace_id, agent_id, dedupe_key, status, scheduled_at)
		VALUES (?, ?, ?, ?, 'succeeded', ?)`,
		types.New(), wsID, agent.AgentID, agent.AgentPublicID+":run", lastRun)
	require.NoError(t, err)

	pf := &ai.PreFlight{Queries: generated.New(testDB)}
	after := lastRun.Add(time.Minute)

	insertPreflightEvent(t, testDB, wsID, "ai.agent.run.started", sql.NullInt32{}, after)
	insertPreflightEvent(t, testDB, wsID, "ai.agent.run.completed", sql.NullInt32{}, after)
	insertPreflightEvent(t, testDB, wsID, "task.commented", sql.NullInt32{Int32: int32(agent.AgentID), Valid: true}, after) //#nosec G115 -- test fixture
	skip, err := pf.ShouldSkip(context.Background(), wsID, agent.AgentID)
	require.NoError(t, err)
	require.True(t, skip, "run bookkeeping and the agent's own events are not new activity")

	insertPreflightEvent(t, testDB, wsID, "task.created", sql.NullInt32{}, after)
	skip, err = pf.ShouldSkip(context.Background(), wsID, agent.AgentID)
	require.NoError(t, err)
	require.False(t, skip, "an event from another source is new activity")
}
