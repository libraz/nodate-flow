package digest

import (
	"strings"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

func TestBuild(t *testing.T) {
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	day := func(offset int) time.Time {
		return time.Date(2026, 4, 8+offset, 0, 0, 0, 0, time.UTC)
	}

	tasks := []TaskSnapshot{
		{TaskID: "a", Title: "Ship v1", State: StateOpen},
		{TaskID: "b", Title: "Review PR", State: StateWaiting, HasDueOn: true, DueOn: day(-2)},
		{TaskID: "c", Title: "Docs update", State: StateDone, HasCompleted: true, CompletedAt: day(-3)},
		{TaskID: "d", Title: "Old task", State: StateDone, HasCompleted: true, CompletedAt: day(-30)},
		{TaskID: "e", Title: "Not due yet", State: StateOpen, HasDueOn: true, DueOn: day(5)},
	}
	d := Build(tasks, now, region.UTC())

	if d.Counts[StateOpen] != 2 {
		t.Errorf("open count: want 2 got %d", d.Counts[StateOpen])
	}
	if d.Counts[StateDone] != 2 {
		t.Errorf("done count: want 2 got %d", d.Counts[StateDone])
	}
	if len(d.CompletedThisWeek) != 1 || d.CompletedThisWeek[0].TaskID != "c" {
		t.Errorf("completed this week: got %+v", d.CompletedThisWeek)
	}
	if len(d.OverdueOpen) != 1 || d.OverdueOpen[0].TaskID != "b" {
		t.Errorf("overdue: got %+v", d.OverdueOpen)
	}
	if !strings.Contains(d.Markdown, "# Weekly digest") {
		t.Errorf("markdown missing header: %q", d.Markdown)
	}
	if !strings.Contains(d.Markdown, "Review PR") {
		t.Errorf("markdown missing overdue task title")
	}
	if !strings.Contains(d.Markdown, "Docs update") {
		t.Errorf("markdown missing completed task title")
	}
}

// TestBuildDueTodayIsNotOverdue pins the overdue list to calendar days:
// a task due 2026-10-23 is not overdue at 15:00 UTC that day, and is from
// midnight on.
func TestBuildDueTodayIsNotOverdue(t *testing.T) {
	tasks := []TaskSnapshot{{
		TaskID: "t", Title: "Due today", State: StateOpen,
		HasDueOn: true, DueOn: time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC),
	}}
	if d := Build(tasks, time.Date(2026, 10, 23, 15, 0, 0, 0, time.UTC), region.UTC()); len(d.OverdueOpen) != 0 {
		t.Fatalf("due today at 15:00Z: overdue = %+v, want none", d.OverdueOpen)
	}
	if d := Build(tasks, time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC), region.UTC()); len(d.OverdueOpen) != 1 {
		t.Fatalf("the next day at 00:00Z: overdue = %+v, want the task", d.OverdueOpen)
	}
}
