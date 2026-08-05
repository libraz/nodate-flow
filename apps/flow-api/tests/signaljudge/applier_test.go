// Package signaljudgetests covers the Phase 3 / J4 Applier
// (apps/flow-api/internal/ai/signaljudge/applier.go) against the
// six scenarios pinned by the release-8 plan:
//
//  1. noop verdict → only SignalJudged emitted, applied_at NULL.
//  2. complete_task × autonomy=auto → SignalJudged + TaskAutoCompleted
//     + SignalApplied, plus the TaskMutator.CompleteTask side effect.
//  3. complete_task × autonomy=suggest → SignalJudged only.
//  4. generate_retro × autonomy=draft → draft task + TaskRetroDrafted.
//  5. verdict schema violation → SignalRejected with reason.
//  6. invariant guard: only the Applier can use AppendJudgeEvent —
//     covered by apps/flow-api/internal/eventbus/judge_guard_test.go
//     (a unit test in the eventbus package itself).
//
// The tests use in-memory fakes for every Applier dependency so they
// stay deterministic, fast, and parallel-safe — no testcontainer.
package signaljudgetests

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/signaljudge"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/signalkinds"
)

// fakeBus records every event passed to AppendJudgeEvent. Exposes
// the captured slice so tests can assert on order, count, and
// payload shape.
type fakeBus struct {
	mu     sync.Mutex
	events []eventbus.Event
	failOn map[string]error
}

func (b *fakeBus) AppendJudgeEvent(_ context.Context, evt eventbus.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err, ok := b.failOn[evt.Type]; ok && err != nil {
		return err
	}
	b.events = append(b.events, evt)
	return nil
}

func (b *fakeBus) snapshot() []eventbus.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]eventbus.Event, len(b.events))
	copy(out, b.events)
	return out
}

// kindsOnly returns the Type values of every recorded event, in
// emission order. Lets tests assert sequences with one comparison.
func kindsOnly(events []eventbus.Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Type
	}
	return out
}

// fakeMutator records each TaskMutator call so tests can assert
// the Applier called the right side effect for the chosen branch.
type fakeMutator struct {
	mu             sync.Mutex
	completed      []int64 // task internal ids
	commented      []string
	drafted        []bool // draft flag per call
	failComplete   error
	failAddComment error
	failDraft      error
	// retroIDOffset names the new task internal id generator: each
	// DraftRetroTask call returns offset+N for the N-th call so
	// tests can pin TaskRetroDrafted.TaskID assertions.
	retroIDOffset int64
}

