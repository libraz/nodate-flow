package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// seedGuestMember invites a fresh user into the owner's workspace at the
// guest role and returns their tenant handle. The returned tenant's own
// workspace fields are left untouched; only AccessToken is meaningful for
// requests against the owner's workspace.
func seedGuestMember(t *testing.T, owner *helpers.TestTenant) *helpers.TestTenant {
	t.Helper()
	guest := newTenant(t)

	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "guest"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		guest.AccessToken, nil, nil)

	return guest
}

// TestGuestIsReadOnlyOnWorkspaceResources locks in the guest contract: a
// guest keeps full read access to the workspace-scoped surface but cannot
// mutate any of it.
//
// The shared workspace furniture is the point. Without the floor, a guest
// invited to review a single project can rename or disable every workspace
// label (breaking every saved filter that referenced it), delete or
// complete a sprint, and publish a lens — a projection of workspace tasks
// — onto an unauthenticated URL.
func TestGuestIsReadOnlyOnWorkspaceResources(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	guest := seedGuestMember(t, owner)
	wsBase := testServerURL + "/workspaces/" + owner.WorkspacePublicID

	// Owner seeds the resources the guest will try to mutate.
	var label struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, wsBase+"/labels", owner.AccessToken,
		map[string]any{"name": "guest-floor-" + randomHex(4), "color": "#3366ff"}, &label)
	require.NotEmpty(t, label.ID)

	var timebox struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, wsBase+"/timeboxes", owner.AccessToken, map[string]any{
		"name":     "Guest Floor Sprint",
		"startsOn": "2025-05-01",
		"endsOn":   "2025-05-14",
	}, &timebox)
	require.NotEmpty(t, timebox.ID)

	var lens struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, wsBase+"/lenses", owner.AccessToken, map[string]any{
		"name":      "Guest Floor Lens",
		"filter":    json.RawMessage(`{"priority":{"gte":3}}`),
		"sort":      json.RawMessage(`[{"field":"priority","dir":"desc"}]`),
		"isDefault": false,
	}, &lens)
	require.NotEmpty(t, lens.ID)

	// Reads stay open at guest role.
	reads := []struct {
		name string
		path string
	}{
		{"list labels", wsBase + "/labels"},
		{"get label", wsBase + "/labels/" + label.ID},
		{"list timeboxes", wsBase + "/timeboxes"},
		{"list lenses", wsBase + "/lenses"},
		{"list projects", wsBase + "/projects"},
	}
	for _, r := range reads {
		t.Run("read "+r.name, func(t *testing.T) {
			status, raw := doJSONStatus(t, http.MethodGet, r.path, guest.AccessToken, nil)
			require.Equalf(t, http.StatusOK, status,
				"guest must keep read access to %s, body=%s", r.name, string(raw))
		})
	}

	// Every mutation is refused with the workspace role code.
	writes := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"create label", http.MethodPost, wsBase + "/labels",
			map[string]any{"name": "guest-made", "color": "#ff0000"}},
		{"rename label", http.MethodPatch, wsBase + "/labels/" + label.ID,
			map[string]any{"name": "renamed by guest"}},
		{"disable label", http.MethodDelete, wsBase + "/labels/" + label.ID, nil},
		{"create timebox", http.MethodPost, wsBase + "/timeboxes",
			map[string]any{"name": "Guest Sprint", "startsOn": "2025-06-01", "endsOn": "2025-06-14"}},
		{"complete timebox", http.MethodPost, wsBase + "/timeboxes/" + timebox.ID + "/status",
			map[string]any{"status": "active"}},
		{"delete timebox", http.MethodDelete, wsBase + "/timeboxes/" + timebox.ID, nil},
		// A complete body on purpose: the role floor must be what rejects
		// this, not schema validation.
		{"create lens", http.MethodPost, wsBase + "/lenses",
			map[string]any{
				"name":      "Guest Lens",
				"filter":    json.RawMessage(`{}`),
				"sort":      json.RawMessage(`[]`),
				"isDefault": false,
			}},
		{"publish lens", http.MethodPost, wsBase + "/lenses/" + lens.ID + "/publish", nil},
		{"unpublish lens", http.MethodPost, wsBase + "/lenses/" + lens.ID + "/unpublish", nil},
		{"create page", http.MethodPost, wsBase + "/pages",
			map[string]any{"title": "Guest Page", "body": "hello"}},
		{"generate page with ai", http.MethodPost, wsBase + "/pages/generate",
			map[string]any{"prompt": "write something"}},
		{"create intake item", http.MethodPost, wsBase + "/intake",
			map[string]any{"title": "guest capture"}},
	}
	for _, w := range writes {
		t.Run("write "+w.name, func(t *testing.T) {
			status, raw := doJSONStatus(t, w.method, w.path, guest.AccessToken, w.body)
			require.Equalf(t, http.StatusForbidden, status,
				"guest must not %s, body=%s", w.name, string(raw))
		})
	}

	// The label survived every attempt.
	var after struct {
		Name string `json:"name"`
	}
	doJSON(t, http.MethodGet, wsBase+"/labels/"+label.ID, owner.AccessToken, nil, &after)
	require.NotEqual(t, "renamed by guest", after.Name)
}

