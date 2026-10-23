package mcp_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestToggleCalendarMemoRefusesAMemoThatIsNotThere holds that the tool
// answers CALENDAR.MEMO.NOT_FOUND, and records nothing, for a memo id that
// resolves to no enabled memo on the named calendar, while a memo that is
// there is toggled and recorded.
func TestToggleCalendarMemoRefusesAMemoThatIsNotThere(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedSharedCalendarFixture(t, db)
	ctx := context.Background()

	deps := mcp.Deps{DB: db, Queries: generated.New(db), CalendarQueries: calendar.New(db)}
	sess := mcp.NewTestSession(fx.managerID, fx.wsID, []string{"write:workspace"})

	memoPub := uuid.Must(uuid.NewV7())
	_, err := db.ExecContext(ctx,
		`INSERT INTO calendar_memos (public_id, workspace_id, calendar_id, created_by_user_id, title) VALUES (?, ?, ?, ?, 'Buy milk')`,
		memoPub[:], fx.wsID, fx.calendarID, fx.managerID)
	require.NoError(t, err)

	recorded := func() (events, audits int) {
		require.NoError(t, db.QueryRow(
			`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = 'calendar.memo.updated'`, fx.wsID).Scan(&events))
		require.NoError(t, db.QueryRow(
			`SELECT COUNT(*) FROM audit_logs WHERE workspace_id = ? AND action = 'calendar.memo.update'`, fx.wsID).Scan(&audits))
		return events, audits
	}
	toggle := func(memoID string) (any, error) {
		return mcp.RunToggleCalendarMemo(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
			"memoId":     memoID,
			"calendarId": fx.calendarPub.String(),
			"done":       true,
		}))
	}

	beforeEvents, beforeAudits := recorded()
	_, err = toggle(uuid.Must(uuid.NewV7()).String())
	requireSpec(t, err, apierrors.CalendarMemoNotFound)
	_, err = toggle("not-a-uuid")
	requireSpec(t, err, apierrors.CalendarMemoNotFound)
	afterEvents, afterAudits := recorded()
	require.Equal(t, beforeEvents, afterEvents, "a refused toggle must write no event")
	require.Equal(t, beforeAudits, afterAudits, "a refused toggle must write no audit row")

	_, err = toggle(memoPub.String())
	require.NoError(t, err)
	var done bool
	require.NoError(t, db.QueryRow(`SELECT done FROM calendar_memos WHERE public_id = ?`, memoPub[:]).Scan(&done))
	require.True(t, done)
}
