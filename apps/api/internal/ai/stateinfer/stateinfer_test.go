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
