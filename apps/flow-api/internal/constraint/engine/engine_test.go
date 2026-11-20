package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/constraint"
)

// fakeStore keeps one stored verdict per row so Mark* report a change
// only when the verdict actually moves, as the SQL store does.
type fakeStore struct {
	facts     constraint.Facts
	rows      []Row
	notFound  bool
	verdicts  map[string]bool
	satisfied map[string]int
	failed    map[string]int
}

func newFake(facts constraint.Facts, rows []Row) *fakeStore {
	return &fakeStore{
		facts:     facts,
		rows:      rows,
		verdicts:  map[string]bool{},
		satisfied: map[string]int{},
		failed:    map[string]int{},
	}
}

func (f *fakeStore) LoadTask(_ context.Context, _ uint32) (Task, error) {
	if f.notFound {
		return Task{}, nil
	}
	return Task{PublicID: "task-1", Facts: f.facts, Rows: f.rows, Found: true}, nil
}

func (f *fakeStore) mark(id string, satisfied bool) bool {
	prev, ok := f.verdicts[id]
	f.verdicts[id] = satisfied
	return !ok || prev != satisfied
}

func (f *fakeStore) MarkSatisfied(_ context.Context, id string, _ time.Time) (bool, error) {
	f.satisfied[id]++
	return f.mark(id, true), nil
}

func (f *fakeStore) MarkFailed(_ context.Context, id string, _ time.Time) (bool, error) {
	f.failed[id]++
	return f.mark(id, false), nil
}

func discardEmit(context.Context, uint32, string, Row, bool) error { return nil }

// emitted is one verdict seen by a recording Emit.
type emitted struct {
	taskID       uint32
	taskPublicID string
	constraintID string
	kind         string
	satisfied    bool
}

func recordEmit(into *[]emitted) EmitFunc {
	return func(_ context.Context, taskID uint32, taskPublicID string, row Row, satisfied bool) error {
		*into = append(*into, emitted{taskID, taskPublicID, row.PublicID, row.Kind, satisfied})
		return nil
	}
}

func date(s string) *time.Time {
	d, _ := time.Parse("2006-01-02", s)
	return &d
}

func TestEngine_MixedOutcomes(t *testing.T) {
	store := newFake(
		constraint.Facts{
			DueOn:            date("2026-04-10"),
			DependencyStates: map[string]string{"x": "done"},
			ActorRoles:       map[string]bool{"reviewer": true},
		},
		[]Row{
			{PublicID: "c1", Expression: []byte(`{"op":"time.due_before","arg":"2026-05-01"}`)},
			{PublicID: "c2", Expression: []byte(`{"op":"dependency.all_done","taskIds":["x"]}`)},
			{PublicID: "c3", Expression: []byte(`{"op":"actor.has_role","arg":"author"}`)},
			{PublicID: "c4", Expression: []byte(`{"op":"bogus"}`)},
		},
	)
	eng := &Engine{Store: store, Emit: discardEmit, Now: func() time.Time { return time.Unix(1700000000, 0) }}
	out, err := eng.evaluate(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 4 {
		t.Fatalf("expected 4 outcomes, got %d", len(out))
	}
	if !out[0].Satisfied || store.satisfied["c1"] != 1 {
		t.Fatal("c1 should be satisfied")
	}
	if !out[1].Satisfied || store.satisfied["c2"] != 1 {
		t.Fatal("c2 should be satisfied")
	}
	if out[2].Satisfied || store.failed["c3"] != 1 {
		t.Fatal("c3 should be failed")
	}
	if out[3].ParseError == nil {
		t.Fatal("c4 should have parse error")
	}
	if store.satisfied["c4"] != 0 || store.failed["c4"] != 0 {
		t.Fatal("c4 should not be touched on parse error")
	}
}

func TestEngine_NoStore(t *testing.T) {
	eng := &Engine{}
	if _, err := eng.evaluate(context.Background(), 1); err == nil {
		t.Fatal("expected error without store")
	}
}

// TestEngineEmitsOnlyChangedOutcomes holds the verdict-event contract: a
// first verdict and every later flip emit once, a re-evaluation that moves
// nothing emits nothing, and Outcome.Changed agrees with the emits.
func TestEngineEmitsOnlyChangedOutcomes(t *testing.T) {
	store := newFake(
		constraint.Facts{DependencyStates: map[string]string{"x": "open"}},
		[]Row{
			{PublicID: "dep", Kind: "dependency", Expression: []byte(`{"op":"dependency.all_done","taskIds":["x"]}`)},
			{PublicID: "role", Kind: "approval", Expression: []byte(`{"op":"actor.has_role","arg":"reviewer"}`)},
			{PublicID: "bad", Kind: "custom", Expression: []byte(`{"op":"bogus"}`)},
		},
	)
	var got []emitted
	eng := &Engine{Store: store, Emit: recordEmit(&got)}
	ctx := context.Background()

	out, err := eng.evaluate(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	want := []emitted{
		{7, "task-1", "dep", "dependency", false},
		{7, "task-1", "role", "approval", false},
	}
	assertEmitted(t, "first evaluation", got, want)
	if !out[0].Changed || !out[1].Changed || out[2].Changed {
		t.Fatalf("first evaluation Changed flags = %v %v %v, want true true false", out[0].Changed, out[1].Changed, out[2].Changed)
	}

	got = nil
	out, err = eng.evaluate(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	assertEmitted(t, "unchanged re-evaluation", got, nil)
	for i, o := range out {
		if o.Changed {
			t.Fatalf("unchanged re-evaluation reported row %d as changed", i)
		}
	}

	got = nil
	store.facts = constraint.Facts{DependencyStates: map[string]string{"x": "done"}}
	out, err = eng.evaluate(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	assertEmitted(t, "dependency completed", got, []emitted{{7, "task-1", "dep", "dependency", true}})
	if !out[0].Changed || out[1].Changed {
		t.Fatalf("after the flip Changed flags = %v %v, want true false", out[0].Changed, out[1].Changed)
	}
}

func assertEmitted(t *testing.T, step string, got, want []emitted) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: emitted %+v, want %+v", step, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: emitted %+v, want %+v", step, got, want)
		}
	}
}

