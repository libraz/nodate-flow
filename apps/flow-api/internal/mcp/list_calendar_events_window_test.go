package mcp_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestListCalendarEventsBoundsItsWindow holds that a reversed or over-wide
// window is refused before any series is expanded, and that the widest
// permitted window is still answered.
func TestListCalendarEventsBoundsItsWindow(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedSharedCalendarFixture(t, db)
	ctx := context.Background()

	deps := mcp.Deps{DB: db, Queries: generated.New(db), CalendarQueries: calendar.New(db)}
	sess := mcp.NewTestSession(fx.managerID, fx.wsID, []string{"read:workspace"})

	start := time.Now().UTC().Truncate(24 * time.Hour).Unix()
	const day = int64(24 * 60 * 60)
	list := func(from, to int64) (any, error) {
		return mcp.RunListCalendarEvents(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
			"startAt": from,
			"endAt":   to,
		}))
	}

	_, err := list(start+day, start)
	requireSpec(t, err, apierrors.McpToolArgumentsInvalid)
	_, err = list(start, start)
	requireSpec(t, err, apierrors.McpToolArgumentsInvalid)
	_, err = list(start, start+367*day)
	requireSpec(t, err, apierrors.McpToolArgumentsInvalid)

	_, err = list(start, start+366*day)
	require.NoError(t, err, "the widest permitted window must still be answered")
}
