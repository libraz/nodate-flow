package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBoardDropTransitionsMatchBackend verifies that every transition
// the frontend transitionForDrop function resolves is accepted by the
// backend POST /tasks/{id}/transitions endpoint. This is a cross-check
// between the client state machine table and the server truth table.
//
// Each sub-test creates a task, walks it to the "from" state, then
// applies the transition that the frontend would send on a board drop.
func TestBoardDropTransitionsMatchBackend(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	// dropCase mirrors a DropResolution from the frontend transitionForDrop.
	type dropCase struct {
		name       string
		setup      []string // transitions to reach the "from" state
		transition string   // the transition the frontend sends
		expect     string   // expected derivedState after
	}

	cases := []dropCase{
		// Direct transitions
		{name: "open→waiting (start)", setup: nil, transition: "start", expect: "waiting"},
		{name: "open→done (complete)", setup: nil, transition: "complete", expect: "done"},
		{name: "open→cancelled (cancel)", setup: nil, transition: "cancel", expect: "cancelled"},
		{name: "waiting→review (submit)", setup: []string{"start"}, transition: "submit", expect: "review"},
		{name: "waiting→open (block)", setup: []string{"start"}, transition: "block", expect: "open"},
		{name: "waiting→cancelled (cancel)", setup: []string{"start"}, transition: "cancel", expect: "cancelled"},
		{name: "review→done (complete)", setup: []string{"start", "submit"}, transition: "complete", expect: "done"},
		{name: "review→cancelled (cancel)", setup: []string{"start", "submit"}, transition: "cancel", expect: "cancelled"},
		{name: "cancelled→open (reopen)", setup: []string{"cancel"}, transition: "reopen", expect: "open"},

		// Lenient transitions (frontend resolves to the closest legal verb)
		{name: "done→waiting (reopen)", setup: []string{"complete"}, transition: "reopen", expect: "waiting"},
		{name: "review→waiting (reopen)", setup: []string{"start", "submit"}, transition: "reopen", expect: "waiting"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tt := newTenant(t)

			var task struct {
				ID string `json:"id"`
			}
			doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
				map[string]any{"projectId": tt.ProjectPublicID, "title": "Board drop: " + tc.name}, &task)

			// Walk to the "from" state.
			for _, tr := range tc.setup {
				doJSON(t, http.MethodPost,
					testServerURL+"/tasks/"+task.ID+"/transitions",
					tt.AccessToken, map[string]any{"transition": tr}, nil)
			}

			// Apply the drop transition.
			var result struct {
				DerivedState string `json:"derivedState"`
			}
			doJSON(t, http.MethodPost,
				testServerURL+"/tasks/"+task.ID+"/transitions",
				tt.AccessToken, map[string]any{"transition": tc.transition}, &result)
			require.Equal(t, tc.expect, result.DerivedState,
				"backend must accept and land in the expected state")
		})
	}
}

// TestBoardDropIllegalTransitionsRejected verifies that transitions the
// frontend considers illegal (transitionForDrop returns null) are also
// rejected by the backend.
func TestBoardDropIllegalTransitionsRejected(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	type illegalCase struct {
		name       string
		setup      []string
		transition string
	}

	cases := []illegalCase{
		// open → review: no direct transition
		{name: "open→review (submit)", setup: nil, transition: "submit"},
		// done → cancelled: needs two steps
		{name: "done→cancel", setup: []string{"complete"}, transition: "cancel"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tt := newTenant(t)

			var task struct {
				ID string `json:"id"`
			}
			doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
				map[string]any{"projectId": tt.ProjectPublicID, "title": "Illegal: " + tc.name}, &task)

			for _, tr := range tc.setup {
				doJSON(t, http.MethodPost,
					testServerURL+"/tasks/"+task.ID+"/transitions",
					tt.AccessToken, map[string]any{"transition": tr}, nil)
			}

			status, _ := doJSONStatus(t, http.MethodPost,
				testServerURL+"/tasks/"+task.ID+"/transitions",
				tt.AccessToken, map[string]any{"transition": tc.transition})
			require.GreaterOrEqual(t, status, 400,
				"backend must reject illegal transition")
			require.Less(t, status, 500,
				"rejection must be a client error, not 5xx")
		})
	}
}
