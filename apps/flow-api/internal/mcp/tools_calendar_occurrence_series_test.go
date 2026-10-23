package mcp_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// continuingMaster finds the series a split created on the fixture's
// calendar: the one master other than the fixture's own.
func continuingMaster(t *testing.T, db *sql.DB, fx *recurringSeriesFixture) (uint32, string) {
	t.Helper()
	var (
		id  uint32
		pub []byte
	)
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT id, public_id FROM calendar_events
		  WHERE calendar_id = ? AND id <> ? AND recurrence_rule IS NOT NULL AND enabled = TRUE`,
		fx.calendarID, fx.seriesID).Scan(&id, &pub))
	parsed, err := uuid.FromBytes(pub)
	require.NoError(t, err)
	return id, parsed.String()
}

// seedMember adds a second enabled member of the fixture's workspace who
// can read its calendar.
func seedMember(t *testing.T, db *sql.DB, fx *recurringSeriesFixture) uint32 {
	t.Helper()
	ctx := context.Background()
	res, err := db.ExecContext(ctx,
		`INSERT INTO users (public_id, email, display_name, locale) VALUES (?, ?, ?, 'en')`,
		types.New(), "series-invitee-"+uuid.New().String()[:8]+"@example.test", "Series invitee")
	require.NoError(t, err)
	raw, err := res.LastInsertId()
	require.NoError(t, err)
	userID := uint32(raw) //#nosec G115 -- LastInsertId in test seed, fits uint32
	_, err = db.ExecContext(ctx,
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role) VALUES (?, ?, ?, 'member')`,
		types.New(), fx.wsID, userID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO calendar_members (public_id, workspace_id, calendar_id, user_id, role) VALUES (?, ?, ?, ?, 'viewer')`,
		types.New(), fx.wsID, fx.calendarID, userID)
	require.NoError(t, err)
	return userID
}

func seedSeriesAttendee(t *testing.T, db *sql.DB, fx *recurringSeriesFixture, userID uint32) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO calendar_event_attendees (public_id, workspace_id, event_id, user_id, rsvp, can_edit)
		 VALUES (?, ?, ?, ?, 'accepted', FALSE)`,
		types.New(), fx.wsID, fx.seriesID, userID)
	require.NoError(t, err)
}

