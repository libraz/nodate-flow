package priorityopt

import (
	"testing"
	"time"
)

func TestEvaluate_OverdueRaisesPriority(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	out := Evaluate([]Signals{{
		TaskID:          "a",
		Title:           "Ship",
		State:           StateOpen,
		CurrentPriority: 1,
		UpdatedAt:       now.Add(-24 * time.Hour),
		HasDueOn:        true,
		DueOn:           now.Add(-48 * time.Hour),
		HasAssignee:     true,
		Now:             now,
	}})
	if len(out) != 1 {
		t.Fatalf("expected 1 suggestion, got %d", len(out))
	}
	if out[0].SuggestedPriority <= 1 {
		t.Fatalf("overdue task should bump priority above 1, got %d", out[0].SuggestedPriority)
	}
	if out[0].Confidence < 0.5 {
		t.Fatalf("overdue confidence should be >=0.5, got %v", out[0].Confidence)
	}
}

func TestEvaluate_SkipsUnchanged(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	out := Evaluate([]Signals{{
		TaskID:          "steady",
		State:           StateOpen,
		CurrentPriority: 2,
		UpdatedAt:       now.Add(-3 * 24 * time.Hour),
		HasAssignee:     true,
		Now:             now,
	}})
	// No due date, no dependencies, assigned → score ≈ 1.5, round=2,
	// delta=0 → filtered out.
	if len(out) != 0 {
		t.Fatalf("expected no suggestion for steady task, got %+v", out)
	}
}

func TestEvaluate_SortsByPriorityThenConfidence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	out := Evaluate([]Signals{
		{
			TaskID: "mid", State: StateOpen, CurrentPriority: 0,
			HasDueOn: true, DueOn: now.Add(72 * time.Hour),
			UpdatedAt: now, HasAssignee: true, Now: now,
		},
		{
			TaskID: "high", State: StateReview, CurrentPriority: 0,
			HasDueOn: true, DueOn: now.Add(-1 * time.Hour),
			UpdatedAt: now, HasAssignee: true, Now: now,
			DependencyCount: 4,
		},
	})
	if len(out) < 2 {
		t.Fatalf("expected 2 suggestions, got %d", len(out))
	}
	if out[0].TaskID != "high" {
		t.Fatalf("expected overdue+review+deps task first, got %q", out[0].TaskID)
	}
}

func TestEvaluate_ClampsToRange(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	out := Evaluate([]Signals{{
		TaskID: "x", State: StateReview, CurrentPriority: 0,
		HasDueOn: true, DueOn: now.Add(-100 * time.Hour),
		DependencyCount: 10, HasAssignee: false, Now: now, UpdatedAt: now,
	}})
	if len(out) == 0 {
		t.Fatal("expected a suggestion")
	}
	if out[0].SuggestedPriority < 0 || out[0].SuggestedPriority > 4 {
		t.Fatalf("priority out of range: %d", out[0].SuggestedPriority)
	}
}

func TestEvaluate_EmptyReturnsNonNil(t *testing.T) {
	t.Parallel()
	out := Evaluate(nil)
	if out == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(out) != 0 {
		t.Fatalf("expected 0, got %d", len(out))
	}
}
