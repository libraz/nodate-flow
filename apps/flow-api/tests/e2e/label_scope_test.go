package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLabelScopeAndParentRules holds that a project-scoped label joins
// only tasks of its project, that a label parent stays in the label's
// scope and never forms a cycle, and that attaching twice reports the
// attachment rather than a name clash.
func TestLabelScopeAndParentRules(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)
	wsURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID

	var other struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, wsURL+"/projects", tt.AccessToken,
		map[string]any{"slug": "other-" + tt.ProjectSlug, "name": "Other project"}, &other)
	require.NotEmpty(t, other.ID)

	createLabel := func(name, project, parent string) (int, []byte) {
		body := map[string]any{"name": name}
		if project != "" {
			body["projectId"] = project
		}
		if parent != "" {
			body["parentLabelId"] = parent
		}
		return doJSONStatus(t, http.MethodPost, wsURL+"/labels", tt.AccessToken, body)
	}
	mustLabel := func(name, project, parent string) string {
		status, raw := createLabel(name, project, parent)
		require.Equal(t, http.StatusOK, status, string(raw))
		var out struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(raw, &out))
		return out.ID
	}

	scoped := mustLabel("scoped", other.ID, "")
	wide := mustLabel("wide", "", "")
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "Scope target")

	status, raw := doJSONStatus(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/labels",
		tt.AccessToken, map[string]any{"labelId": scoped})
	requireDenied(t, status, raw, http.StatusBadRequest, "WS.LABEL.SCOPE_MISMATCH", "cross-project attach")

	status, raw = doJSONStatus(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/labels",
		tt.AccessToken, map[string]any{"labelId": wide})
	require.Equal(t, http.StatusOK, status, string(raw))
	status, raw = doJSONStatus(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/labels",
		tt.AccessToken, map[string]any{"labelId": wide})
	requireDenied(t, status, raw, http.StatusConflict, "WS.LABEL.ALREADY_ATTACHED", "double attach")

	status, raw = createLabel("scoped child", other.ID, wide)
	requireDenied(t, status, raw, http.StatusBadRequest, "WS.LABEL.SCOPE_MISMATCH", "cross-scope parent")

	parent := mustLabel("parent", "", "")
	child := mustLabel("child", "", parent)
	for _, c := range []struct{ id, parent string }{{parent, parent}, {parent, child}} {
		status, raw = doJSONStatus(t, http.MethodPatch, wsURL+"/labels/"+c.id,
			tt.AccessToken, map[string]any{"parentLabelId": c.parent})
		requireDenied(t, status, raw, http.StatusBadRequest, "WS.LABEL.CIRCULAR_PARENT", "cyclic parent")
	}
}
