package signaljudge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// fakeRecentTasks returns a deterministic slice of n TaskSummary
// rows. Used to drive the cap-behaviour test below.
type fakeRecentTasks struct {
	tasks []TaskSummary
}

// LoadRecent implements [RecentTasksLookup]. Returns the canned slice
// up to limit; the test verifies the cap regardless of what the
// lookup itself returned.
func (f *fakeRecentTasks) LoadRecent(_ context.Context, _ uint32, limit int) ([]TaskSummary, error) {
	if limit <= 0 || limit >= len(f.tasks) {
		return f.tasks, nil
	}
	return f.tasks[:limit], nil
}

// fakeLinkedTasks mirrors fakeRecentTasks for the linked-tasks side.
type fakeLinkedTasks struct {
	tasks []TaskSummary
}

// LoadLinked implements [LinkedTasksLookup].
func (f *fakeLinkedTasks) LoadLinked(_ context.Context, _ uint32, _ int32, limit int) ([]TaskSummary, error) {
	if limit <= 0 || limit >= len(f.tasks) {
		return f.tasks, nil
	}
	return f.tasks[:limit], nil
}

// fakeJudgeInstructions returns a fixed instructions string.
type fakeJudgeInstructions struct{ s string }

// LoadInstructions implements [JudgeInstructionsLookup].
func (f *fakeJudgeInstructions) LoadInstructions(_ context.Context, _ uint32) (string, error) {
	return f.s, nil
}

// makeTasks builds n synthetic TaskSummary rows with distinct ids.
func makeTasks(n int) []TaskSummary {
	out := make([]TaskSummary, n)
	for i := 0; i < n; i++ {
		out[i] = TaskSummary{
			PublicID:     fmt.Sprintf("00000000-0000-0000-0000-%012d", i),
			Title:        fmt.Sprintf("Task #%d", i),
			DerivedState: "open",
		}
	}
	return out
}

// TestBuildPromptContextCapsRecentTasks asserts a lookup returning
// 50 rows is truncated to MaxRecentTasks (20) by the builder.
func TestBuildPromptContextCapsRecentTasks(t *testing.T) {
	t.Parallel()
	deps := PromptDeps{
		RecentTasks: &fakeRecentTasks{tasks: makeTasks(50)},
	}
	sig := SignalSnapshot{WorkspaceID: 1, Kind: "manual", SubjectType: "workspace"}
	pc, err := BuildPromptContext(context.Background(), deps, sig)
	if err != nil {
		t.Fatalf("BuildPromptContext: %v", err)
	}
	if got := len(pc.RecentTasks); got > MaxRecentTasks {
		t.Fatalf("RecentTasks len = %d, want <= %d", got, MaxRecentTasks)
	}
	if got := len(pc.RecentTasks); got != MaxRecentTasks {
		t.Fatalf("RecentTasks len = %d, want exactly %d (lookup returned plenty)", got, MaxRecentTasks)
	}
}

// TestBuildPromptContextCapsLinkedTasks asserts a calendar_event
// subject with 30 linked tasks is truncated to MaxLinkedTasks (10).
func TestBuildPromptContextCapsLinkedTasks(t *testing.T) {
	t.Parallel()
	deps := PromptDeps{
		LinkedTasks: &fakeLinkedTasks{tasks: makeTasks(30)},
	}
	sig := SignalSnapshot{
		WorkspaceID: 1,
		Kind:        "calendar.event.ended",
		SubjectType: "calendar_event",
		SubjectID:   sql.NullInt32{Int32: 42, Valid: true},
	}
	pc, err := BuildPromptContext(context.Background(), deps, sig)
	if err != nil {
		t.Fatalf("BuildPromptContext: %v", err)
	}
	if got := len(pc.LinkedTasks); got > MaxLinkedTasks {
		t.Fatalf("LinkedTasks len = %d, want <= %d", got, MaxLinkedTasks)
	}
	if got := len(pc.LinkedTasks); got != MaxLinkedTasks {
		t.Fatalf("LinkedTasks len = %d, want exactly %d", got, MaxLinkedTasks)
	}
}

