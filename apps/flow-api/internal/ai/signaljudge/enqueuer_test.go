package signaljudge

import (
	"testing"
)

// TestDedupeKeyRoundTrip pins the dedupe_key format that both the
// enqueuer and the orchestrator runner parse. Changing the shape
// requires updating agentruntime/judge_dispatch.go in lockstep.
func TestDedupeKeyRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		agentID  uint32
		signalID int64
		wantKey  string
	}{
		{name: "basic", agentID: 1, signalID: 42, wantKey: "judge:1:42"},
		{name: "large_agent", agentID: 4_000_000_000, signalID: 1, wantKey: "judge:4000000000:1"},
		{name: "large_signal", agentID: 1, signalID: 9_000_000_000, wantKey: "judge:1:9000000000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DedupeKeyForSignal(tc.agentID, tc.signalID)
			if got != tc.wantKey {
				t.Fatalf("DedupeKeyForSignal = %q, want %q", got, tc.wantKey)
			}
			parsed, ok := SignalIDFromDedupeKey(got)
			if !ok {
				t.Fatalf("SignalIDFromDedupeKey(%q) ok=false, want true", got)
			}
			if parsed != tc.signalID {
				t.Fatalf("SignalIDFromDedupeKey(%q) = %d, want %d", got, parsed, tc.signalID)
			}
		})
	}
}

// TestSignalIDFromDedupeKey_Rejects guards the parser against
// task-agent dedupe keys (which use shape `<agent>:<unix_minute>` or
// `<agent>:event:<kind>:<nano>`) leaking into the judge dispatch
// branch.
func TestSignalIDFromDedupeKey_Rejects(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",                          // empty
		"123:1700000000",            // interval scheduler shape
		"123:event:task.created:42", // on_event shape
		"judge:abc:42",              // non-numeric agent
		"judge:1:abc",               // non-numeric signal
		"judge:1:0",                 // zero signal rejected
		"judge:1:-5",                // negative signal rejected
		"judge:1",                   // missing signal
		"judge:1:2:3",               // extra field
	}
	for _, in := range cases {
		if _, ok := SignalIDFromDedupeKey(in); ok {
			t.Fatalf("SignalIDFromDedupeKey(%q) ok=true, want false", in)
		}
	}
}