// TestGuestKeepsControlOfOwnRows is the counterweight to the write floor: it
// only guards state the workspace shares. Operations whose every write is
// bound to the caller (user_id = actor) stay available at guest role, because
// blocking them protects nothing and would leave a guest unable to manage
// their own notification bell or their own MCP tokens.
func TestGuestKeepsControlOfOwnRows(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	guest := seedGuestMember(t, owner)
	wsBase := testServerURL + "/workspaces/" + owner.WorkspacePublicID

	status, raw := doJSONStatus(t, http.MethodPost,
		wsBase+"/notifications/read-all", guest.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status,
		"guest must be able to mark their own notifications read, body=%s", string(raw))

	var token struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, wsBase+"/me/mcp-tokens", guest.AccessToken,
		map[string]any{"name": "guest-own-token", "scopes": []string{"read:workspace"}}, &token)
	require.NotEmpty(t, token.ID, "guest must be able to mint their own MCP token")

	status, raw = doJSONStatus(t, http.MethodDelete,
		wsBase+"/me/mcp-tokens/"+token.ID, guest.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status,
		"guest must be able to revoke their own MCP token, body=%s", string(raw))
}

// TestLensPublishRequiresCreatorOrWorkspaceAdmin verifies that publishing a
// lens — which exposes a projection of workspace tasks on an unauthenticated
// URL — is limited to the lens creator and to workspace admins / owners, so
// any workspace member cannot publish someone else's saved view.
func TestLensPublishRequiresCreatorOrWorkspaceAdmin(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := seedWorkspaceMemberWithoutProjectRole(t, owner)
	wsBase := testServerURL + "/workspaces/" + owner.WorkspacePublicID

	var lens struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, wsBase+"/lenses", owner.AccessToken, map[string]any{
		"name":      "Owner Lens " + randomHex(4),
		"filter":    json.RawMessage(`{"priority":{"gte":3}}`),
		"sort":      json.RawMessage(`[{"field":"priority","dir":"desc"}]`),
		"isDefault": false,
	}, &lens)
	require.NotEmpty(t, lens.ID)

	status, raw := doJSONStatus(t, http.MethodPost,
		wsBase+"/lenses/"+lens.ID+"/publish", member.AccessToken, nil)
	require.Equalf(t, http.StatusForbidden, status,
		"a non-creator, non-admin member must not publish another member's lens, body=%s", string(raw))
	require.Equal(t, "WS.MEMBER.ROLE_DENIED", problemType(t, raw))

	// The creator can still publish and unpublish.
	var published struct {
		PublicToken string `json:"publicToken"`
	}
	doJSON(t, http.MethodPost, wsBase+"/lenses/"+lens.ID+"/publish",
		owner.AccessToken, nil, &published)
	require.NotEmpty(t, published.PublicToken)
	doJSON(t, http.MethodPost, wsBase+"/lenses/"+lens.ID+"/unpublish",
		owner.AccessToken, nil, nil)
}

// TestPATBoundWorkspaceCannotReachCrossWorkspaceRoutes verifies that a PAT
// minted for one workspace is refused on the routes that span every workspace
// its owner belongs to. Those routes carry no workspace to compare the
// binding against, so serving them would turn a workspace-scoped token into a
// full-account credential — the token's own workspace stays reachable.
func TestPATBoundWorkspaceCannotReachCrossWorkspaceRoutes(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	pat := mintPatForWorkspace(t, tt.AccessToken, tt.UserPublicID, tt.WorkspacePublicID)

	// Sanity: the bound workspace is still fully reachable with the PAT.
	status, raw := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/labels", pat, nil)
	require.Equalf(t, http.StatusOK, status,
		"PAT must keep working inside its own workspace, body=%s", string(raw))

	crossWorkspace := []struct {
		name   string
		method string
		path   string
	}{
		{"my tasks", http.MethodGet, testServerURL + "/me/tasks"},
		{"my tasks with dates", http.MethodGet, testServerURL + "/me/tasks-with-dates?from=2025-01-01&to=2025-12-31"},
		{"my notifications", http.MethodGet, testServerURL + "/me/notifications"},
		{"my favorites", http.MethodGet, testServerURL + "/me/favorites"},
		{"inbox", http.MethodGet, testServerURL + "/inbox"},
	}
	for _, c := range crossWorkspace {
		t.Run(c.name, func(t *testing.T) {
			status, raw := doJSONStatus(t, c.method, c.path, pat, nil)
			require.Equalf(t, http.StatusForbidden, status,
				"workspace-bound PAT must not reach %s, body=%s", c.name, string(raw))
			require.Equal(t, "WS.WORKSPACE.ACCESS_DENIED", problemType(t, raw))
		})
	}
}

// TestPATBoundWorkspaceCannotWriteAnotherWorkspace verifies the write half of
// the binding: a PAT minted for workspace A cannot create a task in a project
// belonging to workspace B, even though its owner is an owner of both. The
// task collection route has no {wsId} to check up front, so this exercises the
// binding check inside the shared workspace membership gate.
func TestPATBoundWorkspaceCannotWriteAnotherWorkspace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var secondWorkspace struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/workspaces", tt.AccessToken,
		map[string]any{
			"slug": "pat-write-" + randomHex(6),
			"name": "PAT Write B",
		}, &secondWorkspace)
	require.NotEmpty(t, secondWorkspace.ID)

	var secondProject struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+secondWorkspace.ID+"/projects", tt.AccessToken,
		map[string]any{
			"slug": "pat-write-prj-" + randomHex(6),
			"name": "PAT Write Project",
		}, &secondProject)
	require.NotEmpty(t, secondProject.ID)

	pat := mintPatForWorkspace(t, tt.AccessToken, tt.UserPublicID, tt.WorkspacePublicID)

	// Creating in the bound workspace still works.
	var ownTask struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", pat, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "task via bound PAT",
	}, &ownTask)
	require.NotEmpty(t, ownTask.ID)

	// Creating in the other workspace is refused.
	status, raw := doJSONStatus(t, http.MethodPost, testServerURL+"/tasks", pat,
		map[string]any{
			"projectId": secondProject.ID,
			"title":     "task in the wrong workspace",
		})
	require.Equalf(t, http.StatusForbidden, status,
		"PAT bound to workspace A must not create tasks in workspace B, body=%s", string(raw))
}