// TestMCPSplitSeriesCarriesTheWholeSeriesForward drives the
// this-and-following split through the MCP tool and checks the continuing
// master against everything the series carried. REST splits through the
// same calendaroccurrence.SplitSeries.
func TestMCPSplitSeriesCarriesTheWholeSeriesForward(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	deps := occurrenceScopeDeps(db)
	ctx := context.Background()

	t.Run("a counted series keeps its total across the split", func(t *testing.T) {
		t.Parallel()
		fx := seedRecurringSeriesFixture(t, db)
		_, err := db.ExecContext(ctx,
			`UPDATE calendar_events SET recurrence_rule = CAST(? AS JSON) WHERE id = ?`,
			`{"freq":"daily","interval":1,"count":5}`, fx.seriesID)
		require.NoError(t, err)

		_, err = mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         fx.seriesPub.String(),
			"scope":           "thisAndFollowing",
			"occurrenceStart": seriesOccurrence(2).Unix(),
			"title":           "Stand-up (new format)",
		}))
		require.NoError(t, err)

		want := []string{
			occurrenceEntry(fx.seriesTitle, 0),
			occurrenceEntry(fx.seriesTitle, 1),
			occurrenceEntry("Stand-up (new format)", 2),
			occurrenceEntry("Stand-up (new format)", 3),
			occurrenceEntry("Stand-up (new format)", 4),
			occurrenceEntry("One-off review", 9),
		}
		sort.Strings(want)
		require.Equal(t, want, seriesDigest(t, deps, fx),
			"COUNT=5 split at the third occurrence must still be five occurrences, not 2 + 5")
	})

	t.Run("moving the continuing series keeps cancelled and replaced occurrences", func(t *testing.T) {
		t.Parallel()
		fx := seedRecurringSeriesFixture(t, db)
		moved := seriesOccurrence(4).Add(3 * time.Hour)

		_, err := mcp.RunDeleteCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         fx.seriesPub.String(),
			"scope":           "occurrence",
			"occurrenceStart": seriesOccurrence(3).Unix(),
		}))
		require.NoError(t, err)
		_, err = mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         fx.seriesPub.String(),
			"scope":           "occurrence",
			"occurrenceStart": seriesOccurrence(4).Unix(),
			"startAt":         moved.Unix(),
			"endAt":           moved.Add(time.Hour).Unix(),
		}))
		require.NoError(t, err)

		later := seriesOccurrence(2).Add(time.Hour)
		_, err = mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         fx.seriesPub.String(),
			"scope":           "thisAndFollowing",
			"occurrenceStart": seriesOccurrence(2).Unix(),
			"startAt":         later.Unix(),
			"endAt":           later.Add(time.Hour).Unix(),
		}))
		require.NoError(t, err)

		want := []string{
			occurrenceEntry(fx.seriesTitle, 0),
			occurrenceEntry(fx.seriesTitle, 1),
			fmt.Sprintf("%s|%d|%d", fx.seriesTitle, later.Unix(), later.Add(time.Hour).Unix()),
			fmt.Sprintf("%s|%d|%d", fx.seriesTitle, moved.Unix(), moved.Add(time.Hour).Unix()),
			occurrenceEntry("One-off review", 9),
		}
		sort.Strings(want)
		require.Equal(t, want, seriesDigest(t, deps, fx),
			"the cancelled occurrence must stay cancelled and the moved one must appear once")
	})

	t.Run("attendees, invites, shares, checklist and a sent reminder carry over", func(t *testing.T) {
		t.Parallel()
		fx := seedRecurringSeriesFixture(t, db)
		invitee := seedMember(t, db, fx)
		seedSeriesAttendee(t, db, fx, invitee)

		res, err := db.ExecContext(ctx,
			`INSERT INTO calendar_public_shares (public_id, workspace_id, token_hash, title) VALUES (?, ?, ?, 'Public')`,
			types.New(), fx.wsID, fmt.Sprintf("%x", sha256.Sum256([]byte(uuid.New().String()))))
		require.NoError(t, err)
		shareRaw, err := res.LastInsertId()
		require.NoError(t, err)
		_, err = db.ExecContext(ctx,
			`INSERT INTO calendar_public_share_events (public_id, workspace_id, share_id, event_id) VALUES (?, ?, ?, ?)`,
			types.New(), fx.wsID, shareRaw, fx.seriesID)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx,
			`INSERT INTO calendar_event_checklist_items (public_id, workspace_id, event_id, created_by_user_id, title, done)
			 VALUES (?, ?, ?, ?, 'Book the room', TRUE)`,
			types.New(), fx.wsID, fx.seriesID, fx.userID)
		require.NoError(t, err)
		// The reminder for the fourth occurrence has already gone out.
		_, err = db.ExecContext(ctx,
			`INSERT INTO calendar_event_reminders (workspace_id, event_id, occurrence_start) VALUES (?, ?, ?)`,
			fx.wsID, fx.seriesID, seriesOccurrence(3))
		require.NoError(t, err)

		_, err = mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         fx.seriesPub.String(),
			"scope":           "thisAndFollowing",
			"occurrenceStart": seriesOccurrence(2).Unix(),
			"title":           "Stand-up (new format)",
		}))
		require.NoError(t, err)

		newID, newPub := continuingMaster(t, db, fx)
		count := func(q string, args ...any) int {
			var n int
			require.NoError(t, db.QueryRowContext(ctx, q, args...).Scan(&n))
			return n
		}
		require.Equal(t, 1, count(
			`SELECT COUNT(*) FROM calendar_event_attendees WHERE event_id = ? AND user_id = ? AND enabled = TRUE`,
			newID, invitee), "the invitee must attend the continuing series")
		require.Equal(t, 1, count(
			`SELECT COUNT(*) FROM calendar_public_share_events WHERE event_id = ? AND share_id = ? AND enabled = TRUE`,
			newID, shareRaw), "the continuing series must stay published on the share")
		require.Equal(t, 1, count(
			`SELECT COUNT(*) FROM calendar_event_checklist_items WHERE event_id = ? AND title = 'Book the room' AND done = TRUE`,
			newID), "the checklist must carry over with its state")
		require.Equal(t, 1, count(
			`SELECT COUNT(*) FROM calendar_event_reminders WHERE event_id = ? AND occurrence_start = ?`,
			newID, seriesOccurrence(3)), "a reminder already sent must not be sent again under the new row")
		require.Equal(t, 1, count(
			`SELECT COUNT(*) FROM events
			  WHERE workspace_id = ? AND type = 'calendar.event.created'
			    AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.eventId')) = ?`,
			fx.wsID, newPub), "the split must announce the continuing master")
	})
}

// TestMCPOverrideKeepsTheSeriesReminderAndAudience covers the single
// occurrence path: an override is a new row, and neither a reminder the
// series already sent nor the series' attendees may be lost to that.
func TestMCPOverrideKeepsTheSeriesReminderAndAudience(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	deps := occurrenceScopeDeps(db)
	ctx := context.Background()

	fx := seedRecurringSeriesFixture(t, db)
	invitee := seedMember(t, db, fx)
	seedSeriesAttendee(t, db, fx, invitee)
	_, err := db.ExecContext(ctx,
		`INSERT INTO calendar_event_reminders (workspace_id, event_id, occurrence_start) VALUES (?, ?, ?)`,
		fx.wsID, fx.seriesID, seriesOccurrence(1))
	require.NoError(t, err)

	_, err = mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
		"eventId":         fx.seriesPub.String(),
		"scope":           "occurrence",
		"occurrenceStart": seriesOccurrence(1).Unix(),
		"title":           "Stand-up (renamed)",
	}))
	require.NoError(t, err)

	var overrideID uint32
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM calendar_events WHERE recurrence_parent_id = ? AND recurrence_original_start = ?`,
		fx.seriesID, seriesOccurrence(1)).Scan(&overrideID))

	var claims int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calendar_event_reminders WHERE event_id = ? AND occurrence_start = ?`,
		overrideID, seriesOccurrence(1)).Scan(&claims))
	require.Equal(t, 1, claims, "renaming an occurrence already reminded must not re-arm its reminder")

	rows, err := calendar.New(db).ListCalendarEventsByRange(ctx, calendar.ListCalendarEventsByRangeParams{
		ViewerUserID: invitee,
		CalendarID:   fx.calendarID,
		RangeStart:   sql.NullTime{Time: seriesOccurrence(1), Valid: true},
		RangeEnd:     sql.NullTime{Time: seriesOccurrence(2), Valid: true},
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Stand-up (renamed)", rows[0].Title)
	require.True(t, rows[0].IsAttendee, "a series invitee attends each of its overridden occurrences")
}
