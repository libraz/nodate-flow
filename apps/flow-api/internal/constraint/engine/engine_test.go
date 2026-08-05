package engine

import (
	"context"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/constraint"
)

type fakeStore struct {
	facts     constraint.Facts
	rows      []Row
	satisfied map[string]int
	failed    map[string]int
}

func newFake(facts constraint.Facts, rows []Row) *fakeStore {
	return &fakeStore{
		facts:     facts,
		rows:      rows,
		satisfied: map[string]int{},
		failed:    map[string]int{},
	}
}

func (f *fakeStore) LoadTask(_ context.Context, _ uint32) (constraint.Facts, []Row, error) {
	return f.facts, f.rows, nil
}
func (f *fakeStore) MarkSatisfied(_ context.Context, id string, _ time.Time) error {
	f.satisfied[id]++
	return nil
}
func (f *fakeStore) MarkFailed(_ context.Context, id string, _ time.Time) error {
	f.failed[id]++
	return nil
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
	eng := &Engine{Store: store, Now: func() time.Time { return time.Unix(1700000000, 0) }}
	out, err := eng.EvaluateTask(context.Background(), 1)
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
	if _, err := eng.EvaluateTask(context.Background(), 1); err == nil {
		t.Fatal("expected error without store")
	}
}
