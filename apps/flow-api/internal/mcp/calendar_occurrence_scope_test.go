package mcp_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/calendars"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// seriesAnchor is the first occurrence of every series seeded here.
//
// A fixed instant far from now, so the window the assertions read holds
// the whole series whenever the suite runs and nothing outside the
// fixture can drift into it.
var seriesAnchor = time.Date(2030, 3, 4, 9, 0, 0, 0, time.UTC)

// seriesRule repeats daily and stops on the fifth day, so the fixture has
// five occurrences: one before the split point the tests use, one at it,
// and three after.
//
// Bounded by until rather than by count on purpose. A count travels with
// the rule into the series a "this and following" split creates, so the
// remainder would start counting again and produce five more occurrences
// from the split; an until is an instant, and both halves of a split
// series stop at the same one.
const seriesRule = `{"freq":"daily","interval":1,"until":"2030-03-08"}`

// seriesOccurrence returns the start of the nth occurrence, counting from
// zero.
func seriesOccurrence(n int) time.Time {
	return seriesAnchor.AddDate(0, 0, n)
}

// recurringSeriesFixture is one tenant holding a calendar its only member
// owns, a recurring event on it, and a one-off event alongside.
type recurringSeriesFixture struct {
	wsID        uint32
	userID      uint32
	calendarID  uint32
	seriesID    uint32
	seriesPub   uuid.UUID
	oneOffPub   uuid.UUID
	seriesTitle string
}

