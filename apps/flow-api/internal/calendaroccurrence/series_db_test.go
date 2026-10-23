package calendaroccurrence_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendaroccurrence"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/recurrence"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

var realignAnchor = time.Date(2031, 5, 5, 9, 0, 0, 0, time.UTC)

type realignFixture struct {
	wsID       uint32
	calendarID uint32
	masterID   uint32
	masterPub  types.PublicID
	overrideID uint32
}

// seedRealignFixture is a daily series of five with the fourth occurrence
// cancelled and the fifth moved three hours later by an override.
func seedRealignFixture(t *testing.T, db *sql.DB) realignFixture {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.New().String()[:8]
	insert := func(q string, args ...any) uint32 {
		res, err := db.ExecContext(ctx, q, args...)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
	}

	fx := realignFixture{masterPub: types.New()}
	fx.wsID = insert(`INSERT INTO workspaces (public_id, slug, name) VALUES (?, ?, 'Realign')`, types.New(), "realign-"+suffix)
	user := insert(`INSERT INTO users (public_id, email, display_name, locale) VALUES (?, ?, 'Realign', 'en')`,
		types.New(), "realign-"+suffix+"@example.test")
	fx.calendarID = insert(`INSERT INTO calendars (public_id, workspace_id, kind, name) VALUES (?, ?, 'personal', 'Realign')`,
		types.New(), fx.wsID)
	fx.masterID = insert(`INSERT INTO calendar_events
		   (public_id, workspace_id, calendar_id, title, start_at, end_at, timezone,
		    owner_user_id, created_by_user_id, recurrence_rule, recurrence_exceptions)
		 VALUES (?, ?, ?, 'Daily', ?, ?, 'UTC', ?, ?, CAST(? AS JSON), CAST(? AS JSON))`,
		fx.masterPub, fx.wsID, fx.calendarID, realignAnchor, realignAnchor.Add(time.Hour), user, user,
		`{"freq":"daily","until":"2031-05-09"}`,
		`["`+realignAnchor.AddDate(0, 0, 3).Format(time.RFC3339)+`"]`)
	moved := realignAnchor.AddDate(0, 0, 4).Add(3 * time.Hour)
	fx.overrideID = insert(`INSERT INTO calendar_events
		   (public_id, workspace_id, calendar_id, recurrence_parent_id, recurrence_original_start,
		    title, start_at, end_at, timezone, owner_user_id, created_by_user_id)
		 VALUES (?, ?, ?, ?, ?, 'Daily', ?, ?, 'UTC', ?, ?)`,
		types.New(), fx.wsID, fx.calendarID, fx.masterID, realignAnchor.AddDate(0, 0, 4),
		moved, moved.Add(time.Hour), user, user)
	return fx
}

func (fx realignFixture) read(t *testing.T, cq *calendar.Queries) calendar.FindCalendarEventByPublicIdRow {
	t.Helper()
	row, err := cq.FindCalendarEventByPublicId(context.Background(), calendar.FindCalendarEventByPublicIdParams{
		PublicID: fx.masterPub, CalendarID: fx.calendarID, WorkspaceID: fx.wsID,
	})
	require.NoError(t, err)
	return row
}

func realign(t *testing.T, db *sql.DB, fx realignFixture, before calendaroccurrence.SeriesState, after calendar.FindCalendarEventByPublicIdRow) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = calendaroccurrence.RealignSeries(ctx, calendaroccurrence.Queries{
		Calendar: calendar.New(db).WithTx(tx),
		Core:     generated.New(db).WithTx(tx),
	}, fx.wsID, before, after, false)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	require.NoError(t, tx.Commit())
}

// TestRealignSeriesFollowsAMovedStart is the series-scope write: moving
// every occurrence an hour later must take the cancelled occurrence and
// the replaced one along, or the first reappears and the second shows
// twice.
func TestRealignSeriesFollowsAMovedStart(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	ctx := context.Background()
	fx := seedRealignFixture(t, db)
	cq := calendar.New(db)

	before := calendaroccurrence.SeriesStateOf(fx.read(t, cq))
	later := realignAnchor.Add(time.Hour)
	_, err := db.ExecContext(ctx, `UPDATE calendar_events SET start_at = ?, end_at = ? WHERE id = ?`,
		later, later.Add(time.Hour), fx.masterID)
	require.NoError(t, err)
	realign(t, db, fx, before, fx.read(t, cq))

	var originalStart time.Time
	var enabled bool
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT recurrence_original_start, enabled FROM calendar_events WHERE id = ?`, fx.overrideID).
		Scan(&originalStart, &enabled))
	require.True(t, enabled, "an override whose occurrence still exists stays")
	require.Truef(t, originalStart.Equal(realignAnchor.AddDate(0, 0, 4).Add(time.Hour)),
		"the override must name its occurrence at the moved instant, got %v", originalStart)

	exceptions := recurrence.ParseExceptions(fx.read(t, cq).RecurrenceExceptions)
	require.Equal(t, []string{realignAnchor.AddDate(0, 0, 3).Add(time.Hour).Format(time.RFC3339)}, exceptions,
		"the cancelled occurrence must stay cancelled at its moved instant")
}

// TestRealignSeriesWithdrawsOverridesWhenTheRuleIsCleared: an override
// stands in for an occurrence of a series, and a row that no longer
// repeats has none.
func TestRealignSeriesWithdrawsOverridesWhenTheRuleIsCleared(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	ctx := context.Background()
	fx := seedRealignFixture(t, db)
	cq := calendar.New(db)

	before := calendaroccurrence.SeriesStateOf(fx.read(t, cq))
	_, err := db.ExecContext(ctx,
		`UPDATE calendar_events SET recurrence_rule = NULL, recurrence_exceptions = NULL WHERE id = ?`, fx.masterID)
	require.NoError(t, err)
	realign(t, db, fx, before, fx.read(t, cq))

	var enabled bool
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT enabled FROM calendar_events WHERE id = ?`, fx.overrideID).Scan(&enabled))
	require.False(t, enabled, "clearing the rule must withdraw the series' overrides")
}
