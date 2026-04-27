package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestWorkspaceStream exercises the realtime SSE endpoint (ADR 0005).
// It opens a subscription for a fresh tenant, creates a task in a
// separate goroutine, and asserts that a `task.changed` frame shows
// up on the stream within a small budget.
func TestWorkspaceStream(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/stream", nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+tt.AccessToken)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	// Writer goroutine: wait until the reader has observed the initial
	// resync frame (i.e. the subscription is fully registered server-side)
	// before creating the task, so the append hook is guaranteed to find
	// at least this subscriber.
	resyncSeen := make(chan struct{})
	go func() {
		select {
		case <-resyncSeen:
		case <-ctx.Done():
			return
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     "stream-triggering task",
			"priority":  1,
		}, nil)
	}()

	// Reader: parse SSE frames until we see task.changed or time out.
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)

	var (
		currentEvent string
		currentData  string
		sawResync    bool
		sawTask      bool
	)

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			currentEvent = strings.TrimSpace(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			currentData = strings.TrimSpace(line[len("data:"):])
		case line == "":
			// End of frame — dispatch.
			if currentEvent == "" {
				continue
			}
			switch currentEvent {
			case "resync":
				if !sawResync {
					close(resyncSeen)
				}
				sawResync = true
			case "task.changed":
				var evt struct {
					Kind        string `json:"kind"`
					WorkspaceID string `json:"workspaceId"`
				}
				require.NoError(t, json.Unmarshal([]byte(currentData), &evt))
				require.Equal(t, "task.changed", evt.Kind)
				require.Equal(t, tt.WorkspacePublicID, evt.WorkspaceID)
				sawTask = true
			}
			currentEvent = ""
			currentData = ""
			if sawTask {
				cancel()
				break
			}
		}
		if sawTask {
			break
		}
	}

	require.True(t, sawResync, "expected initial resync frame on connect")
	require.True(t, sawTask, "expected a task.changed frame after POST /tasks")
}

// TestWorkspaceStream_RequiresAuth asserts that an unauthenticated
// caller cannot open an SSE subscription.
func TestWorkspaceStream_RequiresAuth(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	req, err := http.NewRequest(http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/stream", nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.GreaterOrEqual(t, resp.StatusCode, 400)
	require.Less(t, resp.StatusCode, 500)
}
