package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// projectACLDenied is the canonical error code returned when a workspace
// member lacks the required project role for a task-scoped mutation. The
// ACL middleware writes it in the RFC 9457 problem+json "type" field via
// writeSpecError (see apps/flow-api/internal/http/middleware/acl.go).
const projectACLDenied = "WS.PROJECT.ACCESS_DENIED"

// problemType decodes the RFC 9457 problem+json "type" field from a raw
// error response body. Middleware-emitted ACL errors carry the canonical
// error code there, so negative-path assertions compare against it.
func problemType(t *testing.T, raw []byte) string {
	t.Helper()
	var body struct {
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(raw, &body), "decode problem body=%s", string(raw))
	return body.Type
}

// seedProjectRoleMember invites a fresh user into the owner's workspace as
// a plain workspace member, accepts the invite, then assigns the supplied
// project role on the owner's default project. The returned tenant acts
// with its own token so role enforcement is exercised end-to-end through
// HTTP (no handler shortcuts, no direct DB writes).
//
// Roles must be one of the project role enum values accepted by
// POST /projects/{id}/members: lead, editor, commenter, viewer.
func seedProjectRoleMember(t *testing.T, owner *helpers.TestTenant, projectRole string) *helpers.TestTenant {
	t.Helper()
	member := newTenant(t)

	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	doJSON(t, http.MethodPost,
		testServerURL+"/projects/"+owner.ProjectPublicID+"/members",
		owner.AccessToken, map[string]any{
			"userId": member.UserPublicID,
			"role":   projectRole,
		}, nil)

	return member
}

// seedElevatedMember invites a fresh user into the owner's workspace as a
// workspace admin and accepts the invite, WITHOUT granting any project
// role. Workspace owners and admins pass RequireProjectRole via the
// ProjectRoleElevated bypass, so this user must be able to mutate tasks in
// the project even though it has no project_members row.
func seedElevatedMember(t *testing.T, owner *helpers.TestTenant) *helpers.TestTenant {
	t.Helper()
	admin := newTenant(t)

	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "admin"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		admin.AccessToken, nil, nil)

	return admin
}

// seedWorkspaceMemberWithoutProjectRole invites a fresh user into the owner's
// workspace as a plain member and deliberately does not add a project_members
// row. Public tasks are visible to this user, but task-scoped write gates must
// still reject them because they have no project role.
func seedWorkspaceMemberWithoutProjectRole(t *testing.T, owner *helpers.TestTenant) *helpers.TestTenant {
	t.Helper()
	member := newTenant(t)

	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	return member
}

// seedPublicTask creates a public-visibility task in the owner's default
// project and returns its public id. Public visibility lets every project
// role (down to viewer) read it, so the negative tests fail on the project
// role gate rather than on Layer-4 task visibility.
func seedPublicTask(t *testing.T, owner *helpers.TestTenant, title string) string {
	t.Helper()
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId":  owner.ProjectPublicID,
			"title":      title,
			"visibility": "public",
		}, &task)
	require.NotEmpty(t, task.ID, "task create did not return id")
	return task.ID
}

// seedWorkspaceLabel creates a workspace label and returns its public id.
// Used to give the editor write tests a real label to attach.
func seedWorkspaceLabel(t *testing.T, owner *helpers.TestTenant, name string) string {
	t.Helper()
	var label struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/labels",
		owner.AccessToken, map[string]any{"name": name, "color": "#3366ff"}, &label)
	require.NotEmpty(t, label.ID, "label create did not return id")
	return label.ID
}

