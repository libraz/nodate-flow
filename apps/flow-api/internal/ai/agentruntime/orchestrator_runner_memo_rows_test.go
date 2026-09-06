package agentruntime

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// A memo write carries `enabled = TRUE`, so it can succeed and match no
// row: the task was disabled or removed while the run was under way. The
// patch then carries counters — the attempt count, the handoff count, the
// handoff status — that the run believes it advanced and that still hold
// their old values, so the next pass reads the same loop budget and the
// same handoff is eligible again.
//
// The runner cannot act on that, so the log is where it has to be
// answerable, and answerable means told apart from the other two
// outcomes rather than merely mentioned. The three tests below drive the
// same patch onto the same task through the same call, differing only in
// what the write reports back, and each one asserts the absence of the
// other two lines: that is what makes a shared or missing line a failure
// rather than something no assertion looks at.

// captureRunnerLogs redirects the default slog logger into a buffer for
// the duration of the test, at debug level so every outcome the runner
// distinguishes is captured. Tests using it must stay sequential (no
// t.Parallel) because the default logger is process-global; Go never runs
// a sequential test alongside a parallel one in the same package.
func captureRunnerLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// The three lines mergeMemo answers with, one per outcome.
const (
	memoApplied = "agentruntime: agent_memo updated"
	memoMissed  = "agentruntime: agent_memo update matched no task"
	memoLost    = "agentruntime: agent_memo update lost"
)

// assertOnlyLogged fails unless the buffer holds want and neither of the
// other two outcome lines.
func assertOnlyLogged(t *testing.T, logs *bytes.Buffer, want string) {
	t.Helper()
	text := logs.String()
	if !strings.Contains(text, want) {
		t.Errorf("the memo write did not report %q; logged %s", want, text)
	}
	for _, other := range []string{memoApplied, memoMissed, memoLost} {
		if other == want {
			continue
		}
		if strings.Contains(text, other) {
			t.Errorf("the memo write reported %q as well as %q, so the two outcomes cannot be "+
				"told apart; logged %s", other, want, text)
		}
	}
}

// TestMemoPatchOnALiveTaskIsReportedAsApplied is the positive control.
// Without it the absences asserted below would hold of a call that wrote
// nothing at all.
func TestMemoPatchOnALiveTaskIsReportedAsApplied(t *testing.T) {
	logs := captureRunnerLogs(t)

	q := newFakeQuerier()
	r := newRunnerWithQuerier(q, nil)
	r.mergeMemo(context.Background(), 1, 11, map[string]any{"attempts": 3})

	if got := q.snapshotMemo(11)["attempts"]; got != float64(3) {
		t.Fatalf("attempts = %v, want 3; the patch did not reach the memo", got)
	}
	assertOnlyLogged(t, logs, memoApplied)
}

// TestMemoPatchThatMatchesNoTaskIsReportedAsChangingNothing is the case
// the count answers. Same patch, same task, same call — the write simply
// matched no row, so the counter the run believed it advanced still holds
// the value the next pass will read.
func TestMemoPatchThatMatchesNoTaskIsReportedAsChangingNothing(t *testing.T) {
	logs := captureRunnerLogs(t)

	q := newFakeQuerier()
	q.memoWriteMisses(11)
	r := newRunnerWithQuerier(q, nil)
	r.mergeMemo(context.Background(), 1, 11, map[string]any{"attempts": 3})

	if got, ok := q.snapshotMemo(11)["attempts"]; ok {
		t.Errorf("attempts = %v; a write that matched no row must leave the memo as it was", got)
	}
	assertOnlyLogged(t, logs, memoMissed)
}

// TestMemoWriteFailureIsReportedApartFromAMatchOfNoTask pins the third
// outcome. A write that never reached the row is a fault to be chased,
// and a write that reached it and matched nothing is the task having gone
// — reporting both the same way would leave a reader unable to tell an
// unreachable database from a stalled agent loop.
//
// The injected error is the permanent class, so the write is attempted
// once and the failure is the outcome rather than a retry away from it.
func TestMemoWriteFailureIsReportedApartFromAMatchOfNoTask(t *testing.T) {
	logs := captureRunnerLogs(t)

	q := newFlakyQuerier(duplicateKeyErr())
	q.failMemo = 99
	r := newRunnerWithQuerier(q, nil)
	r.mergeMemo(context.Background(), 1, 11, map[string]any{"attempts": 3})

	if got, ok := q.snapshotMemo(11)["attempts"]; ok {
		t.Errorf("attempts = %v; a write that failed must leave the memo as it was", got)
	}
	assertOnlyLogged(t, logs, memoLost)
}
