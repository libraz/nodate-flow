package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// addProjectRoleMember grants an existing workspace member a role on a
// specific project. seedProjectRoleMember covers the owner's default project;
// this one takes the project id so a test can build a second project and staff
// it independently.
func addProjectRoleMember(t *testing.T, owner *helpers.TestTenant, projectID, userPublicID, role string) {
	t.Helper()
	doJSON(t, http.MethodPost,
		testServerURL+"/projects/"+projectID+"/members",
		owner.AccessToken, map[string]any{
			"userId": userPublicID,
			"role":   role,
		}, nil)
}

// createProject creates a second project in the owner's workspace and returns
// its public id, so a test that soft-deletes a project does not take the
// tenant's default project (and every task under it) with it.
func createProject(t *testing.T, owner *helpers.TestTenant, name string) string {
	t.Helper()
	var project struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/projects",
		owner.AccessToken, map[string]any{
			"slug": "role-floor-" + randomHex(6),
			"name": name,
		}, &project)
	require.NotEmpty(t, project.ID)
	return project.ID
}

// TestProjectViewerCannotMutateProject pins the floor on /projects/{prjId}:
// holding a project_members row is permission to look, not to reshape.
//
// With membership alone as the whole check on this prefix, a viewer can
// rename the project, soft-delete it along with every task under it, add
// themselves as lead, and remove the leads who invited them — each one a
// mutation the project's own role column exists to refuse.
func TestProjectViewerCannotMutateProject(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	viewer := seedProjectRoleMember(t, owner, "viewer")
	bystander := seedProjectRoleMember(t, owner, "editor")
	prjBase := testServerURL + "/projects/" + owner.ProjectPublicID

	// Reads stay open at viewer role: the floor is on the writes only.
	reads := []struct {
		name string
		path string
	}{
		{"get project", prjBase},
		{"list members", prjBase + "/members"},
		{"list dependencies", prjBase + "/dependencies"},
	}
	for _, r := range reads {
		t.Run("read "+r.name, func(t *testing.T) {
			status, raw := doJSONStatus(t, http.MethodGet, r.path, viewer.AccessToken, nil)
			require.Equalf(t, http.StatusOK, status,
				"a project viewer must keep read access to %s, body=%s", r.name, string(raw))
		})
	}

	writes := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"rename the project", http.MethodPatch, prjBase,
			map[string]any{"name": "renamed by viewer"}},
		{"soft-delete the project", http.MethodDelete, prjBase, nil},
		{"promote themselves to lead", http.MethodPost, prjBase + "/members",
			map[string]any{"userId": viewer.UserPublicID, "role": "lead"}},
		{"remove another member", http.MethodDelete,
			prjBase + "/members/" + bystander.UserPublicID, nil},
	}
	for _, w := range writes {
		t.Run("write "+w.name, func(t *testing.T) {
			status, raw := doJSONStatus(t, w.method, w.path, viewer.AccessToken, w.body)
			require.Equalf(t, http.StatusForbidden, status,
				"a project viewer must not %s, body=%s", w.name, string(raw))
			require.Equal(t, projectACLDenied, problemType(t, raw))
		})
	}

	// Nothing landed: the project kept its name, stayed reachable, and the
	// member the viewer tried to remove is still on it.
	var after struct {
		Name string `json:"name"`
	}
	doJSON(t, http.MethodGet, prjBase, owner.AccessToken, nil, &after)
	require.NotEqual(t, "renamed by viewer", after.Name,
		"the refused PATCH must not have written the name")

	var members struct {
		Members []struct {
			UserID string `json:"userId"`
			Role   string `json:"role"`
		} `json:"members"`
	}
	doJSON(t, http.MethodGet, prjBase+"/members", owner.AccessToken, nil, &members)
	roles := map[string]string{}
	for _, m := range members.Members {
		roles[m.UserID] = m.Role
	}
	require.Equal(t, "editor", roles[bystander.UserPublicID],
		"the refused DELETE must have left the other member in place")
	require.Equal(t, "viewer", roles[viewer.UserPublicID],
		"the refused POST must not have promoted the caller")
}

