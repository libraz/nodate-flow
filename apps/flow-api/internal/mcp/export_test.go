package mcp

import "time"

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

// Exported tool entry points for regression tests.
var (
	RunArchiveTask           = runArchiveTask
	RunUnarchiveTask         = runUnarchiveTask
	RunProposePriority       = runProposePriority
	RunProposeSteps          = runProposeSteps
	RunGeneratePage          = runGeneratePage
	RunResolveTaskRef        = runResolveTaskRef
	RunGetTask               = runGetTask
	RunUpdateTask            = runUpdateTask
	RunTransitionTask        = runTransitionTask
	RunAddComment            = runAddComment
	RunAddTaskLabel          = runAddTaskLabel
	RunCreateTask            = runCreateTask
	RunExportTasks           = runExportTasks
	RunListImportJobs        = runListImportJobs
	RunCreateImportJob       = runCreateImportJob
	RunCreateCalendarEvent   = runCreateCalendarEvent
	RunUpdateCalendarEvent   = runUpdateCalendarEvent
	RunDeleteCalendarEvent   = runDeleteCalendarEvent
	RunToggleCalendarMemo    = runToggleCalendarMemo
	RunCreateEventFromTask   = runCreateEventFromTask
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
