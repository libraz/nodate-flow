package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

type archiveProjectView struct {
	ID         string `json:"id"`
	IsArchived bool   `json:"isArchived"`
}

// projectArchivedInList reports isArchived for the project as the workspace
// listing shows it, failing when the project is absent from the page.
func projectArchivedInList(t *testing.T, tt *helpers.TestTenant, projectID string) bool {
	t.Helper()
	var list struct {
		Projects []archiveProjectView `json:"projects"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/projects?limit=200",
		tt.AccessToken, nil, &list)
	for _, p := range list.Projects {
		if p.ID == projectID {
			return p.IsArchived
		}
	}
	t.Fatalf("project %s missing from the workspace listing", projectID)
	return false
}

func projectArchivedByGet(t *testing.T, tt *helpers.TestTenant, projectID string) bool {
	t.Helper()
	var p archiveProjectView
	doJSON(t, http.MethodGet, testServerURL+"/projects/"+projectID, tt.AccessToken, nil, &p)
	require.Equal(t, projectID, p.ID)
	return p.IsArchived
}

func countProjectAudit(t *testing.T, tt *helpers.TestTenant, action, projectID string) int {
	t.Helper()
	n := 0
	for _, r := range queryAuditLogs(t, testDB, tt.WorkspacePublicID) {
		if r.Action == action && r.ResourcePublicID.String == projectID {
			n++
		}
	}
	return n
}

// TestProjectArchive covers POST /projects/{prjId}/archive and /unarchive:
// the isArchived flag on both read paths, idempotent repeats that leave the
// audit trail alone, the editor role floor, task readability, and 404 after
// the project is disabled.
func TestProjectArchive(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	t.Run("archive and unarchive flip isArchived", func(t *testing.T) {
		t.Parallel()
		owner := newTenant(t)
		prj := createProject(t, owner, "Archive me")
		base := testServerURL + "/projects/" + prj

		require.False(t, projectArchivedInList(t, owner, prj))
		require.False(t, projectArchivedByGet(t, owner, prj))

		status, raw := doJSONStatus(t, http.MethodPost, base+"/archive", owner.AccessToken, nil)
		require.Equalf(t, http.StatusOK, status, "body=%s", string(raw))
		require.True(t, projectArchivedInList(t, owner, prj))
		require.True(t, projectArchivedByGet(t, owner, prj))

		status, raw = doJSONStatus(t, http.MethodPost, base+"/unarchive", owner.AccessToken, nil)
		require.Equalf(t, http.StatusOK, status, "body=%s", string(raw))
		require.False(t, projectArchivedInList(t, owner, prj))
		require.False(t, projectArchivedByGet(t, owner, prj))
	})

	t.Run("repeated archive is 200 and records one audit row", func(t *testing.T) {
		t.Parallel()
		owner := newTenant(t)
		prj := createProject(t, owner, "Archive twice")
		base := testServerURL + "/projects/" + prj

		for i := 0; i < 3; i++ {
			status, raw := doJSONStatus(t, http.MethodPost, base+"/archive", owner.AccessToken, nil)
			require.Equalf(t, http.StatusOK, status, "call %d body=%s", i, string(raw))
		}
		require.Equal(t, 1, countProjectAudit(t, owner, "project.archive", prj))
		require.Equal(t, 0, countProjectAudit(t, owner, "project.unarchive", prj))

		for i := 0; i < 2; i++ {
			status, raw := doJSONStatus(t, http.MethodPost, base+"/unarchive", owner.AccessToken, nil)
			require.Equalf(t, http.StatusOK, status, "call %d body=%s", i, string(raw))
		}
		require.Equal(t, 1, countProjectAudit(t, owner, "project.archive", prj))
		require.Equal(t, 1, countProjectAudit(t, owner, "project.unarchive", prj))
	})

	t.Run("project role floor", func(t *testing.T) {
		t.Parallel()
		owner := newTenant(t)
		viewer := seedProjectRoleMember(t, owner, "viewer")
		commenter := seedProjectRoleMember(t, owner, "commenter")
		editor := seedProjectRoleMember(t, owner, "editor")
		prj := owner.ProjectPublicID
		base := testServerURL + "/projects/" + prj

		for name, member := range map[string]*helpers.TestTenant{"viewer": viewer, "commenter": commenter} {
			status, raw := doJSONStatus(t, http.MethodPost, base+"/archive", member.AccessToken, nil)
			require.Equalf(t, http.StatusForbidden, status, "%s body=%s", name, string(raw))
			require.Equal(t, projectACLDenied, problemType(t, raw))
		}
		require.False(t, projectArchivedByGet(t, owner, prj), "refused calls must not archive")

		status, raw := doJSONStatus(t, http.MethodPost, base+"/archive", editor.AccessToken, nil)
		require.Equalf(t, http.StatusOK, status, "body=%s", string(raw))
		require.True(t, projectArchivedByGet(t, owner, prj))

		for name, member := range map[string]*helpers.TestTenant{"viewer": viewer, "commenter": commenter} {
			status, raw := doJSONStatus(t, http.MethodPost, base+"/unarchive", member.AccessToken, nil)
			require.Equalf(t, http.StatusForbidden, status, "%s body=%s", name, string(raw))
		}
		require.True(t, projectArchivedByGet(t, owner, prj), "refused unarchive must not restore")

		status, raw = doJSONStatus(t, http.MethodPost, base+"/unarchive", editor.AccessToken, nil)
		require.Equalf(t, http.StatusOK, status, "body=%s", string(raw))
		require.False(t, projectArchivedByGet(t, owner, prj))
	})

	t.Run("tasks stay readable in an archived project", func(t *testing.T) {
		t.Parallel()
		owner := newTenant(t)
		prj := createProject(t, owner, "Archived with tasks")
		taskID := createTaskForAttachment(t, owner.AccessToken, prj, "still here")

		status, raw := doJSONStatus(t, http.MethodPost,
			testServerURL+"/projects/"+prj+"/archive", owner.AccessToken, nil)
		require.Equalf(t, http.StatusOK, status, "body=%s", string(raw))

		var task struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}
		status, raw = doJSONStatus(t, http.MethodGet, testServerURL+"/tasks/"+taskID, owner.AccessToken, nil)
		require.Equalf(t, http.StatusOK, status, "body=%s", string(raw))
		require.NoError(t, json.Unmarshal(raw, &task))
		require.Equal(t, taskID, task.ID)
		require.Equal(t, "still here", task.Title)
	})

	t.Run("disabled project is 404", func(t *testing.T) {
		t.Parallel()
		owner := newTenant(t)
		prj := createProject(t, owner, "Disable me")
		base := testServerURL + "/projects/" + prj

		status, raw := doJSONStatus(t, http.MethodDelete, base, owner.AccessToken, nil)
		require.Equalf(t, http.StatusOK, status, "body=%s", string(raw))

		status, raw = doJSONStatus(t, http.MethodPost, base+"/archive", owner.AccessToken, nil)
		require.Equalf(t, http.StatusNotFound, status, "body=%s", string(raw))
		status, raw = doJSONStatus(t, http.MethodPost, base+"/unarchive", owner.AccessToken, nil)
		require.Equalf(t, http.StatusNotFound, status, "body=%s", string(raw))
	})
}
