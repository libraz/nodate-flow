package webhooks

import (
	"encoding/json"
	"testing"
)

// TestNormalizeEventTypesAcceptsOnlyWhatTheWorkerMatches pins create-time
// validation to the delivery matcher, which compares each entry to the
// event kind by exact equality and treats only "*" as a wildcard. A
// pattern or a misspelled kind stored here is a subscription that never
// receives a delivery.
func TestNormalizeEventTypesAcceptsOnlyWhatTheWorkerMatches(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{`["*"]`, true},
		{`["task.transition.complete"]`, true},
		{`["task.created","task.updated"]`, true},
		{`["task.*"]`, false},
		{`["task.completed"]`, false},
		{`["task.created","nope"]`, false},
		{`[]`, false},
		{`null`, false},
		{`"task.created"`, false},
	} {
		_, ok := normalizeEventTypes(json.RawMessage(tc.raw))
		if ok != tc.want {
			t.Errorf("normalizeEventTypes(%s) ok = %v, want %v", tc.raw, ok, tc.want)
		}
	}

	if got, ok := normalizeEventTypes(nil); !ok || string(got) != `["*"]` {
		t.Errorf("absent eventTypes = %s, %v; want [\"*\"], true", got, ok)
	}
}
