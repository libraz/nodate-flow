// Package eventbus tests guard the wire-format strings of every event
// kind. Changing a constant's string value is a breaking change for the
// events table and every downstream consumer (OpenAPI, SDK, webhooks,
// projections), so each value is pinned explicitly and the full set is
// asserted unique.
package eventbus

import "testing"

// TestJudgeLoopKinds pins the wire strings the signaljudge Applier
// emits. These are the only producers of the
// new kinds — if the strings drift, the Applier output schema, the SDK
// enum and the events table CHECK rows all silently disagree.
func TestJudgeLoopKinds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		got  Kind
		want string
	}{
		{"SignalJudged", SignalJudged, "signal.judged"},
		{"SignalApplied", SignalApplied, "signal.applied"},
		{"SignalRejected", SignalRejected, "signal.rejected"},
		{"TaskAutoCompleted", TaskAutoCompleted, "task.auto_completed"},
		{"TaskRetroDrafted", TaskRetroDrafted, "task.retro.drafted"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if string(tc.got) != tc.want {
				t.Fatalf("kind %s: got %q, want %q", tc.name, tc.got, tc.want)
			}
		})
	}
}

// TestKindsAreUnique guards against accidental duplicate string values
// across the full kind set. Two constants resolving to the same string
// would silently merge in projections and webhook routing.
func TestKindsAreUnique(t *testing.T) {
	t.Parallel()

	names := map[string]string{}
	for name, value := range declaredKindConstants(t) {
		if prev, dup := names[value]; dup {
			t.Fatalf("%s and %s both resolve to %q", name, prev, value)
		}
		names[value] = name
	}
}

// TestTaskTransitionHelper verifies the dynamic transition kind builder
// preserves the "task.transition." prefix expected by the projector.
func TestTaskTransitionHelper(t *testing.T) {
	t.Parallel()

	got := TaskTransition("custom")
	const want = "task.transition.custom"
	if got != want {
		t.Fatalf("TaskTransition(\"custom\"): got %q, want %q", got, want)
	}
}
