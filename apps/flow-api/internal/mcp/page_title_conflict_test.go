package mcp_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// pagesTitled counts the live pages a workspace holds under one title,
// which is what "the refused write left nothing behind" means here.
func pagesTitled(t *testing.T, db *sql.DB, wsID uint32, title string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM pages WHERE workspace_id = ? AND title = ? AND enabled = TRUE`,
		wsID, title).Scan(&n))
	return n
}

// pageTitle reads back the title a page carries, so a refused rename can
// be shown to have changed nothing rather than merely to have errored.
func pageTitle(t *testing.T, db *sql.DB, wsID uint32, pageID string) string {
	t.Helper()
	var title string
	require.NoError(t, db.QueryRow(
		`SELECT title FROM pages WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		wsID, pageID).Scan(&title))
	return title
}

// createPage drives create_page and returns the new page's public id.
func createPage(t *testing.T, deps mcp.Deps, s *mcp.TestSession, title, parentID string) (string, error) {
	t.Helper()
	args := map[string]any{"title": title, "body": "Body of " + title}
	if parentID != "" {
		args["parentPageId"] = parentID
	}
	out, err := mcp.RunCreatePage(context.Background(), deps, s, mcpVisJSON(t, args))
	if err != nil {
		return "", err
	}
	created, ok := out.(map[string]any)
	require.True(t, ok)
	id, ok := created["id"].(string)
	require.True(t, ok)
	return id, nil
}

// TestMCPPageWritesRefuseATitleALiveSiblingHolds drives the three tools
// that write a page title and pins what each one answers when the title
// is already in use among the live children of the same parent.
//
// The key that raises it is the caller's to resolve: another title, or
// another parent. Reported as a tool-execution failure the refusal reads
// as a server fault, which is the one class an agent retries, so the loop
// spins on a call that cannot succeed while the caller never learns what
// was wrong. Each case therefore asserts three things — the code is the
// one the REST route answers, the refused write left nothing behind, and
// a write that does not collide still goes through.
func TestMCPPageWritesRefuseATitleALiveSiblingHolds(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	ctx := context.Background()

	t.Run("create_page/a title another root page already holds", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPVisibilityFixture(t, db)
		deps := mcp.Deps{DB: db, Queries: generated.New(db)}
		sess := mcp.NewTestSession(fx.creatorID, fx.wsID, []string{"write:workspace"})

		// The control: without a first create that succeeds, a refusal on
		// the second proves nothing about duplicates.
		parentID, err := createPage(t, deps, sess, "Release process", "")
		require.NoError(t, err)
		require.NotEmpty(t, parentID)

		_, err = createPage(t, deps, sess, "Release process", "")
		requireDuplicateRefusal(t, err, apierrors.PagePageTitleTaken)
		require.Equal(t, 1, pagesTitled(t, db, fx.wsID, "Release process"),
			"a refused create must not have left a second page behind")

		// The other control: the key binds siblings, so the same title
		// under a different parent is a different page and still creates.
		// Without this the refusal above is also what a tool that refuses
		// every second page would produce.
		childID, err := createPage(t, deps, sess, "Release process", parentID)
		require.NoError(t, err)
		require.NotEmpty(t, childID)
		require.Equal(t, 2, pagesTitled(t, db, fx.wsID, "Release process"))
	})

	t.Run("update_page/renamed onto a sibling's title", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPVisibilityFixture(t, db)
		deps := mcp.Deps{DB: db, Queries: generated.New(db)}
		sess := mcp.NewTestSession(fx.creatorID, fx.wsID, []string{"write:workspace"})

		_, err := createPage(t, deps, sess, "Runbook", "")
		require.NoError(t, err)
		draftID, err := createPage(t, deps, sess, "Runbook (draft)", "")
		require.NoError(t, err)

		_, err = mcp.RunUpdatePage(ctx, deps, sess, mcpVisJSON(t, map[string]any{
			"pageId": draftID,
			"title":  "Runbook",
		}))
		requireDuplicateRefusal(t, err, apierrors.PagePageTitleTaken)
		require.Equal(t, "Runbook (draft)", pageTitle(t, db, fx.wsID, draftID),
			"a refused rename must leave the page under the title it had")
		require.Equal(t, 1, pagesTitled(t, db, fx.wsID, "Runbook"))

		// The control: a title nothing else holds is still accepted, so
		// the refusal above is about the collision rather than about
		// renaming.
		_, err = mcp.RunUpdatePage(ctx, deps, sess, mcpVisJSON(t, map[string]any{
			"pageId": draftID,
			"title":  "Runbook (v2)",
		}))
		require.NoError(t, err)
		require.Equal(t, "Runbook (v2)", pageTitle(t, db, fx.wsID, draftID))
	})

	t.Run("generate_page/the model drafted a title the workspace already holds", func(t *testing.T) {
		t.Parallel()
		// The title comes from the model, so two runs of the same brief
		// in one workspace draft the same title and collide. This is the
		// ordinary way the tool meets the key, and every retry of a
		// refusal reported as a server fault spends another model call.
		fx := seedMCPVisibilityFixture(t, db)
		deps := mcp.Deps{
			DB:      db,
			Queries: generated.New(db),
			AI: cannedOrchestrator(
				`[{"title":"Release process","description":"Cut from develop, fast-forward main, tag last.","priority":"medium"}]`),
		}
		sess := mcp.NewTestSession(fx.creatorID, fx.wsID, []string{"write:workspace"})
		brief := map[string]any{"contextDescription": "Summarise how the release process works."}

		require.Equal(t, 0, pagesInWorkspace(t, db, fx.wsID),
			"the fixture has to start with no pages for the counts below to mean anything")

		_, err := mcp.RunGeneratePage(ctx, deps, sess, mcpVisJSON(t, brief))
		require.NoError(t, err, "the first generation is the control the refusal below rests on")
		require.Equal(t, 1, pagesInWorkspace(t, db, fx.wsID))

		_, err = mcp.RunGeneratePage(ctx, deps, sess, mcpVisJSON(t, brief))
		requireDuplicateRefusal(t, err, apierrors.PagePageTitleTaken)
		require.Equal(t, 1, pagesInWorkspace(t, db, fx.wsID),
			"a refused generation must leave nothing partial behind")
	})
}
