// Package signaljudgetests — the audience a retro draft is created with.
//
// A retro draft quotes the task it was drafted from: the source's title
// goes into the draft's own title and into its description. That makes the
// draft a second, persistent copy of those words, and a copy has no
// visibility rule of its own to fall back on — whatever the source's
// audience was, the only thing standing between the copy and a reader is
// the audience the draft row itself was created with.
//
// So the property is not "the draft has a visibility" but "the draft has
// the source's", and the way to see it is from the outside: a workspace
// member who cannot read the source must not be able to read the source's
// title anywhere, including out of a row somebody else's automation wrote.
package signaljudgetests

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/signaljudge"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestRetroDraftCarriesTheSourceTaskAudience drafts a retro from a task of
// each audience the column can hold and holds the draft to the source's.
//
// Every case runs the same three questions, because the visibility column
// on its own does not answer them: the draft has to land in the source's
// project (a project-visible row in another project is visible to another
// set of people), the source's title must not become readable to somebody
// the source keeps it from, and the people who could read the source must
// still be able to read the draft — an audience narrowed to nobody would
// satisfy a leak test while breaking the feature.
func TestRetroDraftCarriesTheSourceTaskAudience(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	cases := []struct {
		name string
		// visibility is the audience the source task is created with.
		visibility string
		// workspaceMemberMayRead says whether a member of the workspace who
		// is neither in the task's project nor an actor on it can read the
		// source. It is the whole difference between the cases: for public
		// the answer is yes, and the draft inheriting the audience means
		// they may read the draft too.
		workspaceMemberMayRead bool
	}{
		{name: "public source stays readable by the whole workspace", visibility: "public", workspaceMemberMayRead: true},
		{name: "project source stays inside its project", visibility: "project", workspaceMemberMayRead: false},
		{name: "private source stays with the task's actors", visibility: "private", workspaceMemberMayRead: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			owner := helpers.CreateTestTenant(t, testSrv.BaseURL)
			// A member of the owner's workspace who was never added to the
			// project holding the task and is not an actor on it. That is
			// the reader both the project and the private branch exclude,
			// and the one the public branch includes.
			outsider := joinWorkspaceAsMember(t, owner)
			wsID := lookupWorkspaceIDForRetro(t, testDB, owner.WorkspacePublicID)

			// The title is the thing that travels into the copy, so it is a
			// string nothing else in the workspace could produce: every
			// assertion below is a substring search over whole response
			// bodies, and a common word would match something unrelated.
			sourceTitle := "RETRO-SOURCE-" + helpers.RandomHex(12)
			sourcePublicID, sourceInternalID := createTaskWithVisibility(t, owner, sourceTitle, tc.visibility)

			// Precondition. Without it the case where the outsider cannot
			// read the draft would pass just as well against an outsider who
			// was never in the workspace at all, and the case where they can
			// would pass against one who can read everything.
			requireTaskReadable(t, owner, sourcePublicID, sourceTitle,
				"the owner must be able to read the task they created")
			if tc.workspaceMemberMayRead {
				requireTaskReadable(t, outsider, sourcePublicID, sourceTitle,
					"a workspace member reading a public task")
			} else {
				requireTaskConcealed(t, outsider, sourcePublicID, sourceTitle,
					"a workspace member outside the project and off the actor list "+
						"reading a "+tc.visibility+" task")
			}

			mutator := &signaljudge.SQLTaskMutator{
				DB:      testDB,
				Queries: generated.New(testDB),
				Logger:  slog.New(slog.DiscardHandler),
			}
			// An empty title is what makes the draft quote the source in its
			// title as well as in its description: the mutator falls back to
			// the source's own title behind the retro prefix. The agent id
			// is only carried into the rollback log, so a judged run and this
			// one produce the same row.
			_, retroPublicID, err := mutator.DraftRetroTask(ctx, wsID, sourceInternalID, 0, "", true)
			require.NoError(t, err, "drafting a retro from a %s task", tc.visibility)
			require.NotEmpty(t, retroPublicID)

			// What the draft is made of. If neither field quoted the source
			// there would be nothing for the audience to protect, and the
			// reads below would prove nothing.
			retro := loadTaskByPublicID(t, testDB, wsID, retroPublicID)
			require.Contains(t, retro.title, sourceTitle,
				"the retro draft's title must quote the source task's title; got %q", retro.title)
			require.Contains(t, retro.description, sourceTitle,
				"the retro draft's description must quote the source task's title; got %q", retro.description)

			// The harm itself first, asked of the API rather than of the
			// column: can somebody who could not read the source read its
			// title now? It is asked before the column is inspected because
			// this is the failure worth reading — a run that stopped at
			// "visibility is public" would leave a reader to work out for
			// themselves what that let through.
			if tc.workspaceMemberMayRead {
				requireTaskReadable(t, outsider, retroPublicID, sourceTitle,
					"a workspace member reading the retro drafted from a public task")
				require.Contains(t, listTasksBody(t, outsider, owner.WorkspacePublicID), sourceTitle,
					"the retro drafted from a public task must stay in a workspace member's list")
			} else {
				requireTaskConcealed(t, outsider, retroPublicID, sourceTitle,
					"a workspace member reading the retro drafted from a "+tc.visibility+" task")
				require.NotContains(t, listTasksBody(t, outsider, owner.WorkspacePublicID), sourceTitle,
					"the source task's title reached a workspace member through the retro draft "+
						"listing, so the draft was created with a wider audience than the source")
			}

			// The audience the copy was written with, read off the row.
			require.Equal(t, tc.visibility, loadTaskVisibility(t, testDB, wsID, retroPublicID),
				"the retro draft must carry the source task's visibility, not the create-path default")

			// A project-visible row in a different project is visible to a
			// different set of people, so the column alone does not say the
			// audience is the same one.
			require.Equal(t, loadTaskProjectID(t, testDB, wsID, sourceInternalID), retro.projectID,
				"the retro draft must land in the source task's project")

			// And the other direction: the draft is still readable by the
			// audience the source had. An inheritance that narrowed every
			// draft to nobody would pass every assertion above.
			requireTaskReadable(t, owner, retroPublicID, sourceTitle,
				"the owner of the source task reading the retro drafted from it")
		})
	}
}

