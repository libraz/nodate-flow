package engine

import "testing"

func TestReplay_HappyPath(t *testing.T) {
	evs := []TransitionEvent{
		{Name: "start"},
		{Name: "submit"},
		{Name: "complete"},
	}
	st, err := Replay(evs)
	if err != nil || st != StateDone {
		t.Fatalf("expected done, got %v %v", st, err)
	}
}

func TestReplay_ReopenLoop(t *testing.T) {
	evs := []TransitionEvent{
		{Name: "cancel"},
		{Name: "reopen"},
		{Name: "start"},
		{Name: "submit"},
		{Name: "reopen"},
	}
	st, _ := Replay(evs)
	if st != StateWaiting {
		t.Fatalf("expected waiting, got %v", st)
	}
}

func TestReplay_IllegalTransition(t *testing.T) {
	_, err := Replay([]TransitionEvent{{Name: "submit"}}) // open→submit is illegal
	if err != ErrIllegalTransition {
		t.Fatalf("expected ErrIllegalTransition, got %v", err)
	}
}

func TestReplay_SkipsReversedTransitionPair(t *testing.T) {
	reversesStart := int64(1)
	evs := []TransitionEvent{
		{ID: 1, Name: "start"},
		{ID: 2, Name: "start", ReversesEventID: &reversesStart},
		{ID: 3, Name: "complete"},
	}
	st, err := Replay(evs)
	if err != nil || st != StateDone {
		t.Fatalf("expected reversed start pair to replay as open→complete=done, got %v %v", st, err)
	}
}

func TestReplay_Empty(t *testing.T) {
	st, err := Replay(nil)
	if err != nil || st != StateOpen {
		t.Fatalf("empty replay should stay open, got %v %v", st, err)
	}
}

func TestParseTransitionName(t *testing.T) {
	cases := map[string]struct {
		name string
		ok   bool
	}{
		"task.transition.start":    {"start", true},
		"task.transition.complete": {"complete", true},
		"task.transition.":         {"", false},
		"task.created":             {"", false},
		"":                         {"", false},
	}
	for in, want := range cases {
		got, ok := ParseTransitionName(in)
		if got != want.name || ok != want.ok {
			t.Errorf("ParseTransitionName(%q) = (%q,%v), want (%q,%v)", in, got, ok, want.name, want.ok)
		}
	}
}
