package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// A saved lens may only carry what some surface reads. A field stored
// and honoured nowhere answers its author with a lens that behaves as
// though they had saved nothing, and a value no task can carry answers
// them with a lens that saves cleanly and matches nothing — in both
// cases without an error, and in both cases at a moment when they could
// still have fixed it.

// lensErrorField pulls the offending field path out of a refusal body.
// The path travels as an RFC 9457 extension member, so a refusal that
// names nothing is as visible here as one that names the wrong thing.
func lensErrorField(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Extensions struct {
			Field string `json:"field"`
		} `json:"extensions"`
	}
	require.NoError(t, json.Unmarshal(body, &env), "refusal body is not JSON: %s", string(body))
	return env.Extensions.Field
}

// createLensExpectingRefusal posts a lens body and returns the status
// and the field path the refusal named.
func createLensExpectingRefusal(t *testing.T, tt *helpers.TestTenant, body map[string]any) (int, string, []byte) {
	t.Helper()
	body["name"] = "Refused " + randomHex(6)
	body["isDefault"] = false
	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/lenses", tt.AccessToken, body)
	if status >= 200 && status < 300 {
		return status, "", raw
	}
	return status, lensErrorField(t, raw), raw
}

// storeRawLensSort writes an ordering straight into lens_json, standing
// in for a lens stored before the write-time refusal existed.
func storeRawLensSort(t *testing.T, lensID, sort string) {
	t.Helper()
	pub, err := uuid.Parse(lensID)
	require.NoError(t, err, "fixture: lens id is not a uuid")
	res, err := testDB.ExecContext(context.Background(),
		`UPDATE lenses SET lens_json = JSON_SET(lens_json, '$.sort', CAST(? AS JSON)) WHERE public_id = ?`,
		sort, pub[:])
	require.NoError(t, err, "fixture: storing the ordering")
	affected, err := res.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), affected, "fixture: the lens row must have been updated")
}

// TestLensWriteRefusesADefinitionNothingApplies pins the refusal of the
// two definition fields no surface reads. The share page is capped and
// unpaginated and the authenticated list takes no ordering from a lens,
// so an ordering applied on the share page alone would publish a
// different leading set than its author sees — which is the disagreement
// the shared order exists to prevent.
func TestLensWriteRefusesADefinitionNothingApplies(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/lenses"
	const filter = `{"status":{"values":["open"]}}`

	for name, tc := range map[string]struct {
		body  map[string]any
		field string
	}{
		"an ordering": {
			map[string]any{
				"filter": json.RawMessage(filter),
				"sort":   json.RawMessage(`[{"field":"priority","dir":"desc"}]`),
			},
			"sort",
		},
		"an ordering that is not even a list": {
			map[string]any{
				"filter": json.RawMessage(filter),
				"sort":   json.RawMessage(`{"field":"priority"}`),
			},
			"sort",
		},
		"a grouping": {
			map[string]any{
				"filter":  json.RawMessage(filter),
				"sort":    json.RawMessage(`[]`),
				"groupBy": "status",
			},
			"groupBy",
		},
	} {
		status, field, raw := createLensExpectingRefusal(t, tt, tc.body)
		requireDenied(t, status, raw, http.StatusUnprocessableEntity,
			"VALIDATION.BODY.FIELD_INVALID", name)
		require.Equalf(t, tc.field, field,
			"%s: the refusal must name the field it refused; body %s", name, string(raw))
	}

	// The counterweight: naming no ordering at all is what the clients
	// send, and it still stores.
	for name, sort := range map[string]any{
		"an empty ordering": json.RawMessage(`[]`),
		"a null ordering":   nil,
	} {
		var created struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{
			"name":      "Accepted " + randomHex(6),
			"filter":    json.RawMessage(filter),
			"sort":      sort,
			"isDefault": false,
		}, &created)
		require.NotEmptyf(t, created.ID, "%s: must still be accepted", name)
	}

	// A patch is a write of the field it carries, and only of that field.
	var lens struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"name":      "Patched " + randomHex(6),
		"filter":    json.RawMessage(filter),
		"sort":      json.RawMessage(`[]`),
		"isDefault": false,
	}, &lens)
	require.NotEmpty(t, lens.ID)

	status, raw := doJSONStatus(t, http.MethodPatch, base+"/"+lens.ID, tt.AccessToken,
		map[string]any{"sort": json.RawMessage(`[{"field":"due_on","dir":"asc"}]`)})
	requireDenied(t, status, raw, http.StatusUnprocessableEntity,
		"VALIDATION.BODY.FIELD_INVALID", "a patch naming an ordering")
	require.Equal(t, "sort", lensErrorField(t, raw),
		"the refusal must name the field it refused")
}