// TestProjectViewerCannotWriteTask locks in that a project viewer (a
// read-only role that can SEE the task) is blocked from every task-scoped
// write: structural editor mutations, commenter mutations, and reactions.
// A pure read (GET /tasks/{id}) must still succeed.
//
// Routes asserted (see apps/flow-api/internal/http/router/router.go ~822-844
// and the tasks/labels/reactions register split):
//   - PATCH  /tasks/{id}                 -> 403 WS.PROJECT.ACCESS_DENIED (editor group)
//   - DELETE /tasks/{id}                 -> 403 WS.PROJECT.ACCESS_DENIED (editor group, disable)
//   - POST   /tasks/{id}/transitions     -> 403 WS.PROJECT.ACCESS_DENIED (editor group)
//   - POST   /tasks/{id}/actors          -> 403 WS.PROJECT.ACCESS_DENIED (editor group)
//   - POST   /tasks/{id}/labels          -> 403 WS.PROJECT.ACCESS_DENIED (editor group)
//   - POST   /tasks/{id}/comments        -> 403 WS.PROJECT.ACCESS_DENIED (commenter group)
//   - POST   /tasks/{id}/reactions       -> 403 WS.PROJECT.ACCESS_DENIED (commenter group)
//   - POST   /tasks                      -> 403 WS.PROJECT.ACCESS_DENIED (project editor required)
//   - POST   /tasks/reorder              -> 403 WS.PROJECT.ACCESS_DENIED (project editor required)
//   - GET    /tasks/{id}                 -> 200 (read group, RequireTaskAccess only)
func TestProjectViewerCannotWriteTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	viewer := seedProjectRoleMember(t, owner, "viewer")
	taskID := seedPublicTask(t, owner, "Viewer Write Boundary")
	base := testServerURL + "/tasks/" + taskID

	denials := []struct {
		name   string
		method string
		url    string
		body   any
	}{
		{"patch task", http.MethodPatch, base, map[string]any{"title": "viewer rename"}},
		{"disable task", http.MethodDelete, base, nil},
		{"transition", http.MethodPost, base + "/transitions", map[string]any{"transition": "start"}},
		{"actor add", http.MethodPost, base + "/actors", map[string]any{"userId": viewer.UserPublicID, "role": "watcher"}},
		{"label add", http.MethodPost, base + "/labels", map[string]any{"labelId": seedWorkspaceLabel(t, owner, "viewer-label")}},
		{"comment add", http.MethodPost, base + "/comments", map[string]any{"body": "viewer comment"}},
		{"reaction add", http.MethodPost, base + "/reactions", map[string]any{"emoji": "👍"}},
		{"create task", http.MethodPost, testServerURL + "/tasks", map[string]any{"projectId": owner.ProjectPublicID, "title": "viewer create"}},
		{"reorder tasks", http.MethodPost, testServerURL + "/tasks/reorder", map[string]any{
			"projectId": owner.ProjectPublicID,
			"items":     []map[string]any{{"id": taskID, "sortWeight": 100}},
		}},
	}

	for _, d := range denials {
		d := d
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			status, raw := doJSONStatus(t, d.method, d.url, viewer.AccessToken, d.body)
			require.Equal(t, http.StatusForbidden, status,
				"viewer %s must be forbidden, body=%s", d.name, string(raw))
			require.Equal(t, projectACLDenied, problemType(t, raw),
				"viewer %s must surface %s, body=%s", d.name, projectACLDenied, string(raw))
		})
	}

	t.Run("read task", func(t *testing.T) {
		t.Parallel()
		var got struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}
		doJSON(t, http.MethodGet, base, viewer.AccessToken, nil, &got)
		require.Equal(t, taskID, got.ID, "viewer must be able to read the task")
		require.Equal(t, "Viewer Write Boundary", got.Title)
	})
}

// TestWorkspaceMemberWithoutProjectRoleCannotWritePublicTask locks in the
// read/write split: workspace members with no project_members row may read a
// public task, but they must not pass RequireProjectRole for mutations.
func TestWorkspaceMemberWithoutProjectRoleCannotWritePublicTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := seedWorkspaceMemberWithoutProjectRole(t, owner)
	taskID := seedPublicTask(t, owner, "No Project Role Boundary")
	base := testServerURL + "/tasks/" + taskID

	var got struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodGet, base, member.AccessToken, nil, &got)
	require.Equal(t, taskID, got.ID, "workspace member must be able to read public task")

	status, raw := doJSONStatus(t, http.MethodPatch, base,
		member.AccessToken, map[string]any{"title": "non-member rename"})
	require.Equal(t, http.StatusForbidden, status,
		"workspace member without project role must not patch public task, body=%s", string(raw))
	require.Equal(t, projectACLDenied, problemType(t, raw),
		"non-member patch must surface %s, body=%s", projectACLDenied, string(raw))
}

