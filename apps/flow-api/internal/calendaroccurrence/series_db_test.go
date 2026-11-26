package calendaroccurrence_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarlabels"
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

// TestSplitSeriesCarriesOnlyCalendarMemberAttendees: the continuing master
// copies the attendees that still qualify, so a user whose calendar
// membership has been revoked does not reappear on the new series.
func TestSplitSeriesCarriesOnlyCalendarMemberAttendees(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	ctx := context.Background()
	fx := seedRealignFixture(t, db)
	cq := calendar.New(db)
	master := fx.read(t, cq)

	member := func(label string, calendarMember bool) uint32 {
		suffix := uuid.New().String()[:8]
		res, err := db.ExecContext(ctx, `INSERT INTO users (public_id, email, display_name, locale) VALUES (?, ?, ?, 'en')`,
			types.New(), label+"-"+suffix+"@example.test", label)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		uid := uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
		_, err = db.ExecContext(ctx, `INSERT INTO workspace_members (public_id, workspace_id, user_id) VALUES (?, ?, ?)`,
			types.New(), fx.wsID, uid)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx,
			`INSERT INTO calendar_members (public_id, workspace_id, calendar_id, user_id, enabled) VALUES (?, ?, ?, ?, ?)`,
			types.New(), fx.wsID, fx.calendarID, uid, calendarMember)
		require.NoError(t, err)
		_, err = cq.CreateCalendarEventAttendee(ctx, calendar.CreateCalendarEventAttendeeParams{
			PublicID:    types.New(),
			WorkspaceID: fx.wsID,
			EventID:     sql.NullInt32{Int32: int32(fx.masterID), Valid: true}, //#nosec G115 -- test row id
			UserID:      uid,
			Rsvp:        calendar.CalendarEventAttendeesRsvpPending,
		})
		require.NoError(t, err)
		return uid
	}
	stays := member("stays", true)
	departed := member("departed", false)

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	written, refusal, err := calendaroccurrence.SplitSeries(ctx, calendaroccurrence.Queries{
		Calendar: calendar.New(db).WithTx(tx),
		Core:     generated.New(db).WithTx(tx),
	}, calendaroccurrence.Split{
		WorkspaceID: fx.wsID,
		ActorID:     master.OwnerUserID,
		CalendarID:  fx.calendarID,
		Master:      master,
		SplitStart:  realignAnchor.AddDate(0, 0, 2),
	})
	if err != nil || refusal != nil {
		_ = tx.Rollback()
		t.Fatalf("split: err=%v refusal=%v", err, refusal)
	}
	require.NoError(t, tx.Commit())

	rows, err := db.QueryContext(ctx, `SELECT user_id FROM calendar_event_attendees WHERE event_id = ? AND enabled = TRUE`, written.ID)
	require.NoError(t, err)
	defer rows.Close()
	var carried []uint32
	for rows.Next() {
		var uid uint32
		require.NoError(t, rows.Scan(&uid))
		carried = append(carried, uid)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []uint32{stays}, carried, "only the attendee who still holds the calendar is copied (departed=%d)", departed)
}

