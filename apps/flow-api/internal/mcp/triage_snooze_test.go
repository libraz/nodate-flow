package mcp_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestTriageIntakeItemRequiresSnoozeDeadline proves a snooze without a
// deadline is refused.
//
// Nothing resurfaces a snoozed item: the intake queue filters on
// triage_status, and no job scans snooze_until. An item accepted as snoozed
// with a NULL deadline therefore left the pending list permanently, and the
// tool answered {"ok": true} — the caller was told it would come back.
//
// The guard is an argument check and runs before the item is read, so the
// ids below need not name real rows: reaching WS.INTAKE.NOT_FOUND instead
// would itself be the regression.
func TestTriageIntakeItemRequiresSnoozeDeadline(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedMCPVisibilityFixture(t, db)

	deps := mcp.Deps{DB: db, Queries: generated.New(db)}
	ctx := context.Background()
	s := mcp.NewTestSession(fx.creatorID, fx.wsID, []string{"write:workspace"})

	itemID := uuid.Must(uuid.NewV7()).String()

	tests := []struct {
		name string
		args map[string]any
	}{
		{"no deadline", map[string]any{"intakeItemId": itemID, "status": "snoozed"}},
		{"zero deadline", map[string]any{"intakeItemId": itemID, "status": "snoozed", "snoozeUntil": 0}},
		{"negative deadline", map[string]any{"intakeItemId": itemID, "status": "snoozed", "snoozeUntil": -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := mcp.RunTriageIntakeItem(ctx, deps, s, mcpVisJSON(t, tt.args))
			require.Error(t, err)
			require.Equal(t, apierrors.McpToolArgumentsInvalid.Code, apiErrorCode(t, err))
		})
	}

	// A snooze that carries a deadline gets past the argument check and is
	// refused for the reason it should be: the item does not exist.
	_, err := mcp.RunTriageIntakeItem(ctx, deps, s, mcpVisJSON(t, map[string]any{
		"intakeItemId": itemID,
		"status":       "snoozed",
		"snoozeUntil":  4_102_444_800,
	}))
	require.Error(t, err)
	require.Equal(t, apierrors.WsIntakeNotFound.Code, apiErrorCode(t, err))
}