func seedRecurringSeriesFixture(t *testing.T, db *sql.DB) *recurringSeriesFixture {
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

	wsPub := uuid.Must(uuid.NewV7())
	res, err := tx.ExecContext(ctx,
		`INSERT INTO workspaces (public_id, slug, name) VALUES (?, ?, ?)`,
		wsPub[:], "recurscope-ws-"+suffix, "RecurScope Workspace")
	require.NoError(t, err)
	wsID64, err := res.LastInsertId()
	require.NoError(t, err)
	wsID := uint32(wsID64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	userPub := uuid.Must(uuid.NewV7())
	res, err = tx.ExecContext(ctx,
		`INSERT INTO users (public_id, email, display_name, locale) VALUES (?, ?, ?, 'en')`,
		userPub[:], "recurscope-"+suffix+"@example.test", "RecurScope Owner")
	require.NoError(t, err)
	userID64, err := res.LastInsertId()
	require.NoError(t, err)
	userID := uint32(userID64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	wmPub := uuid.Must(uuid.NewV7())
	_, err = tx.ExecContext(ctx,
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role) VALUES (?, ?, ?, 'member')`,
		wmPub[:], wsID, userID)
	require.NoError(t, err)

	calPub := uuid.Must(uuid.NewV7())
	res, err = tx.ExecContext(ctx,
		`INSERT INTO calendars (public_id, workspace_id, kind, name) VALUES (?, ?, 'personal', ?)`,
		calPub[:], wsID, "RecurScope Calendar")
	require.NoError(t, err)
	calID64, err := res.LastInsertId()
	require.NoError(t, err)
	calID := uint32(calID64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	cmPub := uuid.Must(uuid.NewV7())
	_, err = tx.ExecContext(ctx,
		`INSERT INTO calendar_members (public_id, workspace_id, calendar_id, user_id, role) VALUES (?, ?, ?, ?, 'owner')`,
		cmPub[:], wsID, calID, userID)
	require.NoError(t, err)

	const seriesTitle = "Daily stand-up"
	seriesPub := uuid.Must(uuid.NewV7())
	res, err = tx.ExecContext(ctx,
		`INSERT INTO calendar_events
		   (public_id, workspace_id, calendar_id, title, start_at, end_at, timezone,
		    owner_user_id, created_by_user_id, recurrence_rule)
		 VALUES (?, ?, ?, ?, ?, ?, 'UTC', ?, ?, CAST(? AS JSON))`,
		seriesPub[:], wsID, calID, seriesTitle,
		seriesAnchor, seriesAnchor.Add(time.Hour), userID, userID, seriesRule)
	require.NoError(t, err)
	seriesID64, err := res.LastInsertId()
	require.NoError(t, err)
	seriesID := uint32(seriesID64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	// A row on the same calendar that does not repeat, so the refusal a
	// non-series scope earns on it can be driven without a second tenant.
	oneOffPub := uuid.Must(uuid.NewV7())
	_, err = tx.ExecContext(ctx,
		`INSERT INTO calendar_events
		   (public_id, workspace_id, calendar_id, title, start_at, end_at, timezone,
		    owner_user_id, created_by_user_id)
		 VALUES (?, ?, ?, ?, ?, ?, 'UTC', ?, ?)`,
		oneOffPub[:], wsID, calID, "One-off review",
		seriesOccurrence(9), seriesOccurrence(9).Add(time.Hour), userID, userID)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())
	committed = true

	return &recurringSeriesFixture{
		wsID:        wsID,
		userID:      userID,
		calendarID:  calID,
		seriesID:    seriesID,
		seriesPub:   seriesPub,
		oneOffPub:   oneOffPub,
		seriesTitle: seriesTitle,
	}
}

func (fx *recurringSeriesFixture) session() *mcp.TestSession {
	return mcp.NewTestSession(fx.userID, fx.wsID, []string{"read:workspace", "write:workspace"})
}

// seriesDigest is what the caller's own calendar looks like across the
// window the fixture's series lives in: one entry per occurrence, naming
// its title and its window and nothing else.
//
// Read through list_calendar_events rather than off the rows, because
// that is where an occurrence and its override stop being two rows and
// become the schedule somebody reads. Public ids are deliberately left
// out so two tenants seeded the same way produce comparable digests —
// which is how "an omitted scope and an explicit series scope are the
// same call" is stated as an equality rather than as two lists of
// assertions that happen to match.
func seriesDigest(t *testing.T, deps mcp.Deps, fx *recurringSeriesFixture) []string {
	t.Helper()
	out, err := mcp.RunListCalendarEvents(context.Background(), deps, fx.session(), mcpTrailArgs(t, map[string]any{
		"startAt": seriesOccurrence(-3).Unix(),
		"endAt":   seriesOccurrence(12).Unix(),
	}))
	require.NoError(t, err)

	m, ok := out.(map[string]any)
	require.True(t, ok)
	events, ok := m["events"].([]map[string]any)
	require.Truef(t, ok, "unexpected events shape %T", m["events"])

	digest := make([]string, 0, len(events))
	for _, e := range events {
		startAt, ok := e["startAt"].(int64)
		require.Truef(t, ok, "occurrence has no startAt: %v", e)
		endAt, ok := e["endAt"].(int64)
		require.Truef(t, ok, "occurrence has no endAt: %v", e)
		digest = append(digest, fmt.Sprintf("%s|%d|%d", e["title"], startAt, endAt))
	}
	sort.Strings(digest)
	return digest
}

// wholeSeriesDigest is the digest of an untouched fixture: five hour-long
// occurrences under the series title, plus the one-off row.
func wholeSeriesDigest(title string) []string {
	digest := make([]string, 0, 6)
	for n := range 5 {
		start := seriesOccurrence(n)
		digest = append(digest, fmt.Sprintf("%s|%d|%d", title, start.Unix(), start.Add(time.Hour).Unix()))
	}
	start := seriesOccurrence(9)
	digest = append(digest, fmt.Sprintf("One-off review|%d|%d", start.Unix(), start.Add(time.Hour).Unix()))
	sort.Strings(digest)
	return digest
}

// occurrenceEntry renders one expected occurrence into the digest's shape.
func occurrenceEntry(title string, n int) string {
	start := seriesOccurrence(n)
	return fmt.Sprintf("%s|%d|%d", title, start.Unix(), start.Add(time.Hour).Unix())
}

func occurrenceScopeDeps(db *sql.DB) mcp.Deps {
	return mcp.Deps{DB: db, Queries: generated.New(db), CalendarQueries: calendar.New(db)}
}

// TestMCPCalendarScopeReachesOnlyTheOccurrencesItNames drives every value
// of the scope argument against a five-occurrence series and reads back
// what a caller's calendar then shows.
//
// Each case asserts on the occurrences that were meant to stay as much as
// on the ones that were meant to change. A scope that reaches too far
// leaves a calendar that still looks plausible — every occurrence is
// there, carrying the wrong title or missing outright — so counting the
// changed ones alone cannot tell a correct edit from one that rewrote the
// series.
func TestMCPCalendarScopeReachesOnlyTheOccurrencesItNames(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	deps := occurrenceScopeDeps(db)
	ctx := context.Background()

	t.Run("update/occurrence changes one and leaves four", func(t *testing.T) {
		t.Parallel()
		fx := seedRecurringSeriesFixture(t, db)
		require.Equal(t, wholeSeriesDigest(fx.seriesTitle), seriesDigest(t, deps, fx),
			"the fixture has to start as five occurrences of one series")

		_, err := mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         fx.seriesPub.String(),
			"scope":           "occurrence",
			"occurrenceStart": seriesOccurrence(1).Unix(),
			"title":           "Stand-up (this week only)",
		}))
		require.NoError(t, err)

		want := []string{
			occurrenceEntry(fx.seriesTitle, 0),
			occurrenceEntry("Stand-up (this week only)", 1),
			occurrenceEntry(fx.seriesTitle, 2),
			occurrenceEntry(fx.seriesTitle, 3),
			occurrenceEntry(fx.seriesTitle, 4),
			occurrenceEntry("One-off review", 9),
		}
		sort.Strings(want)
		require.Equal(t, want, seriesDigest(t, deps, fx))

		// The changed occurrence is a row of its own, pinned to the
		// occurrence it replaces rather than to wherever the edit moved
		// it.
		var overrides int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM calendar_events
			  WHERE recurrence_parent_id = ? AND recurrence_original_start = ? AND enabled = TRUE`,
			fx.seriesID, seriesOccurrence(1)).Scan(&overrides))
		require.Equal(t, 1, overrides, "the edited occurrence has to be one override row")

		// And the series itself was not rewritten.
		var masterTitle string
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT title FROM calendar_events WHERE id = ?`, fx.seriesID).Scan(&masterTitle))
		require.Equal(t, fx.seriesTitle, masterTitle)
	})

	t.Run("update/occurrence moves only the occurrence it names", func(t *testing.T) {
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

		want := []string{
			occurrenceEntry(fx.seriesTitle, 0),
			occurrenceEntry(fx.seriesTitle, 1),
			fmt.Sprintf("%s|%d|%d", fx.seriesTitle, moved.Unix(), moved.Add(time.Hour).Unix()),
			occurrenceEntry(fx.seriesTitle, 3),
			occurrenceEntry(fx.seriesTitle, 4),
			occurrenceEntry("One-off review", 9),
		}
		sort.Strings(want)
		require.Equal(t, want, seriesDigest(t, deps, fx),
			"a moved occurrence must appear once, at its new time")
	})

	t.Run("update/thisAndFollowing splits the series at the occurrence", func(t *testing.T) {
		t.Parallel()
		fx := seedRecurringSeriesFixture(t, db)

		_, err := mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
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
		require.Equal(t, want, seriesDigest(t, deps, fx))

		// The original series keeps its title and stops before the split,
		// which is what leaves the two occurrences before it alone.
		var masterTitle string
		var recurrenceEnd sql.NullTime
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT title, recurrence_end FROM calendar_events WHERE id = ?`,
			fx.seriesID).Scan(&masterTitle, &recurrenceEnd))
		require.Equal(t, fx.seriesTitle, masterTitle)
		require.True(t, recurrenceEnd.Valid, "the truncated series has to carry an end")
		require.True(t, recurrenceEnd.Time.UTC().Before(seriesOccurrence(2)),
			"the series has to stop before the occurrence the split named")
	})

	t.Run("update/series changes every occurrence", func(t *testing.T) {
		t.Parallel()
		fx := seedRecurringSeriesFixture(t, db)

		_, err := mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId": fx.seriesPub.String(),
			"scope":   "series",
			"title":   "Stand-up (renamed)",
		}))
		require.NoError(t, err)
		require.Equal(t, wholeSeriesDigest("Stand-up (renamed)"), seriesDigest(t, deps, fx))
	})

	t.Run("update/an omitted scope is the series scope", func(t *testing.T) {
		t.Parallel()
		// Two tenants seeded identically, one call each, and the two
		// calendars compared. The digest carries no ids, so the only way
		// these agree is that the two calls did the same thing.
		omitted := seedRecurringSeriesFixture(t, db)
		explicit := seedRecurringSeriesFixture(t, db)

		_, err := mcp.RunUpdateCalendarEvent(ctx, deps, omitted.session(), mcpTrailArgs(t, map[string]any{
			"eventId": omitted.seriesPub.String(),
			"title":   "Stand-up (renamed)",
		}))
		require.NoError(t, err)

		_, err = mcp.RunUpdateCalendarEvent(ctx, deps, explicit.session(), mcpTrailArgs(t, map[string]any{
			"eventId": explicit.seriesPub.String(),
			"scope":   "series",
			"title":   "Stand-up (renamed)",
		}))
		require.NoError(t, err)

		require.Equal(t, seriesDigest(t, deps, explicit), seriesDigest(t, deps, omitted),
			"a call that names no scope has to land exactly where an explicit series scope lands")
		require.Equal(t, wholeSeriesDigest("Stand-up (renamed)"), seriesDigest(t, deps, omitted),
			"and that is every occurrence of the series")
	})

	t.Run("delete/occurrence removes one and leaves four", func(t *testing.T) {
		t.Parallel()
		fx := seedRecurringSeriesFixture(t, db)

		_, err := mcp.RunDeleteCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         fx.seriesPub.String(),
			"scope":           "occurrence",
			"occurrenceStart": seriesOccurrence(3).Unix(),
		}))
		require.NoError(t, err)

		want := []string{
			occurrenceEntry(fx.seriesTitle, 0),
			occurrenceEntry(fx.seriesTitle, 1),
			occurrenceEntry(fx.seriesTitle, 2),
			occurrenceEntry(fx.seriesTitle, 4),
			occurrenceEntry("One-off review", 9),
		}
		sort.Strings(want)
		require.Equal(t, want, seriesDigest(t, deps, fx))

		// The series survives the cancellation: a delete that disabled the
		// master would leave the same four occurrences missing rather than
		// the one that was named.
		var enabled bool
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT enabled FROM calendar_events WHERE id = ?`, fx.seriesID).Scan(&enabled))
		require.True(t, enabled, "cancelling one occurrence must not delete the series")
	})

	t.Run("delete/occurrence also withdraws an override standing in for it", func(t *testing.T) {
		t.Parallel()
		// An occurrence that was moved is still that occurrence. Cancelling
		// it through the master alone would suppress what the rule produces
		// and leave the moved copy on the calendar.
		fx := seedRecurringSeriesFixture(t, db)
		moved := seriesOccurrence(1).Add(5 * time.Hour)

		_, err := mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         fx.seriesPub.String(),
			"scope":           "occurrence",
			"occurrenceStart": seriesOccurrence(1).Unix(),
			"startAt":         moved.Unix(),
			"endAt":           moved.Add(time.Hour).Unix(),
		}))
		require.NoError(t, err)

		_, err = mcp.RunDeleteCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         fx.seriesPub.String(),
			"scope":           "occurrence",
			"occurrenceStart": seriesOccurrence(1).Unix(),
		}))
		require.NoError(t, err)

		want := []string{
			occurrenceEntry(fx.seriesTitle, 0),
			occurrenceEntry(fx.seriesTitle, 2),
			occurrenceEntry(fx.seriesTitle, 3),
			occurrenceEntry(fx.seriesTitle, 4),
			occurrenceEntry("One-off review", 9),
		}
		sort.Strings(want)
		require.Equal(t, want, seriesDigest(t, deps, fx),
			"the moved copy has to go with the occurrence it replaced")
	})

	t.Run("delete/thisAndFollowing keeps what came before it", func(t *testing.T) {
		t.Parallel()
		fx := seedRecurringSeriesFixture(t, db)

		_, err := mcp.RunDeleteCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         fx.seriesPub.String(),
			"scope":           "thisAndFollowing",
			"occurrenceStart": seriesOccurrence(2).Unix(),
		}))
		require.NoError(t, err)

		want := []string{
			occurrenceEntry(fx.seriesTitle, 0),
			occurrenceEntry(fx.seriesTitle, 1),
			occurrenceEntry("One-off review", 9),
		}
		sort.Strings(want)
		require.Equal(t, want, seriesDigest(t, deps, fx))

		var enabled bool
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT enabled FROM calendar_events WHERE id = ?`, fx.seriesID).Scan(&enabled))
		require.True(t, enabled, "stopping a series must not delete the part that already happened")
	})

	t.Run("delete/an omitted scope is the series scope", func(t *testing.T) {
		t.Parallel()
		omitted := seedRecurringSeriesFixture(t, db)
		explicit := seedRecurringSeriesFixture(t, db)

		_, err := mcp.RunDeleteCalendarEvent(ctx, deps, omitted.session(), mcpTrailArgs(t, map[string]any{
			"eventId": omitted.seriesPub.String(),
		}))
		require.NoError(t, err)

		_, err = mcp.RunDeleteCalendarEvent(ctx, deps, explicit.session(), mcpTrailArgs(t, map[string]any{
			"eventId": explicit.seriesPub.String(),
			"scope":   "series",
		}))
		require.NoError(t, err)

		require.Equal(t, seriesDigest(t, deps, explicit), seriesDigest(t, deps, omitted),
			"a call that names no scope has to land exactly where an explicit series scope lands")
		// Every occurrence of the series is gone; the one-off row is the
		// control that proves the calendar was not emptied.
		require.Equal(t,
			[]string{occurrenceEntry("One-off review", 9)},
			seriesDigest(t, deps, omitted))
	})
}

