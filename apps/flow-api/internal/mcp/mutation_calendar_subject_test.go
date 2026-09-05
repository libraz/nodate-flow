package mcp_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// A calendar mutation names two things: what happened, and where. The
// first half is what the trail checks already read. This file reads the
// second — events.calendar_id, the column a per-calendar activity feed
// selects on.
//
// Every assertion here resolves the expected calendar from the row the
// tool actually wrote, never from the argument the tool was handed. The
// two coincide today for all four sites, which is exactly why an
// assertion built on the argument would keep passing after they stopped
// coinciding. It is also why "not nil" is not enough: an event surfacing
// in a colleague's calendar feed is a worse answer than one surfacing in
// none, and a nil check cannot tell those apart.

// calSubjectFixture is one tenant holding two writable calendars. The
// second one is never written to; it is there so that "the calendar this
// landed on" and "some calendar in this workspace" are different claims,
// and a recording that satisfies only the weaker one fails.
type calSubjectFixture struct {
	wsID   uint32
	userID uint32

	// subjectPub / subjectID is the calendar every tool below writes to.
	subjectPub uuid.UUID
	subjectID  uint32
	// decoyID is the other calendar in the same workspace.
	decoyID uint32

	memoPub uuid.UUID
	// taskPub / taskID is an unlinked task, the subject of
	// create_event_from_task. The internal id is kept because the
	// projection link is a column on the event, not a public id.
	taskPub uuid.UUID
	taskID  uint32
	// linkedEventPub is a projection event on another task, so
	// update_calendar_event reaches its itemkit branch rather than its
	// standalone one.
	linkedEventPub uuid.UUID
}

