package inboxtriage

import (
	"testing"
	"time"
)

func TestEvaluate_SortsByScoreDesc(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)

	signals := []Signal{
		{
			InboxItemID: "a-stale-comment",
			Source:      "github",
			Kind:        "comment",
			ReceivedAt:  now.Add(-72 * time.Hour),
			Now:         now,
		},
		{
			InboxItemID: "b-fresh-review",
			Source:      "github",
			Kind:        "pr.review_requested",
			ReceivedAt:  now.Add(-1 * time.Hour),
			Now:         now,
		},
		{
			InboxItemID: "c-mention",
			Source:      "slack",
			Kind:        "mention",
			ReceivedAt:  now.Add(-6 * time.Hour),
			Now:         now,
		},
	}

	out := Evaluate(signals)
	if len(out) != 3 {
		t.Fatalf("expected 3, got %d", len(out))
	}
	if out[0].InboxItemID != "b-fresh-review" {
		t.Fatalf("expected fresh review first, got %q", out[0].InboxItemID)
	}
	if out[0].RecommendedAction != ActionOpen {
		t.Fatalf("fresh review should recommend open, got %q", out[0].RecommendedAction)
	}
	if out[2].InboxItemID != "a-stale-comment" {
		t.Fatalf("expected stale comment last, got %q", out[2].InboxItemID)
	}
	// Stale low-weight signal should archive.
	if out[2].RecommendedAction != ActionArchive {
		t.Fatalf("stale comment should archive, got %q", out[2].RecommendedAction)
	}
}

func TestEvaluate_BoundToTaskReducesScore(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()

	bound := Signal{
		InboxItemID: "bound",
		Source:      "github",
		Kind:        "pr.review_requested",
		ReceivedAt:  now,
		HasTask:     true,
		Now:         now,
	}
	free := Signal{
		InboxItemID: "free",
		Source:      "github",
		Kind:        "pr.review_requested",
		ReceivedAt:  now,
		HasTask:     false,
		Now:         now,
	}

	out := Evaluate([]Signal{bound, free})
	if out[0].InboxItemID != "free" {
		t.Fatalf("free signal should outrank bound, got %+v", out)
	}
	if out[0].Score <= out[1].Score {
		t.Fatalf("free score should exceed bound, got %v vs %v", out[0].Score, out[1].Score)
	}
}

func TestEvaluate_EmptyReturnsNonNil(t *testing.T) {
	t.Parallel()
	out := Evaluate(nil)
	if out == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(out) != 0 {
		t.Fatalf("expected length 0, got %d", len(out))
	}
}

func TestEvaluate_ScoreBounds(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	out := Evaluate([]Signal{
		{InboxItemID: "x", Source: "slack", Kind: "mention", ReceivedAt: now, Now: now},
	})
	if out[0].Score < 0 || out[0].Score > 1 {
		t.Fatalf("score out of range: %v", out[0].Score)
	}
}

func TestEvaluate_UnknownKindFallsBackToDefault(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	out := Evaluate([]Signal{
		{InboxItemID: "u", Source: "custom", Kind: "totally.unknown", ReceivedAt: now, Now: now},
	})
	// Default weight (0.45) fresh → no decay → should be ~0.45
	// and recommend snooze (score in [0.25, 0.60), unbound).
	if out[0].RecommendedAction != ActionSnooze {
		t.Fatalf("unknown kind default should snooze, got %q", out[0].RecommendedAction)
	}
}

func TestPow2_RoughlyAccurate(t *testing.T) {
	t.Parallel()
	cases := map[float32]float32{
		0:  1.0,
		-1: 0.5,
		-2: 0.25,
		-3: 0.125,
	}
	for in, want := range cases {
		got := pow2(in)
		diff := got - want
		if diff < 0 {
			diff = -diff
		}
		if diff > 0.02 {
			t.Fatalf("pow2(%v) = %v, want ~%v", in, got, want)
		}
	}
}
