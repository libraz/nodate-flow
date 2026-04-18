package reminders

import (
	"testing"
	"time"
)

func TestEvaluate(t *testing.T) {
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	day := func(offset int) time.Time {
		return time.Date(2026, 4, 8+offset, 0, 0, 0, 0, time.UTC)
	}

	cases := []struct {
		name string
		in   Signals
		want *Reminder
	}{
		{
			name: "no due date → nil",
			in:   Signals{State: StateOpen, Now: now},
			want: nil,
		},
		{
			name: "done state ignored",
			in:   Signals{State: StateDone, HasDueOn: true, DueOn: day(-5), Now: now},
			want: nil,
		},
		{
			name: "cancelled state ignored",
			in:   Signals{State: StateCancelled, HasDueOn: true, DueOn: day(-5), Now: now},
			want: nil,
		},
		{
			name: "overdue by 2 days",
			in:   Signals{State: StateOpen, HasDueOn: true, DueOn: day(-2), Now: now},
			want: &Reminder{Kind: KindOverdue, DaysUntilDue: -2},
		},
		{
			name: "due today",
			in:   Signals{State: StateWaiting, HasDueOn: true, DueOn: day(0), Now: now},
			want: &Reminder{Kind: KindDueToday, DaysUntilDue: 0},
		},
		{
			name: "due in 2 days",
			in:   Signals{State: StateReview, HasDueOn: true, DueOn: day(2), Now: now},
			want: &Reminder{Kind: KindDueSoon, DaysUntilDue: 2},
		},
		{
			name: "due far in the future → nil",
			in:   Signals{State: StateOpen, HasDueOn: true, DueOn: day(14), Now: now},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.in)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected reminder %+v, got nil", tc.want)
			}
			if got.Kind != tc.want.Kind {
				t.Errorf("kind: want %q got %q", tc.want.Kind, got.Kind)
			}
			if got.DaysUntilDue != tc.want.DaysUntilDue {
				t.Errorf("daysUntilDue: want %d got %d", tc.want.DaysUntilDue, got.DaysUntilDue)
			}
			if got.Message == "" {
				t.Errorf("message should not be empty")
			}
		})
	}
}
