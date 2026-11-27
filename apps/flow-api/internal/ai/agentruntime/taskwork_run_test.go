package agentruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

func TestTaskWorkLoop_TriggerDerivation(t *testing.T) {
	t.Parallel()

	agent := types.New().String()
	other := types.New().String()
	assignee := string(generated.TaskActorsRoleAssignee)
	watcher := string(generated.TaskActorsRoleWatcher)
	human := func(kind eventbus.Kind) sourceEvent {
		return sourceEvent{Type: string(kind), TaskID: 11, ActorUserID: 5}
	}
	with := func(ev sourceEvent, f func(*sourceEvent)) sourceEvent {
		f(&ev)
		return ev
	}

	cases := []struct {
		name string
		ev   sourceEvent
		want bool
	}{
		{"assigned to this agent", with(human(eventbus.TaskActorAdded), func(e *sourceEvent) { e.PayloadAgentID, e.PayloadRole = agent, assignee }), true},
		{"assigned to another agent", with(human(eventbus.TaskActorAdded), func(e *sourceEvent) { e.PayloadAgentID, e.PayloadRole = other, assignee }), false},
		{"added as watcher", with(human(eventbus.TaskActorAdded), func(e *sourceEvent) { e.PayloadAgentID, e.PayloadRole = agent, watcher }), false},
		{"handed to this agent", with(human(eventbus.AgentTaskHandoffToAgent), func(e *sourceEvent) { e.PayloadAgentID = agent }), true},
		{"handed to another agent", with(human(eventbus.AgentTaskHandoffToAgent), func(e *sourceEvent) { e.PayloadAgentID = other }), false},
		{"run requested for this agent", with(human(eventbus.AgentTaskRunRequested), func(e *sourceEvent) { e.PayloadAgentID = agent }), true},
		{"run request with no agent", human(eventbus.AgentTaskRunRequested), false},
		{"comment by a person", human(eventbus.TaskCommentAdded), true},
		{"comment by an agent", with(human(eventbus.TaskCommentAdded), func(e *sourceEvent) { e.ActorAgentID = 3 }), false},
		{"comment with no human actor", with(human(eventbus.TaskCommentAdded), func(e *sourceEvent) { e.ActorUserID = 0 }), false},
		{"event on no task", with(human(eventbus.TaskCommentAdded), func(e *sourceEvent) { e.TaskID = 0 }), false},
		{"other kind", human(eventbus.AiAgentRunCompleted), false},
	}
	for _, tc := range cases {
		if got := isTaskWorkSource(tc.ev, agent); got != tc.want {
			t.Errorf("%s: isTaskWorkSource = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// ctxExecutor captures the run key and trigger the runner hands over.
type ctxExecutor struct {
	result ExecutionResult
	err    error
	runKey string
}

func (e *ctxExecutor) ExecuteAgent(ctx context.Context, _, _ uint32) (ExecutionResult, error) {
	e.runKey = RunKeyFromContext(ctx)
	return e.result, e.err
}

func payloadOf(t *testing.T, evt eventbus.Event) map[string]any {
	t.Helper()
	p, ok := evt.Payload.(map[string]any)
	if !ok {
		t.Fatalf("%s payload is %T, want map[string]any", evt.Type, evt.Payload)
	}
	return p
}

func TestTaskWorkLoop_RunEventsCarryRunKeyModeAndEndedReason(t *testing.T) {
	t.Parallel()

	t.Run("completed", func(t *testing.T) {
		t.Parallel()
		q := newFakeQuerier()
		ctxEvt := types.New()
		exec := &ctxExecutor{result: ExecutionResult{EndedReason: "done", ContextEventPublicID: ctxEvt}}
		if err := newRunnerWithFakes(q, exec).Run(context.Background(), Job{AgentID: 7, WsID: 42}, time.Time{}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if exec.runKey == "" {
			t.Fatal("executor got no run key")
		}
		if len(q.agentEvents) != 2 {
			t.Fatalf("events = %v", q.eventTypes())
		}
		for _, evt := range q.agentEvents {
			p := payloadOf(t, evt)
			if p["runKey"] != exec.runKey || p["mode"] != RunModeTick {
				t.Fatalf("%s payload = %v, want runKey %q and mode tick", evt.Type, p, exec.runKey)
			}
		}
		done := payloadOf(t, q.agentEvents[1])
		if done["endedReason"] != "done" || done["contextEventId"] != ctxEvt.String() {
			t.Fatalf("completed payload = %v", done)
		}
		if _, ok := payloadOf(t, q.agentEvents[0])["endedReason"]; ok {
			t.Fatal("started payload carries an endedReason")
		}
	})

	t.Run("failed", func(t *testing.T) {
		t.Parallel()
		q := newFakeQuerier()
		exec := &ctxExecutor{result: ExecutionResult{EndedReason: "timeout"}, err: errors.New("deadline")}
		if err := newRunnerWithFakes(q, exec).Run(context.Background(), Job{AgentID: 7, WsID: 42}, time.Time{}); err == nil {
			t.Fatal("Run succeeded, want the executor error")
		}
		failed := q.agentEvents[1]
		p := payloadOf(t, failed)
		if failed.Type != eventbus.AiAgentRunFailed || p["endedReason"] != "timeout" || p["runKey"] != exec.runKey {
			t.Fatalf("failed event = %s %v", failed.Type, p)
		}
		if _, ok := p["contextEventId"]; ok {
			t.Fatal("a run with no context event writes contextEventId")
		}
	})
}

func TestTaskWorkLoop_NoRuntimeHandBackDisable(t *testing.T) {
	t.Parallel()

	t.Run("stopped hands nothing back", func(t *testing.T) {
		t.Parallel()
		q := newFakeQuerier()
		exec := &ctxExecutor{result: ExecutionResult{EndedReason: "stopped"}}
		if err := newRunnerWithFakes(q, exec).Run(context.Background(), Job{AgentID: 7, WsID: 42}, time.Time{}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(q.handoffEvents) != 0 {
			t.Fatalf("stopped run recorded %d hand-backs, want 0", len(q.handoffEvents))
		}
	})

	t.Run("tool_error only records the hand-back", func(t *testing.T) {
		t.Parallel()
		q := newFakeQuerier()
		exec := &ctxExecutor{result: ExecutionResult{EndedReason: "tool_error", ConsecutiveToolFailures: 3}}
		if err := newRunnerWithFakes(q, exec).Run(context.Background(), Job{AgentID: 7, WsID: 42}, time.Time{}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(q.handoffEvents) != 1 || q.handoffEvents[0].Payload["reason"] != handoffReasonToolError {
			t.Fatalf("hand-backs = %+v, want one tool_error", q.handoffEvents)
		}
		if got := q.eventTypes(); len(got) != 2 || got[1] != string(eventbus.AiAgentRunCompleted) {
			t.Fatalf("events = %v, want started then completed", got)
		}
		if p := payloadOf(t, q.agentEvents[1]); p["endedReason"] != "tool_error" {
			t.Fatalf("completed payload = %v", p)
		}
	})
}
