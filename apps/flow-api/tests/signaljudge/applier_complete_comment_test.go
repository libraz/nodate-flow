// Package signaljudgetests — integration coverage for the production
// `complete_task` and `add_comment` Applier branches backed by the
// SQL [signaljudge.SQLTaskMutator].
//
// Where applier_retro_test.go covers the retro branch end-to-end, this
// file pins the `complete_task` and `add_comment` branches:
//
//   - complete_task under autonomy=auto must move the source task's
//     tasks.derived_state to 'done' (the real mutation), not merely
//     emit TaskAutoCompleted. A second auto-complete on the already-done
//     task is idempotent (no error, the state stays 'done').
//   - add_comment under autonomy=auto must persist a real comments row
//     authored by a resolvable workspace member, with the body carrying
//     the signal-judge attribution prefix.
//
// All tests gate on NF_TEST_INTEGRATION via bootstrap(t) and skip
// cleanly on machines without Docker.
package signaljudgetests

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/signaljudge"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestApplierCompleteTaskMovesDerivedStateToDone pins the complete_task
// branch: a verdict with action=complete_task under autonomy=auto
// must (a) move the source task's tasks.derived_state to 'done' through
// the canonical transition path and (b) emit TaskAutoCompleted — the
// state change and the audit event must agree, no divergence.
func TestApplierCompleteTaskMovesDerivedStateToDone(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := helpers.CreateTestTenant(t, testSrv.BaseURL)
	wsID := lookupWorkspaceIDForRetro(t, testDB, tt.WorkspacePublicID)

	srcPub, srcInternalID := createSourceTask(t, tt, "Ship the release")

	// Sanity: the task starts open (the source state from which the
	// state machine permits a direct complete).
	require.Equal(t, "open", loadDerivedState(t, testDB, wsID, srcInternalID),
		"source task must start at derived_state='open'")

	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{Kind: "signal_judge"})
	signalInternalID := insertSignalRowForRetro(t, testDB, wsID, "calendar.event_day_arrived")

	bus := &recordingBus{}
	applier := &signaljudge.Applier{
		Bus: bus,
		Tasks: &signaljudge.SQLTaskMutator{
			DB:      testDB,
			Queries: generated.New(testDB),
			Logger:  slog.New(slog.DiscardHandler),
		},
		Resolver: &signaljudge.SQLTaskResolver{DB: testDB},
		Signals:  nopSignalUpdater{},
		Autonomy: &staticAutonomy{level: signaljudge.AutonomyAuto},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := applier.Apply(ctx, signaljudge.SignalRef{
		InternalID:  signalInternalID,
		PublicID:    "00000000-0000-0000-0000-c0ffee000001",
		WorkspaceID: wsID,
		Kind:        "calendar.event_day_arrived",
	}, signaljudge.AgentRef{InternalID: agent.AgentID}, 10, signaljudge.Verdict{
		Action:             signaljudge.ActionCompleteTask,
		TargetTaskPublicID: ptrString(srcPub),
		Confidence:         0.92,
		ReasoningExcerpt:   "all acceptance criteria met; closing the task",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.False(t, res.Skipped, "auto complete_task must materialise (Skipped=false)")

	// 1. The real mutation landed: derived_state is now 'done'. This is
	//    the assertion a no-op mutator fails.
	require.Equal(t, "done", loadDerivedState(t, testDB, wsID, srcInternalID),
		"complete_task must move tasks.derived_state to 'done', not just emit an event")

	// 2. Both the canonical transition event and the judge audit event
	//    landed. TaskAutoCompleted must be present, and a
	//    task.transition.complete row must exist for the same task —
	//    proving the state change and the audit trail agree.
	events := bus.snapshot()
	require.True(t, hasEventType(events, eventbus.TaskAutoCompleted),
		"TaskAutoCompleted must be emitted by the Applier")
	require.Equal(t, 1, countTransitionCompleteEvents(t, testDB, wsID, srcInternalID),
		"the canonical task.transition.complete event must be appended exactly once")
}

// TestApplierCompleteTaskIsIdempotent pins that re-running an
// auto-complete on a task that is already 'done' is a no-op success:
// the mutator returns nil (so the Applier still emits its audit event)
// and the state stays 'done'. No second task.transition.complete row is
// appended because the state machine has no complete-out-of-done edge.
func TestApplierCompleteTaskIsIdempotent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := helpers.CreateTestTenant(t, testSrv.BaseURL)
	wsID := lookupWorkspaceIDForRetro(t, testDB, tt.WorkspacePublicID)

	srcPub, srcInternalID := createSourceTask(t, tt, "Already finished work")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{Kind: "signal_judge"})
	signalInternalID := insertSignalRowForRetro(t, testDB, wsID, "calendar.event_day_arrived")

	mutator := &signaljudge.SQLTaskMutator{
		DB:      testDB,
		Queries: generated.New(testDB),
		Logger:  slog.New(slog.DiscardHandler),
	}
	applier := &signaljudge.Applier{
		Bus:      &recordingBus{},
		Tasks:    mutator,
		Resolver: &signaljudge.SQLTaskResolver{DB: testDB},
		Signals:  nopSignalUpdater{},
		Autonomy: &staticAutonomy{level: signaljudge.AutonomyAuto},
	}

	verdict := signaljudge.Verdict{
		Action:             signaljudge.ActionCompleteTask,
		TargetTaskPublicID: ptrString(srcPub),
		Confidence:         0.9,
		ReasoningExcerpt:   "closing the task",
	}
	sig := signaljudge.SignalRef{
		InternalID:  signalInternalID,
		PublicID:    "00000000-0000-0000-0000-c0ffee000002",
		WorkspaceID: wsID,
		Kind:        "calendar.event_day_arrived",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// First completion.
	_, err := applier.Apply(ctx, sig, signaljudge.AgentRef{InternalID: agent.AgentID}, 20, verdict)
	require.NoError(t, err)
	require.Equal(t, "done", loadDerivedState(t, testDB, wsID, srcInternalID))

	// Second completion on the already-done task: must not error.
	_, err = applier.Apply(ctx, sig, signaljudge.AgentRef{InternalID: agent.AgentID}, 21, verdict)
	require.NoError(t, err, "re-completing an already-done task must be idempotent")
	require.Equal(t, "done", loadDerivedState(t, testDB, wsID, srcInternalID),
		"state must remain 'done' after the idempotent re-completion")

	// Exactly one transition event — the state machine refuses a second
	// complete-out-of-done, so no duplicate transition row is written.
	require.Equal(t, 1, countTransitionCompleteEvents(t, testDB, wsID, srcInternalID),
		"only the first completion appends a task.transition.complete event")
}

// TestApplierAddCommentPersistsRealCommentRow pins that an
// add_comment verdict under autonomy=auto writes a real comments row
// (not a silent no-op). The row is authored by a resolvable workspace
// member and the body carries the signal-judge attribution prefix.
func TestApplierAddCommentPersistsRealCommentRow(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := helpers.CreateTestTenant(t, testSrv.BaseURL)
	wsID := lookupWorkspaceIDForRetro(t, testDB, tt.WorkspacePublicID)

	srcPub, srcInternalID := createSourceTask(t, tt, "Discuss the blocker")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{Kind: "signal_judge"})
	signalInternalID := insertSignalRowForRetro(t, testDB, wsID, "calendar.event_day_arrived")

	before := countCommentsForTask(t, testDB, wsID, srcInternalID)

	applier := &signaljudge.Applier{
		Bus: &recordingBus{},
		Tasks: &signaljudge.SQLTaskMutator{
			DB:      testDB,
			Queries: generated.New(testDB),
			Logger:  slog.New(slog.DiscardHandler),
		},
		Resolver: &signaljudge.SQLTaskResolver{DB: testDB},
		Signals:  nopSignalUpdater{},
		Autonomy: &staticAutonomy{level: signaljudge.AutonomyAuto},
	}

	const reasoning = "blocked on the upstream API; recommend escalating to the platform team"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := applier.Apply(ctx, signaljudge.SignalRef{
		InternalID:  signalInternalID,
		PublicID:    "00000000-0000-0000-0000-c0ffee000003",
		WorkspaceID: wsID,
		Kind:        "calendar.event_day_arrived",
	}, signaljudge.AgentRef{InternalID: agent.AgentID}, 30, signaljudge.Verdict{
		Action:             signaljudge.ActionAddComment,
		TargetTaskPublicID: ptrString(srcPub),
		Confidence:         0.88,
		ReasoningExcerpt:   reasoning,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.False(t, res.Skipped, "auto add_comment must materialise (Skipped=false)")

	// Exactly one new comment row landed for the task.
	require.Equal(t, before+1, countCommentsForTask(t, testDB, wsID, srcInternalID),
		"add_comment must persist exactly one real comments row")

	body, authorID := loadLatestComment(t, testDB, wsID, srcInternalID)
	require.NotZero(t, authorID, "the comment must be authored by a real workspace member")
	require.True(t, strings.HasPrefix(body, "[signal judge] "),
		"the comment body must carry the signal-judge attribution prefix; got %q", body)
	require.Contains(t, body, reasoning,
		"the comment body must preserve the judge's reasoning excerpt")
}

// ----- test helpers ----------------------------------------------------------

// loadDerivedState returns the current derived_state of a task by its
// internal id.
func loadDerivedState(t *testing.T, db *sql.DB, workspaceID uint32, taskInternalID int64) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var state string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT derived_state FROM tasks WHERE workspace_id = ? AND id = ? LIMIT 1`,
		workspaceID, taskInternalID,
	).Scan(&state))
	return state
}

// hasEventType reports whether the recorded bus saw at least one event
// of the given type.
func hasEventType(events []eventbus.Event, want eventbus.Kind) bool {
	for _, e := range events {
		if e.Type == want {
			return true
		}
	}
	return false
}

// countTransitionCompleteEvents counts the canonical
// task.transition.complete rows the taskstate helper persisted for a
// task. The Applier writes TaskAutoCompleted through its own bus, while
// taskstate.ApplyTransitionTx writes the transition event straight to
// the events table inside the same transaction — so this count confirms
// the real state-machine path ran.
func countTransitionCompleteEvents(t *testing.T, db *sql.DB, workspaceID uint32, taskInternalID int64) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var n int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events
		 WHERE workspace_id = ? AND task_id = ? AND type = ?`,
		workspaceID, taskInternalID, string(eventbus.TaskTransition("complete")),
	).Scan(&n))
	return n
}

// countCommentsForTask returns the number of enabled comments on a
// task by its internal id.
func countCommentsForTask(t *testing.T, db *sql.DB, workspaceID uint32, taskInternalID int64) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var n int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM comments WHERE workspace_id = ? AND task_id = ? AND enabled = TRUE`,
		workspaceID, taskInternalID,
	).Scan(&n))
	return n
}

// loadLatestComment returns the body and author_id of the most recently
// created comment on a task.
func loadLatestComment(t *testing.T, db *sql.DB, workspaceID uint32, taskInternalID int64) (string, uint32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var (
		body     string
		authorID uint32
	)
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT body, author_id FROM comments
		 WHERE workspace_id = ? AND task_id = ? AND enabled = TRUE
		 ORDER BY id DESC LIMIT 1`,
		workspaceID, taskInternalID,
	).Scan(&body, &authorID))
	return body, authorID
}
