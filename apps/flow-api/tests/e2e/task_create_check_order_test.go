package e2e

import (
	"net/http"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/stretchr/testify/require"
)

// TestTaskCreateRefusesBlankTitleBeforeResolvingProject pins the order in
// which POST /tasks applies its two independent refusals.
//
// Create trims and refuses a blank title before it parses or looks up the
// project id, so a request that is wrong in both ways is answered
// VALIDATION.BODY.FIELD_INVALID rather than WS.PROJECT.NOT_FOUND. That is a
// contract, not an accident of how the function happens to be laid out: the
// answer a caller gets for a doubly-invalid request must not depend on which
// of the two checks a later refactor happens to place first.
//
// The ordering is easy to lose. Once the title check is a call rather than
// inline statements, the natural place to put it is next to the other
// body-shape validation below the project lookup — and moving it there
// silently flips the answer to the not-found, with every single-fault test
// still green. If you moved the title check and landed here, this test is
// what you broke: put the refusal back above the project lookup, or change
// the contract deliberately.
//
// One case cannot state a precedence, so all three run together: the
// doubly-invalid request fixes the winner, and the two single-fault requests
// prove each check really does answer its own code when it is the only thing
// wrong. Without them the first case could pass because the endpoint refuses
// everything with the same code.
func TestTaskCreateRefusesBlankTitleBeforeResolvingProject(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Well-formed UUID that belongs to no project row, so the lookup fails
	// on ErrNoRows rather than on parsing.
	missingProjectID := types.New().UUID().String()

	// Both faults at once: the title check wins.
	status, body := doJSONStatus(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": missingProjectID,
		"title":     "   ",
	})
	require.Equal(t, http.StatusUnprocessableEntity, status,
		"a blank title must be refused before the project lookup, body=%s", string(body))
	require.Equal(t, "VALIDATION.BODY.FIELD_INVALID", decodeErrorCode(t, body),
		"a blank title outranks an unresolvable project, body=%s", string(body))

	// Only the project is wrong: the lookup gets to answer.
	status, body = doJSONStatus(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": missingProjectID,
		"title":     "a title the handler accepts",
	})
	require.Equal(t, http.StatusNotFound, status,
		"an unresolvable project must be a not-found, body=%s", string(body))
	require.Equal(t, "WS.PROJECT.NOT_FOUND", decodeErrorCode(t, body),
		"an unresolvable project must surface WS.PROJECT.NOT_FOUND, body=%s", string(body))

	// Only the title is wrong: the same code as the doubly-invalid case,
	// which is what makes the first assertion mean the title check ran.
	status, body = doJSONStatus(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "   ",
	})
	require.Equal(t, http.StatusUnprocessableEntity, status,
		"a blank title must be refused, body=%s", string(body))
	require.Equal(t, "VALIDATION.BODY.FIELD_INVALID", decodeErrorCode(t, body),
		"a blank title must surface VALIDATION.BODY.FIELD_INVALID, body=%s", string(body))
}
