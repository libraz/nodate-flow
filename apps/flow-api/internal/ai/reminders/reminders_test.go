package reminders

import (
	"testing"
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/region"
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

// TestEvaluateReadsTodayInTheUsersZone pins that "today" is the calendar
// date in the user's zone, not in UTC: at 20:00 UTC it is already the
// next day in Tokyo, so a task due that next day is due today there.
func TestEvaluateReadsTodayInTheUsersZone(t *testing.T) {
	tokyo, err := region.Resolve("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	due := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 22, 20, 0, 0, 0, time.UTC)

	got := Evaluate(Signals{State: StateOpen, HasDueOn: true, DueOn: due, Now: now, Zone: tokyo})
	if got == nil || got.Kind != KindDueToday {
		t.Fatalf("Asia/Tokyo at 2026-10-23T05:00+09:00: got %+v, want due_today", got)
	}
	got = Evaluate(Signals{State: StateOpen, HasDueOn: true, DueOn: due, Now: now, Zone: region.UTC()})
	if got == nil || got.Kind != KindDueSoon || got.DaysUntilDue != 1 {
		t.Fatalf("UTC at 2026-10-22T20:00Z: got %+v, want due_soon in 1 day", got)
	}
}
