package e2e

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// depRaceResult carries one worker's response back to the main
// goroutine so all assertions run outside the goroutines.
type depRaceResult struct {
	status int
	body   []byte
	err    error
}

// countEnabledEdge returns the number of enabled task_dependencies rows
// pointing from the task with fromPublicID to the task with toPublicID.
func countEnabledEdge(t *testing.T, fromPublicID, toPublicID string) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*)
		 FROM task_dependencies td
		 INNER JOIN tasks ft ON ft.id = td.from_task_id
		 INNER JOIN tasks tt ON tt.id = td.to_task_id
		 WHERE td.enabled = TRUE
		   AND ft.public_id = UUID_TO_BIN(?, 0)
		   AND tt.public_id = UUID_TO_BIN(?, 0)`,
		fromPublicID, toPublicID,
	).Scan(&n))
	return n
}

// TestConcurrentReverseDependencyPostsKeepGraphAcyclic races two
// simultaneous POSTs, A->B and B->A, against the same task pair. The
// cycle check runs inside the insert transaction behind a project row
// lock, so exactly one edge must commit and the loser must be rejected
// with WS.TASK.DEPENDENCY_CYCLE; without the lock both requests read a
// pre-insert edge set, both pass the check, and the pair commits a
// mutual-block cycle. Several rounds run on fresh task pairs to widen
// the interleaving coverage.
func TestConcurrentReverseDependencyPostsKeepGraphAcyclic(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	const rounds = 4
	for round := range rounds {
		a := createTask(t, tt.AccessToken, tt.ProjectPublicID, fmt.Sprintf("Dep race A %d", round))
		b := createTask(t, tt.AccessToken, tt.ProjectPublicID, fmt.Sprintf("Dep race B %d", round))

		results := make(chan depRaceResult, 2)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, pair := range [][2]string{{a, b}, {b, a}} {
			wg.Add(1)
			go func(from, to string) {
				defer wg.Done()
				<-start
				status, body, err := sendJSONStatus(http.MethodPost,
					testServerURL+"/tasks/"+from+"/dependencies", tt.AccessToken,
					map[string]any{"toTaskId": to, "kind": "blocks"})
				results <- depRaceResult{status: status, body: body, err: err}
			}(pair[0], pair[1])
		}
		close(start)
		wg.Wait()
		close(results)

		var successes, cycles int
		for r := range results {
			require.NoError(t, r.err)
			switch {
			case r.status >= 200 && r.status < 300:
				successes++
			case r.status == http.StatusUnprocessableEntity:
				require.Equal(t, "WS.TASK.DEPENDENCY_CYCLE", problemType(t, r.body),
					"round %d: loser must be rejected as a cycle: %s", round, string(r.body))
				cycles++
			default:
				t.Fatalf("round %d: unexpected status %d body=%s", round, r.status, string(r.body))
			}
		}
		require.Equal(t, 1, successes, "round %d: exactly one edge must commit", round)
		require.Equal(t, 1, cycles, "round %d: exactly one request must lose with a cycle error", round)

		// The committed graph must stay acyclic: exactly one direction
		// exists between the pair, never both.
		ab := countEnabledEdge(t, a, b)
		ba := countEnabledEdge(t, b, a)
		require.Equal(t, 1, ab+ba, "round %d: exactly one edge direction must exist (a->b=%d, b->a=%d)", round, ab, ba)
	}
}