// joinWorkspaceAsMember registers a second user and brings them into the
// owner's workspace through the invite flow, without adding them to the
// owner's project. Workspace membership is what makes them a reader the
// task visibility rule has to answer about at all — a stranger is refused
// one layer earlier, by the workspace gate, and would prove nothing about
// visibility.
func joinWorkspaceAsMember(t *testing.T, owner *helpers.TestTenant) *helpers.TestTenant {
	t.Helper()
	member := helpers.CreateTestTenant(t, testSrv.BaseURL)

	var invite struct {
		Token string `json:"token"`
	}
	helpers.DoJSON(t, http.MethodPost,
		testSrv.BaseURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	require.NotEmpty(t, invite.Token, "workspace invite returned no token")
	helpers.DoJSON(t, http.MethodPost,
		testSrv.BaseURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	return member
}

// createTaskWithVisibility creates a task through POST /tasks at the given
// audience and returns its public and internal ids.
func createTaskWithVisibility(t *testing.T, tt *helpers.TestTenant, title, visibility string) (string, int64) {
	t.Helper()
	var created struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, testSrv.BaseURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId":  tt.ProjectPublicID,
		"title":      title,
		"visibility": visibility,
	}, &created)
	require.NotEmpty(t, created.ID, "POST /tasks returned no id for a %s task", visibility)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var internalID int64
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		created.ID,
	).Scan(&internalID))
	require.NotZero(t, internalID)
	return created.ID, internalID
}

// loadTaskVisibility reads the audience a task row was written with.
func loadTaskVisibility(t *testing.T, db *sql.DB, workspaceID uint32, publicID string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var visibility string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT visibility FROM tasks WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspaceID, publicID,
	).Scan(&visibility))
	return visibility
}

// requireTaskReadable asserts that GET /tasks/{id} answers this reader with
// the task, and that the answer actually carries the title — a 200 whose
// body had dropped the title would satisfy the status check while proving
// nothing about what the reader can see.
func requireTaskReadable(t *testing.T, reader *helpers.TestTenant, taskPublicID, title, why string) {
	t.Helper()
	status, body := helpers.DoJSONStatus(t, http.MethodGet,
		testSrv.BaseURL+"/tasks/"+taskPublicID, reader.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status, "%s: expected the read to succeed, body=%s", why, string(body))
	require.Containsf(t, string(body), title, "%s: the response does not carry the task's title", why)
}

// requireTaskConcealed asserts that GET /tasks/{id} conceals the task from
// this reader: not-found rather than forbidden, so the status cannot be
// used to confirm the row exists, and no trace of the title in the refusal.
func requireTaskConcealed(t *testing.T, reader *helpers.TestTenant, taskPublicID, title, why string) {
	t.Helper()
	status, body := helpers.DoJSONStatus(t, http.MethodGet,
		testSrv.BaseURL+"/tasks/"+taskPublicID, reader.AccessToken, nil)
	require.Equalf(t, http.StatusNotFound, status,
		"%s: expected the task to be concealed, body=%s", why, string(body))
	require.NotContainsf(t, string(body), title,
		"%s: the refusal carries the title it concealed", why)
}

// listTasksBody returns the raw body of the workspace task list as this
// reader sees it. The assertions search the whole body rather than a
// decoded title field: a title copied into any other field of the response
// reaches the same reader, and this is a test about words on the wire.
func listTasksBody(t *testing.T, reader *helpers.TestTenant, workspacePublicID string) string {
	t.Helper()
	status, body := helpers.DoJSONStatus(t, http.MethodGet,
		testSrv.BaseURL+"/tasks?workspaceId="+workspacePublicID, reader.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status, "listing tasks as a workspace member, body=%s", string(body))
	return string(body)
}
