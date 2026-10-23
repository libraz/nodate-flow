package mcp_test

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestMCPCalendarWritesFollowTheSharedSeriesTransforms holds the calendar
// tools to the transforms REST's calendar routes go through: an occurrence
// is addressed by an instant the rule produces, a moved occurrence deleted
// on its own stays deleted, a series moved in time brings its exceptions
// along, and an emptied text field is stored as NULL.
func TestMCPCalendarWritesFollowTheSharedSeriesTransforms(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	deps := occurrenceScopeDeps(db)
	ctx := context.Background()

	t.Run("delete/an occurrenceStart the rule does not produce is refused", func(t *testing.T) {
		t.Parallel()
		cases := map[string]time.Time{
			"off the rule":     seriesOccurrence(1).Add(30 * time.Minute),
			"before the start": seriesOccurrence(-1),
			"after the end":    seriesOccurrence(6),
		}
		for name, at := range cases {
			fx := seedRecurringSeriesFixture(t, db)
			_, err := mcp.RunDeleteCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
				"eventId":         fx.seriesPub.String(),
				"scope":           "occurrence",
				"occurrenceStart": at.Unix(),
			}))
			requireSpec(t, err, apierrors.CalendarEventOccurrenceStartNotInSeries)
			require.Equalf(t, wholeSeriesDigest(fx.seriesTitle), seriesDigest(t, deps, fx),
				"%s: a refused delete must leave the series as it was", name)
		}
	})

	t.Run("delete/thisAndFollowing at an instant the rule does not produce is refused as such", func(t *testing.T) {
		t.Parallel()
		fx := seedRecurringSeriesFixture(t, db)
		_, err := mcp.RunDeleteCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         fx.seriesPub.String(),
			"scope":           "thisAndFollowing",
			"occurrenceStart": seriesOccurrence(2).Add(time.Minute).Unix(),
		}))
		requireSpec(t, err, apierrors.CalendarEventOccurrenceStartNotInSeries)
		require.Equal(t, wholeSeriesDigest(fx.seriesTitle), seriesDigest(t, deps, fx))
	})

	t.Run("delete/a moved occurrence deleted on its own stays deleted", func(t *testing.T) {
		t.Parallel()
		fx := seedRecurringSeriesFixture(t, db)
		moved := seriesOccurrence(2).Add(3 * time.Hour)
		_, err := mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         fx.seriesPub.String(),
			"scope":           "occurrence",
			"occurrenceStart": seriesOccurrence(2).Unix(),
			"startAt":         moved.Unix(),
			"endAt":           moved.Add(time.Hour).Unix(),
		}))
		require.NoError(t, err)

		var overridePub []byte
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT public_id FROM calendar_events
			  WHERE recurrence_parent_id = ? AND recurrence_original_start = ? AND enabled = TRUE`,
			fx.seriesID, seriesOccurrence(2)).Scan(&overridePub))
		overrideUUID, err := uuid.FromBytes(overridePub)
		require.NoError(t, err)
		overrideID := overrideUUID.String()

		_, err = mcp.RunDeleteCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId": overrideID,
		}))
		require.NoError(t, err)

		want := []string{
			occurrenceEntry(fx.seriesTitle, 0),
			occurrenceEntry(fx.seriesTitle, 1),
			occurrenceEntry(fx.seriesTitle, 3),
			occurrenceEntry(fx.seriesTitle, 4),
			occurrenceEntry("One-off review", 9),
		}
		sort.Strings(want)
		require.Equal(t, want, seriesDigest(t, deps, fx),
			"neither the moved copy nor the original slot may come back")
	})

	t.Run("update/moving the series keeps a cancelled occurrence cancelled", func(t *testing.T) {
		t.Parallel()
		fx := seedRecurringSeriesFixture(t, db)
		_, err := mcp.RunDeleteCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         fx.seriesPub.String(),
			"scope":           "occurrence",
			"occurrenceStart": seriesOccurrence(1).Unix(),
		}))
		require.NoError(t, err)

		shift := 2 * time.Hour
		_, err = mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId": fx.seriesPub.String(),
			"startAt": seriesOccurrence(0).Add(shift).Unix(),
			"endAt":   seriesOccurrence(0).Add(shift + time.Hour).Unix(),
		}))
		require.NoError(t, err)

		entry := func(n int) string {
			start := seriesOccurrence(n).Add(shift)
			return fmt.Sprintf("%s|%d|%d", fx.seriesTitle, start.Unix(), start.Add(time.Hour).Unix())
		}
		want := []string{entry(0), entry(2), entry(3), entry(4), occurrenceEntry("One-off review", 9)}
		sort.Strings(want)
		require.Equal(t, want, seriesDigest(t, deps, fx),
			"the cancelled occurrence must follow the series rather than reappear at its new time")
	})

	t.Run("update/an empty location, memo or block label clears it to NULL", func(t *testing.T) {
		t.Parallel()
		fx := seedRecurringSeriesFixture(t, db)
		_, err := db.ExecContext(ctx,
			`UPDATE calendar_events SET location = 'Room 1', memo = 'Bring notes', block_label = 'Focus' WHERE public_id = ?`,
			fx.oneOffPub[:])
		require.NoError(t, err)

		_, err = mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":    fx.oneOffPub.String(),
			"location":   "",
			"memo":       "",
			"blockLabel": "",
		}))
		require.NoError(t, err)

		var location, memo, blockLabel sql.NullString
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT location, memo, block_label FROM calendar_events WHERE public_id = ?`,
			fx.oneOffPub[:]).Scan(&location, &memo, &blockLabel))
		require.False(t, location.Valid, "an emptied location must be NULL, as REST's clear leaves it")
		require.False(t, memo.Valid, "an emptied memo must be NULL")
		require.False(t, blockLabel.Valid, "an emptied block label must be NULL")
	})
}
