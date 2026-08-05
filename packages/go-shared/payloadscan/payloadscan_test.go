package payloadscan_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/payloadscan"
)

// TestScanSeparatesLeaksFromLookalikes runs the scanner over a fixture
// package holding both the shapes it must catch and the shapes a source
// scan gets wrong.
//
// The two lookalikes matter as much as the leaks. `"taskId": taskID`
// where taskID is already a UUID string is the false positive that would
// have gone into an allowlist, and a commented-out `.String()` call is
// the false negative that a substring check waves through — the same
// failure that let a commented-out bridge call pass its guard.
func TestScanSeparatesLeaksFromLookalikes(t *testing.T) {
	findings, err := payloadscan.Scan(payloadscan.Config{Dir: "testdata/sample"})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	got := map[string]string{}
	for _, f := range findings {
		got[f.Key] = f.Type
	}

	wantKeys := []string{
		"userId",  // written inline
		"eventId", // hoisted into a local variable
		"labelId", // hidden behind an any-typed variable
		"agentId", // in ExtraPayload rather than Payload
		"taskId",  // the commented-out fix
	}
	for _, key := range wantKeys {
		if _, ok := got[key]; !ok {
			t.Errorf("scan missed the leak under %q; found %v", key, keys(got))
		}
	}

	// CleanInline and CleanPassThrough both write strings, so the only
	// taskId finding must be the commented-out one. Anything else means
	// a string value was reported.
	taskIDFindings := 0
	for _, f := range findings {
		if f.Key == "taskId" {
			taskIDFindings++
			if strings.Contains(f.Type, "string") {
				t.Errorf("a string value was reported as a leak: %s", f)
			}
		}
	}
	if taskIDFindings != 1 {
		t.Errorf("taskId reported %d times, want exactly 1 (the commented-out fix)", taskIDFindings)
	}
}

// TestScanIgnoresMapsOutsideAPayload keeps the rule scoped: an id-shaped
// key in a map that never reaches the events table is not this check's
// business, and reporting it would push callers toward an allowlist.
func TestScanIgnoresMapsOutsideAPayload(t *testing.T) {
	findings, err := payloadscan.Scan(payloadscan.Config{Dir: "testdata/sample"})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, f := range findings {
		if strings.Contains(f.Pos, "UnrelatedMap") {
			t.Errorf("scan reported a map outside a payload field: %s", f)
		}
	}
	// UnrelatedMap is the only place a bare id-shaped map appears, and it
	// sits on its own line, so a finding pointing at it would surface as
	// an extra taskId entry — covered by the count assertion above.
}

// TestIsIDKeyMatchesTheRuntimeRule pins the key rule the scan shares with
// the append-time validator. The two drifting apart would mean code that
// passes the scan and then fails in production, or the reverse.
func TestIsIDKeyMatchesTheRuntimeRule(t *testing.T) {
	t.Parallel()

	idKeys := []string{"id", "ids", "taskId", "taskID", "taskIds", "sourceTaskId", "userId"}
	for _, key := range idKeys {
		if !payloadscan.IsIDKey(key) {
			t.Errorf("IsIDKey(%q) = false, want true", key)
		}
	}
	plainKeys := []string{"title", "count", "idempotencyKey", "valid", "hidden", "confidence"}
	for _, key := range plainKeys {
		if payloadscan.IsIDKey(key) {
			t.Errorf("IsIDKey(%q) = true, want false", key)
		}
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
