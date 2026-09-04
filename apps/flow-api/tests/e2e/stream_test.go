package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// streamReadBudget bounds the worst case of the SSE read loop. The happy
// path cancels as soon as the awaited frame arrives, so the budget is
// only ever spent when something is wrong. Inside the full parallel run
// of this package the whole test takes about 0.2s, so the budget clears
// what it needs by two orders of magnitude: wide enough that a loaded
// machine cannot reach it, tight enough that a genuinely stalled stream
// still fails the suite promptly.
const streamReadBudget = 30 * time.Second

// streamPostResult carries the outcome of the create the writer
// goroutine performs. It distinguishes a create that ran from one that
// never started, because a status of 0 is not an answer the server
// gave and must never be asserted against as if it were.
type streamPostResult struct {
	attempted bool
	status    int
	body      []byte
	blockedBy error // why the create never started
}

// describe renders the create's outcome for a failure message.
func (r streamPostResult) describe() string {
	switch {
	case r.attempted:
		return fmt.Sprintf("POST /tasks -> %d body=%s", r.status, string(r.body))
	case r.blockedBy != nil:
		return fmt.Sprintf("POST /tasks never ran: the read budget ended (%v) before the initial resync frame arrived", r.blockedBy)
	default:
		return "POST /tasks never ran"
	}
}

// streamFrameLog records which SSE event kinds arrived, in order of
// first appearance and with counts. A failure has to say what the
// stream did deliver: "resync arrived, task.changed did not" and
// "nothing arrived at all" are different faults and must not read the
// same.
type streamFrameLog struct {
	order  []string
	counts map[string]int
}

func (l *streamFrameLog) add(kind string) {
	if l.counts == nil {
		l.counts = make(map[string]int)
	}
	if l.counts[kind] == 0 {
		l.order = append(l.order, kind)
	}
	l.counts[kind]++
}

func (l *streamFrameLog) String() string {
	if len(l.order) == 0 {
		return "frames observed: none"
	}
	parts := make([]string, 0, len(l.order))
	for _, kind := range l.order {
		parts = append(parts, fmt.Sprintf("%s x%d", kind, l.counts[kind]))
	}
	return "frames observed: " + strings.Join(parts, ", ")
}

// TestWorkspaceStream exercises the realtime SSE endpoint (ADR 0005).
// It opens a subscription for a fresh tenant, creates a task in a
// separate goroutine, and asserts that a `task.changed` frame shows
// up on the stream.
//
// The read loop can end for three unrelated reasons — an expired read
// budget, an I/O error on the connection, or the server never sending
// the frame — and only the last one is a fault in the event pipeline.
// Each therefore fails with its own words, so a timeout or a dropped
// connection never sends the next reader hunting for a missing
// realtime event that was in fact never missing.
func TestWorkspaceStream(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	ctx, cancel := context.WithTimeout(context.Background(), streamReadBudget)
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
	writerDone := make(chan streamPostResult, 1)
	resyncSeen := make(chan struct{})
	go func() {
		select {
		case <-resyncSeen:
		case <-ctx.Done():
			// The create never started, so there is no status to
			// report; carry the reason instead so the failure names
			// the stalled subscription rather than a bogus 0.
			writerDone <- streamPostResult{blockedBy: context.Cause(ctx)}
			return
		}
		status, body := doJSONStatus(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     "stream-triggering task",
			"priority":  1,
		})
		writerDone <- streamPostResult{attempted: true, status: status, body: body}
	}()

	// Reader: parse SSE frames until we see task.changed or time out.
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)

	var (
		currentEvent string
		currentData  string
		sawResync    bool
		sawTask      bool
		frames       streamFrameLog
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
			frames.add(currentEvent)
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

	// Why the loop ended decides what may be claimed from here on.
	readErr := scanner.Err()
	// The reader cancels ctx itself the moment the awaited frame
	// arrives, so a cancelled context is not a timeout. Only an
	// expired deadline is, and ctx keeps its first error, so a later
	// deadline cannot overwrite that cancellation either.
	budgetExpired := errors.Is(ctx.Err(), context.DeadlineExceeded)

	// Take the create's outcome if the writer already has one, so the
	// diagnosis can say whether the write that should have produced
	// the frame even reached the server. On the happy path the create
	// is still in flight here and is awaited further down.
	var (
		post         streamPostResult
		postReported bool
	)
	select {
	case post = <-writerDone:
		postReported = true
	default:
	}
	postState := "POST /tasks: no outcome reported yet"
	if postReported {
		postState = post.describe()
	}

	switch {
	case budgetExpired:
		// An expired budget says the reader stopped waiting. It says
		// nothing about whether the server would have sent the frame.
		stopped := ""
		if readErr != nil {
			stopped = fmt.Sprintf(" the read stopped with: %v.", readErr)
		}
		require.FailNowf(t, "SSE read budget expired",
			"the %s budget elapsed while waiting for task.changed, so this is a timeout rather than a missing realtime event.%s %s; %s",
			streamReadBudget, stopped, frames.String(), postState)
	case readErr != nil:
		// A broken connection ends the read wherever it happens to be;
		// nothing about the event pipeline follows from it.
		require.FailNowf(t, "SSE stream read failed",
			"reading the stream stopped on an I/O error, so no conclusion about delivered frames follows: %v. %s; %s",
			readErr, frames.String(), postState)
	}

	// The stream stayed readable for its whole budget, so what did and
	// did not arrive on it is now evidence about the server.
	require.Truef(t, sawResync,
		"the stream ended cleanly without an initial resync frame on connect; %s; %s", frames.String(), postState)
	require.Truef(t, sawTask,
		"the stream stayed open and readable but no task.changed frame followed the create; %s; %s", frames.String(), postState)

	// The create that produced the frame must itself have succeeded: a
	// realtime event must never be delivered for a task write that did
	// not commit. Assert the POST outcome here on the main goroutine.
	if !postReported {
		post = <-writerDone
	}
	require.Truef(t, post.attempted, "%s", post.describe())
	require.GreaterOrEqualf(t, post.status, 200, "%s", post.describe())
	require.Lessf(t, post.status, 300, "%s", post.describe())
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
