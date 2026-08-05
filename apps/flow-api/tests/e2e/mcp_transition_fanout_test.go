package e2e

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// fanOutObservation is what a notify subscriber saw for one event: the
// event type, and whether the row was already readable from another
// connection when the subscriber ran. Every real subscriber (webhook
// worker, notification fan-out, SSE tap, on_event triggers) reads the
// event row on its own connection, so "not yet visible" is the same as
// "no delivery" for all of them.
type fanOutObservation struct {
	eventType string
	visible   bool
}

// TestTransitionFanOutMatchesBetweenRESTAndMCP pins the contract that a
// state transition fans out the same way whoever drove it.
//
// Both paths run the identical state-machine helper, but each opened
// its own transaction and only the REST one went through dbretry.InTx.
// The append fires the fan-out through a commit hook, and a hand-rolled
// transaction gives it no commit to hang on: the subscribers ran while
// the transaction still held the row, so the event they were told about
// did not exist yet on their connection. From a user's seat that reads
// as "webhooks fire when I move a task in the web app but not when my
// agent does", with no error anywhere.
func TestTransitionFanOutMatchesBetweenRESTAndMCP(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	ctx := context.Background()
	tt := newTenant(t)
	wsInternalID := lookupWorkspaceInternalID(ctx, t, testDB, tt.WorkspacePublicID)

	// Subscribe to the fan-out the way every production subscriber
	// does, and check from a second connection whether the row it was
	// handed is readable yet.
	var (
		mu       sync.Mutex
		observed []fanOutObservation
	)
	handle := eventbus.AddNotifyHook(func(_ context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
		if workspaceID != wsInternalID {
			return // another test's tenant; the registry is process-global
		}
		var pub string
		err := testDB.QueryRow(
			`SELECT BIN_TO_UUID(public_id, 0) FROM events WHERE id = ?`, eventInternalID).Scan(&pub)
		mu.Lock()
		observed = append(observed, fanOutObservation{eventType: eventType, visible: err == nil && pub != ""})
		mu.Unlock()
	})
	t.Cleanup(func() { eventbus.RemoveNotifyHook(handle) })

	newTask := func(title string) string {
		var task struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
			map[string]any{"projectId": tt.ProjectPublicID, "title": title}, &task)
		require.NotEmpty(t, task.ID)
		return task.ID
	}

	restTask := newTask("Transition via REST")
	mcpTask := newTask("Transition via MCP")

	var mcpToken struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/me/mcp-tokens",
		tt.AccessToken, map[string]any{
			"name":   "transition-fanout",
			"scopes": []string{"read:workspace", "write:workspace"},
		}, &mcpToken)
	require.NotEmpty(t, mcpToken.Token)

	// --- REST: the path that already fanned out correctly ---
	var restResult struct {
		DerivedState string `json:"derivedState"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+restTask+"/transitions",
		tt.AccessToken, map[string]any{"transition": "start"}, &restResult)
	require.Equal(t, "waiting", restResult.DerivedState)

	// --- MCP: the path that used to append and never deliver ---
	callBody := mcpCall(t, mcpToken.Token, "tools/call", map[string]any{
		"name": "transition_task",
		"arguments": map[string]any{
			"taskId":     mcpTask,
			"transition": "start",
		},
	})
	mcpResult := mcpToolTextJSON[struct {
		ID      string `json:"id"`
		ToState string `json:"toState"`
	}](t, callBody)
	require.Equal(t, "waiting", mcpResult.ToState, "MCP transition must move the task")

	transitionsSeen := func() []fanOutObservation {
		mu.Lock()
		defer mu.Unlock()
		var out []fanOutObservation
		for _, o := range observed {
			if o.eventType == "task.transition.start" {
				out = append(out, o)
			}
		}
		return out
	}

	// Subscribers are dispatched off the append path, so poll rather
	// than read once.
	require.Eventually(t, func() bool { return len(transitionsSeen()) >= 2 },
		5*time.Second, 25*time.Millisecond,
		"both transitions must reach the fan-out; got %d", len(transitionsSeen()))

	for _, o := range transitionsSeen() {
		require.True(t, o.visible,
			"a subscriber was told about an event that is not readable on another connection yet: "+
				"the transaction had not committed, so every real subscriber would resolve nothing and deliver nothing")
	}
}
