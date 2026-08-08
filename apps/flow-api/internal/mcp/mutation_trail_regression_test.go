package mcp_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// mcpTrailFixture is one tenant with everything an MCP mutation needs to
// land: a workspace the actor owns, a project, and a calendar they hold a
// membership on.
type mcpTrailFixture struct {
	wsID        uint32
	userID      uint32
	projectPub  uuid.UUID
	calendarPub uuid.UUID
	memoPub     uuid.UUID
}

func seedMCPTrailFixture(t *testing.T, db *sql.DB) *mcpTrailFixture {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	suffix := uuid.New().String()[:8]

	userPub := uuid.Must(uuid.NewV7())
	res, err := tx.ExecContext(ctx,
		`INSERT INTO users (public_id, email, display_name, locale) VALUES (?, ?, ?, 'en')`,
		userPub[:], "mcptrail-"+suffix+"@example.test", "MCPTrail "+suffix)
	require.NoError(t, err)
	userID64, err := res.LastInsertId()
	require.NoError(t, err)
	userID := uint32(userID64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	wsPub := uuid.Must(uuid.NewV7())
	res, err = tx.ExecContext(ctx,
		`INSERT INTO workspaces (public_id, slug, name) VALUES (?, ?, ?)`,
		wsPub[:], "mcptrail-ws-"+suffix, "MCPTrail Workspace")
	require.NoError(t, err)
	wsID64, err := res.LastInsertId()
	require.NoError(t, err)
	wsID := uint32(wsID64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	memberPub := uuid.Must(uuid.NewV7())
	_, err = tx.ExecContext(ctx,
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role) VALUES (?, ?, ?, 'owner')`,
		memberPub[:], wsID, userID)
	require.NoError(t, err)

	prjPub := uuid.Must(uuid.NewV7())
	_, err = tx.ExecContext(ctx,
		`INSERT INTO projects (public_id, workspace_id, slug, name, identifier) VALUES (?, ?, ?, ?, ?)`,
		prjPub[:], wsID, "mcptrail-prj-"+suffix, "MCPTrail Project", "MT"+suffix[:3])
	require.NoError(t, err)

	calPub := uuid.Must(uuid.NewV7())
	res, err = tx.ExecContext(ctx,
		`INSERT INTO calendars (public_id, workspace_id, kind, name, owner_user_id) VALUES (?, ?, 'personal', ?, ?)`,
		calPub[:], wsID, "MCPTrail Calendar", userID)
	require.NoError(t, err)
	calID64, err := res.LastInsertId()
	require.NoError(t, err)
	calID := uint32(calID64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	calMemberPub := uuid.Must(uuid.NewV7())
	_, err = tx.ExecContext(ctx,
		`INSERT INTO calendar_members (public_id, workspace_id, calendar_id, user_id, role) VALUES (?, ?, ?, ?, 'owner')`,
		calMemberPub[:], wsID, calID, userID)
	require.NoError(t, err)

	memoPub := uuid.Must(uuid.NewV7())
	_, err = tx.ExecContext(ctx,
		`INSERT INTO calendar_memos (public_id, workspace_id, calendar_id, created_by_user_id, title) VALUES (?, ?, ?, ?, ?)`,
		memoPub[:], wsID, calID, userID, "Bring the projector")
	require.NoError(t, err)

	require.NoError(t, tx.Commit())
	committed = true

	return &mcpTrailFixture{
		wsID:        wsID,
		userID:      userID,
		projectPub:  prjPub,
		calendarPub: calPub,
		memoPub:     memoPub,
	}
}

// countAuditRows counts audit_logs rows for one action inside one
// workspace. Scoped to the fixture's own workspace so a parallel run
// against the shared database cannot change the answer.
func countAuditRows(t *testing.T, db *sql.DB, wsID uint32, action string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM audit_logs WHERE workspace_id = ? AND action = ?`,
		wsID, action).Scan(&n))
	return n
}

