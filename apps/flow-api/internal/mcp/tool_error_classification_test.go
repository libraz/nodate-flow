package mcp_test

import (
	"context"
	"database/sql"
	stderrors "errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// errclassSession is a caller holding the write scope the calendar and
// task tools are registered under.
func errclassSession(userID, wsID uint32) *mcp.TestSession {
	return mcp.NewTestSession(userID, wsID, []string{"write:workspace"})
}

// TestMCPCalendarEventToolsNameTheirRefusals pins what
// update_calendar_event and delete_calendar_event answer when they say no.
//
// Both used to answer a missing event and a caller without edit rights
// with the same generic tool-execution failure, which is a server error.
// An agent that receives a server error retries, and neither of these gets
// better on a retry. The two also have to stay distinct from each other:
// 404 for an event that is not there, 403 for one the caller may not edit.
// Collapsing either into the other tells the caller to do the wrong thing,
// and telling somebody they lack permission when the lookup merely failed
// is a claim about their authority that is not true.
func TestMCPCalendarEventToolsNameTheirRefusals(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedSharedCalendarFixture(t, db)

	deps := mcp.Deps{DB: db, Queries: generated.New(db), CalendarQueries: calendar.New(db)}
	ctx := context.Background()

	// Every event is filed under the target member by the manager, the one
	// member allowed to delegate. The editor can write to this calendar and
	// still may not touch an event that is not theirs, which is the standing
	// the 403 exists for.
	newEvent := func(t *testing.T, title string) string {
		t.Helper()
		start := time.Now().UTC().Add(time.Hour).Unix()
		out, err := mcp.RunCreateCalendarEvent(ctx, deps, errclassSession(fx.managerID, fx.wsID),
			mcpTrailArgs(t, map[string]any{
				"calendarId":  fx.calendarPub.String(),
				"title":       title,
				"startAt":     start,
				"endAt":       start + 3600,
				"ownerUserId": fx.targetPub.String(),
			}))
		require.NoError(t, err)
		m, ok := out.(map[string]any)
		require.True(t, ok)
		id, ok := m["id"].(string)
		require.True(t, ok)
		return id
	}

	update := func(t *testing.T, actorID uint32, eventID, title string) (any, error) {
		t.Helper()
		return mcp.RunUpdateCalendarEvent(ctx, deps, errclassSession(actorID, fx.wsID),
			mcpTrailArgs(t, map[string]any{"eventId": eventID, "title": title}))
	}
	remove := func(t *testing.T, actorID uint32, eventID string) (any, error) {
		t.Helper()
		return mcp.RunDeleteCalendarEvent(ctx, deps, errclassSession(actorID, fx.wsID),
			mcpTrailArgs(t, map[string]any{"eventId": eventID}))
	}

	// An id in the form the tools accept that names nothing. Anything the
	// argument check rejects would never reach the lookup.
	absentID := uuid.Must(uuid.NewV7()).String()

	t.Run("absent_event/update_is_not_found", func(t *testing.T) {
		t.Parallel()
		_, err := update(t, fx.targetID, absentID, "Renamed")
		requireSpec(t, err, apierrors.CalendarEventNotFound)
	})

	t.Run("absent_event/delete_is_not_found", func(t *testing.T) {
		t.Parallel()
		_, err := remove(t, fx.targetID, absentID)
		requireSpec(t, err, apierrors.CalendarEventNotFound)
	})

	t.Run("present_event/owner_updates_it", func(t *testing.T) {
		t.Parallel()
		// The control that separates "a missing event is refused" from "every
		// update is refused". Same caller, same tool, only the id differs.
		id := newEvent(t, "Quarterly review")
		_, err := update(t, fx.targetID, id, "Quarterly review (moved)")
		require.NoError(t, err)

		pub := uuid.MustParse(id)
		var title string
		require.NoError(t, db.QueryRow(
			`SELECT title FROM calendar_events WHERE public_id = ?`, pub[:]).Scan(&title))
		require.Equal(t, "Quarterly review (moved)", title,
			"an accepted update has to reach the row it named")
	})

	t.Run("present_event/owner_deletes_it", func(t *testing.T) {
		t.Parallel()
		id := newEvent(t, "Cancelled sync")
		_, err := remove(t, fx.targetID, id)
		require.NoError(t, err)

		pub := uuid.MustParse(id)
		var enabled bool
		require.NoError(t, db.QueryRow(
			`SELECT enabled FROM calendar_events WHERE public_id = ?`, pub[:]).Scan(&enabled))
		require.False(t, enabled, "an accepted delete has to soft-disable the event it named")
	})

	t.Run("calendar_writer_who_may_not_edit/update_is_permission_required", func(t *testing.T) {
		t.Parallel()
		// The editor passes the calendar-write check — they may add events
		// here — and still may not edit one filed under somebody else. That
		// is a statement about this event, not about the calendar, and it is
		// the same spec the REST patch answers with.
		id := newEvent(t, "Somebody else's slot")
		_, err := update(t, fx.editorID, id, "Hijacked")
		requireSpec(t, err, apierrors.CalendarEventEditPermissionRequired)

		pub := uuid.MustParse(id)
		var title string
		require.NoError(t, db.QueryRow(
			`SELECT title FROM calendar_events WHERE public_id = ?`, pub[:]).Scan(&title))
		require.Equal(t, "Somebody else's slot", title,
			"a refused update must not have written anything")
	})

	t.Run("calendar_writer_who_may_not_edit/delete_is_permission_required", func(t *testing.T) {
		t.Parallel()
		id := newEvent(t, "Not yours to cancel")
		_, err := remove(t, fx.editorID, id)
		requireSpec(t, err, apierrors.CalendarEventEditPermissionRequired)

		pub := uuid.MustParse(id)
		var enabled bool
		require.NoError(t, db.QueryRow(
			`SELECT enabled FROM calendar_events WHERE public_id = ?`, pub[:]).Scan(&enabled))
		require.True(t, enabled, "a refused delete must not have disabled the event")
	})
}

// errclassBrokenAttendeeLookup fails exactly one of the statements the
// calendar tools issue — the attendee read the event-edit check is built
// from — and passes every other one through to the real database.
//
// Narrowing it to that one statement is the point. The tools reach the
// edit check through the same handle as the event and calendar lookups
// that run before it, so a handle that is simply closed fails the first of
// them and the edit check is never reached. The substitute names a table
// that does not exist, so what comes back is a driver error rather than a
// value a test invented.
type errclassBrokenAttendeeLookup struct {
	*sql.DB
}

func (d errclassBrokenAttendeeLookup) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if strings.Contains(query, "-- name: FindCalendarEventAttendee :one") {
		return d.DB.QueryRowContext(ctx, "SELECT 1 FROM nf_table_that_does_not_exist")
	}
	return d.DB.QueryRowContext(ctx, query, args...)
}