// TestMCPCalendarScopeRefusalsNameTheCondition pins what the two tools
// answer for a scope that names no occurrence to act on.
//
// Each of these is a request an agent can correct, and only if it is told
// which correction: a generic bad-argument answer says the call was
// rejected without saying whether the row repeats at all, whether the id
// already names one occurrence of something else, or that the occurrence
// itself was never named. Every refusal below also has to leave the
// calendar exactly as it was.
func TestMCPCalendarScopeRefusalsNameTheCondition(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	deps := occurrenceScopeDeps(db)
	ctx := context.Background()

	t.Run("update/occurrence without an occurrenceStart", func(t *testing.T) {
		t.Parallel()
		fx := seedRecurringSeriesFixture(t, db)
		_, err := mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId": fx.seriesPub.String(),
			"scope":   "occurrence",
			"title":   "Nowhere in particular",
		}))
		requireSpec(t, err, apierrors.CalendarEventOccurrenceStartRequired)
		require.Equal(t, wholeSeriesDigest(fx.seriesTitle), seriesDigest(t, deps, fx),
			"a refused update must not have written anything")
	})

	t.Run("delete/thisAndFollowing without an occurrenceStart", func(t *testing.T) {
		t.Parallel()
		fx := seedRecurringSeriesFixture(t, db)
		_, err := mcp.RunDeleteCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId": fx.seriesPub.String(),
			"scope":   "thisAndFollowing",
		}))
		requireSpec(t, err, apierrors.CalendarEventOccurrenceStartRequired)
		require.Equal(t, wholeSeriesDigest(fx.seriesTitle), seriesDigest(t, deps, fx),
			"a refused delete must not have removed anything")
	})

	t.Run("update/occurrence on an event that does not repeat", func(t *testing.T) {
		t.Parallel()
		fx := seedRecurringSeriesFixture(t, db)
		_, err := mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         fx.oneOffPub.String(),
			"scope":           "occurrence",
			"occurrenceStart": seriesOccurrence(9).Unix(),
			"title":           "Not one of many",
		}))
		requireSpec(t, err, apierrors.CalendarEventNotRecurring)
		require.Equal(t, wholeSeriesDigest(fx.seriesTitle), seriesDigest(t, deps, fx))
	})

	t.Run("delete/occurrence on an event that does not repeat", func(t *testing.T) {
		t.Parallel()
		fx := seedRecurringSeriesFixture(t, db)
		_, err := mcp.RunDeleteCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         fx.oneOffPub.String(),
			"scope":           "occurrence",
			"occurrenceStart": seriesOccurrence(9).Unix(),
		}))
		requireSpec(t, err, apierrors.CalendarEventNotRecurring)
		require.Equal(t, wholeSeriesDigest(fx.seriesTitle), seriesDigest(t, deps, fx))
	})

	t.Run("update/occurrence on a row that already replaces one", func(t *testing.T) {
		t.Parallel()
		// An override of an override inserts happily and is then
		// unreachable: nothing expands an override, so the second row
		// would be written and never read.
		fx := seedRecurringSeriesFixture(t, db)
		_, err := mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         fx.seriesPub.String(),
			"scope":           "occurrence",
			"occurrenceStart": seriesOccurrence(1).Unix(),
			"title":           "Stand-up (this week only)",
		}))
		require.NoError(t, err)

		var raw []byte
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT public_id FROM calendar_events
			  WHERE recurrence_parent_id = ? AND recurrence_original_start = ?`,
			fx.seriesID, seriesOccurrence(1)).Scan(&raw))
		overridePub, err := uuid.FromBytes(raw)
		require.NoError(t, err)

		_, err = mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         overridePub.String(),
			"scope":           "occurrence",
			"occurrenceStart": seriesOccurrence(1).Unix(),
			"title":           "Divided again",
		}))
		requireSpec(t, err, apierrors.CalendarEventAlreadyOccurrenceOverride)

		var overrides int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM calendar_events WHERE recurrence_parent_id = ?`,
			fx.seriesID).Scan(&overrides))
		require.Equal(t, 1, overrides, "the refused call must not have written a second override")
	})

	t.Run("delete/thisAndFollowing on a row that already replaces one occurrence", func(t *testing.T) {
		t.Parallel()
		fx := seedRecurringSeriesFixture(t, db)
		_, err := mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         fx.seriesPub.String(),
			"scope":           "occurrence",
			"occurrenceStart": seriesOccurrence(1).Unix(),
			"title":           "Stand-up (this week only)",
		}))
		require.NoError(t, err)

		var raw []byte
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT public_id FROM calendar_events
			  WHERE recurrence_parent_id = ? AND recurrence_original_start = ?`,
			fx.seriesID, seriesOccurrence(1)).Scan(&raw))
		overridePub, err := uuid.FromBytes(raw)
		require.NoError(t, err)

		_, err = mcp.RunDeleteCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         overridePub.String(),
			"scope":           "thisAndFollowing",
			"occurrenceStart": seriesOccurrence(1).Unix(),
		}))
		requireSpec(t, err, apierrors.CalendarEventAlreadyOccurrenceOverride)

		var enabled bool
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT enabled FROM calendar_events WHERE public_id = ?`, raw).Scan(&enabled))
		require.True(t, enabled, "the refused call must not have disabled the override")
	})

	t.Run("a scope outside the closed set is a bad argument", func(t *testing.T) {
		t.Parallel()
		fx := seedRecurringSeriesFixture(t, db)
		_, err := mcp.RunUpdateCalendarEvent(ctx, deps, fx.session(), mcpTrailArgs(t, map[string]any{
			"eventId":         fx.seriesPub.String(),
			"scope":           "everything",
			"occurrenceStart": seriesOccurrence(1).Unix(),
			"title":           "Whole series by accident",
		}))
		requireSpec(t, err, apierrors.McpToolArgumentsInvalid)
		// The refusal matters because the alternative is silence: an
		// unrecognised value falling through to the series path rewrites
		// every occurrence while answering success.
		require.Equal(t, wholeSeriesDigest(fx.seriesTitle), seriesDigest(t, deps, fx))
	})
}

