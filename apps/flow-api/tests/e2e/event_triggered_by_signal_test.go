// Package e2e — coverage for the ADR 0008 D4 traceability link surfaced
// on event-returning endpoints.
//
// The test creates a signal through the public POST /signals path, then
// appends an event via the in-process eventbus with `triggered_by_signal_id`
// pointing at that signal. Finally it GETs the workspace timeline and
// asserts the new event carries `triggeredBySignalId` equal to the
// signal's public id. A second, signal-less event is appended in the same
// workspace to lock in that the field is unambiguously null when there is
// no signal lineage.
package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestEventTriggeredBySignalIDFlowsThroughTimeline locks in that
// `triggered_by_signal_id` is round-tripped from eventbus.Append() to the
// GET /workspaces/{wsId}/timeline response as a public_id string. The
// linkage is the contract the Applier will rely on once it starts writing
// SignalApplied / TaskAutoCompleted events.
func TestEventTriggeredBySignalIDFlowsThroughTimeline(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tt.WorkspacePublicID) })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Create a task so the workspace has at least one event-producing row
	// the signal can attach to. The Create handler emits task.created.
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Signal traceability target",
	}, &task)
	require.NotEmpty(t, task.ID)

	// Inject a manual signal attached to the task. The handler also
	// emits SignalAttached so the timeline will already have that row
	// without a `triggered_by_signal_id` set — handy as the "no signal"
	// control sample for the assertion below.
	var signal struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/signals", tt.AccessToken, map[string]any{
		"workspaceId": tt.WorkspacePublicID,
		"source":      "manual",
		"kind":        "manual",
		"taskId":      task.ID,
		"payload":     map[string]any{"trace": "synthetic"},
	}, &signal)
	require.NotEmpty(t, signal.ID)

	// Resolve internal ids: the eventbus contract is in terms of
	// internal BIGINTs so the assertion exercises the same path the
	// Applier will use when it lands.
	wsInternalID, userInternalID := lookupWorkspaceAndOwner(ctx, t, tt.WorkspacePublicID)
	var signalInternalID int64
	require.NoError(t,
		testDB.QueryRowContext(ctx,
			`SELECT id FROM signals WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
			wsInternalID, signal.ID).Scan(&signalInternalID),
		"signal must exist before triggered_by_signal_id can reference it")
	require.Greater(t, signalInternalID, int64(0))

	// Resolve the task's internal id so the synthetic event can be
	// scoped to the task. The timeline endpoint will then surface it
	// under v_task_timeline like any other task event.
	var taskInternalID int64
	require.NoError(t,
		testDB.QueryRowContext(ctx,
			`SELECT id FROM tasks WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
			wsInternalID, task.ID).Scan(&taskInternalID))
	require.Greater(t, taskInternalID, int64(0))

	// Append the traceable event. The chosen type ("task.note") is not
	// a real production kind on purpose — we want to be sure the
	// assertion is keyed on `triggered_by_signal_id` rather than
	// matching any incidental event the handlers emit.
	actorUserID := int64(userInternalID)
	require.NoError(t, eventbus.Append(ctx, dbretry.AutoCommit(testDB), eventbus.Event{
		Type:                "task.note",
		WorkspaceID:         wsInternalID,
		ActorUserID:         &actorUserID,
		TaskID:              &taskInternalID,
		TriggeredBySignalID: &signalInternalID,
		Payload: map[string]any{
			"reason": "test event with signal lineage",
		},
	}), "eventbus.Append must accept TriggeredBySignalID")

	// And append a sibling event with no signal lineage so the null
	// branch of the mapper gets exercised by the same response.
	require.NoError(t, eventbus.Append(ctx, dbretry.AutoCommit(testDB), eventbus.Event{
		Type:        "task.note",
		WorkspaceID: wsInternalID,
		ActorUserID: &actorUserID,
		TaskID:      &taskInternalID,
		Payload: map[string]any{
			"reason": "test event without signal lineage",
		},
	}))

	// Fetch the workspace timeline and locate the two synthetic events.
	// Filtering by kind keeps the assertion focused even when parallel
	// tests share the harness; workspace scoping ensures isolation.
	var tl struct {
		Total  int64 `json:"total"`
		Events []struct {
			ID                  string  `json:"id"`
			Type                string  `json:"type"`
			TriggeredBySignalID *string `json:"triggeredBySignalId"`
			ActorSystemSource   *string `json:"actorSystemSource"`
		} `json:"events"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/timeline?kind=task.note",
		tt.AccessToken, nil, &tl)
	require.Equal(t, int64(2), tl.Total, "timeline must surface both synthetic events")
	require.Len(t, tl.Events, 2)

	var (
		withSignal    int
		withoutSignal int
	)
	for _, ev := range tl.Events {
		require.Equal(t, "task.note", ev.Type)
		require.Nil(t, ev.ActorSystemSource,
			"events without a worker-tick origin must surface actorSystemSource as null")
		if ev.TriggeredBySignalID != nil {
			require.Equal(t, signal.ID, *ev.TriggeredBySignalID,
				"triggeredBySignalId must round-trip the signal's public_id, never the internal id")
			withSignal++
		} else {
			withoutSignal++
		}
	}
	require.Equal(t, 1, withSignal, "exactly one event must carry the signal lineage")
	require.Equal(t, 1, withoutSignal, "exactly one event must omit the signal lineage")
}

// TestEventActorSystemSourceFlowsThroughTimeline locks in that the third
// actor source (ADR 0008 D8, worker-tick events) round-trips from
// eventbus.Append() to the GET /workspaces/{wsId}/timeline response as a
// `actorSystemSource` string. Worker binaries are not yet wired up but the
// contract is exercised here so the worker has a stable boundary.
func TestEventActorSystemSourceFlowsThroughTimeline(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tt.WorkspacePublicID) })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	wsInternalID, _ := lookupWorkspaceAndOwner(ctx, t, tt.WorkspacePublicID)

	require.NoError(t, eventbus.Append(ctx, dbretry.AutoCommit(testDB), eventbus.Event{
		Type:              "workspace.tick",
		WorkspaceID:       wsInternalID,
		ActorSystemSource: "worker.calendar",
		Payload: map[string]any{
			"reason": "synthetic worker tick",
		},
	}))

	var tl struct {
		Total  int64 `json:"total"`
		Events []struct {
			ID                  string  `json:"id"`
			Type                string  `json:"type"`
			ActorSystemSource   *string `json:"actorSystemSource"`
			TriggeredBySignalID *string `json:"triggeredBySignalId"`
		} `json:"events"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/timeline?kind=workspace.tick",
		tt.AccessToken, nil, &tl)
	require.Equal(t, int64(1), tl.Total)
	require.Len(t, tl.Events, 1)
	require.NotNil(t, tl.Events[0].ActorSystemSource)
	require.Equal(t, "worker.calendar", *tl.Events[0].ActorSystemSource)
	require.Nil(t, tl.Events[0].TriggeredBySignalID,
		"events with no signal lineage must surface triggeredBySignalId as null")
}