// TestMCPCalendarEventToolsDoNotBlameTheCallerForALookupFailure pins the
// half of the edit check that is not a decision.
//
// Both tools used to answer `err != nil || !ok` with one refusal, so a
// lookup that failed came back as "you may not edit this event": false
// about the caller's authority, and it hid an outage behind a message the
// caller would read as final. The two are separate now, and the failing
// half has to answer as the server failure it is.
func TestMCPCalendarEventToolsDoNotBlameTheCallerForALookupFailure(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedSharedCalendarFixture(t, db)

	healthy := mcp.Deps{DB: db, Queries: generated.New(db), CalendarQueries: calendar.New(db)}
	broken := mcp.Deps{
		DB:              db,
		Queries:         generated.New(db),
		CalendarQueries: calendar.New(errclassBrokenAttendeeLookup{db}),
	}
	ctx := context.Background()

	newEvent := func(t *testing.T, title string) string {
		t.Helper()
		start := time.Now().UTC().Add(time.Hour).Unix()
		out, err := mcp.RunCreateCalendarEvent(ctx, healthy, errclassSession(fx.managerID, fx.wsID),
			mcpTrailArgs(t, map[string]any{
				"calendarId":  fx.calendarPub.String(),
				"title":       title,
				"startAt":     start,
				"endAt":       start + 3600,
				"ownerUserId": fx.targetPub.String(),
			}))
		require.NoError(t, err)
		m, ok := out.(map[string]any)
		require.True(t, ok)
		id, ok := m["id"].(string)
		require.True(t, ok)
		return id
	}

	requireNotAPermissionClaim := func(t *testing.T, err error) {
		t.Helper()
		require.Error(t, err, "a lookup that failed has to be reported, not swallowed")
		var ae *apierrors.APIError
		require.Truef(t, stderrors.As(err, &ae), "want *apierrors.APIError, got %T: %v", err, err)
		require.NotNil(t, ae.Spec)
		require.NotEqual(t, apierrors.CalendarEventEditPermissionRequired.Code, ae.Spec.Code,
			"a check that never completed says nothing about what the caller may do")
		require.GreaterOrEqualf(t, ae.Spec.Status, 500,
			"a failure to decide belongs to the server; got %s", ae.Spec.Code)
	}

	// Each case runs the same call twice on the same event as the same
	// caller — the owner, who is allowed. Only the attendee lookup differs,
	// so the healthy half rules out a handler that refuses everything and
	// the broken half can only be the branch under test.
	t.Run("update/failed_lookup_is_not_a_refusal", func(t *testing.T) {
		t.Parallel()
		id := newEvent(t, "Owned and editable")

		update := func(deps mcp.Deps, title string) (any, error) {
			return mcp.RunUpdateCalendarEvent(ctx, deps, errclassSession(fx.targetID, fx.wsID),
				mcpTrailArgs(t, map[string]any{"eventId": id, "title": title}))
		}

		_, err := update(broken, "Renamed while the lookup was down")
		requireNotAPermissionClaim(t, err)

		_, err = update(healthy, "Renamed")
		require.NoError(t, err, "the same caller on the same event is allowed once the lookup works")
	})

	t.Run("delete/failed_lookup_is_not_a_refusal", func(t *testing.T) {
		t.Parallel()
		id := newEvent(t, "Owned and deletable")

		remove := func(deps mcp.Deps) (any, error) {
			return mcp.RunDeleteCalendarEvent(ctx, deps, errclassSession(fx.targetID, fx.wsID),
				mcpTrailArgs(t, map[string]any{"eventId": id}))
		}

		_, err := remove(broken)
		requireNotAPermissionClaim(t, err)

		pub := uuid.MustParse(id)
		var enabled bool
		require.NoError(t, db.QueryRow(
			`SELECT enabled FROM calendar_events WHERE public_id = ?`, pub[:]).Scan(&enabled))
		require.True(t, enabled, "a call that could not decide must not have deleted anything")

		_, err = remove(healthy)
		require.NoError(t, err, "the same caller on the same event is allowed once the lookup works")
	})
}