func (m *fakeMutator) CompleteTask(_ context.Context, _ uint32, taskInternalID int64, _ uint32) error {
	if m.failComplete != nil {
		return m.failComplete
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completed = append(m.completed, taskInternalID)
	return nil
}

func (m *fakeMutator) AddComment(_ context.Context, _ uint32, _ int64, _ uint32, body string) error {
	if m.failAddComment != nil {
		return m.failAddComment
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commented = append(m.commented, body)
	return nil
}

func (m *fakeMutator) DraftRetroTask(_ context.Context, _ uint32, _ int64, _ uint32, _ string, draft bool) (int64, string, error) {
	if m.failDraft != nil {
		return 0, "", m.failDraft
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.drafted = append(m.drafted, draft)
	id := m.retroIDOffset + int64(len(m.drafted))
	return id, "new-retro-public-id", nil
}

// fakeResolver returns a fixed id for a fixed public id; anything
// else short-circuits as "not found".
type fakeResolver struct {
	knownPublicID string
	knownInternal int64
}

func (r *fakeResolver) ResolveTask(_ context.Context, _ uint32, publicID string) (int64, bool, error) {
	if publicID == r.knownPublicID && r.knownInternal > 0 {
		return r.knownInternal, true, nil
	}
	return 0, false, nil
}

// fakeSignalUpdater captures the most recent UpdateJudgeOutput call.
type fakeSignalUpdater struct {
	mu             sync.Mutex
	calls          int
	lastRunID      uint32
	lastConfidence float64
	lastAppliedAt  *time.Time
	lastOutput     json.RawMessage
	fail           error
}

func (u *fakeSignalUpdater) UpdateJudgeOutput(_ context.Context, _ int64, runID uint32, output json.RawMessage, confidence float64, appliedAt *time.Time) error {
	if u.fail != nil {
		return u.fail
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls++
	u.lastRunID = runID
	u.lastConfidence = confidence
	u.lastAppliedAt = appliedAt
	u.lastOutput = append(json.RawMessage(nil), output...)
	return nil
}

// fakeAutonomy returns a fixed AutonomyDecision regardless of input.
type fakeAutonomy struct {
	level signaljudge.AutonomyLevel
}

func (a *fakeAutonomy) Resolve(_ context.Context, _ uint32, _ signalkinds.Kind, _ float64) (signaljudge.AutonomyDecision, error) {
	return signaljudge.AutonomyDecision{Level: a.level}, nil
}

// fixedClock returns a constant timestamp so applied_at assertions
// can be exact.
func fixedNow() func() time.Time {
	t := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

// stockSig returns a SignalRef that every test reuses; lets the
// table per-test only declare the fields it cares about.
func stockSig() signaljudge.SignalRef {
	return signaljudge.SignalRef{
		InternalID:  42,
		PublicID:    "00000000-0000-0000-0000-000000000042",
		WorkspaceID: 7,
		Kind:        string(signalkinds.Manual),
	}
}

func stockAgent() signaljudge.AgentRef {
	return signaljudge.AgentRef{InternalID: 11}
}

// newApplier builds an Applier with the supplied fakes plus
// sensible defaults for everything else.
func newApplier(t *testing.T, bus *fakeBus, mutator *fakeMutator, resolver *fakeResolver, signals *fakeSignalUpdater, level signaljudge.AutonomyLevel) *signaljudge.Applier {
	t.Helper()
	if bus == nil {
		t.Fatalf("newApplier requires a non-nil bus")
	}
	return &signaljudge.Applier{
		Bus:      bus,
		Tasks:    mutator,
		Resolver: resolver,
		Signals:  signals,
		Autonomy: &fakeAutonomy{level: level},
		Now:      fixedNow(),
	}
}

// ----- Scenario 1: noop verdict ----------------------------------------------

// TestApplierNoopVerdictEmitsOnlySignalJudged covers Phase 3 / J4
// scenario #1: a verdict with action=noop must emit only
// SignalJudged, must not set applied_at, and must not touch the
// TaskMutator.
func TestApplierNoopVerdictEmitsOnlySignalJudged(t *testing.T) {
	t.Parallel()

	bus := &fakeBus{}
	mutator := &fakeMutator{}
	signals := &fakeSignalUpdater{}
	applier := newApplier(t, bus, mutator, nil, signals, signaljudge.AutonomyAuto)

	res, err := applier.Apply(context.Background(), stockSig(), stockAgent(), 99, signaljudge.Verdict{
		Action:           signaljudge.ActionNoop,
		Confidence:       0.42,
		ReasoningExcerpt: "nothing actionable yet",
	})
	if err != nil {
		t.Fatalf("Apply: unexpected error: %v", err)
	}
	if res == nil || !res.Skipped || res.SkipReason != "noop" {
		t.Fatalf("Apply result: want Skipped=true reason=noop, got %+v", res)
	}
	events := bus.snapshot()
	if got, want := kindsOnly(events), []string{eventbus.SignalJudged}; !equalStrSlice(got, want) {
		t.Fatalf("event sequence = %v, want %v", got, want)
	}
	if len(mutator.completed) != 0 || len(mutator.commented) != 0 || len(mutator.drafted) != 0 {
		t.Fatalf("noop verdict must not call TaskMutator; got completed=%v commented=%v drafted=%v",
			mutator.completed, mutator.commented, mutator.drafted)
	}
	if signals.lastAppliedAt != nil {
		t.Fatalf("noop verdict must leave signals.applied_at NULL; got %v", signals.lastAppliedAt)
	}
	if signals.lastRunID != 99 {
		t.Fatalf("UpdateJudgeOutput must carry runID=99; got %d", signals.lastRunID)
	}
}

// ----- Scenario 2: complete_task × autonomy=auto -----------------------------

// TestApplierCompleteTaskAutoEmitsFullChain covers scenario #2: an
// action=complete_task verdict under autonomy=auto must emit
// SignalJudged + TaskAutoCompleted + SignalApplied (in that order),
// must call TaskMutator.CompleteTask, and must set applied_at.
//
// Additionally asserts the invariant that the Applier never reaches
// for a direct UPDATE to derive_state: every event in the captured
// stream goes through AppendJudgeEvent (the fake bus is the only
// writer in this test).
func TestApplierCompleteTaskAutoEmitsFullChain(t *testing.T) {
	t.Parallel()

	bus := &fakeBus{}
	mutator := &fakeMutator{}
	signals := &fakeSignalUpdater{}
	resolver := &fakeResolver{knownPublicID: "task-public-id", knownInternal: 314}
	applier := newApplier(t, bus, mutator, resolver, signals, signaljudge.AutonomyAuto)

	taskPub := "task-public-id"
	verdict := signaljudge.Verdict{
		Action:             signaljudge.ActionCompleteTask,
		TargetTaskPublicID: &taskPub,
		Confidence:         0.91,
		ReasoningExcerpt:   "user reported it was done in Slack",
	}
	res, err := applier.Apply(context.Background(), stockSig(), stockAgent(), 77, verdict)
	if err != nil {
		t.Fatalf("Apply: unexpected error: %v", err)
	}
	if res == nil || res.Skipped {
		t.Fatalf("Apply result: want not skipped, got %+v", res)
	}
	events := bus.snapshot()
	wantSeq := []string{
		eventbus.SignalJudged,
		eventbus.TaskAutoCompleted,
		eventbus.SignalApplied,
	}
	if got := kindsOnly(events); !equalStrSlice(got, wantSeq) {
		t.Fatalf("event sequence = %v, want %v", got, wantSeq)
	}
	// Every emitted event must carry the signal lineage and the
	// agent actor — both are part of the audit contract.
	for i, evt := range events {
		if evt.TriggeredBySignalID == nil || *evt.TriggeredBySignalID != stockSig().InternalID {
			t.Fatalf("events[%d] (%s): TriggeredBySignalID = %v, want %d",
				i, evt.Type, evt.TriggeredBySignalID, stockSig().InternalID)
		}
		if evt.ActorAgentID == nil || *evt.ActorAgentID != int64(stockAgent().InternalID) {
			t.Fatalf("events[%d] (%s): ActorAgentID = %v, want %d",
				i, evt.Type, evt.ActorAgentID, stockAgent().InternalID)
		}
	}
	if len(mutator.completed) != 1 || mutator.completed[0] != 314 {
		t.Fatalf("TaskMutator.CompleteTask not called for task 314; completed=%v", mutator.completed)
	}
	if signals.lastAppliedAt == nil {
		t.Fatalf("complete_task autonomy=auto must set applied_at; got nil")
	}
}

// ----- Scenario 3: complete_task × autonomy=suggest --------------------------

// TestApplierCompleteTaskSuggestEmitsJudgedOnly covers scenario #3:
// the same verdict under autonomy=suggest must NOT close the task.
// Only SignalJudged is emitted, the TaskMutator is untouched, and
// applied_at stays NULL — the verdict has been recorded but not
// applied.
func TestApplierCompleteTaskSuggestEmitsJudgedOnly(t *testing.T) {
	t.Parallel()

	bus := &fakeBus{}
	mutator := &fakeMutator{}
	signals := &fakeSignalUpdater{}
	resolver := &fakeResolver{knownPublicID: "task-public-id", knownInternal: 314}
	applier := newApplier(t, bus, mutator, resolver, signals, signaljudge.AutonomySuggest)

	taskPub := "task-public-id"
	res, err := applier.Apply(context.Background(), stockSig(), stockAgent(), 1, signaljudge.Verdict{
		Action:             signaljudge.ActionCompleteTask,
		TargetTaskPublicID: &taskPub,
		Confidence:         0.51,
		ReasoningExcerpt:   "looks done but I am not sure",
	})
	if err != nil {
		t.Fatalf("Apply: unexpected error: %v", err)
	}
	if res == nil || !res.Skipped || res.SkipReason != "suggested_only" {
		t.Fatalf("Apply result: want Skipped=true reason=suggested_only, got %+v", res)
	}
	if got, want := kindsOnly(bus.snapshot()), []string{eventbus.SignalJudged}; !equalStrSlice(got, want) {
		t.Fatalf("event sequence = %v, want %v", got, want)
	}
	if len(mutator.completed) != 0 {
		t.Fatalf("suggest autonomy must not call CompleteTask; completed=%v", mutator.completed)
	}
	if signals.lastAppliedAt != nil {
		t.Fatalf("suggest autonomy must leave applied_at NULL; got %v", signals.lastAppliedAt)
	}
}

// ----- Scenario 4: generate_retro × autonomy=draft ---------------------------

// TestApplierGenerateRetroDraftCreatesNewTask covers scenario #4: an
// action=generate_retro verdict under autonomy=draft must create
// exactly one new task (in draft status), must emit TaskRetroDrafted,
// and must NOT emit SignalApplied (the Draft branch is intentionally
// SignalApplied-free — the existence of the draft task IS the
// materialisation that operators review).
func TestApplierGenerateRetroDraftCreatesNewTask(t *testing.T) {
	t.Parallel()

	bus := &fakeBus{}
	mutator := &fakeMutator{retroIDOffset: 1000}
	signals := &fakeSignalUpdater{}
	resolver := &fakeResolver{knownPublicID: "source-task", knownInternal: 314}
	applier := newApplier(t, bus, mutator, resolver, signals, signaljudge.AutonomyDraft)

	src := "source-task"
	res, err := applier.Apply(context.Background(), stockSig(), stockAgent(), 5, signaljudge.Verdict{
		Action:             signaljudge.ActionGenerateRetro,
		TargetTaskPublicID: &src,
		Confidence:         0.74,
		ReasoningExcerpt:   "45m meeting with no task; here is a draft",
	})
	if err != nil {
		t.Fatalf("Apply: unexpected error: %v", err)
	}
	if res == nil || res.Skipped {
		t.Fatalf("Apply result: want not skipped, got %+v", res)
	}
	wantSeq := []string{eventbus.SignalJudged, eventbus.TaskRetroDrafted}
	if got := kindsOnly(bus.snapshot()); !equalStrSlice(got, wantSeq) {
		t.Fatalf("event sequence = %v, want %v", got, wantSeq)
	}
	if len(mutator.drafted) != 1 || !mutator.drafted[0] {
		t.Fatalf("Draft autonomy must call DraftRetroTask(draft=true) once; got %v", mutator.drafted)
	}
	if signals.lastAppliedAt == nil {
		t.Fatalf("Draft branch must set applied_at on the source signal; got nil")
	}
	// Inspect the TaskRetroDrafted payload to confirm the new task
	// id round-tripped from the mutator into the event.
	retroEvent := bus.snapshot()[1]
	payload, ok := retroEvent.Payload.(map[string]any)
	if !ok {
		t.Fatalf("TaskRetroDrafted payload type = %T, want map[string]any", retroEvent.Payload)
	}
	if payload["newTaskPublicId"] != "new-retro-public-id" {
		t.Fatalf("TaskRetroDrafted.newTaskPublicId = %v, want new-retro-public-id", payload["newTaskPublicId"])
	}
	if payload["draft"] != true {
		t.Fatalf("TaskRetroDrafted.draft = %v, want true", payload["draft"])
	}
}

// ----- Scenario 5: schema violation -----------------------------------------

// TestApplierSchemaViolationEmitsSignalRejected covers scenario #5:
// a verdict that fails ValidateVerdict (here, confidence > 1.0) must
// emit SignalRejected with a structured reason and MUST NOT emit any
// other event.
func TestApplierSchemaViolationEmitsSignalRejected(t *testing.T) {
	t.Parallel()

	bus := &fakeBus{}
	mutator := &fakeMutator{}
	signals := &fakeSignalUpdater{}
	applier := newApplier(t, bus, mutator, nil, signals, signaljudge.AutonomyAuto)

	res, err := applier.Apply(context.Background(), stockSig(), stockAgent(), 3, signaljudge.Verdict{
		Action:           signaljudge.ActionNoop,
		Confidence:       1.5, // out of range — fails validation
		ReasoningExcerpt: "irrelevant — validation should fire first",
	})
	if err != nil {
		t.Fatalf("Apply: unexpected error: %v", err)
	}
	if res == nil || !res.Skipped || res.SkipReason != "verdict_invalid" {
		t.Fatalf("Apply result: want Skipped=true reason=verdict_invalid, got %+v", res)
	}
	if got, want := kindsOnly(bus.snapshot()), []string{eventbus.SignalRejected}; !equalStrSlice(got, want) {
		t.Fatalf("event sequence = %v, want %v", got, want)
	}
	// The SignalRejected payload must carry a structured reason so
	// the timeline UI can render "the LLM tried but the verdict was
	// bad" without parsing the raw verdict.
	evt := bus.snapshot()[0]
	payload, ok := evt.Payload.(map[string]any)
	if !ok {
		t.Fatalf("SignalRejected payload type = %T, want map[string]any", evt.Payload)
	}
	if payload["reason"] != "verdict_invalid" {
		t.Fatalf("SignalRejected.reason = %v, want verdict_invalid", payload["reason"])
	}
	if v, ok := payload["validationError"].(string); !ok || !strings.Contains(v, "confidence") {
		t.Fatalf("SignalRejected.validationError = %v, want a string mentioning 'confidence'", payload["validationError"])
	}
	if signals.lastAppliedAt != nil {
		t.Fatalf("rejected verdicts must not set applied_at; got %v", signals.lastAppliedAt)
	}
}

// ----- Additional coverage: bad action / missing target -----------------------

// TestApplierUnknownActionRejected pins that an empty/unknown action
// string surfaces as SignalRejected (not as a panic), so a future
// LLM-output drift can never silently no-op.
func TestApplierUnknownActionRejected(t *testing.T) {
	t.Parallel()

	bus := &fakeBus{}
	signals := &fakeSignalUpdater{}
	applier := newApplier(t, bus, &fakeMutator{}, nil, signals, signaljudge.AutonomyAuto)

	res, err := applier.Apply(context.Background(), stockSig(), stockAgent(), 4, signaljudge.Verdict{
		Action:           "demolish_workspace",
		Confidence:       0.9,
		ReasoningExcerpt: "I want to delete everything",
	})
	if err != nil {
		t.Fatalf("Apply: unexpected error: %v", err)
	}
	if res == nil || !res.Skipped {
		t.Fatalf("Apply result: want Skipped, got %+v", res)
	}
	if got, want := kindsOnly(bus.snapshot()), []string{eventbus.SignalRejected}; !equalStrSlice(got, want) {
		t.Fatalf("event sequence = %v, want %v", got, want)
	}
}

// TestApplierMissingTargetRejected pins that an action requiring a
// target_task_public_id but pointing at a non-existent task results
// in SignalRejected, not a TaskAutoCompleted with TaskID=0.
func TestApplierMissingTargetRejected(t *testing.T) {
	t.Parallel()

	bus := &fakeBus{}
	signals := &fakeSignalUpdater{}
	resolver := &fakeResolver{} // never resolves anything
	applier := newApplier(t, bus, &fakeMutator{}, resolver, signals, signaljudge.AutonomyAuto)

	ghost := "00000000-0000-0000-0000-deadbeefdead"
	res, err := applier.Apply(context.Background(), stockSig(), stockAgent(), 6, signaljudge.Verdict{
		Action:             signaljudge.ActionCompleteTask,
		TargetTaskPublicID: &ghost,
		Confidence:         0.99,
		ReasoningExcerpt:   "user said it is done",
	})
	if err != nil {
		t.Fatalf("Apply: unexpected error: %v", err)
	}
	if res == nil || !res.Skipped {
		t.Fatalf("Apply result: want Skipped, got %+v", res)
	}
	if got, want := kindsOnly(bus.snapshot()), []string{eventbus.SignalRejected}; !equalStrSlice(got, want) {
		t.Fatalf("event sequence = %v, want %v", got, want)
	}
}

// TestApplierBusFailurePropagates verifies that a failing event
// append returns a non-nil error rather than swallowing it — the
// Applier's audit invariant is "every event lands or the whole call
// fails", so half-written event chains must surface.
func TestApplierBusFailurePropagates(t *testing.T) {
	t.Parallel()

	bus := &fakeBus{failOn: map[string]error{
		eventbus.SignalJudged: errors.New("simulated bus failure"),
	}}
	applier := newApplier(t, bus, &fakeMutator{}, nil, &fakeSignalUpdater{}, signaljudge.AutonomyAuto)
	_, err := applier.Apply(context.Background(), stockSig(), stockAgent(), 1, signaljudge.Verdict{
		Action:           signaljudge.ActionNoop,
		Confidence:       0.5,
		ReasoningExcerpt: "noop",
	})
	if err == nil {
		t.Fatalf("Apply: want error from bus failure, got nil")
	}
}

// equalStrSlice is a tiny helper that returns true when two []string
// slices have identical length and elements in order.
func equalStrSlice(a, b []string) bool {
	return slices.Equal(a, b)
}
