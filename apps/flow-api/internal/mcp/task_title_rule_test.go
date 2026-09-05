package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// The tools below refuse a blank title before they touch the database,
// so these run against a zero Deps: reaching a nil pool would panic
// rather than fail, which is itself the assertion that the refusal comes
// first.

// TestCreateTaskRefusesWhitespaceTitle is the case the shared rule
// exists for. create_task used to compare the raw argument against "",
// so a title of spaces was stored verbatim and the task appeared in
// every list with nothing to click on — while the same body sent to
// POST /tasks was refused. The two surfaces now answer the same way.
func TestCreateTaskRefusesWhitespaceTitle(t *testing.T) {
	for _, raw := range []string{"", " ", "   ", "\t", "\n", " \t\n "} {
		args, err := json.Marshal(map[string]any{
			"projectId": "01900000-0000-7000-8000-000000000000",
			"title":     raw,
		})
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		_, err = runCreateTask(context.Background(), Deps{}, &session{}, args)
		requireArgumentsInvalid(t, err)
	}
}

// TestSmartCreateTaskRefusesWhitespaceTitle covers the second tool that
// takes a caller-supplied parent title. Its subtask titles come from the
// model rather than the caller and are dropped individually, so only the
// parent is a refusal.
func TestSmartCreateTaskRefusesWhitespaceTitle(t *testing.T) {
	for _, raw := range []string{"", "  ", "\t\n"} {
		args, err := json.Marshal(map[string]any{
			"projectId": "01900000-0000-7000-8000-000000000000",
			"title":     raw,
		})
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		_, err = runSmartCreateTask(context.Background(), Deps{}, &session{}, args)
		requireArgumentsInvalid(t, err)
	}
}
