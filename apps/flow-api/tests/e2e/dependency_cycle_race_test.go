package e2e

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// createProjectForCycleRace creates a project in the tenant's workspace
// and returns its public id. The race needs four separate projects: the
// defect it pins is specifically that two writers whose endpoint
// projects do not overlap never met.
func createProjectForCycleRace(t *testing.T, accessToken, wsID, slug string) string {
	t.Helper()
	var prj struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/workspaces/"+wsID+"/projects", accessToken,
		map[string]any{"slug": slug, "name": "Cycle race " + slug}, &prj)
	require.NotEmpty(t, prj.ID)
	return prj.ID
}

// TestConcurrentCrossProjectEdgesCannotCloseCycle pins the DAG contract
// under concurrency across more projects than either writer touches.
//
// The shape is the smallest one that defeats a per-project lock. Four
// tasks, one per project, with T1->T2 and T3->T4 already drawn; two
// requests then arrive at once asking for T2->T3 and T4->T1. Each one
// spans two projects, and the two pairs are disjoint, so writers that
// lock only their own endpoints never block each other: both read an
// edge set missing the other's edge, both pass the cycle check, and both
// commit T1->T2->T3->T4->T1.
//
// A cycle here is not a visible failure. `dependency.all_done` is
// evaluated by a cycle-tolerant walk, so the constraint simply never
// becomes satisfiable while every screen keeps looking healthy, and the
// only way out is deleting an edge by hand.
//
// Both outcomes are legal per round — whichever request is serialized
// second is the one rejected — so the assertion is on the pair: exactly
// one accepted, exactly one WS.TASK.DEPENDENCY_CYCLE. Rounds are run
// because a single pair can be serialized by luck; each round builds
// fresh tasks so the rounds do not interfere.
func TestConcurrentCrossProjectEdgesCannotCloseCycle(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	projects := make([]string, 4)
	projects[0] = tt.ProjectPublicID
	for i := 1; i < 4; i++ {
		projects[i] = createProjectForCycleRace(t, tt.AccessToken, tt.WorkspacePublicID,
			fmt.Sprintf("%s-cyc%d", tt.ProjectSlug, i))
	}

	const rounds = 12
	for round := range rounds {
		tasks := make([]string, 4)
		for i := range tasks {
			tasks[i] = createTask(t, tt.AccessToken, projects[i],
				fmt.Sprintf("Cycle race r%d p%d", round, i))
		}

		// The two edges that already exist. Each sits inside one pair of
		// projects, leaving the pairs disjoint.
		doJSON(t, http.MethodPost, testServerURL+"/tasks/"+tasks[0]+"/dependencies", tt.AccessToken,
			map[string]any{"toTaskId": tasks[1], "kind": "blocks"}, nil)
		doJSON(t, http.MethodPost, testServerURL+"/tasks/"+tasks[2]+"/dependencies", tt.AccessToken,
			map[string]any{"toTaskId": tasks[3], "kind": "blocks"}, nil)

		type outcome struct {
			status int
			body   []byte
		}
		results := make([]outcome, 2)
		closing := [2][2]string{
			{tasks[1], tasks[2]}, // T2 -> T3
			{tasks[3], tasks[0]}, // T4 -> T1
		}

		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range closing {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				status, body, err := postJSONForConcurrency(t,
					testServerURL+"/tasks/"+closing[i][0]+"/dependencies", tt.AccessToken,
					map[string]any{"toTaskId": closing[i][1], "kind": "blocks"})
				if err != nil {
					results[i] = outcome{status: -1, body: []byte(err.Error())}
					return
				}
				results[i] = outcome{status: status, body: body}
			}(i)
		}
		close(start)
		wg.Wait()

		accepted, rejected := 0, 0
		for _, r := range results {
			switch {
			case r.status >= 200 && r.status < 300:
				accepted++
			case r.status == http.StatusUnprocessableEntity:
				require.Equalf(t, "WS.TASK.DEPENDENCY_CYCLE", problemType(t, r.body),
					"round %d: the rejected edge must be rejected as a cycle, got %s", round, string(r.body))
				rejected++
			default:
				t.Fatalf("round %d: unexpected response %d: %s", round, r.status, string(r.body))
			}
		}
		require.Equalf(t, 1, accepted,
			"round %d: %d of the two edges were accepted; both closing the loop leaves "+
				"T1->T2->T3->T4->T1 in the graph, which no single request could see and "+
				"nothing later repairs", round, accepted)
		require.Equalf(t, 1, rejected, "round %d: expected exactly one rejection", round)

		// The graph the round leaves behind must still be a DAG: adding
		// the edge that lost is refused on its own, with no concurrency
		// involved.
		loser := 0
		if results[0].status >= 200 && results[0].status < 300 {
			loser = 1
		}
		status, raw := doJSONStatus(t, http.MethodPost,
			testServerURL+"/tasks/"+closing[loser][0]+"/dependencies", tt.AccessToken,
			map[string]any{"toTaskId": closing[loser][1], "kind": "blocks"})
		require.Equalf(t, http.StatusUnprocessableEntity, status,
			"round %d: the losing edge must still close a cycle when retried alone: %s",
			round, string(raw))
		require.Equal(t, "WS.TASK.DEPENDENCY_CYCLE", problemType(t, raw))
	}
}