// TestPublicLensWithAStoredOrderingStillRenders covers the lens the
// refusal above cannot reach: one written before it existed. Its saved
// ordering is not applied — nothing applies one — but it is also not a
// reason to stop serving the lens or to start refusing edits to the rest
// of it.
func TestPublicLensWithAStoredOrderingStillRenders(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	title := "served under a stored ordering " + randomHex(4)
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId":  tt.ProjectPublicID,
		"title":      title,
		"visibility": "public",
	}, &task)
	require.NotEmpty(t, task.ID, "fixture: task create returned no id")

	lensID, token := publishLens(t, tt, json.RawMessage(`{"status":{"values":["open"]}}`))
	require.Equal(t, []string{title}, sharedTaskTitles(t, token),
		"fixture: the share page must serve the task before the ordering is stored")

	const stored = `[{"field":"priority","dir":"desc"}]`
	storeRawLensSort(t, lensID, stored)
	require.Equal(t, []string{title}, sharedTaskTitles(t, token),
		"a lens carrying a stored ordering must keep rendering")

	// Editing the name is not a write of the ordering, so the patch is
	// answered rather than refused, and what it did not name survives it.
	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/lenses"
	renamed := "Renamed " + randomHex(6)
	status, raw := doJSONStatus(t, http.MethodPatch, base+"/"+lensID, tt.AccessToken,
		map[string]any{"name": renamed})
	require.Equalf(t, http.StatusOK, status,
		"a patch that does not name the ordering must not be refused for carrying one; body %s", string(raw))

	var after struct {
		Name string          `json:"name"`
		Sort json.RawMessage `json:"sort"`
	}
	doJSON(t, http.MethodGet, base+"/"+lensID, tt.AccessToken, nil, &after)
	require.Equal(t, renamed, after.Name)
	require.JSONEq(t, stored, string(after.Sort),
		"the stored ordering must survive a patch that did not name it")
}

// TestLensPriorityRangeMatchesTheAuthenticatedList pins the two surfaces
// to one answer about which priorities exist. A lens naming only
// priorities no task can carry is refused where its author can still fix
// it; one that also names a real priority keeps that priority and drops
// the rest, which is what the authenticated list does with the same
// input.
func TestLensPriorityRangeMatchesTheAuthenticatedList(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	for name, filter := range map[string]string{
		"a set of priorities outside the column's range": `{"priority":{"values":[-1,99]}}`,
		"a single priority outside it":                   `{"priority":{"eq":9}}`,
		"a negative priority":                            `{"priority":{"in":[-3]}}`,
	} {
		status, field, raw := createLensExpectingRefusal(t, tt, map[string]any{
			"filter": json.RawMessage(filter),
			"sort":   json.RawMessage(`[]`),
		})
		requireDenied(t, status, raw, http.StatusUnprocessableEntity,
			"VALIDATION.BODY.FIELD_INVALID", name)
		require.Equalf(t, "filter.priority", field,
			"%s: the refusal must name the key it refused; body %s", name, string(raw))
	}

	// A set that names one real priority keeps it. The out-of-range
	// member is dropped exactly as the authenticated list drops it, so
	// the two surfaces select the same rows rather than disagreeing about
	// whether the lens was valid at all.
	kept := "priority one " + randomHex(4)
	other := "priority two " + randomHex(4)
	createOrderedTasks(t, tt, []orderedLensTask{
		{title: kept, priority: 1, sortWeight: 10},
		{title: other, priority: 2, sortWeight: 20},
	})

	_, token := publishLens(t, tt, json.RawMessage(`{"priority":{"values":[1,99]}}`))

	var list struct {
		Tasks []struct {
			Title string `json:"title"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet,
		fmt.Sprintf("%s/tasks?projectId=%s&priority=1&limit=200", testServerURL, tt.ProjectPublicID),
		tt.AccessToken, nil, &list)
	authorTitles := make([]string, 0, len(list.Tasks))
	for _, task := range list.Tasks {
		authorTitles = append(authorTitles, task.Title)
	}

	require.Equal(t, []string{kept}, authorTitles,
		"fixture: the author's list must narrow to the priority the lens keeps")
	require.Equal(t, []string{kept}, sharedTaskTitles(t, token),
		"the share page must publish the rows the surviving priority names, and only those")
	require.Equal(t, []string{kept, other}, authorTaskTitles(t, tt, 200),
		"fixture: the unfiltered list must hold both rows, so the narrowing above is the filter's doing")
}
