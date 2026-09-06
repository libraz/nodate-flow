package signaljudge

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

// A judge run reads its evidence through four lookups and then renders it
// into one flat body. Nothing in that body distinguishes a section that
// was assembled and came back empty from a section that was never
// assembled at all, so a lookup whose failure is swallowed does not
// produce a visibly broken prompt — it produces a smaller workspace. The
// judge reasons over the smaller one, finds nothing to target, and
// returns a noop at ordinary confidence, which ai_invocations then records
// as a decision that was made.
//
// These tests hold the builder to telling the difference: the three
// lookups that carry evidence fail the run, and the one that carries only
// a frame of reference degrades in writing.

// errLookupFailed stands in for whatever the database returned.
var errLookupFailed = errors.New("lookup failed")

// erroringRecentTasks fails every LoadRecent call.
type erroringRecentTasks struct{}

func (erroringRecentTasks) LoadRecent(_ context.Context, _ uint32, _ int) ([]TaskSummary, error) {
	return nil, errLookupFailed
}

// erroringLinkedTasks fails every LoadLinked call.
type erroringLinkedTasks struct{}

func (erroringLinkedTasks) LoadLinked(_ context.Context, _ uint32, _ int32, _ int) ([]TaskSummary, error) {
	return nil, errLookupFailed
}

// erroringJudgeInstructions fails every LoadInstructions call.
type erroringJudgeInstructions struct{}

func (erroringJudgeInstructions) LoadInstructions(_ context.Context, _ uint32) (string, error) {
	return "", errLookupFailed
}

// calendarEventSignal is the subject shape that makes the builder consult
// the linked-tasks lookup.
func calendarEventSignal() SignalSnapshot {
	return SignalSnapshot{
		WorkspaceID: 1,
		Kind:        "calendar.event.ended",
		Source:      "calendar",
		SubjectType: "calendar_event",
		SubjectID:   sql.NullInt32{Int32: 42, Valid: true},
	}
}

// TestBuildPromptContextRefusesAContextItCouldNotAssemble covers each
// evidence-bearing lookup in turn.
//
// The assertion is deliberately on the whole return value and not just on
// the one section: a builder that reported the error and handed back the
// rest of the context would still let a caller that ignores errors render
// the truncated prompt, which is the behaviour being removed.
func TestBuildPromptContextRefusesAContextItCouldNotAssemble(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		deps PromptDeps
	}{
		{
			name: "recent tasks",
			deps: PromptDeps{RecentTasks: erroringRecentTasks{}},
		},
		{
			name: "linked tasks",
			deps: PromptDeps{LinkedTasks: erroringLinkedTasks{}},
		},
		{
			name: "judge instructions",
			deps: PromptDeps{JudgeInstructions: erroringJudgeInstructions{}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pc, err := BuildPromptContext(context.Background(), tc.deps, calendarEventSignal())
			if err == nil {
				t.Fatalf("the %s lookup failed and the build reported success; the judge would "+
					"read the resulting prompt as a workspace with nothing in it", tc.name)
			}
			if !errors.Is(err, errLookupFailed) {
				t.Errorf("the failure that reached the caller is %v, which does not carry the "+
					"lookup's own error", err)
			}
			if pc.Now != "" || len(pc.RecentTasks) != 0 || len(pc.LinkedTasks) != 0 ||
				pc.JudgeInstructions != "" || pc.Signal.Kind != "" {
				t.Errorf("a refused build returned a renderable context: %+v", pc)
			}
		})
	}
}

// TestBuildPromptContextSeparatesAnEmptySectionFromAFailedOne is the
// other half: refusing a failure is only worth anything if an ordinary
// empty workspace still builds.
func TestBuildPromptContextSeparatesAnEmptySectionFromAFailedOne(t *testing.T) {
	t.Parallel()

	deps := PromptDeps{
		RecentTasks:       &fakeRecentTasks{},
		LinkedTasks:       &fakeLinkedTasks{},
		JudgeInstructions: &fakeJudgeInstructions{},
	}
	pc, err := BuildPromptContext(context.Background(), deps, calendarEventSignal())
	if err != nil {
		t.Fatalf("a workspace with no tasks and no policy is not a failure: %v", err)
	}
	if len(pc.ContextGaps) != 0 {
		t.Errorf("lookups that answered are reported as gaps: %v", pc.ContextGaps)
	}
}

// TestExecuteJudgeRefusesToJudgeATruncatedContext is the same rule one
// layer up, where the cost of getting it wrong is paid.
//
// The linked-tasks lookup is the one that fails because a calendar_event
// signal's linked tasks are its most specific evidence: the run that
// loses them is the run most likely to answer confidently about the wrong
// thing. Nothing may reach the provider, so nothing is billed and no
// verdict — confident or otherwise — is recorded.
func TestExecuteJudgeRefusesToJudgeATruncatedContext(t *testing.T) {
	t.Parallel()

	prov := &recordingProvider{
		kind: providers.Kind("mock"),
		resp: &providers.Response{
			Text: `{"action":"noop","confidence":0.8,"reasoning_excerpt":"no linked task matches"}`,
		},
	}
	var logged []InvocationRecord
	r := &Runner{
		Agents:   &fakeAgentLookup{snap: AgentSnapshot{AgentID: 1, WorkspaceID: 1}},
		Signals:  &fakeSignalLookup{snap: calendarEventSignal()},
		Resolver: &fakeResolver{provider: prov},
		Log:      func(_ context.Context, rec InvocationRecord) { logged = append(logged, rec) },
		Prompt:   PromptDeps{LinkedTasks: erroringLinkedTasks{}},
	}

	_, err := r.ExecuteJudge(context.Background(), 1, 1, 1)
	if err == nil {
		t.Fatal("the judge ran on a context whose linked-tasks section could not be loaded, " +
			"and the run reported success")
	}
	if !errors.Is(err, errLookupFailed) {
		t.Errorf("the run failed with %v, which does not name the lookup that failed", err)
	}
	if len(prov.reqs) != 0 {
		t.Errorf("the provider was called %d times on a context that could not be assembled; "+
			"the run is billed and a verdict is recorded for evidence nobody read", len(prov.reqs))
	}
	if len(logged) != 0 {
		t.Errorf("ai_invocations recorded %d rows for a run that never reached the provider: %+v",
			len(logged), logged)
	}
}