func seedLabel(t *testing.T, db *sql.DB, fx realignFixture, name string) uint32 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO calendar_labels (public_id, workspace_id, calendar_id, name, color) VALUES (?, ?, ?, ?, '#112233')`,
		types.New(), fx.wsID, fx.calendarID, name)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
}

func linkOf(t *testing.T, db *sql.DB, eventID uint32) uint32 {
	t.Helper()
	got, err := calendarlabels.CurrentLabel(context.Background(), calendar.New(db), eventID)
	require.NoError(t, err)
	return got
}

func inTx(t *testing.T, db *sql.DB, fn func(q calendaroccurrence.Queries)) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	fn(calendaroccurrence.Queries{Calendar: calendar.New(db).WithTx(tx), Core: generated.New(db).WithTx(tx)})
	require.NoError(t, tx.Commit())
}

func writeOverride(t *testing.T, db *sql.DB, fx realignFixture, day int, change calendarlabels.Change) calendaroccurrence.Written {
	t.Helper()
	var written calendaroccurrence.Written
	inTx(t, db, func(q calendaroccurrence.Queries) {
		master := fx.read(t, calendar.New(db))
		w, refusal, err := calendaroccurrence.WriteOverride(context.Background(), q, calendaroccurrence.Override{
			WorkspaceID:   fx.wsID,
			ActorID:       master.OwnerUserID,
			CalendarID:    fx.calendarID,
			Master:        master,
			OriginalStart: realignAnchor.AddDate(0, 0, day),
			Patch:         calendaroccurrence.Patch{Label: change},
		})
		require.Nil(t, refusal)
		require.NoError(t, err)
		written = w
	})
	return written
}

func set(id uint32) calendarlabels.Change {
	return calendarlabels.Change{Mode: calendarlabels.ChangeSet, LabelID: id}
}

var clearLabel = calendarlabels.Change{Mode: calendarlabels.ChangeClear}

func TestWriteOverrideLabelOnCreate(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	db := helpers.StartShared(t).DB
	fx := seedRealignFixture(t, db)
	seriesLabel, other := seedLabel(t, db, fx, "series"), seedLabel(t, db, fx, "other")
	_, err := db.Exec(`INSERT INTO calendar_event_labels (event_id, label_id) VALUES (?, ?)`, fx.masterID, seriesLabel)
	require.NoError(t, err)

	keep := writeOverride(t, db, fx, 0, calendarlabels.Change{})
	require.Equal(t, seriesLabel, linkOf(t, db, keep.ID), "Keep copies the series' label")
	set1 := writeOverride(t, db, fx, 1, set(other))
	require.Equal(t, other, linkOf(t, db, set1.ID))
	cleared := writeOverride(t, db, fx, 2, clearLabel)
	require.Zero(t, linkOf(t, db, cleared.ID))
	require.Equal(t, seriesLabel, linkOf(t, db, fx.masterID), "the series itself is untouched")
}

func TestWriteOverrideLabelOnExisting(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	db := helpers.StartShared(t).DB
	fx := seedRealignFixture(t, db)
	seriesLabel, own := seedLabel(t, db, fx, "series"), seedLabel(t, db, fx, "own")
	_, err := db.Exec(`INSERT INTO calendar_event_labels (event_id, label_id) VALUES (?, ?), (?, ?)`,
		fx.masterID, seriesLabel, fx.overrideID, own)
	require.NoError(t, err)

	w := writeOverride(t, db, fx, 4, calendarlabels.Change{})
	require.Equal(t, fx.overrideID, w.ID, "the update path answers with the row id")
	require.Equal(t, own, linkOf(t, db, fx.overrideID), "Keep leaves an enabled override's own label")
	writeOverride(t, db, fx, 4, set(seriesLabel))
	require.Equal(t, seriesLabel, linkOf(t, db, fx.overrideID))
	writeOverride(t, db, fx, 4, clearLabel)
	require.Zero(t, linkOf(t, db, fx.overrideID))
}

func TestWriteOverrideRevivalDoesNotKeepOldLink(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	db := helpers.StartShared(t).DB
	fx := seedRealignFixture(t, db)
	seriesLabel, stale := seedLabel(t, db, fx, "series"), seedLabel(t, db, fx, "stale")
	_, err := db.Exec(`INSERT INTO calendar_event_labels (event_id, label_id) VALUES (?, ?), (?, ?)`,
		fx.masterID, seriesLabel, fx.overrideID, stale)
	require.NoError(t, err)

	disable := func() {
		_, err := db.Exec(`UPDATE calendar_events SET enabled = FALSE WHERE id = ?`, fx.overrideID)
		require.NoError(t, err)
	}
	disable()
	writeOverride(t, db, fx, 4, calendarlabels.Change{})
	require.Equal(t, seriesLabel, linkOf(t, db, fx.overrideID), "Keep on a revived row takes the series' label")

	disable()
	writeOverride(t, db, fx, 4, clearLabel)
	require.Zero(t, linkOf(t, db, fx.overrideID))

	disable()
	writeOverride(t, db, fx, 4, set(stale))
	require.Equal(t, stale, linkOf(t, db, fx.overrideID))
}

func TestSplitSeriesLabel(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	db := helpers.StartShared(t).DB
	ctx := context.Background()
	fx := seedRealignFixture(t, db)
	seriesLabel, other := seedLabel(t, db, fx, "series"), seedLabel(t, db, fx, "other")
	_, err := db.Exec(`INSERT INTO calendar_event_labels (event_id, label_id) VALUES (?, ?), (?, ?)`,
		fx.masterID, seriesLabel, fx.overrideID, other)
	require.NoError(t, err)

	split := func(change calendarlabels.Change) uint32 {
		var id uint32
		inTx(t, db, func(q calendaroccurrence.Queries) {
			master := fx.read(t, calendar.New(db))
			w, refusal, err := calendaroccurrence.SplitSeries(ctx, q, calendaroccurrence.Split{
				WorkspaceID: fx.wsID,
				ActorID:     master.OwnerUserID,
				CalendarID:  fx.calendarID,
				Master:      master,
				SplitStart:  realignAnchor.AddDate(0, 0, 2),
				Patch:       calendaroccurrence.Patch{Label: change},
			})
			require.Nil(t, refusal)
			require.NoError(t, err)
			id = w.ID
		})
		return id
	}

	kept := split(calendarlabels.Change{})
	require.Equal(t, seriesLabel, linkOf(t, db, kept), "Keep carries the series' label to the new master")
	require.Equal(t, other, linkOf(t, db, fx.overrideID), "a reparented override keeps its own link")
	require.Equal(t, seriesLabel, linkOf(t, db, fx.masterID))

	fx2 := seedRealignFixture(t, db)
	l2, l2other := seedLabel(t, db, fx2, "series"), seedLabel(t, db, fx2, "other")
	_, err = db.Exec(`INSERT INTO calendar_event_labels (event_id, label_id) VALUES (?, ?)`, fx2.masterID, l2)
	require.NoError(t, err)
	fx = fx2
	require.Equal(t, l2other, linkOf(t, db, split(set(l2other))))

	fx3 := seedRealignFixture(t, db)
	l3 := seedLabel(t, db, fx3, "series")
	_, err = db.Exec(`INSERT INTO calendar_event_labels (event_id, label_id) VALUES (?, ?)`, fx3.masterID, l3)
	require.NoError(t, err)
	fx = fx3
	require.Zero(t, linkOf(t, db, split(clearLabel)))
}
