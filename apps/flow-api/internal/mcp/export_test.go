package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// This file exposes a minimal, test-only surface of the package's
// unexported tool entry points so that external (mcp_test) integration
// tests can drive individual tools against a real database. It exists
// only in the test build. External test packages are used here because a
// DB-backed test must import tests/helpers, which transitively imports
// this package; that import is only legal from an external test package.

// TestSession aliases the unexported per-call caller context so external
// test packages can construct a session for direct tool invocation.
type TestSession = session

// NewTestSession builds a caller session bound to a workspace with the
// given scopes. agentID is left zero (human-owned token).
func NewTestSession(userID, workspaceID uint32, scopes []string) *TestSession {
	return &session{userID: userID, workspaceID: workspaceID, scopes: scopes}
}

// ToolFloors returns the role floor every registered tool was registered
// under, keyed by tool name. It is how a test outside this package compares
// the MCP tool table against another transport's operation inventory.
func ToolFloors() map[string]auth.Floor {
	reg := catalogue()
	out := make(map[string]auth.Floor, len(reg))
	for name, t := range reg {
		out[name] = t.floor
	}
	return out
}

// toolEntry returns a registered tool's run function, taken from the
// registry rather than from the implementation directly.
//
// Going through the registry is what makes a direct invocation behave like
// a dispatched one: registration is where the declared floor is bound to
// the call, so a test that reached past it would exercise the tool with no
// floor at all and prove nothing about the floor the transport applies.
func toolEntry(name string) toolRun {
	return func(ctx context.Context, deps Deps, s *session, args json.RawMessage) (any, error) {
		t, ok := catalogue()[name]
		if !ok {
			return nil, apierrors.Newf(apierrors.McpToolNotFound, "tool not found: %s", name)
		}
		return t.run(ctx, deps, s, args)
	}
}

// Exported tool entry points for regression tests.
var (
	RunArchiveTask           = toolEntry("archive_task")
	RunUnarchiveTask         = toolEntry("unarchive_task")
	RunProposePriority       = toolEntry("propose_priority")
	RunProposeSteps          = toolEntry("propose_steps")
	RunGeneratePage          = toolEntry("generate_page")
	RunResolveTaskRef        = toolEntry("resolve_task_ref")
	RunGetTask               = toolEntry("get_task")
	RunUpdateTask            = toolEntry("update_task")
	RunTransitionTask        = toolEntry("transition_task")
	RunAddComment            = toolEntry("add_comment")
	RunAddTaskLabel          = toolEntry("add_task_label")
	RunCreateTask            = toolEntry("create_task")
	RunExportTasks           = toolEntry("export_tasks")
	RunAddFavorite           = toolEntry("add_favorite")
	RunListFavorites         = toolEntry("list_favorites")
	RunTriageIntakeItem      = toolEntry("triage_intake_item")
	RunListImportJobs        = toolEntry("list_import_jobs")
	RunCreateImportJob       = toolEntry("create_import_job")
	RunCreateCalendarEvent   = toolEntry("create_calendar_event")
	RunUpdateCalendarEvent   = toolEntry("update_calendar_event")
	RunDeleteCalendarEvent   = toolEntry("delete_calendar_event")
	RunToggleCalendarMemo    = toolEntry("toggle_calendar_memo")
	RunCreateEventFromTask   = toolEntry("create_event_from_task")
	WithInvocationTargetTest = withInvocationTarget
	InvocationTaskIDTest     = invocationTaskID
)

// SetSSEIntervalsForTest shortens the stream's heartbeat and
// revalidation periods and returns a restore func.
//
// The production revalidation period is measured in tens of seconds,
// which is the right cadence for a live stream and the wrong one for a
// test that has to observe a revoked credential closing a connection.
// The test drives the same loop the server runs; only the clock moves.
func SetSSEIntervalsForTest(heartbeat, revalidate time.Duration) func() {
	prevHeartbeat, prevRevalidate := heartbeatInterval, sseRevalidateInterval
	heartbeatInterval, sseRevalidateInterval = heartbeat, revalidate
	return func() {
		heartbeatInterval, sseRevalidateInterval = prevHeartbeat, prevRevalidate
	}
}