// errclassProjection is a task that already carries a projection event,
// which is the only shape in which the itemkit branches of update_task and
// update_calendar_event are reachable at all.
type errclassProjection struct {
	*mcpWiringFixture
	eventPublicID string
	eventID       uint32
}

// seedErrclassProjection gives the wiring fixture's member a calendar and
// links their task to a projection event on it.
//
// The link is written through itemkit because itemkit is the only writer
// trg_calendar_events_projection_guard_ins admits; a direct INSERT carrying
// task_id is rejected by the database.
func seedErrclassProjection(t *testing.T, db *sql.DB) *errclassProjection {
	t.Helper()
	ctx := context.Background()
	fx := seedMCPWiringFixture(t, db)

	calPub := uuid.Must(uuid.NewV7())
	res, err := db.ExecContext(ctx,
		`INSERT INTO calendars (public_id, workspace_id, kind, name) VALUES (?, ?, 'personal', ?)`,
		calPub[:], fx.wsID, "Projection Calendar")
	require.NoError(t, err)
	calID64, err := res.LastInsertId()
	require.NoError(t, err)
	calID := uint32(calID64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	cmPub := uuid.Must(uuid.NewV7())
	_, err = db.ExecContext(ctx,
		`INSERT INTO calendar_members (public_id, workspace_id, calendar_id, user_id, role) VALUES (?, ?, ?, ?, 'owner')`,
		cmPub[:], fx.wsID, calID, fx.userID)
	require.NoError(t, err)

	start := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	var eventPublicID string
	var eventID uint32
	require.NoError(t, dbretry.InTx(ctx, db, "test.seed_projection", nil,
		func(ctx context.Context, tx *dbretry.Tx) error {
			pub, id, serr := itemkit.ScheduleTask(ctx, tx, itemkit.ScheduleTaskArgs{
				WorkspaceID: fx.wsID,
				TaskID:      fx.taskInternalID,
				CalendarID:  calID,
				ActorUserID: fx.userID,
				Role:        itemkit.RoleDue,
				StartAt:     start,
				EndAt:       start.Add(time.Hour),
				Timezone:    "UTC",
			})
			eventPublicID, eventID = pub.String(), id
			return serr
		}))

	return &errclassProjection{mcpWiringFixture: fx, eventPublicID: eventPublicID, eventID: eventID}
}

// TestMCPToolsKeepItemkitFailuresInTheirOwnDomain pins what the tools
// answer when the shared itemkit engine refuses a write.
//
// itemkit is shared between the task tools and the calendar tools, so its
// errors carry no domain of their own: the same "row is gone" reads as a
// missing task through update_task and as a missing event through the
// calendar tools. The translation used to recognise one invariant by
// substring and hand everything else back as a server error, so a caller
// whose row had vanished was told to retry a call that could never work.
func TestMCPToolsKeepItemkitFailuresInTheirOwnDomain(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB

	deps := mcp.Deps{DB: db, Queries: generated.New(db), CalendarQueries: calendar.New(db)}
	ctx := context.Background()

	rescheduleEvent := func(t *testing.T, fx *errclassProjection, at time.Time) (any, error) {
		t.Helper()
		return mcp.RunUpdateCalendarEvent(ctx, deps, errclassSession(fx.userID, fx.wsID),
			mcpTrailArgs(t, map[string]any{
				"eventId": fx.eventPublicID,
				"startAt": at.Unix(),
				"endAt":   at.Add(time.Hour).Unix(),
			}))
	}
	retaskDueOn := func(t *testing.T, fx *errclassProjection, dueOn string) (any, error) {
		t.Helper()
		return mcp.RunUpdateTask(ctx, deps, errclassSession(fx.userID, fx.wsID),
			mcpTrailArgs(t, map[string]any{
				"taskId": fx.taskPub.String(),
				"dueOn":  dueOn,
			}))
	}

	t.Run("update_calendar_event/projection_reschedule_succeeds", func(t *testing.T) {
		t.Parallel()
		// The control for the case below. Without it, "a missing task is
		// reported as a missing event" is also what an implementation that
		// refuses every linked reschedule produces.
		fx := seedErrclassProjection(t, db)
		at := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
		_, err := rescheduleEvent(t, fx, at)
		require.NoError(t, err)

		var startAt time.Time
		require.NoError(t, db.QueryRow(
			`SELECT start_at FROM calendar_events WHERE id = ?`, fx.eventID).Scan(&startAt))
		require.Equal(t, at.Unix(), startAt.UTC().Unix(),
			"an accepted reschedule has to move the event it named")
	})

	t.Run("update_calendar_event/task_behind_the_projection_is_gone", func(t *testing.T) {
		t.Parallel()
		// The projection outlives the task it projects. itemkit reads the
		// task to name it in the event it appends, finds nothing, and says
		// so; the tool has to answer with its own 404 rather than with a
		// server error the caller would retry forever.
		fx := seedErrclassProjection(t, db)
		_, err := db.Exec(`UPDATE tasks SET enabled = FALSE WHERE id = ?`, fx.taskInternalID)
		require.NoError(t, err)

		at := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
		_, err = rescheduleEvent(t, fx, at)
		requireSpec(t, err, apierrors.CalendarEventNotFound)
	})

	t.Run("update_task/projection_reschedule_succeeds", func(t *testing.T) {
		t.Parallel()
		fx := seedErrclassProjection(t, db)
		dueOn := time.Now().UTC().Add(96 * time.Hour).Format("2006-01-02")
		_, err := retaskDueOn(t, fx, dueOn)
		require.NoError(t, err)

		var stored time.Time
		require.NoError(t, db.QueryRow(
			`SELECT due_on FROM tasks WHERE id = ?`, fx.taskInternalID).Scan(&stored))
		require.Equal(t, dueOn, stored.Format("2006-01-02"),
			"an accepted reschedule has to write the date it was given")
	})

	t.Run("update_task/broken_projection_is_the_invariant_not_a_missing_task", func(t *testing.T) {
		t.Parallel()
		// A projection event whose stored zone is not a zone. itemkit cannot
		// rebuild the instant, so it refuses on the invariant — and the task
		// is right there, so answering the caller's own 404 here would be a
		// lie. This is what separates a classifier from a translator that
		// stamps the caller's not-found onto everything.
		//
		// The not-found arm of this site is unreachable through the tool and
		// is meant to be: update_task resolves the task through v_task_detail
		// (WHERE t.enabled = TRUE) and itemkit reads the same row under the
		// same predicate, so a task itemkit cannot find was already answered
		// as WS.TASK.NOT_FOUND before the transaction opened. Only a delete
		// landing between the two reaches it. The arm stays because that race
		// is real; the invariant is what a test can hold it to.
		fx := seedErrclassProjection(t, db)
		_, err := db.Exec(
			`UPDATE calendar_events SET timezone = 'Mars/Olympus' WHERE id = ?`, fx.eventID)
		require.NoError(t, err)

		dueOn := time.Now().UTC().Add(120 * time.Hour).Format("2006-01-02")
		_, err = retaskDueOn(t, fx, dueOn)
		requireSpec(t, err, apierrors.ItemItemkitInvariantViolation)
	})
}
