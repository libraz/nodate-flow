package autoactions

import "testing"

// TestDecodeAgentMemo exercises the JSON decoder against the shape the
// orchestrator runtime writes into tasks.agent_memo.
func TestDecodeAgentMemo(t *testing.T) {
	t.Parallel()
	attempts, finished := decodeAgentMemo(nil)
	if attempts != 0 || !finished.IsZero() {
		t.Fatalf("nil memo = (%d, %v)", attempts, finished)
	}
	raw := []byte(`{"attempts": 3, "last_finished_at": 1700000000}`)
	attempts, finished = decodeAgentMemo(raw)
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if got := finished.Unix(); got != 1_700_000_000 {
		t.Fatalf("last_finished_at = %d, want 1700000000", got)
	}
}