// TestBuildPromptContextWritesTheClockGapIntoThePrompt covers the one
// degradation the builder proceeds through.
//
// UTC is a correct substitute for a workspace timezone, so refusing the
// whole run over it would trade a small distortion for a total one. What
// it must not do is substitute silently: a due date judged against the
// wrong timezone reads exactly like one judged against the right one.
func TestBuildPromptContextWritesTheClockGapIntoThePrompt(t *testing.T) {
	t.Parallel()

	cases := map[string]func(context.Context, uint32) (string, error){
		"an error": func(context.Context, uint32) (string, error) {
			return "", errLookupFailed
		},
		"an empty answer": func(context.Context, uint32) (string, error) {
			return "", nil
		},
	}

	for name, clock := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pc, err := BuildPromptContext(context.Background(),
				PromptDeps{WorkspaceNow: clock}, calendarEventSignal())
			if err != nil {
				t.Fatalf("the workspace clock carries no evidence and has a correct "+
					"substitute; %s from it must not fail the run: %v", name, err)
			}
			if pc.Now == "" {
				t.Error("the fallback timestamp is missing, so the judge has no reference " +
					"point at all for a time-sensitive signal")
			}
			if len(pc.ContextGaps) != 1 || pc.ContextGaps[0] != ContextGapWorkspaceClock {
				t.Fatalf("the substitution was not recorded: %v", pc.ContextGaps)
			}

			rendered := RenderUserPrompt(pc)
			if !strings.Contains(rendered, "## Context gaps") {
				t.Error("the gap is in the context but not in the prompt; the model and the " +
					"operator reading ai_invocations.prompt_redacted both see only the prompt")
			}
			if !strings.Contains(rendered, ContextGapWorkspaceClock) {
				t.Errorf("the rendered body does not say what was missing:\n%s", rendered)
			}
		})
	}
}

// TestRenderUserPromptNamesTheSectionsItDropsForSize covers the other
// way a section leaves the prompt.
//
// The byte cap removes a whole section, and what it leaves behind is a
// body with no heading — the same body a lookup failure used to leave.
// The judge reading it concludes the workspace has no recent tasks, which
// is the conclusion the cap exists to avoid having to draw.
func TestRenderUserPromptNamesTheSectionsItDropsForSize(t *testing.T) {
	t.Parallel()

	// The titles are assigned rather than passed through capTasks,
	// which would shorten each one to MaxTaskTitleLen and leave the
	// section far under the byte cap — a fixture that never triggers the
	// path it is named after.
	big := make([]TaskSummary, MaxRecentTasks)
	pad := strings.Repeat("padding ", 200) // ~1.6 KB per title, ~32 KB in total
	for i := range big {
		big[i] = TaskSummary{PublicID: "00000000-0000-0000-0000-000000000000", Title: pad, DerivedState: "open"}
	}
	pc := PromptContext{
		Signal:      SignalContext{Kind: "manual"},
		RecentTasks: big,
		Now:         "2026-05-17T12:00:00Z",
	}

	rendered := RenderUserPrompt(pc)
	if len(rendered) > MaxContextBytes {
		t.Fatalf("the body is still %d bytes after truncation", len(rendered))
	}
	if strings.Contains(rendered, "## Recent tasks") {
		t.Fatal("the section was not dropped, so this proves nothing about how a drop is reported")
	}
	if !strings.Contains(rendered, ContextGapRecentTasksDropped) {
		t.Errorf("a section left the prompt without the prompt saying so:\n%s", rendered)
	}
	if len(pc.ContextGaps) != 0 {
		t.Errorf("rendering mutated the caller's context: %v", pc.ContextGaps)
	}
}

// TestSystemPromptAnswersTheContextGapsSection holds the two halves of
// the contract to each other.
//
// Rendering a "Context gaps" section is only worth the tokens if the
// system prompt tells the model what a gap obliges it to do; a section
// the model has never been told about is a section it will summarise back
// at us and then ignore.
func TestSystemPromptAnswersTheContextGapsSection(t *testing.T) {
	t.Parallel()

	rendered := RenderUserPrompt(PromptContext{ContextGaps: []string{ContextGapWorkspaceClock}})
	if !strings.Contains(rendered, "## Context gaps") {
		t.Fatal("the renderer no longer emits the section this test is about")
	}

	prompt := SystemPrompt()
	if !strings.Contains(prompt, "Context gaps") {
		t.Error("the renderer emits a Context gaps section the system prompt never mentions, " +
			"so the model has no instruction attached to it")
	}
	if !strings.Contains(prompt, "keep confidence at or below 0.5") {
		t.Error("the system prompt names no confidence ceiling for an incomplete context, so " +
			"a run over one can still report a confident verdict")
	}
}
