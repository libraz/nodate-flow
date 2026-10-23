package stateinfer

import (
	"testing"
	"time"
)

func TestInfer(t *testing.T) {
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	daysAgo := func(d int) time.Time { return now.Add(-time.Duration(d) * 24 * time.Hour) }

	cases := []struct {
		name string
		in   Signals
		want *Proposal
	}{
		{
			name: "open idle past threshold proposes start",
			in:   Signals{State: StateOpen, UpdatedAt: daysAgo(4), Now: now},
			want: &Proposal{Transition: TransitionStart, Confidence: 0.70},
		},
		{
			name: "open fresh → no proposal",
			in:   Signals{State: StateOpen, UpdatedAt: daysAgo(1), Now: now},
			want: nil,
		},
		{
			name: "waiting overdue → submit",
			in: Signals{
				State: StateWaiting, UpdatedAt: daysAgo(1),
				HasDueOn: true, DueOn: daysAgo(1), Now: now,
			},
			want: &Proposal{Transition: TransitionSubmit, Confidence: 0.80},
		},
		{
			name: "waiting idle with deps → block",
			in: Signals{
				State: StateWaiting, UpdatedAt: daysAgo(6),
				DependencyCount: 2, Now: now,
			},
			want: &Proposal{Transition: TransitionBlock, Confidence: 0.75},
		},
		{
			name: "waiting idle no deps → no proposal",
			in:   Signals{State: StateWaiting, UpdatedAt: daysAgo(6), Now: now},
			want: nil,
		},
		{
			name: "review open too long → complete",
			in:   Signals{State: StateReview, UpdatedAt: daysAgo(3), Now: now},
			want: &Proposal{Transition: TransitionComplete, Confidence: 0.70},
		},
		{
			name: "done → never proposes",
			in:   Signals{State: StateDone, UpdatedAt: daysAgo(30), Now: now},
			want: nil,
		},
		{
			name: "cancelled → never proposes",
			in:   Signals{State: StateCancelled, UpdatedAt: daysAgo(30), Now: now},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Infer(tc.in)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected proposal %+v, got nil", tc.want)
			}
			if got.Transition != tc.want.Transition {
				t.Errorf("transition: want %q got %q", tc.want.Transition, got.Transition)
			}
			if got.Confidence != tc.want.Confidence {
				t.Errorf("confidence: want %v got %v", tc.want.Confidence, got.Confidence)
			}
			if got.Reason == "" {
				t.Errorf("reason should not be empty")
			}
		})
	}
}

// TestInferDueTodayIsNotOverdue pins the due_on comparison to calendar
// days: a task due on 2026-10-23 is not past due at 15:00 UTC that day,
// and is from midnight on.
func TestInferDueTodayIsNotOverdue(t *testing.T) {
	due := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	base := Signals{State: StateWaiting, HasDueOn: true, DueOn: due}

	base.UpdatedAt = time.Date(2026, 10, 23, 14, 0, 0, 0, time.UTC)
	base.Now = time.Date(2026, 10, 23, 15, 0, 0, 0, time.UTC)
	if got := Infer(base); got != nil {
		t.Fatalf("due today at 15:00Z: got %+v, want no past-due proposal", got)
	}
	base.Now = time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	if got := Infer(base); got == nil || got.Transition != TransitionSubmit {
		t.Fatalf("the next day at 00:00Z: got %+v, want the past-due submit proposal", got)
	}
}
