package e2e

import (
	"encoding/json"
	"net/http"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// A published lens has to resolve to the set its author saved. The share
// URL is unauthenticated, so a resolver that reads the filter more
// loosely than the author's own list does not merely disagree — it puts
// tasks nobody selected on a public page.
//
// The fixture is chosen to make both loosenings visible at once: two
// states, so a resolver that keeps only the first drops half the set,
// and two non-adjacent priorities, so a resolver that brackets them into
// a range picks up everything in between.

// lensParityTask is one fixture row.
type lensParityTask struct {
	title      string
	priority   int32
	transition string // empty for tasks left in `open`
	inLens     bool
}

// createParityTask creates a public task and, when the fixture asks for
// it, walks it to the state the lens filters on.
func createParityTask(t *testing.T, tt *helpers.TestTenant, spec lensParityTask) string {
	t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId":  tt.ProjectPublicID,
		"title":      spec.title,
		"visibility": "public",
		"priority":   spec.priority,
	}, &out)
	require.NotEmpty(t, out.ID, "fixture: task create returned no id")
	if spec.transition != "" {
		doJSON(t, http.MethodPost, testServerURL+"/tasks/"+out.ID+"/transitions",
			tt.AccessToken, map[string]any{"transition": spec.transition}, nil)
	}
	return out.ID
}

// TestPublicLensResolvesTheSameSetAsItsAuthor publishes a lens and
// compares the shared page against the same filter applied through the
// authenticated task list, which is what the lens picker drives for its
// author.
func TestPublicLensResolvesTheSameSetAsItsAuthor(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// status ∈ {open, done}, priority ∈ {1, 4}.
	specs := []lensParityTask{
		{title: "parity open p1", priority: 1, inLens: true},
		{title: "parity done p4", priority: 4, transition: "complete", inLens: true},
		{title: "parity open p2", priority: 2, inLens: false},
		{title: "parity open p3", priority: 3, inLens: false},
		{title: "parity waiting p1", priority: 1, transition: "block", inLens: false},
		{title: "parity done p2", priority: 2, transition: "complete", inLens: false},
	}
	ids := make(map[string]lensParityTask, len(specs))
	expected := map[string]bool{}
	for _, spec := range specs {
		id := createParityTask(t, tt, spec)
		ids[id] = spec
		if spec.inLens {
			expected[id] = true
		}
	}

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/lenses"
	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"name":      "Parity board " + randomHex(4),
		"filter":    json.RawMessage(`{"status":{"values":["open","done"]},"priority":{"values":[1,4]}}`),
		"sort":      json.RawMessage(`[]`),
		"isDefault": false,
	}, &created)
	require.NotEmpty(t, created.ID)

	var published struct {
		PublicToken string `json:"publicToken"`
	}
	doJSON(t, http.MethodPost, base+"/"+created.ID+"/publish", tt.AccessToken, nil, &published)
	require.NotEmpty(t, published.PublicToken)

	var pub struct {
		Tasks []struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Status   string `json:"status"`
			Priority int32  `json:"priority"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/public/lenses/"+published.PublicToken, "", nil, &pub)

	shared := map[string]bool{}
	for _, task := range pub.Tasks {
		if _, ours := ids[task.ID]; !ours {
			// The tenant is fresh, but never assert on rows this test
			// did not create.
			continue
		}
		shared[task.ID] = true
	}

	require.Equal(t, sortedTitles(t, ids, expected), sortedTitles(t, ids, shared),
		"the share page must resolve the set the lens names")

	// The same filter through the author's own list. The lens picker
	// sends the states as a query parameter and narrows the priorities
	// client-side; mirroring that here means the comparison is against
	// what the author actually sees, not against a restatement of the
	// fix. The state list goes on the wire comma-separated, which is the
	// serialisation the published spec declares for it (explode: false)
	// and the one the generated SDK emits.
	var list struct {
		Tasks []struct {
			ID       string `json:"id"`
			Priority int32  `json:"priority"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks?projectId="+tt.ProjectPublicID+"&state=open,done&limit=200",
		tt.AccessToken, nil, &list)

	authorSet := map[string]bool{}
	for _, task := range list.Tasks {
		if _, ours := ids[task.ID]; !ours {
			continue
		}
		if task.Priority == 1 || task.Priority == 4 {
			authorSet[task.ID] = true
		}
	}
	require.NotEmpty(t, authorSet, "fixture: the author's own list produced nothing to compare against")
	require.Equal(t, sortedTitles(t, ids, authorSet), sortedTitles(t, ids, shared),
		"the share page and the author's list must resolve the same set")
}

// sortedTitles renders an id set as a sorted title list so a mismatch
// names the tasks instead of printing opaque uuids.
func sortedTitles(t *testing.T, ids map[string]lensParityTask, set map[string]bool) []string {
	t.Helper()
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, ids[id].title)
	}
	sort.Strings(out)
	return out
}