// restScopeEnum reads the closed set a REST input type declares on its
// scope field, from the struct tag the route is generated from.
//
// Read rather than restated: a literal here would agree with REST on the
// day it was written and never again, which is the whole failure this
// test exists to catch.
func restScopeEnum(t *testing.T, input any, path ...string) []string {
	t.Helper()
	typ := reflect.TypeOf(input)
	for _, name := range path {
		field, ok := typ.FieldByName(name)
		require.Truef(t, ok, "%s has no field %s", typ, name)
		typ = field.Type
	}
	scope, ok := typ.FieldByName("Scope")
	require.Truef(t, ok, "%s has no Scope field", typ)
	enum := scope.Tag.Get("enum")
	require.NotEmptyf(t, enum, "%s.Scope declares no enum", typ)
	return strings.Split(enum, ",")
}

// TestMCPCalendarScopeVocabularyMatchesREST pins the values the tools
// accept against the ones the REST routes declare.
//
// The MCP schema and the REST field enums are written in different
// packages, and a caller that can say "this and following" to the web app
// but not to an agent has been handed two different products. This is
// what fails when one of the two is edited alone.
func TestMCPCalendarScopeVocabularyMatchesREST(t *testing.T) {
	t.Parallel()

	cases := map[string][]string{
		"update_calendar_event": restScopeEnum(t, calendars.PatchEventInput{}, "Body"),
		"delete_calendar_event": restScopeEnum(t, calendars.DeleteEventInput{}),
	}

	for tool, want := range cases {
		schema := mcp.ToolInputSchema(tool)
		props, ok := schema["properties"].(map[string]any)
		require.Truef(t, ok, "%s declares no properties", tool)
		scope, ok := props["scope"].(map[string]any)
		require.Truef(t, ok, "%s declares no scope argument", tool)
		require.Equalf(t, want, scope["enum"],
			"%s advertises a scope vocabulary the REST route does not", tool)
		require.Containsf(t, want, "series",
			"%s: the default this tool falls back to has to be a value REST also accepts", tool)
	}
}
