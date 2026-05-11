package autoactions

import (
	"testing"
	"time"
)

func TestEvaluate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		sig    Signals
		want   Kind // empty = nil
		reason string
	}{
		{
			name: "done task yields nothing",
			sig: Signals{
				State:     StateDone,
				UpdatedAt: now.Add(-30 * 24 * time.Hour),
				Now:       now,
			},
		},
		{
			name: "cancelled task yields nothing",
			sig: Signals{
				State:     StateCancelled,
				UpdatedAt: now.Add(-30 * 24 * time.Hour),
				Now:       now,
			},
		},
		{
			name: "overdue open task escalates",
			sig: Signals{
				State:       StateOpen,
				UpdatedAt:   now.Add(-2 * 24 * time.Hour),
				DueOn:       now.Add(-24 * time.Hour),
				HasDueOn:    true,
				HasAssignee: true,
				Now:         now,
			},
			want: KindEscalateOverdue,
		},
		{
			name: "overdue review also escalates (beats stale review)",
			sig: Signals{
				State:     StateReview,
				UpdatedAt: now.Add(-10 * 24 * time.Hour),
				DueOn:     now.Add(-24 * time.Hour),
				HasDueOn:  true,
				Now:       now,
			},
			want: KindEscalateOverdue,
		},
		{
			name: "open idle unassigned → assign owner",
			sig: Signals{
				State:       StateOpen,
				UpdatedAt:   now.Add(-2 * 24 * time.Hour),
				HasAssignee: false,
				Now:         now,
			},
			want: KindAssignOwner,
		},
		{
			name: "fresh open unassigned → no action",
			sig: Signals{
				State:       StateOpen,
				UpdatedAt:   now.Add(-2 * time.Hour),
				HasAssignee: false,
				Now:         now,
			},
		},
		{
			name: "open with assignee idle 3 days → nudge",
			sig: Signals{
				State:         StateOpen,
				UpdatedAt:     now.Add(-4 * 24 * time.Hour),
				HasAssignee:   true,
				AssigneeCount: 1,
				Now:           now,
			},
			want: KindNudgeAssignee,
		},
		{
			name: "review idle 6 days → close stale review",
			sig: Signals{
				State:     StateReview,
				UpdatedAt: now.Add(-6 * 24 * time.Hour),
				Now:       now,
			},
			want: KindCloseStaleReview,
		},
		{
			name: "waiting task without overdue yields nothing",
			sig: Signals{
				State:     StateWaiting,
				UpdatedAt: now.Add(-10 * 24 * time.Hour),
				Now:       now,
			},
		},
		{
			name: "open with agent assignee, attempts>=3, idle>4h → handoff_to_user",
			sig: Signals{
				State:               StateOpen,
				UpdatedAt:           now.Add(-2 * time.Hour),
				HasAssignee:         true,
				HasAgentAssignee:    true,
				AgentAttempts:       3,
				AgentLastFinishedAt: now.Add(-5 * time.Hour),
				Now:                 now,
			},
			want: KindHandoffToUser,
		},
		{
			name: "open with agent assignee but attempts<3 → no handoff",
			sig: Signals{
				State:               StateOpen,
				UpdatedAt:           now.Add(-2 * time.Hour),
				HasAssignee:         true,
				HasAgentAssignee:    true,
				AgentAttempts:       2,
				AgentLastFinishedAt: now.Add(-10 * time.Hour),
				Now:                 now,
			},
		},
		{
			name: "open with agent assignee, attempts>=3 but idle<4h → no handoff",
			sig: Signals{
				State:               StateOpen,
				UpdatedAt:           now.Add(-1 * time.Hour),
				HasAssignee:         true,
				HasAgentAssignee:    true,
				AgentAttempts:       5,
				AgentLastFinishedAt: now.Add(-1 * time.Hour),
				Now:                 now,
			},
		},
		{
			name: "no agent assignee → handoff never fires even with attempts",
			sig: Signals{
				State:               StateOpen,
				UpdatedAt:           now.Add(-5 * 24 * time.Hour),
				HasAssignee:         true,
				HasAgentAssignee:    false,
				AgentAttempts:       9,
				AgentLastFinishedAt: now.Add(-99 * time.Hour),
				Now:                 now,
			},
			want: KindNudgeAssignee,
		},
		{
			name: "agent assignee handoff falls back to UpdatedAt when last_finished_at unset",
			sig: Signals{
				State:            StateOpen,
				UpdatedAt:        now.Add(-6 * time.Hour),
				HasAssignee:      true,
				HasAgentAssignee: true,
				AgentAttempts:    3,
				Now:              now,
			},
			want: KindHandoffToUser,
		},
		{
			name: "agent assignee but task overdue → escalate beats handoff",
			sig: Signals{
				State:               StateOpen,
				UpdatedAt:           now.Add(-2 * 24 * time.Hour),
				DueOn:               now.Add(-24 * time.Hour),
				HasDueOn:            true,
				HasAssignee:         true,
				HasAgentAssignee:    true,
				AgentAttempts:       5,
				AgentLastFinishedAt: now.Add(-10 * time.Hour),
				Now:                 now,
			},
			want: KindEscalateOverdue,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Evaluate(tc.sig)
			if tc.want == "" {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected %q, got nil", tc.want)
			}
			if got.Kind != tc.want {
				t.Fatalf("expected kind %q, got %q", tc.want, got.Kind)
			}
			if got.Confidence <= 0 || got.Confidence > 1 {
				t.Fatalf("confidence out of range: %v", got.Confidence)
			}
			if got.Reason == "" {
				t.Fatalf("expected non-empty reason")
			}
		})
	}
}
