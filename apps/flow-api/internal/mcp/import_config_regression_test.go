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

// TestMCPCreateImportJobRejectsCredentials drives the tool rather than
// the validator, because the defect this closes was not a missing rule
// — it was a rule REST applied and MCP did not.
//
// import_jobs.config_json is plaintext, returned by the job read
// endpoints and carried into backups. github / jira / linear offer no
// other place to put a credential, so "paste your token in the
// configuration" is the obvious thing to try, and an agent is the
// caller most likely to be told to do it. A validator that only guards
// the REST handler leaves the column open through the surface this
// product points agents at.
//
// The row count is the assertion that matters: refused means nothing
// was written, not written-then-flagged.
func TestMCPCreateImportJobRejectsCredentials(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedMCPTrailFixture(t, db)

	ctx := context.Background()
	deps := mcp.Deps{DB: db, Queries: generated.New(db)}
	sess := mcp.NewTestSession(fx.userID, fx.wsID, []string{"write:workspace"})

	before := countImportJobs(t, db, fx.wsID)

	_, err := mcp.RunCreateImportJob(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
		"source":     "github",
		"configJson": `{"repo":"owner/name","token":"ghp_live"}`,
	}))
	requireSpec(t, err, apierrors.WsImportConfigSecretRejected)
	require.Equal(t, before, countImportJobs(t, db, fx.wsID),
		"a refused configuration must leave no row; the point is that the token is never stored")

	// Positive control: the same call without the credential succeeds,
	// so the guard is refusing the token rather than the source.
	_, err = mcp.RunCreateImportJob(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
		"source":     "github",
		"configJson": `{"repo":"owner/name"}`,
	}))
	require.NoError(t, err)
	require.Equal(t, before+1, countImportJobs(t, db, fx.wsID))
}

func countImportJobs(t *testing.T, db *sql.DB, wsID uint32) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM import_jobs WHERE workspace_id = ?`, wsID).Scan(&n))
	return n
}