// countEventRows counts events rows of one kind inside one workspace.
func countEventRows(t *testing.T, db *sql.DB, wsID uint32, kind string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = ?`,
		wsID, kind).Scan(&n))
	return n
}

func mcpTrailArgs(t *testing.T, v map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// TestMCPMutationsLeaveTheSameTrailAsREST drives the MCP tools the audit
// named and proves each one lands both halves of the record REST lands:
// the row in `events` that the timeline, notifications, webhooks and live
// streams read, and the row in `audit_logs` that an administrator queries
// by action name.
//
// Counts are taken before and after each call and asserted as deltas
// inside the fixture's own workspace, never as instance totals.
func TestMCPMutationsLeaveTheSameTrailAsREST(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedMCPTrailFixture(t, db)

	deps := mcp.Deps{DB: db, Queries: generated.New(db), CalendarQueries: calendar.New(db)}
	ctx := context.Background()
	sess := mcp.NewTestSession(fx.userID, fx.wsID, []string{"write:workspace"})

	var eventPublicID string

	t.Run("create_calendar_event", func(t *testing.T) {
		beforeAudit := countAuditRows(t, db, fx.wsID, "calendar.event.create")
		beforeEvent := countEventRows(t, db, fx.wsID, "calendar.event.created")

		start := time.Now().UTC().Add(time.Hour).Unix()
		out, err := mcp.RunCreateCalendarEvent(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
			"calendarId": fx.calendarPub.String(),
			"title":      "Agent-scheduled review",
			"startAt":    start,
			"endAt":      start + 3600,
		}))
		require.NoError(t, err)
		m, ok := out.(map[string]any)
		require.True(t, ok)
		eventPublicID, ok = m["id"].(string)
		require.True(t, ok)

		require.Equal(t, beforeAudit+1, countAuditRows(t, db, fx.wsID, "calendar.event.create"),
			"an agent's calendar entry must reach audit_logs under the same action REST records")
		require.Equal(t, beforeEvent+1, countEventRows(t, db, fx.wsID, "calendar.event.created"),
			"an agent's calendar entry must reach the event log, or it appears on no timeline and fires no webhook")
	})

	t.Run("update_calendar_event", func(t *testing.T) {
		require.NotEmpty(t, eventPublicID, "create_calendar_event must have run first")
		beforeAudit := countAuditRows(t, db, fx.wsID, "calendar.event.update")
		beforeEvent := countEventRows(t, db, fx.wsID, "calendar.event.updated")

		title := "Agent-rescheduled review"
		_, err := mcp.RunUpdateCalendarEvent(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
			"eventId": eventPublicID,
			"title":   title,
		}))
		require.NoError(t, err)

		require.Equal(t, beforeAudit+1, countAuditRows(t, db, fx.wsID, "calendar.event.update"))
		require.Equal(t, beforeEvent+1, countEventRows(t, db, fx.wsID, "calendar.event.updated"),
			"an edit to a standalone event never reaches itemkit, so this branch must append its own event")
	})

	t.Run("delete_calendar_event", func(t *testing.T) {
		require.NotEmpty(t, eventPublicID, "create_calendar_event must have run first")
		beforeAudit := countAuditRows(t, db, fx.wsID, "calendar.event.delete")

		_, err := mcp.RunDeleteCalendarEvent(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
			"eventId": eventPublicID,
		}))
		require.NoError(t, err)

		require.Equal(t, beforeAudit+1, countAuditRows(t, db, fx.wsID, "calendar.event.delete"))
	})

	t.Run("toggle_calendar_memo", func(t *testing.T) {
		beforeAudit := countAuditRows(t, db, fx.wsID, "calendar.memo.update")
		beforeEvent := countEventRows(t, db, fx.wsID, "calendar.memo.updated")

		_, err := mcp.RunToggleCalendarMemo(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
			"memoId":     fx.memoPub.String(),
			"calendarId": fx.calendarPub.String(),
			"done":       true,
		}))
		require.NoError(t, err)

		require.Equal(t, beforeAudit+1, countAuditRows(t, db, fx.wsID, "calendar.memo.update"))
		require.Equal(t, beforeEvent+1, countEventRows(t, db, fx.wsID, "calendar.memo.updated"))
	})

	t.Run("export_tasks", func(t *testing.T) {
		beforeAudit := countAuditRows(t, db, fx.wsID, "export.create")
		beforeEvent := countEventRows(t, db, fx.wsID, "export.requested")

		_, err := mcp.RunExportTasks(ctx, deps, sess, mcpTrailArgs(t, map[string]any{}))
		require.NoError(t, err)

		require.Equal(t, beforeAudit+1, countAuditRows(t, db, fx.wsID, "export.create"),
			"bulk extraction over MCP must be visible to the same audit query that finds a REST export")
		require.Equal(t, beforeEvent+1, countEventRows(t, db, fx.wsID, "export.requested"))
	})

	t.Run("create_import_job", func(t *testing.T) {
		beforeAudit := countAuditRows(t, db, fx.wsID, "import.create")
		beforeEvent := countEventRows(t, db, fx.wsID, "import.job.created")

		args := mcpTrailArgs(t, map[string]any{
			"source":    "csv",
			"projectId": fx.projectPub.String(),
		})
		_, err := mcp.RunCreateImportJob(ctx, deps, sess, args)
		require.NoError(t, err)

		require.Equal(t, beforeAudit+1, countAuditRows(t, db, fx.wsID, "import.create"))
		require.Equal(t, beforeEvent+1, countEventRows(t, db, fx.wsID, "import.job.created"))

		// The concurrency guard REST enforces: a second import into the
		// same project while the first is still pending is refused, and
		// refusing it must not leave a second trail behind either.
		_, err = mcp.RunCreateImportJob(ctx, deps, sess, args)
		requireSpec(t, err, apierrors.WsImportAlreadyRunning)
		require.Equal(t, beforeAudit+1, countAuditRows(t, db, fx.wsID, "import.create"))
		require.Equal(t, beforeEvent+1, countEventRows(t, db, fx.wsID, "import.job.created"))
	})
}

// TestMCPListImportJobsFiltersBeforePaging builds the situation the audit
// described: the only failed imports in a workspace are its oldest, and
// the listing is ordered newest first. Filtering after the page is taken
// answers "are there failed imports?" with "no" — which an agent reports
// to its user as a fact about the workspace.
func TestMCPListImportJobsFiltersBeforePaging(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedMCPTrailFixture(t, db)

	ctx := context.Background()
	base := time.Now().UTC().Add(-72 * time.Hour)
	const (
		totalJobs  = 60
		failedJobs = 3
	)
	for i := 0; i < totalJobs; i++ {
		status := "completed"
		if i < failedJobs {
			status = "failed"
		}
		pub := uuid.Must(uuid.NewV7())
		_, err := db.ExecContext(ctx,
			`INSERT INTO import_jobs (public_id, workspace_id, source, status, config_json, created_at)
			 VALUES (?, ?, 'csv', ?, '{}', ?)`,
			pub[:], fx.wsID, status, base.Add(time.Duration(i)*time.Minute))
		require.NoError(t, err)
	}

	deps := mcp.Deps{DB: db, Queries: generated.New(db)}
	sess := mcp.NewTestSession(fx.userID, fx.wsID, []string{"read:workspace"})

	filtered := decodeImportJobList(ctx, t, deps, sess, map[string]any{"status": "failed", "limit": 10})
	require.Len(t, filtered.Jobs, failedJobs,
		"the failed imports are the oldest %d of %d; a filter applied after the page finds none of them",
		failedJobs, totalJobs)
	for _, j := range filtered.Jobs {
		require.Equal(t, "failed", j.Status)
	}
	require.Equal(t, int64(failedJobs), filtered.Total,
		"total must count the matches, so a caller can tell an empty page from an empty workspace")

	// Positive control: without the filter the same call returns the
	// newest page, which holds none of the failed jobs. A listing that
	// ignored the filter and returned everything would satisfy the
	// assertion above for the wrong reason.
	unfiltered := decodeImportJobList(ctx, t, deps, sess, map[string]any{"limit": 10})
	require.Equal(t, int64(totalJobs), unfiltered.Total)
	require.Len(t, unfiltered.Jobs, 10)
	for _, j := range unfiltered.Jobs {
		require.Equal(t, "completed", j.Status,
			"the newest page must hold no failed job, or the fixture is not the situation under test")
	}
}

type importJobListResult struct {
	Total int64 `json:"total"`
	Jobs  []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"jobs"`
}

// decodeImportJobList runs list_import_jobs and reads the result through
// JSON, which is the shape a caller actually receives.
func decodeImportJobList(
	ctx context.Context, t *testing.T, deps mcp.Deps, sess *mcp.TestSession, args map[string]any,
) importJobListResult {
	t.Helper()
	out, err := mcp.RunListImportJobs(ctx, deps, sess, mcpTrailArgs(t, args))
	require.NoError(t, err)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	var res importJobListResult
	require.NoError(t, json.Unmarshal(raw, &res))
	return res
}