// TestProjectCommenterPermissions locks in the commenter boundary: a
// project commenter may post comments and reactions (the commenter write
// group) but is still blocked from structural editor mutations.
//
// Routes asserted:
//   - POST  /tasks/{id}/comments   -> 2xx (commenter group)
//   - POST  /tasks/{id}/reactions  -> 2xx (commenter group)
//   - PATCH /tasks/{id}            -> 403 WS.PROJECT.ACCESS_DENIED (editor group)
//   - POST  /tasks/{id}/actors     -> 403 WS.PROJECT.ACCESS_DENIED (editor group)
//   - POST  /tasks                 -> 403 WS.PROJECT.ACCESS_DENIED (project editor required)
//   - POST  /tasks/reorder         -> 403 WS.PROJECT.ACCESS_DENIED (project editor required)
func TestProjectCommenterPermissions(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	commenter := seedProjectRoleMember(t, owner, "commenter")
	taskID := seedPublicTask(t, owner, "Commenter Boundary")
	base := testServerURL + "/tasks/" + taskID

	t.Run("comment allowed", func(t *testing.T) {
		t.Parallel()
		var comment struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, base+"/comments",
			commenter.AccessToken, map[string]any{"body": "commenter note"}, &comment)
		require.NotEmpty(t, comment.ID, "commenter comment must be created")
	})

	t.Run("reaction allowed", func(t *testing.T) {
		t.Parallel()
		status, raw := doJSONStatus(t, http.MethodPost, base+"/reactions",
			commenter.AccessToken, map[string]any{"emoji": "🎉"})
		require.GreaterOrEqualf(t, status, 200, "reaction body=%s", string(raw))
		require.Lessf(t, status, 300, "commenter reaction must succeed, body=%s", string(raw))
	})

	t.Run("patch denied", func(t *testing.T) {
		t.Parallel()
		status, raw := doJSONStatus(t, http.MethodPatch, base,
			commenter.AccessToken, map[string]any{"title": "commenter rename"})
		require.Equal(t, http.StatusForbidden, status,
			"commenter must not patch the task, body=%s", string(raw))
		require.Equal(t, projectACLDenied, problemType(t, raw),
			"commenter patch must surface %s, body=%s", projectACLDenied, string(raw))
	})

	t.Run("actor add denied", func(t *testing.T) {
		t.Parallel()
		status, raw := doJSONStatus(t, http.MethodPost, base+"/actors",
			commenter.AccessToken, map[string]any{"userId": commenter.UserPublicID, "role": "watcher"})
		require.Equal(t, http.StatusForbidden, status,
			"commenter must not add actors, body=%s", string(raw))
		require.Equal(t, projectACLDenied, problemType(t, raw),
			"commenter actor add must surface %s, body=%s", projectACLDenied, string(raw))
	})

	t.Run("create denied", func(t *testing.T) {
		t.Parallel()
		status, raw := doJSONStatus(t, http.MethodPost, testServerURL+"/tasks",
			commenter.AccessToken, map[string]any{"projectId": owner.ProjectPublicID, "title": "commenter create"})
		require.Equal(t, http.StatusForbidden, status,
			"commenter must not create tasks, body=%s", string(raw))
		require.Equal(t, projectACLDenied, problemType(t, raw),
			"commenter create must surface %s, body=%s", projectACLDenied, string(raw))
	})

	t.Run("reorder denied", func(t *testing.T) {
		t.Parallel()
		status, raw := doJSONStatus(t, http.MethodPost, testServerURL+"/tasks/reorder",
			commenter.AccessToken, map[string]any{
				"projectId": owner.ProjectPublicID,
				"items":     []map[string]any{{"id": taskID, "sortWeight": 100}},
			})
		require.Equal(t, http.StatusForbidden, status,
			"commenter must not reorder tasks, body=%s", string(raw))
		require.Equal(t, projectACLDenied, problemType(t, raw),
			"commenter reorder must surface %s, body=%s", projectACLDenied, string(raw))
	})
}

