package ai

import "time"

// Limits on one task-work run. The cumulative subtask cap and the
// pending-proposal cap are taskservice's (MaxAgentChildren,
// MaxPendingProposals).
const (
	// TaskWorkMaxCalls counts every LLM call of a run, repair retries included.
	TaskWorkMaxCalls = 8
	// TaskWorkMaxWrites counts applied and proposed steps.
	TaskWorkMaxWrites = 5
	// TaskWorkMaxSubtasks is how many create_subtask steps one run may write.
	TaskWorkMaxSubtasks = 3
	// TaskWorkTimeout bounds a run's wall-clock time.
	TaskWorkTimeout = 120 * time.Second
	// TaskWorkMaxFailures is the run of consecutive failed steps that ends a
	// run with tool_error.
	TaskWorkMaxFailures = 3
	// TaskWorkMaxRuns is how many task-work runs one agent may start on one
	// task within TaskWorkRateWindow.
	TaskWorkMaxRuns = 20
	// TaskWorkRateWindow is the window TaskWorkMaxRuns is counted over.
	TaskWorkRateWindow = 24 * time.Hour
)

// How a task-work run ended, written to the run's completed or failed
// event as endedReason.
const (
	endedDone          = "done"
	endedMaxSteps      = "max_steps"
	endedWriteCap      = "write_cap"
	endedRepeat        = "repeat"
	endedTimeout       = "timeout"
	endedBudget        = "budget"
	endedInvalidOutput = "invalid_output"
	endedToolError     = "tool_error"
	endedStopped       = "stopped"
	endedCoalesced     = "coalesced"
	endedRateLimited   = "rate_limited"
	endedError         = "error"
)
