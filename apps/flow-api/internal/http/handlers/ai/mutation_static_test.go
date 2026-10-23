package ai

import (
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog/mutationguard"
)

// The handlers here still write audit-only rows for configuration
// changes nothing subscribes to, so the package cannot ban the audit
// import the way a fully migrated package does. What it can hold is the
// event half: every events row is appended through the recorder, inside
// the transaction of the change it describes.

// eventMutationGates are the recorder entry points that append an event.
var eventMutationGates = map[string]bool{
	"RecordInTx":   true,
	"RecordStrict": true,
	"Record":       true,
}

// TestAIHandlersAppendEventsOnlyThroughTheMutationLog proves no handler
// appends an events row on its own, after its change has committed.
func TestAIHandlersAppendEventsOnlyThroughTheMutationLog(t *testing.T) {
	t.Parallel()

	a, err := mutationguard.Load(".")
	if err != nil {
		t.Fatalf("load package: %v", err)
	}
	for _, f := range a.Centralized("", []string{
		"eventbus.Append",
		"eventbus.AppendBestEffort",
		"eventbus.AppendJudgeEvent",
		"eventbus.AppendReverseEvent",
	}) {
		t.Error(f.String())
	}
	if !a.Reaches("PauseAgent", eventMutationGates) {
		t.Error("PauseAgent appends ai.agent.paused/resumed and must reach a recorder entry point")
	}
}
