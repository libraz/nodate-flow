package agentruntime

import (
	"strconv"
	"strings"
)

// judgeDedupeKeyPrefix is the constant scope token the signaljudge
// enqueuer stamps onto agent_runs.dedupe_key (shape:
// `judge:<agentID>:<signalID>`). The orchestrator runner uses it to
// decide whether a claimed row should dispatch through JudgeExecutor
// instead of the regular AgentExecutor.
//
// Kept in agentruntime (rather than signaljudge) so the runner does
// not import signaljudge — the import direction is signaljudge →
// agentruntime, and inverting it would create a cycle.
const judgeDedupeKeyPrefix = "judge:"

// parseJudgeDedupeKey returns the signal id when dedupeKey was shaped
// by signaljudge.DedupeKeyForSignal and ok=true. ok=false means the
// runner should dispatch through the task-agent AgentExecutor path.
//
// Implementation matches signaljudge.SignalIDFromDedupeKey verbatim;
// the duplication is intentional so the runner stays free of an
// import cycle. Any change to the key shape must be applied in both
// places (the test in signaljudge/enqueuer_test.go pins the format).
func parseJudgeDedupeKey(dedupeKey string) (signalID int64, ok bool) {
	if !strings.HasPrefix(dedupeKey, judgeDedupeKeyPrefix) {
		return 0, false
	}
	parts := strings.Split(dedupeKey, ":")
	if len(parts) != 3 {
		return 0, false
	}
	if agentID, err := strconv.ParseUint(parts[1], 10, 32); err != nil || agentID == 0 {
		return 0, false
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
