// Package eventbus — unit tests for the three-way actor exclusion guard
// added in ADR 0008 D8.
package eventbus

import (
	"errors"
	"testing"

	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
)

// TestValidateActors locks in the three-way exclusion: at most one of
// ActorUserID, ActorAgentID, ActorSystemSource may be set per event.
// Zero set is allowed and represents the legacy "system actor" path
// (orchestrator runner, MCP tool calls that predate the system-source
// column). Setting two or three is a server bug and short-circuits
// with INTERNAL.EVENTBUS.ACTOR_MULTIPLE so the canonical RFC 9457
// envelope shows up at the boundary.
func TestValidateActors(t *testing.T) {
	t.Parallel()

	userOne := int64(1)
	agentOne := int64(2)

	tests := []struct {
		name      string
		evt       Event
		expectErr bool
	}{
		// --- zero actors ------------------------------------------------
		{
			name:      "none set is allowed (legacy system actor)",
			evt:       Event{Type: "task.created", WorkspaceID: 1},
			expectErr: false,
		},
		// --- exactly one actor ------------------------------------------
		{
			name:      "user-only is allowed",
			evt:       Event{Type: "task.created", WorkspaceID: 1, ActorUserID: &userOne},
			expectErr: false,
		},
		{
			name:      "agent-only is allowed",
			evt:       Event{Type: "ai.agent.run.started", WorkspaceID: 1, ActorAgentID: &agentOne},
			expectErr: false,
		},
		{
			name:      "system-source-only is allowed",
			evt:       Event{Type: "calendar.event_day_arrived", WorkspaceID: 1, ActorSystemSource: "worker.calendar"},
			expectErr: false,
		},
		// --- two actors -------------------------------------------------
		{
			name:      "user + agent is rejected",
			evt:       Event{Type: "task.created", WorkspaceID: 1, ActorUserID: &userOne, ActorAgentID: &agentOne},
			expectErr: true,
		},
		{
			name:      "user + system is rejected",
			evt:       Event{Type: "task.created", WorkspaceID: 1, ActorUserID: &userOne, ActorSystemSource: "worker.retention"},
			expectErr: true,
		},
		{
			name:      "agent + system is rejected",
			evt:       Event{Type: "task.created", WorkspaceID: 1, ActorAgentID: &agentOne, ActorSystemSource: "worker.calendar"},
			expectErr: true,
		},
		// --- all three actors -------------------------------------------
		{
			name: "all three set is rejected",
			evt: Event{
				Type:              "task.created",
				WorkspaceID:       1,
				ActorUserID:       &userOne,
				ActorAgentID:      &agentOne,
				ActorSystemSource: "worker.retention",
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateActors(tc.evt)
			if tc.expectErr {
				if err == nil {
					t.Fatalf("validateActors: want error, got nil")
				}
				var ae *apierr.APIError
				if !errors.As(err, &ae) {
					t.Fatalf("validateActors: want *apierr.APIError, got %T", err)
				}
				if ae.Spec == nil || ae.Spec.Code != apierrors.InternalEventbusActorMultiple.Code {
					t.Fatalf("validateActors: want code %q, got %#v", apierrors.InternalEventbusActorMultiple.Code, ae.Spec)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateActors: want nil, got %v", err)
			}
		})
	}
}