// TestBuildPromptContextSkipsLinkedForNonCalendar asserts the
// linked-tasks lookup is NOT invoked when the subject is not a
// calendar_event — different subjects must not pay the JOIN cost.
func TestBuildPromptContextSkipsLinkedForNonCalendar(t *testing.T) {
	t.Parallel()
	called := false
	deps := PromptDeps{
		LinkedTasks: linkedTasksProbe{onCall: func() { called = true }},
	}
	sig := SignalSnapshot{
		WorkspaceID: 1,
		Kind:        "manual",
		SubjectType: "user",
		SubjectID:   sql.NullInt32{Int32: 1, Valid: true},
	}
	if _, err := BuildPromptContext(context.Background(), deps, sig); err != nil {
		t.Fatalf("BuildPromptContext: %v", err)
	}
	if called {
		t.Fatalf("LinkedTasks lookup invoked for subject_type=user")
	}
}

// TestBuildPromptContextRedactsJudgeInstructions asserts a bearer
// token spliced into ai_settings.judge_instructions is removed
// before the prompt is rendered.
func TestBuildPromptContextRedactsJudgeInstructions(t *testing.T) {
	t.Parallel()
	deps := PromptDeps{
		JudgeInstructions: &fakeJudgeInstructions{s: "Always carry Bearer abc12345xyz with requests"},
	}
	sig := SignalSnapshot{WorkspaceID: 1, Kind: "manual", SubjectType: "workspace"}
	pc, err := BuildPromptContext(context.Background(), deps, sig)
	if err != nil {
		t.Fatalf("BuildPromptContext: %v", err)
	}
	if strings.Contains(pc.JudgeInstructions, "abc12345xyz") {
		t.Fatalf("bearer token leaked through to instructions: %q", pc.JudgeInstructions)
	}
	if !strings.Contains(pc.JudgeInstructions, redactionMarker) {
		t.Fatalf("redaction marker missing from instructions: %q", pc.JudgeInstructions)
	}
}

// TestBuildPromptContextRedactsSignalPayload asserts the JSON-walk
// scrubber strips an access_token value from the signal payload.
func TestBuildPromptContextRedactsSignalPayload(t *testing.T) {
	t.Parallel()
	sig := SignalSnapshot{
		WorkspaceID: 1,
		Kind:        "manual",
		SubjectType: "workspace",
		PayloadJSON: json.RawMessage(`{"access_token":"xxx","user":"alice"}`),
	}
	pc, err := BuildPromptContext(context.Background(), PromptDeps{}, sig)
	if err != nil {
		t.Fatalf("BuildPromptContext: %v", err)
	}
	if strings.Contains(string(pc.Signal.PayloadJSON), `"xxx"`) {
		t.Fatalf("access_token survived in payload: %s", string(pc.Signal.PayloadJSON))
	}
	if !strings.Contains(string(pc.Signal.PayloadJSON), redactionMarker) {
		t.Fatalf("redaction marker missing from payload: %s", string(pc.Signal.PayloadJSON))
	}
}

// TestRenderUserPromptTruncates asserts that when the rendered body
// exceeds MaxContextBytes the truncator drops RecentTasks first.
//
// The titles go into the context as they are. Running them through
// capTasks first, as this once did, shortens each to MaxTaskTitleLen and
// leaves the whole section around 3 KB — under the cap, so nothing was
// truncated and the size assertion held for the wrong reason.
func TestRenderUserPromptTruncates(t *testing.T) {
	t.Parallel()
	// Build a context where RecentTasks alone is many KB long.
	bigTasks := make([]TaskSummary, MaxRecentTasks)
	pad := strings.Repeat("padding ", 200) // ~1.6 KB per task title
	for i := range bigTasks {
		bigTasks[i] = TaskSummary{
			PublicID:     fmt.Sprintf("00000000-0000-0000-0000-%012d", i),
			Title:        pad,
			DerivedState: "open",
		}
	}
	pc := PromptContext{
		Signal:      SignalContext{Kind: "manual", PayloadJSON: json.RawMessage(`{"x":1}`)},
		RecentTasks: bigTasks,
	}
	out := RenderUserPrompt(pc)
	if len(out) > MaxContextBytes {
		t.Fatalf("rendered body too large after truncation: %d bytes", len(out))
	}
	if strings.Contains(out, "## Recent tasks") {
		t.Fatalf("the oversized section survived, so the truncator did not run")
	}
	if !strings.Contains(out, "## Signal") {
		t.Fatalf("Signal section dropped during truncation; must always survive")
	}
}

// linkedTasksProbe is a no-op LinkedTasksLookup used to verify the
// builder did not invoke it under inappropriate subject types.
type linkedTasksProbe struct {
	onCall func()
}

// LoadLinked implements [LinkedTasksLookup].
func (p linkedTasksProbe) LoadLinked(_ context.Context, _ uint32, _ int32, _ int) ([]TaskSummary, error) {
	p.onCall()
	return nil, nil
}