// TestEngineErrorsWhenEmitMissing pins that a changed verdict with nowhere
// to go is an error, while an evaluation that changes nothing needs no Emit.
func TestEngineErrorsWhenEmitMissing(t *testing.T) {
	store := newFake(constraint.Facts{}, []Row{
		{PublicID: "c1", Kind: "approval", Expression: []byte(`{"op":"actor.has_role","arg":"reviewer"}`)},
	})
	eng := &Engine{Store: store}
	ctx := context.Background()

	if _, err := eng.evaluate(ctx, 1); !errors.Is(err, errEmitMissing) {
		t.Fatalf("a changed verdict without Emit must fail with errEmitMissing; got %v", err)
	}
	if _, err := eng.evaluate(ctx, 1); err != nil {
		t.Fatalf("an evaluation that changes nothing must not need Emit; got %v", err)
	}
}

// TestEngineSkipsTaskNotFound pins that a disabled or foreign task is not
// evaluated: no outcome, no marker, no verdict event.
func TestEngineSkipsTaskNotFound(t *testing.T) {
	store := newFake(constraint.Facts{}, []Row{
		{PublicID: "c1", Kind: "deadline", Expression: []byte(`{"op":"time.due_before","arg":"2026-05-01"}`)},
	})
	store.notFound = true
	var got []emitted
	eng := &Engine{Store: store, Emit: recordEmit(&got)}

	out, err := eng.evaluate(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("a task that was not found must yield no outcomes; got %d", len(out))
	}
	if store.satisfied["c1"] != 0 || store.failed["c1"] != 0 {
		t.Fatal("a task that was not found must not have markers written")
	}
	if len(got) != 0 {
		t.Fatalf("a task that was not found must emit nothing; got %+v", got)
	}
}

// TestEngineSurfacesEmitError pins that an Emit failure stops the
// evaluation and reaches the caller.
func TestEngineSurfacesEmitError(t *testing.T) {
	boom := errors.New("emit failed")
	store := newFake(constraint.Facts{}, []Row{
		{PublicID: "c1", Kind: "approval", Expression: []byte(`{"op":"actor.has_role","arg":"reviewer"}`)},
		{PublicID: "c2", Kind: "approval", Expression: []byte(`{"op":"actor.has_role","arg":"author"}`)},
	})
	eng := &Engine{Store: store, Emit: func(context.Context, uint32, string, Row, bool) error { return boom }}

	if _, err := eng.evaluate(context.Background(), 1); !errors.Is(err, boom) {
		t.Fatalf("an Emit failure must reach the caller; got %v", err)
	}
	if store.failed["c2"] != 0 {
		t.Fatal("evaluation must stop at the first Emit failure")
	}
}
