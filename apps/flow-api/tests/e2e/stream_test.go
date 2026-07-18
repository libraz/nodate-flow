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
// up on the stream. The read budget is generous enough to absorb CI
// load; the happy path cancels immediately once the frame is seen, so
// the budget only bounds the worst case.
func TestWorkspaceStream(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	//
	// The realtime frame is flushed to the reader during the POST handler
	// (right after the create commits, before the HTTP response is
	// written), so the reader can observe task.changed and finish the
	// test while this POST is still in flight. Report the create's
	// outcome on a channel and assert it on the main goroutine after the
	// read loop instead of calling require.* here — a require failure in
	// a goroutine that outlives the test panics with "Fail in goroutine
	// after test has completed".
	type postResult struct {
		status int
		body   []byte
	}
	writerDone := make(chan postResult, 1)
	resyncSeen := make(chan struct{})
	go func() {
		select {
		case <-resyncSeen:
		case <-ctx.Done():
			writerDone <- postResult{}
			return
		}
		status, body := doJSONStatus(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     "stream-triggering task",
			"priority":  1,
		})
		writerDone <- postResult{status: status, body: body}
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

	// The create that produced the frame must itself have succeeded: a
	// realtime event must never be delivered for a task write that did
	// not commit. Assert the POST outcome here on the main goroutine.
	res := <-writerDone
	require.GreaterOrEqualf(t, res.status, 200, "POST /tasks -> %d body=%s", res.status, string(res.body))
	require.Lessf(t, res.status, 300, "POST /tasks -> %d body=%s", res.status, string(res.body))
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
