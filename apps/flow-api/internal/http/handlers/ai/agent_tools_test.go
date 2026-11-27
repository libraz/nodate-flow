package ai

import (
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

func TestAgentToolsView(t *testing.T) {
	if got := agentToolsView(generated.AiAgentsKindSignalJudge, []byte(`{"add_comment":"auto"}`)); got != nil {
		t.Errorf("signal_judge carries no grants, got %v", got)
	}
	for _, raw := range []string{``, `null`, `{}`, `["add_comment"]`, `{"nope":"auto"}`} {
		got := agentToolsView(generated.AiAgentsKindTaskAgent, []byte(raw))
		if got == nil || len(got) != 0 {
			t.Errorf("task_agent with %q should show an empty, non-null object, got %#v", raw, got)
		}
	}
	got := agentToolsView(generated.AiAgentsKindTaskAgent, []byte(`{"add_comment":"auto","set_priority":"approval"}`))
	if len(got) != 2 || got["add_comment"] != "auto" || got["set_priority"] != "approval" {
		t.Errorf("grants not rendered: %v", got)
	}
}

func TestValidEventTriggerTypes_RuntimeKindsRefused(t *testing.T) {
	if !validEventTriggerTypes([]string{string(eventbus.TaskCommentAdded)}) {
		t.Fatal("an ordinary task event must stay subscribable")
	}
	for _, k := range []string{
		string(eventbus.AgentTaskStep),
		string(eventbus.AgentActionProposed),
		string(eventbus.AgentActionDecided),
		string(eventbus.AgentTaskRunRequested),
	} {
		if validEventTriggerTypes([]string{k}) {
			t.Errorf("%s is appended by the task-work machinery and must not be subscribable", k)
		}
	}
}