// TestProjectEditorPermissions locks in that a project editor passes the
// structural editor write gate: patching, transitioning, and attaching
// labels all succeed.
//
// Routes asserted:
//   - PATCH /tasks/{id}              -> 2xx (editor group)
//   - POST  /tasks/{id}/transitions  -> 2xx (editor group)
//   - POST  /tasks/{id}/labels       -> 2xx (editor group)
//   - POST  /tasks                   -> 2xx (project editor required)
//   - POST  /tasks/reorder           -> 2xx (project editor required)
func TestProjectEditorPermissions(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	editor := seedProjectRoleMember(t, owner, "editor")
	taskID := seedPublicTask(t, owner, "Editor Boundary")
	base := testServerURL + "/tasks/" + taskID

	t.Run("patch allowed", func(t *testing.T) {
		var patched struct {
			Title string `json:"title"`
		}
		doJSON(t, http.MethodPatch, base,
			editor.AccessToken, map[string]any{"title": "editor renamed"}, &patched)
		require.Equal(t, "editor renamed", patched.Title, "editor patch must apply")
	})

	t.Run("transition allowed", func(t *testing.T) {
		status, raw := doJSONStatus(t, http.MethodPost, base+"/transitions",
			editor.AccessToken, map[string]any{"transition": "start"})
		require.GreaterOrEqualf(t, status, 200, "transition body=%s", string(raw))
		require.Lessf(t, status, 300, "editor transition must succeed, body=%s", string(raw))
	})

	t.Run("label add allowed", func(t *testing.T) {
		labelID := seedWorkspaceLabel(t, owner, "editor-label")
		status, raw := doJSONStatus(t, http.MethodPost, base+"/labels",
			editor.AccessToken, map[string]any{"labelId": labelID})
		require.GreaterOrEqualf(t, status, 200, "label add body=%s", string(raw))
		require.Lessf(t, status, 300, "editor label add must succeed, body=%s", string(raw))
	})

	t.Run("create allowed", func(t *testing.T) {
		var created struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks",
			editor.AccessToken, map[string]any{"projectId": owner.ProjectPublicID, "title": "editor create"}, &created)
		require.NotEmpty(t, created.ID, "editor create must return an id")
	})

	t.Run("reorder allowed", func(t *testing.T) {
		status, raw := doJSONStatus(t, http.MethodPost, testServerURL+"/tasks/reorder",
			editor.AccessToken, map[string]any{
				"projectId": owner.ProjectPublicID,
				"items":     []map[string]any{{"id": taskID, "sortWeight": 200}},
			})
		require.GreaterOrEqualf(t, status, 200, "reorder body=%s", string(raw))
		require.Lessf(t, status, 300, "editor reorder must succeed, body=%s", string(raw))
	})
}

// TestWorkspaceAdminBypassesProjectRole locks in the ProjectRoleElevated
// bypass: a workspace admin with NO project_members row can still patch a
// task in the project. This confirms RequireProjectRole short-circuits for
// workspace owners / admins.
//
// Routes asserted:
//   - PATCH /tasks/{id} -> 2xx (editor group, via elevated bypass)
func TestWorkspaceAdminBypassesProjectRole(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	admin := seedElevatedMember(t, owner)
	taskID := seedPublicTask(t, owner, "Elevated Bypass")
	base := testServerURL + "/tasks/" + taskID

	var patched struct {
		Title string `json:"title"`
	}
	doJSON(t, http.MethodPatch, base,
		admin.AccessToken, map[string]any{"title": "admin renamed"}, &patched)
	require.Equal(t, "admin renamed", patched.Title,
		"workspace admin must bypass project role and patch the task")
}