// TestProjectCommenterCannotMutateProject covers the second role below the
// floor. A commenter may talk on a project's tasks; that is not the same
// permission as editing the project or deciding who else is on it.
func TestProjectCommenterCannotMutateProject(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	commenter := seedProjectRoleMember(t, owner, "commenter")
	prjBase := testServerURL + "/projects/" + owner.ProjectPublicID

	for _, w := range []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"rename the project", http.MethodPatch, prjBase,
			map[string]any{"name": "renamed by commenter"}},
		{"soft-delete the project", http.MethodDelete, prjBase, nil},
		{"add a member", http.MethodPost, prjBase + "/members",
			map[string]any{"userId": commenter.UserPublicID, "role": "lead"}},
	} {
		t.Run(w.name, func(t *testing.T) {
			status, raw := doJSONStatus(t, w.method, w.path, commenter.AccessToken, w.body)
			require.Equalf(t, http.StatusForbidden, status,
				"a project commenter must not %s, body=%s", w.name, string(raw))
			require.Equal(t, projectACLDenied, problemType(t, raw))
		})
	}
}

// TestProjectEditorEditsButDoesNotGrantRoles pins the split between the two
// write floors. An editor reshapes the project; deciding who reaches it is
// held one role higher, because an editor who could also grant roles could
// promote themselves and remove every lead above them.
func TestProjectEditorEditsButDoesNotGrantRoles(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	editor := seedProjectRoleMember(t, owner, "editor")
	outsider := seedWorkspaceMemberWithoutProjectRole(t, owner)
	prjBase := testServerURL + "/projects/" + owner.ProjectPublicID

	renamed := "edited by editor " + randomHex(4)
	status, raw := doJSONStatus(t, http.MethodPatch, prjBase,
		editor.AccessToken, map[string]any{"name": renamed})
	require.Equalf(t, http.StatusOK, status,
		"a project editor must still be able to edit the project, body=%s", string(raw))

	var after struct {
		Name string `json:"name"`
	}
	doJSON(t, http.MethodGet, prjBase, owner.AccessToken, nil, &after)
	require.Equal(t, renamed, after.Name, "the editor's PATCH must have landed")

	status, raw = doJSONStatus(t, http.MethodPost, prjBase+"/members",
		editor.AccessToken, map[string]any{
			"userId": outsider.UserPublicID,
			"role":   "editor",
		})
	require.Equalf(t, http.StatusForbidden, status,
		"a project editor must not decide who reaches the project, body=%s", string(raw))
	require.Equal(t, projectACLDenied, problemType(t, raw))
}

// TestProjectLeadKeepsEveryProjectMutation is the counterweight to the floors:
// the role the product does hand project administration to must still be able
// to use it, or the fix would read as correct while breaking the feature.
func TestProjectLeadKeepsEveryProjectMutation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	projectID := createProject(t, owner, "Lead Floor Project")
	lead := seedWorkspaceMemberWithoutProjectRole(t, owner)
	addProjectRoleMember(t, owner, projectID, lead.UserPublicID, "lead")
	target := seedWorkspaceMemberWithoutProjectRole(t, owner)
	prjBase := testServerURL + "/projects/" + projectID

	renamed := "led rename " + randomHex(4)
	status, raw := doJSONStatus(t, http.MethodPatch, prjBase,
		lead.AccessToken, map[string]any{"name": renamed})
	require.Equalf(t, http.StatusOK, status,
		"a project lead must be able to edit the project, body=%s", string(raw))

	status, raw = doJSONStatus(t, http.MethodPost, prjBase+"/members",
		lead.AccessToken, map[string]any{
			"userId": target.UserPublicID,
			"role":   "editor",
		})
	require.Equalf(t, http.StatusOK, status,
		"a project lead must be able to add a member, body=%s", string(raw))

	status, raw = doJSONStatus(t, http.MethodDelete,
		prjBase+"/members/"+target.UserPublicID, lead.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status,
		"a project lead must be able to remove a member, body=%s", string(raw))

	status, raw = doJSONStatus(t, http.MethodDelete, prjBase, lead.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status,
		"a project lead must be able to soft-delete the project, body=%s", string(raw))
}