func seedCalSubjectFixture(t *testing.T, db *sql.DB) *calSubjectFixture {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.New().String()[:8]

	fx := &calSubjectFixture{}
	var linkedTaskID uint32

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	lastID := func(res sql.Result) uint32 {
		id, idErr := res.LastInsertId()
		require.NoError(t, idErr)
		return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
	}

	userPub := uuid.Must(uuid.NewV7())
	res, err := tx.ExecContext(ctx,
		`INSERT INTO users (public_id, email, display_name, locale) VALUES (?, ?, ?, 'en')`,
		userPub[:], "calsubject-"+suffix+"@example.test", "CalSubject "+suffix)
	require.NoError(t, err)
	fx.userID = lastID(res)

	wsPub := uuid.Must(uuid.NewV7())
	res, err = tx.ExecContext(ctx,
		`INSERT INTO workspaces (public_id, slug, name) VALUES (?, ?, ?)`,
		wsPub[:], "calsubject-ws-"+suffix, "CalSubject Workspace")
	require.NoError(t, err)
	fx.wsID = lastID(res)

	memberPub := uuid.Must(uuid.NewV7())
	_, err = tx.ExecContext(ctx,
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role) VALUES (?, ?, ?, 'owner')`,
		memberPub[:], fx.wsID, fx.userID)
	require.NoError(t, err)

	prjPub := uuid.Must(uuid.NewV7())
	res, err = tx.ExecContext(ctx,
		`INSERT INTO projects (public_id, workspace_id, slug, name, identifier) VALUES (?, ?, ?, ?, ?)`,
		prjPub[:], fx.wsID, "calsubject-prj-"+suffix, "CalSubject Project", "CS"+suffix[:3])
	require.NoError(t, err)
	prjID := lastID(res)

	// Two calendars, both writable by the same actor. Only the first is
	// ever written to.
	newCalendar := func(name string) (uuid.UUID, uint32) {
		pub := uuid.Must(uuid.NewV7())
		created, cerr := tx.ExecContext(ctx,
			`INSERT INTO calendars (public_id, workspace_id, kind, name, owner_user_id) VALUES (?, ?, 'personal', ?, ?)`,
			pub[:], fx.wsID, name, fx.userID)
		require.NoError(t, cerr)
		id := lastID(created)
		mpub := uuid.Must(uuid.NewV7())
		_, cerr = tx.ExecContext(ctx,
			`INSERT INTO calendar_members (public_id, workspace_id, calendar_id, user_id, role) VALUES (?, ?, ?, ?, 'owner')`,
			mpub[:], fx.wsID, id, fx.userID)
		require.NoError(t, cerr)
		return pub, id
	}
	fx.subjectPub, fx.subjectID = newCalendar("CalSubject Subject")
	_, fx.decoyID = newCalendar("CalSubject Decoy")

	memoPub := uuid.Must(uuid.NewV7())
	_, err = tx.ExecContext(ctx,
		`INSERT INTO calendar_memos (public_id, workspace_id, calendar_id, created_by_user_id, title) VALUES (?, ?, ?, ?, ?)`,
		memoPub[:], fx.wsID, fx.subjectID, fx.userID, "Confirm the room")
	require.NoError(t, err)
	fx.memoPub = memoPub

	newTask := func(number int, title string) (uuid.UUID, uint32) {
		pub := uuid.Must(uuid.NewV7())
		created, terr := tx.ExecContext(ctx,
			`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title, visibility, created_by_user_id)
			 VALUES (?, ?, ?, ?, ?, 'public', ?)`,
			pub[:], fx.wsID, prjID, number, title, fx.userID)
		require.NoError(t, terr)
		return pub, lastID(created)
	}
	fx.taskPub, fx.taskID = newTask(1, "Draft the agenda")
	_, linkedTaskID = newTask(2, "Run the review")

	require.NoError(t, tx.Commit())
	committed = true

	// The projection event is created through itemkit rather than by an
	// INSERT: a task-linked calendar_events row may only be written by the
	// projection engine, and the guard trigger enforces that.
	require.NoError(t, dbretry.InTx(ctx, db, "test.seed_linked_event", nil,
		func(ctx context.Context, tx *dbretry.Tx) error {
			start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Hour)
			pub, _, serr := itemkit.ScheduleTask(ctx, tx, itemkit.ScheduleTaskArgs{
				WorkspaceID: fx.wsID,
				TaskID:      linkedTaskID,
				CalendarID:  fx.subjectID,
				ActorUserID: fx.userID,
				Role:        itemkit.RoleDue,
				Title:       "Run the review",
				StartAt:     start,
				EndAt:       start.Add(time.Hour),
				Timezone:    "UTC",
			})
			if serr != nil {
				return serr
			}
			fx.linkedEventPub = pub.UUID()
			return nil
		}))

	return fx
}

// calSubjectRecordedCalendar returns the calendar id on the events row a
// tool appended, found by the public id its payload names.
//
// The lookup goes through the payload rather than through "the most
// recent row", so a run alongside another test in the same database
// cannot hand back somebody else's event.
func calSubjectRecordedCalendar(t *testing.T, db *sql.DB, wsID uint32, kind, payloadKey, publicID string) sql.NullInt64 {
	t.Helper()
	var got sql.NullInt64
	err := db.QueryRow(
		`SELECT calendar_id FROM events
		 WHERE workspace_id = ? AND type = ?
		   AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, ?)) = ?`,
		wsID, kind, "$."+payloadKey, publicID).Scan(&got)
	require.NoErrorf(t, err, "no %s event names %s in its %s; the change was not recorded at all",
		kind, publicID, payloadKey)
	return got
}

// calSubjectLandedCalendar reads the calendar the event row itself sits
// on. This is the answer the recorded value has to match, and it is read
// from the stored row rather than from the argument the tool was given.
func calSubjectLandedCalendar(t *testing.T, db *sql.DB, wsID uint32, eventPublicID uuid.UUID) uint32 {
	t.Helper()
	var got uint32
	require.NoError(t, db.QueryRow(
		`SELECT calendar_id FROM calendar_events WHERE workspace_id = ? AND public_id = ?`,
		wsID, eventPublicID[:]).Scan(&got))
	return got
}

func calSubjectMemoCalendar(t *testing.T, db *sql.DB, wsID uint32, memoPublicID uuid.UUID) uint32 {
	t.Helper()
	var got uint32
	require.NoError(t, db.QueryRow(
		`SELECT calendar_id FROM calendar_memos WHERE workspace_id = ? AND public_id = ?`,
		wsID, memoPublicID[:]).Scan(&got))
	return got
}

// TestMCPCalendarMutationsNameTheCalendarTheyLandedOn drives every tool
// that records a calendar subject and checks the recorded id against the
// calendar the written row actually belongs to.
func TestMCPCalendarMutationsNameTheCalendarTheyLandedOn(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedCalSubjectFixture(t, db)

	deps := mcp.Deps{DB: db, Queries: generated.New(db), CalendarQueries: calendar.New(db)}
	ctx := context.Background()
	sess := mcp.NewTestSession(fx.userID, fx.wsID, []string{"write:workspace"})

	// Without two distinguishable calendars, matching the landed row and
	// matching any calendar in the workspace are the same assertion.
	require.NotEqual(t, fx.subjectID, fx.decoyID,
		"the fixture must hold two distinct calendars, or nothing below can tell the right one from a wrong one")

	var standalonePub string

	t.Run("create_calendar_event", func(t *testing.T) {
		start := time.Now().UTC().Add(time.Hour).Unix()
		out, err := mcp.RunCreateCalendarEvent(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
			"calendarId": fx.subjectPub.String(),
			"title":      "Agent-scheduled review",
			"startAt":    start,
			"endAt":      start + 3600,
		}))
		require.NoError(t, err)
		m, ok := out.(map[string]any)
		require.True(t, ok)
		standalonePub, ok = m["id"].(string)
		require.True(t, ok)

		eventPub, err := uuid.Parse(standalonePub)
		require.NoError(t, err)
		landed := calSubjectLandedCalendar(t, db, fx.wsID, eventPub)
		recorded := calSubjectRecordedCalendar(t, db, fx.wsID, "calendar.event.created", "eventId", standalonePub)

		require.True(t, recorded.Valid,
			"a calendar creation with no calendar named reaches no per-calendar feed")
		require.Equal(t, int64(landed), recorded.Int64,
			"the event was written to calendar %d and recorded against %d; it surfaces on the wrong feed",
			landed, recorded.Int64)
	})

	t.Run("update_calendar_event/standalone", func(t *testing.T) {
		require.NotEmpty(t, standalonePub, "create_calendar_event must have run first")
		_, err := mcp.RunUpdateCalendarEvent(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
			"eventId": standalonePub,
			"title":   "Agent-retitled review",
		}))
		require.NoError(t, err)

		eventPub, err := uuid.Parse(standalonePub)
		require.NoError(t, err)
		landed := calSubjectLandedCalendar(t, db, fx.wsID, eventPub)
		recorded := calSubjectRecordedCalendar(t, db, fx.wsID, "calendar.event.updated", "eventId", standalonePub)

		require.True(t, recorded.Valid,
			"this branch appends its own event, so it is also the branch that has to name the calendar")
		require.Equal(t, int64(landed), recorded.Int64,
			"the edited event sits on calendar %d and the edit was recorded against %d", landed, recorded.Int64)
	})

	t.Run("update_calendar_event/linked_records_no_calendar", func(t *testing.T) {
		// Stated behaviour, not an oversight. A linked event's edit is
		// appended by itemkit inside its own transaction, through an event
		// type that carries no calendar column at all — so this branch has
		// no calendar id to record and deliberately passes none. Anyone
		// "fixing" it has to change what itemkit appends first; until then
		// a value here would be written by nobody and read by nobody.
		require.NotEqual(t, uuid.Nil, fx.linkedEventPub, "the projection event was not created")
		linkedPub := fx.linkedEventPub.String()
		beforeAudit := countAuditRows(t, db, fx.wsID, "calendar.event.update")

		_, err := mcp.RunUpdateCalendarEvent(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
			"eventId": linkedPub,
			"title":   "Run the review, revised",
		}))
		require.NoError(t, err)

		rows, err := db.Query(
			`SELECT type, calendar_id FROM events
			 WHERE workspace_id = ? AND payload_json->>'$.eventPublicId' = ?`,
			fx.wsID, linkedPub)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()

		seen := 0
		for rows.Next() {
			var kind string
			var calID sql.NullInt64
			require.NoError(t, rows.Scan(&kind, &calID))
			seen++
			require.Falsef(t, calID.Valid,
				"%s carries calendar %d; itemkit appends this row and has no calendar to put there, so a value in it came from somewhere that did not read the event",
				kind, calID.Int64)
		}
		require.NoError(t, rows.Err())
		require.NotZero(t, seen,
			"itemkit appended nothing for the linked edit; the branch under test was never reached")

		// The audit half is the one this branch does record, and it is what
		// makes the absent calendar id a choice rather than a lost write.
		require.Equal(t, beforeAudit+1, countAuditRows(t, db, fx.wsID, "calendar.event.update"),
			"the linked branch records the audit row itself; without it the edit is invisible on both surfaces")
	})

	t.Run("toggle_calendar_memo", func(t *testing.T) {
		_, err := mcp.RunToggleCalendarMemo(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
			"memoId":     fx.memoPub.String(),
			"calendarId": fx.subjectPub.String(),
			"done":       true,
		}))
		require.NoError(t, err)

		landed := calSubjectMemoCalendar(t, db, fx.wsID, fx.memoPub)
		recorded := calSubjectRecordedCalendar(t, db, fx.wsID, "calendar.memo.updated", "memoId", fx.memoPub.String())

		require.True(t, recorded.Valid, "a memo tick with no calendar named reaches no per-calendar feed")
		require.Equal(t, int64(landed), recorded.Int64,
			"the memo belongs to calendar %d and the tick was recorded against %d", landed, recorded.Int64)
	})

	t.Run("create_event_from_task", func(t *testing.T) {
		beforeAudit := countAuditRows(t, db, fx.wsID, "calendar.event.create")
		start := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Hour)

		out, err := mcp.RunCreateEventFromTask(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
			"taskId":     fx.taskPub.String(),
			"calendarId": fx.subjectPub.String(),
			"startAt":    start.Unix(),
			"endAt":      start.Add(30 * time.Minute).Unix(),
		}))
		require.NoError(t, err)
		m, ok := out.(map[string]any)
		require.True(t, ok)
		eventID, ok := m["id"].(string)
		require.True(t, ok)
		eventPub, err := uuid.Parse(eventID)
		require.NoError(t, err)

		// The projection the tool reports is a row that exists, sits on the
		// calendar asked for, and carries the link that makes it a
		// projection rather than a loose entry that happens to share a
		// title. The id in the response is what names it, so a response
		// naming something else fails here rather than downstream.
		var (
			landedCal uint32
			linkedTo  sql.NullInt32
			role      sql.NullString
			enabled   bool
		)
		require.NoError(t, db.QueryRow(
			`SELECT calendar_id, task_id, task_role, enabled FROM calendar_events
			 WHERE workspace_id = ? AND public_id = ?`,
			fx.wsID, eventPub[:]).Scan(&landedCal, &linkedTo, &role, &enabled))
		require.Equal(t, fx.subjectID, landedCal,
			"the projection landed on calendar %d, not the one the call named", landedCal)
		require.True(t, linkedTo.Valid, "the row carries no task link, so it is not a projection at all")
		require.Equal(t, int32(fx.taskID), linkedTo.Int32) //#nosec G115 -- fixture task id, fits int32
		require.Equal(t, "due", role.String,
			"a task-projected event holds the due role; any other value is a row the projection engine will not recognise")
		require.True(t, enabled)

		// No calendar id is recorded, and that follows from the event type
		// the projection engine appends through rather than from the tool
		// forgetting: itemkit files item.scheduled and its legacy
		// counterpart, and neither carries a calendar column. So this path
		// records the audit half only — the same shape as the linked branch
		// of update_calendar_event above. A value here would have to be
		// written by something that does not read the event.
		rows, err := db.Query(
			`SELECT type, calendar_id FROM events
			 WHERE workspace_id = ? AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.eventPublicId')) = ?`,
			fx.wsID, eventID)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		seen := 0
		for rows.Next() {
			var kind string
			var calID sql.NullInt64
			require.NoError(t, rows.Scan(&kind, &calID))
			seen++
			require.Falsef(t, calID.Valid,
				"%s carries calendar %d; the projection engine appends this row through an event type with no calendar field",
				kind, calID.Int64)
		}
		require.NoError(t, rows.Err())
		require.NotZero(t, seen,
			"the projection engine appended nothing, so the creation reaches no timeline")

		// The audit row is the half this path does record, and its action
		// still has to be the one the counterpart operation files. The
		// parity check that compares the two reads source rather than rows,
		// so it cannot see an action that never reaches the table.
		require.Equal(t, beforeAudit+1, countAuditRows(t, db, fx.wsID, "calendar.event.create"),
			"an agent projecting a task onto a calendar must answer the same audit query a request-made one does")

		// Projecting the same task twice moves the one event rather than
		// adding a second, which is what the request path does. "create" in
		// the tool's name invites a duplicate guard that would break that.
		again, err := mcp.RunCreateEventFromTask(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
			"taskId":     fx.taskPub.String(),
			"calendarId": fx.subjectPub.String(),
			"startAt":    start.Add(48 * time.Hour).Unix(),
			"endAt":      start.Add(48*time.Hour + 30*time.Minute).Unix(),
		}))
		require.NoError(t, err)
		repeat, ok := again.(map[string]any)
		require.True(t, ok)
		require.Equal(t, eventID, repeat["id"],
			"the second call reported a different event; the task now projects onto two entries, and the two disagree about when it is due")

		var projections int
		require.NoError(t, db.QueryRow(
			`SELECT COUNT(*) FROM calendar_events
			 WHERE workspace_id = ? AND task_id = ? AND task_role = 'due' AND enabled = TRUE`,
			fx.wsID, fx.taskID).Scan(&projections))
		require.Equal(t, 1, projections,
			"a task holds one due projection; %d of them put the same deadline on the calendar more than once", projections)

		var movedStart sql.NullTime
		require.NoError(t, db.QueryRow(
			`SELECT start_at FROM calendar_events WHERE workspace_id = ? AND public_id = ?`,
			fx.wsID, eventPub[:]).Scan(&movedStart))
		require.True(t, movedStart.Valid)
		require.Equal(t, start.Add(48*time.Hour).UTC(), movedStart.Time.UTC(),
			"the repeat call returned the existing event without moving it, so the window the caller asked for was dropped")
	})
}
