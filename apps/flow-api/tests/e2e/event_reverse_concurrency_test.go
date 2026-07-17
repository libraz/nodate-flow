package e2e

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// reverseResult carries one goroutine's outcome back to the test
// goroutine. require/t.FailNow must not run off the test goroutine, so
// the workers only collect and the assertions happen after Wait.
type reverseResult struct {
	status int
	body   string
	err    error
}

// fireReverse issues POST .../events/{pub}/reverse without any
// require calls so it is safe to run inside a worker goroutine.
func fireReverse(url, bearer string) reverseResult {
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return reverseResult{err: err}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return reverseResult{err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return reverseResult{status: resp.StatusCode, err: err}
	}
	return reverseResult{status: resp.StatusCode, body: string(raw)}
}

// countReverseRows returns how many enabled compensating rows point at
// the given target event. The UNIQUE (workspace_id, reverses_event_id)
// index guarantees at most one; the test pins exactly one.
func countReverseRows(t *testing.T, wsID uint32, reversesEventID uint64) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.QueryRow(`
		SELECT COUNT(*) FROM events
		WHERE workspace_id = ? AND reverses_event_id = ? AND enabled = TRUE`,
		wsID, reversesEventID,
	).Scan(&n))
	return n
}

// TestReverseConcurrentDoubleReverse races two simultaneous reverses
// of the same LLM-origin event against live MySQL. Both requests can
// pass the WasReversed pre-check before either commits; the UNIQUE
// (workspace_id, reverses_event_id) index then rejects the loser's
// compensating INSERT with ER_DUP_ENTRY, which the handler must map to
// the canonical AI.REVERSE.ALREADY_REVERSED 409 — never a 500.
//
// The race window is a few milliseconds wide, so the test runs several
// rounds (a fresh target event each round) with a start barrier to
// make hitting the INSERT-level race likely; whichever path the loser
// takes (pre-check or unique violation), the observable contract is
// identical: exactly one 201, exactly one 409, exactly one
// compensating row.
func TestReverseConcurrentDoubleReverse(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Reverse: concurrent double")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)

	const rounds = 4
	for round := 0; round < rounds; round++ {
		origID, origPub := appendAgentEventForTask(t, testDB, wsID, taskInternalID, agent.AgentID,
			"ai.agent.run.completed")
		url := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/events/" + origPub + "/reverse"

		start := make(chan struct{})
		results := make([]reverseResult, 2)
		var wg sync.WaitGroup
		for i := range results {
			wg.Add(1)
			go func(slot int) {
				defer wg.Done()
				<-start
				results[slot] = fireReverse(url, tt.AccessToken)
			}(i)
		}
		close(start)
		wg.Wait()

		var created, conflicted int
		for _, r := range results {
			require.NoError(t, r.err, "round %d: reverse request failed", round)
			switch {
			case r.status == http.StatusCreated:
				created++
			case r.status == http.StatusConflict:
				conflicted++
				require.True(t, strings.Contains(r.body, "AI.REVERSE.ALREADY_REVERSED"),
					"round %d: 409 must carry the canonical code; body=%s", round, r.body)
			default:
				t.Fatalf("round %d: unexpected status %d (must be 201 or 409, never 5xx); body=%s",
					round, r.status, r.body)
			}
		}
		require.Equal(t, 1, created, "round %d: exactly one reverse must win", round)
		require.Equal(t, 1, conflicted, "round %d: the loser must get 409 ALREADY_REVERSED", round)
		require.Equal(t, 1, countReverseRows(t, wsID, origID),
			"round %d: exactly one compensating event row must exist", round)
	}
}
