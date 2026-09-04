package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// A share URL has no reader identity, so the only safe answer to "which
// tasks does this lens name?" when the stored filter cannot be rendered
// is "none". A reader that instead drops the filter it could not read
// answers with every public task in scope, to anyone holding the link.
//
// The lens endpoints now refuse such a filter on the way in, so these
// cases put the blob in the column directly: what they pin is a row that
// predates the refusal, which is the situation the reader exists for.
// They go through the unauthenticated endpoint rather than the parser so
// the exposure itself is what is pinned, and they are paired with a
// filter the reader does implement so they cannot pass on a handler that
// has simply stopped returning tasks.

// publishLens creates a lens carrying the given filter, publishes it, and
// returns the lens id and the share token.
func publishLens(t *testing.T, tt *helpers.TestTenant, filter any) (string, string) {
	t.Helper()
	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/lenses"
	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"name":      "Share " + randomHex(6),
		"filter":    filter,
		"sort":      json.RawMessage(`[]`),
		"isDefault": false,
	}, &created)
	require.NotEmpty(t, created.ID, "fixture: lens create returned no id")

	var published struct {
		PublicToken string `json:"publicToken"`
	}
	doJSON(t, http.MethodPost, base+"/"+created.ID+"/publish", tt.AccessToken, nil, &published)
	require.NotEmpty(t, published.PublicToken, "fixture: publish returned no token")
	return created.ID, published.PublicToken
}

// storeRawLensFilter writes a filter blob straight into lens_json,
// standing in for a row stored before the write-time refusal existed.
func storeRawLensFilter(t *testing.T, lensID, filter string) {
	t.Helper()
	pub, err := uuid.Parse(lensID)
	require.NoError(t, err, "fixture: lens id is not a uuid")
	res, err := testDB.ExecContext(context.Background(),
		`UPDATE lenses SET lens_json = JSON_OBJECT('filter', CAST(? AS JSON), 'sort', JSON_ARRAY()) WHERE public_id = ?`,
		filter, pub[:])
	require.NoError(t, err, "fixture: storing the filter blob")
	affected, err := res.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), affected, "fixture: the lens row must have been updated")
}

// sharedTaskIDs fetches a share page without a bearer and returns the
// task ids it exposed.
func sharedTaskIDs(t *testing.T, token string) map[string]string {
	t.Helper()
	var pub struct {
		Tasks []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/public/lenses/"+token, "", nil, &pub)
	out := make(map[string]string, len(pub.Tasks))
	for _, task := range pub.Tasks {
		out[task.ID] = task.Title
	}
	return out
}

func TestPublicLensServesNothingWhenItsFilterCannotBeRead(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId":  tt.ProjectPublicID,
		"title":      "unshared by an unreadable lens " + randomHex(4),
		"visibility": "public",
	}, &task)
	require.NotEmpty(t, task.ID, "fixture: task create returned no id")

	for name, stored := range map[string]string{
		// The value is a string where the reader expects an operator
		// map, so the blob does not decode.
		"filter that does not decode": `{"state":"open"}`,
		// A key the lens grammar defines and this reader does not
		// implement.
		"filter naming an unimplemented key": `{"labels":{"in":["urgent"]}}`,
		// An operator the grammar defines and this reader does not
		// implement, on a key it does.
		"filter naming an unimplemented operator": `{"status":{"neq":"done"}}`,
	} {
		lensID, token := publishLens(t, tt, json.RawMessage(`{"status":{"values":["open"]}}`))

		// The lens serves the task while its filter is readable, so the
		// emptiness below is the stored blob's doing.
		require.Contains(t, sharedTaskIDs(t, token), task.ID,
			"%s: fixture must start from a share page that serves the task", name)

		storeRawLensFilter(t, lensID, stored)
		shared := sharedTaskIDs(t, token)
		require.NotContains(t, shared, task.ID,
			"%s: the share page must publish nothing rather than everything", name)
		require.Empty(t, shared,
			"%s: the share page must publish nothing rather than everything", name)
	}
}

// The write-time counterpart: the endpoints refuse to store a filter the
// share resolver could not render, naming the path that stopped it, so a
// row like the ones above cannot be created through the API at all.
func TestLensWriteRefusesAFilterTheResolverCannotRender(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/lenses"

	for name, tc := range map[string]struct {
		filter json.RawMessage
		field  string
	}{
		"filter that does not decode":             {json.RawMessage(`{"state":"open"}`), "filter"},
		"filter naming an unimplemented key":      {json.RawMessage(`{"labels":{"in":["urgent"]}}`), "filter.labels"},
		"filter naming an unimplemented operator": {json.RawMessage(`{"status":{"neq":"done"}}`), "filter.status.neq"},
		"filter naming a state no task can carry": {json.RawMessage(`{"status":{"values":["shipped"]}}`), "filter.status"},
	} {
		status, body := doJSONStatus(t, http.MethodPost, base, tt.AccessToken, map[string]any{
			"name":      "Refused " + randomHex(6),
			"filter":    tc.filter,
			"sort":      json.RawMessage(`[]`),
			"isDefault": false,
		})
		require.Equal(t, http.StatusUnprocessableEntity, status,
			"%s: creating a lens with it must be refused; body %s", name, body)
		require.Contains(t, string(body), tc.field,
			"%s: the refusal must name what stopped the reading", name)
	}

	// A patch replacing the filter is checked the same way, and the lens
	// keeps the filter it had.
	const good = `{"status":{"values":["open"]}}`
	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"name":      "Patched " + randomHex(6),
		"filter":    json.RawMessage(good),
		"sort":      json.RawMessage(`[]`),
		"isDefault": false,
	}, &created)
	require.NotEmpty(t, created.ID)

	status, body := doJSONStatus(t, http.MethodPatch, base+"/"+created.ID, tt.AccessToken,
		map[string]any{"filter": json.RawMessage(`{"labels":{"in":["urgent"]}}`)})
	require.Equal(t, http.StatusUnprocessableEntity, status,
		"a patch carrying an unrenderable filter must be refused; body %s", body)

	var after struct {
		Filter json.RawMessage `json:"filter"`
	}
	doJSON(t, http.MethodGet, base+"/"+created.ID, tt.AccessToken, nil, &after)
	require.JSONEq(t, good, string(after.Filter),
		"the refused patch must leave the stored filter alone")
}
