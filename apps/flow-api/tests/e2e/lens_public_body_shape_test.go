// The public lens body is served to anyone holding the share link, with
// no reader identity behind it. A lens definition names things the link
// holder was never given — user public ids under the assignee key, and
// whatever free text its author saved under the search key — so the
// question this pins is what the anonymous body carries, not what the
// mapper was written to copy.
//
// The assertions run against the whole serialised response and against
// its top-level key set, because a field checked by name still passes
// when the same content reappears somewhere else in the body.
package e2e

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPublicLensBodyOmitsTheLensDefinition publishes a lens whose filter
// names an assignee by user public id and carries a saved free-text
// search, then reads the share URL with no bearer.
//
// The search needle is stored in upper case and matched against a lower
// case title. The lens reader lowercases the needle before it reaches
// SQL, so the filter still resolves the task, while the needle as the
// author wrote it appears nowhere in a body that renders titles — which
// makes a whole-body search for it a test of the definition alone.
func TestPublicLensBodyOmitsTheLensDefinition(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	searchNeedle := "QUARTERCLOSE" + strings.ToUpper(randomHex(4))
	taskTitle := "review " + strings.ToLower(searchNeedle) + " with finance"

	// The creator is auto-attached as the sole assignee, so the assignee
	// key below names a user who really holds this task.
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId":  tt.ProjectPublicID,
		"title":      taskTitle,
		"visibility": "public",
		"priority":   3,
	}, &task)
	require.NotEmpty(t, task.ID, "fixture: task create returned no id")

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/lenses"
	lensName := "Finance board " + randomHex(4)
	lensDescription := "What finance is closing this quarter"
	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"projectId":   tt.ProjectPublicID,
		"name":        lensName,
		"description": lensDescription,
		"filter": map[string]any{
			"assignee": map[string]any{"value": tt.UserPublicID},
			"search":   map[string]any{"value": searchNeedle},
		},
		"sort":      json.RawMessage(`[]`),
		"groupBy":   "status",
		"isDefault": false,
	}, &created)
	require.NotEmpty(t, created.ID, "fixture: lens create returned no id")

	var published struct {
		PublicToken string `json:"publicToken"`
	}
	doJSON(t, http.MethodPost, base+"/"+created.ID+"/publish", tt.AccessToken, nil, &published)
	require.NotEmpty(t, published.PublicToken, "fixture: publish returned no token")

	// The definition is discoverable to a member: the workspace-scoped
	// read hands it back, which is what makes the absences below a
	// property of the anonymous surface rather than of the fixture.
	status, authBody := doJSONStatus(t, http.MethodGet, base+"/"+created.ID, tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "lens get must render; body=%s", string(authBody))
	require.Contains(t, string(authBody), tt.UserPublicID,
		"fixture: the saved filter must name the assignee for the negation below to mean anything")
	require.Contains(t, string(authBody), searchNeedle,
		"fixture: the saved filter must carry the search string for the negation below to mean anything")

	status, publicBody := doJSONStatus(t, http.MethodGet,
		testServerURL+"/public/lenses/"+published.PublicToken, "", nil)
	require.Equal(t, http.StatusOK, status, "share page must render; body=%s", string(publicBody))

	assert.NotContains(t, string(publicBody), tt.UserPublicID,
		"the share body must not name a workspace user by public id")
	assert.NotContains(t, string(publicBody), searchNeedle,
		"the share body must not carry the free text the lens author saved")

	// The top-level key set is pinned against an allowlist rather than a
	// deny list, so a definition served back under a name nobody thought
	// to forbid fails here too. `$schema` is the self-description link
	// the framework attaches to every response body.
	allowed := map[string]struct{}{
		"$schema": {}, "id": {}, "name": {}, "description": {}, "tasks": {},
	}
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(publicBody, &fields), "body=%s", string(publicBody))
	var unexpected []string
	for k := range fields {
		if _, ok := allowed[k]; !ok {
			unexpected = append(unexpected, k)
		}
	}
	sort.Strings(unexpected)
	assert.Empty(t, unexpected,
		"the share body carries the heading and the resolved tasks, nothing else; body=%s",
		string(publicBody))

	// The positive side: the page's own fields are present and correct,
	// and the filter still selected the task it names, so none of the
	// above can pass on a handler that answers with nothing.
	var pub struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Tasks       []struct {
			ID                  string `json:"id"`
			Title               string `json:"title"`
			Status              string `json:"status"`
			Priority            int32  `json:"priority"`
			AssigneeDisplayName string `json:"assigneeDisplayName"`
		} `json:"tasks"`
	}
	require.NoError(t, json.Unmarshal(publicBody, &pub), "body=%s", string(publicBody))
	assert.Equal(t, created.ID, pub.ID)
	assert.Equal(t, lensName, pub.Name)
	assert.Equal(t, lensDescription, pub.Description)
	require.Len(t, pub.Tasks, 1, "the assignee+search filter must still resolve the task it names; body=%s",
		string(publicBody))
	assert.Equal(t, task.ID, pub.Tasks[0].ID)
	assert.Equal(t, taskTitle, pub.Tasks[0].Title)
	assert.Equal(t, "open", pub.Tasks[0].Status)
	assert.Equal(t, int32(3), pub.Tasks[0].Priority)
	assert.Equal(t, tt.DisplayName, pub.Tasks[0].AssigneeDisplayName)
}