// TestWorkspaceAdminReachesProjectMutationsWithoutProjectRole pins the
// elevation bypass through the new floors. Workspace owners and admins hold no
// project_members row, so a floor that read the role column literally would
// lock the people who administer the workspace out of its projects.
func TestWorkspaceAdminReachesProjectMutationsWithoutProjectRole(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	admin := seedElevatedMember(t, owner)
	target := seedWorkspaceMemberWithoutProjectRole(t, owner)
	prjBase := testServerURL + "/projects/" + owner.ProjectPublicID

	renamed := "admin rename " + randomHex(4)
	status, raw := doJSONStatus(t, http.MethodPatch, prjBase,
		admin.AccessToken, map[string]any{"name": renamed})
	require.Equalf(t, http.StatusOK, status,
		"a workspace admin must reach the editor floor without a project role, body=%s", string(raw))

	status, raw = doJSONStatus(t, http.MethodPost, prjBase+"/members",
		admin.AccessToken, map[string]any{
			"userId": target.UserPublicID,
			"role":   "viewer",
		})
	require.Equalf(t, http.StatusOK, status,
		"a workspace admin must reach the lead floor without a project role, body=%s", string(raw))
}

// TestGuestCannotDrainTheIntakeQueue pins the write floor on the intake
// endpoints. A signal row carries no user column, so archiving or snoozing one
// takes it off every member's list — shared workspace state, which the
// read-only workspace role does not get to change.
func TestGuestCannotDrainTheIntakeQueue(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	guest := seedGuestMember(t, owner)
	wsQuery := "?workspaceId=" + owner.WorkspacePublicID

	var signal struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/signals", owner.AccessToken, map[string]any{
		"workspaceId": owner.WorkspacePublicID,
		"source":      "manual",
		"kind":        "manual",
	}, &signal)
	require.NotEmpty(t, signal.ID)

	// Reading the queue stays open: guests are read-only, not blind.
	status, raw := doJSONStatus(t, http.MethodGet,
		testServerURL+"/inbox"+wsQuery, guest.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status,
		"a guest must keep read access to the intake queue, body=%s", string(raw))

	status, raw = doJSONStatus(t, http.MethodPost,
		testServerURL+"/inbox/"+signal.ID+"/snooze"+wsQuery,
		guest.AccessToken, map[string]any{"snoozeUntil": time.Now().Add(time.Hour).Unix()})
	require.Equalf(t, http.StatusForbidden, status,
		"a guest must not snooze an item every member reads, body=%s", string(raw))

	status, raw = doJSONStatus(t, http.MethodPost,
		testServerURL+"/inbox/"+signal.ID+"/archive"+wsQuery, guest.AccessToken, nil)
	require.Equalf(t, http.StatusForbidden, status,
		"a guest must not archive an item every member reads, body=%s", string(raw))

	// The item survived both attempts, and the role that does hold the queue
	// can still clear it.
	status, raw = doJSONStatus(t, http.MethodPost,
		testServerURL+"/inbox/"+signal.ID+"/archive"+wsQuery, owner.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status,
		"a workspace member must still be able to archive an intake item, body=%s", string(raw))
}

// TestGuestCannotCreateCalendars pins the write floor on calendar creation. A
// calendar is workspace state rather than the creator's own row: a team
// calendar is discoverable to the whole workspace, and the creator lands in it
// as owner — a role the read-only workspace role holds nowhere else.
func TestGuestCannotCreateCalendars(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	guest := seedGuestMember(t, owner)
	member := seedWorkspaceMemberWithoutProjectRole(t, owner)
	calBase := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/calendars"

	status, raw := doJSONStatus(t, http.MethodPost, calBase, guest.AccessToken, map[string]any{
		"kind":  "personal",
		"name":  "Guest Calendar " + randomHex(4),
		"color": "#4285F4",
	})
	require.Equalf(t, http.StatusForbidden, status,
		"a guest must not create a calendar in the workspace, body=%s", string(raw))

	// Listing calendars stays open, and a full member still creates.
	status, raw = doJSONStatus(t, http.MethodGet, calBase, guest.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status,
		"a guest must keep read access to the calendar list, body=%s", string(raw))

	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, calBase, member.AccessToken, map[string]any{
		"kind":  "personal",
		"name":  "Member Calendar " + randomHex(4),
		"color": "#4285F4",
	}, &created)
	require.NotEmpty(t, created.ID,
		"a workspace member must still be able to create a calendar")
}
