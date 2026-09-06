// The public lens body is served to anyone holding the share link, with
// no reader identity behind it. A lens definition names things the link
// holder was never given — user public ids under the assignee key, and
// whatever free text its author saved under the search key — and so does
// the name of the person a task is assigned to. The question this pins
// is what the anonymous body carries, not what the mapper was written to
// copy.
//
// The assertions run against the whole serialised response and against
// its key set at every depth, because a field checked by name still
// passes when the same content reappears somewhere else in the body, and
// a name-checked field that no longer exists on the Go type decodes to
// the zero value whether the endpoint withholds it or not.
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

// jsonKeysAtEveryDepth collects every object key in a decoded JSON
// value into out, descending through arrays and sub-objects. The body
// is read this way so that a person-naming field reintroduced one level
// down — on a task, on a group heading — is caught by the same
// assertion as one reintroduced at the top.
func jsonKeysAtEveryDepth(v any, out map[string]struct{}) {
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			out[k] = struct{}{}
			jsonKeysAtEveryDepth(sub, out)
		}
	case []any:
		for _, sub := range t {
			jsonKeysAtEveryDepth(sub, out)
		}
	}
}

// TestPublicLensBodyOmitsTheLensDefinition publishes a lens whose filter
// names an assignee by user public id and carries a saved free-text
// search, then reads the share URL with no bearer.
//
// The search needle is stored in upper case and matched against a lower
// case title. The lens reader lowercases the needle before it reaches
// SQL, so the filter still resolves the task, while the needle as the
// author wrote it appears nowhere in a body that renders titles — which
// makes a whole-body search for it a test of the definition alone.
//
// The assignee carries a display name of the same shape: unique to this
// tenant, and absent from every field the share page legitimately
// renders, so finding it anywhere in the body can only mean the person
// behind the task was named.
func TestPublicLensBodyOmitsTheLensDefinition(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	searchNeedle := "QUARTERCLOSE" + strings.ToUpper(randomHex(4))
	taskTitle := "review " + strings.ToLower(searchNeedle) + " with finance"
	assigneeName := "Marisol Vantreight " + randomHex(4)
	dueOn := "2026-06-10"

	var profile struct {
		DisplayName string `json:"displayName"`
	}
	doJSON(t, http.MethodPatch, testServerURL+"/me", tt.AccessToken,
		map[string]any{"displayName": assigneeName}, &profile)
	require.Equal(t, assigneeName, profile.DisplayName, "fixture: display name did not take")

	// The creator is auto-attached as the sole assignee, so the assignee
	// key below names a user who really holds this task.
	var task struct {
		ID    string `json:"id"`
		DueOn string `json:"dueOn"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId":  tt.ProjectPublicID,
		"title":      taskTitle,
		"visibility": "public",
		"priority":   3,
		"dueOn":      dueOn,
	}, &task)
	require.NotEmpty(t, task.ID, "fixture: task create returned no id")
	require.Equal(t, dueOn, task.DueOn, "fixture: task create did not keep the due date")

	// The name is reachable through the authenticated surface for this
	// exact task, which is what makes its absence below a property of the
	// anonymous body rather than of a user who has no name to leak.
	var actors struct {
		Actors []struct {
			DisplayName string `json:"displayName"`
			Role        string `json:"role"`
		} `json:"actors"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+task.ID+"/actors", tt.AccessToken, nil, &actors)
	require.Len(t, actors.Actors, 1, "fixture: the task must have exactly its creator as assignee")
	require.Equal(t, "assignee", actors.Actors[0].Role)
	require.Equal(t, assigneeName, actors.Actors[0].DisplayName,
		"fixture: the assignee must hold the name the negation below looks for")

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
	assert.NotContains(t, string(publicBody), assigneeName,
		"the share body must not name the person a task is assigned to")

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

	// Below the top level the rule is about who the body may name, so the
	// key set is read at every depth and matched on shape rather than on
	// one exact spelling: an anonymous reader is served no person's name
	// under any key, whatever it is called.
	var anyBody any
	require.NoError(t, json.Unmarshal(publicBody, &anyBody), "body=%s", string(publicBody))
	nested := map[string]struct{}{}
	jsonKeysAtEveryDepth(anyBody, nested)
	var naming []string
	for k := range nested {
		if strings.Contains(strings.ToLower(k), "displayname") {
			naming = append(naming, k)
		}
	}
	sort.Strings(naming)
	assert.Empty(t, naming,
		"the share body must carry no display-name key at any depth; body=%s",
		string(publicBody))

	// The positive side: the page's own fields are present and correct,
	// and the filter still selected the task it names, so none of the
	// above can pass on a handler that answers with nothing.
	var pub struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Tasks       []struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Status   string `json:"status"`
			Priority int32  `json:"priority"`
			DueOn    string `json:"dueOn"`
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
	assert.Equal(t, dueOn, pub.Tasks[0].DueOn)
}
