package mcp_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// calendarRoleFixture is one workspace holding the three shapes a calendar
// takes, with a member at every role on the one that distinguishes them.
type calendarRoleFixture struct {
	wsID uint32

	// sharedPub belongs to a group rather than to a person, so
	// calendars.owner_user_id is NULL — the shape the column requires of a
	// calendar meant to outlive any single member.
	sharedPub uuid.UUID
	// personalPub belongs to one user, named in calendars.owner_user_id.
	personalPub uuid.UUID
	// systemPub is populated from a provider feed and is read-only to
	// users at every role.
	systemPub uuid.UUID

	viewerID   uint32
	editorID   uint32
	managerID  uint32
	ownerID    uint32
	personalID uint32
	feedID     uint32
}

func seedCalendarRoleFixture(t *testing.T, db *sql.DB) *calendarRoleFixture {
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
		wsPub[:], "calrole-ws-"+suffix, "CalRole Workspace")
	require.NoError(t, err)
	wsID64, err := res.LastInsertId()
	require.NoError(t, err)
	wsID := uint32(wsID64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	newMember := func(label string) uint32 {
		userPub := uuid.Must(uuid.NewV7())
		r, insErr := tx.ExecContext(ctx,
			`INSERT INTO users (public_id, email, display_name, locale) VALUES (?, ?, ?, 'en')`,
			userPub[:], "calrole-"+label+"-"+suffix+"@example.test", "CalRole "+label+" "+suffix)
		require.NoError(t, insErr)
		id64, idErr := r.LastInsertId()
		require.NoError(t, idErr)
		id := uint32(id64) //#nosec G115 -- LastInsertId in test seed, fits uint32

		wmPub := uuid.Must(uuid.NewV7())
		_, insErr = tx.ExecContext(ctx,
			`INSERT INTO workspace_members (public_id, workspace_id, user_id, role) VALUES (?, ?, ?, 'member')`,
			wmPub[:], wsID, id)
		require.NoError(t, insErr)
		return id
	}

	viewerID := newMember("viewer")
	editorID := newMember("editor")
	managerID := newMember("manager")
	ownerID := newMember("owner")
	personalID := newMember("personal")
	feedID := newMember("feed")

	newCalendar := func(kind, name string, owner sql.NullInt32, slug sql.NullString) (uuid.UUID, uint32) {
		calPub := uuid.Must(uuid.NewV7())
		r, insErr := tx.ExecContext(ctx,
			`INSERT INTO calendars (public_id, workspace_id, kind, name, owner_user_id, system_slug)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			calPub[:], wsID, kind, name, owner, slug)
		require.NoError(t, insErr)
		id64, idErr := r.LastInsertId()
		require.NoError(t, idErr)
		return calPub, uint32(id64) //#nosec G115 -- LastInsertId in test seed, fits uint32
	}

	addMember := func(calID, userID uint32, role string) {
		cmPub := uuid.Must(uuid.NewV7())
		_, insErr := tx.ExecContext(ctx,
			`INSERT INTO calendar_members (public_id, workspace_id, calendar_id, user_id, role) VALUES (?, ?, ?, ?, ?)`,
			cmPub[:], wsID, calID, userID, role)
		require.NoError(t, insErr)
	}

	// owner_user_id stays NULL. This is the calendar every reported role
	// used to collapse on, because the column the role was derived from
	// holds nothing here.
	sharedPub, sharedID := newCalendar("personal", "Team Calendar", sql.NullInt32{}, sql.NullString{})
	addMember(sharedID, viewerID, "viewer")
	addMember(sharedID, editorID, "editor")
	addMember(sharedID, managerID, "manager")
	addMember(sharedID, ownerID, "owner")

	personalPub, personalCalID := newCalendar("personal", "Personal Calendar",
		sql.NullInt32{Int32: int32(personalID), Valid: true}, sql.NullString{}) //#nosec G115 -- seeded users.id, fits int32
	addMember(personalCalID, personalID, "owner")

	systemPub, systemCalID := newCalendar("system", "Holidays", sql.NullInt32{},
		sql.NullString{String: "holidays." + suffix, Valid: true})
	addMember(systemCalID, feedID, "viewer")

	require.NoError(t, tx.Commit())
	committed = true

	return &calendarRoleFixture{
		wsID:        wsID,
		sharedPub:   sharedPub,
		personalPub: personalPub,
		systemPub:   systemPub,
		viewerID:    viewerID,
		editorID:    editorID,
		managerID:   managerID,
		ownerID:     ownerID,
		personalID:  personalID,
		feedID:      feedID,
	}
}

// storedCalendarRole reads the grant the calendar rules are decided on, so
// the expectation comes from the same row the product reads rather than
// from a literal repeated next to the seed.
func storedCalendarRole(t *testing.T, db *sql.DB, calendarPub uuid.UUID, userID uint32) string {
	t.Helper()
	var role string
	require.NoError(t, db.QueryRow(
		`SELECT cm.role
		   FROM calendar_members cm
		   INNER JOIN calendars c ON c.id = cm.calendar_id
		  WHERE c.public_id = ? AND cm.user_id = ?`,
		calendarPub[:], userID).Scan(&role))
	return role
}

// reportedCalendarRole drives list_calendars as one actor and returns the
// role the tool reports for one calendar.
func reportedCalendarRole(
	ctx context.Context,
	t *testing.T,
	deps mcp.Deps,
	wsID, userID uint32,
	calendarPub uuid.UUID,
) string {
	t.Helper()
	sess := mcp.NewTestSession(userID, wsID, []string{"read:workspace"})
	out, err := mcp.RunListCalendars(ctx, deps, sess, mcpTrailArgs(t, map[string]any{}))
	require.NoError(t, err)
	m, ok := out.(map[string]any)
	require.True(t, ok)
	items, ok := m["calendars"].([]map[string]any)
	require.True(t, ok, "list_calendars answers with a list of calendars")
	for _, item := range items {
		if item["id"] != calendarPub.String() {
			continue
		}
		role, isString := item["role"].(string)
		require.True(t, isString, "the reported role has to be a string")
		return role
	}
	t.Fatalf("calendar %s is missing from the list its own member was given", calendarPub)
	return ""
}

// TestMCPListCalendarsReportsTheMembershipRole pins the role the tool
// reports to the grant every calendar decision is taken on.
//
// The role used to be derived from calendars.owner_user_id and the
// calendar's kind. That column is NULL on every calendar a group shares,
// so on exactly the calendars that have distinct roles the derivation
// could not tell them apart: a viewer, an editor, a manager and the
// calendar's own owner were all reported as "editor". A caller that reads
// this field to decide whether to attempt a write was told it could, said
// so to its user, and was then correctly refused by the rule that governs
// the write.
func TestMCPListCalendarsReportsTheMembershipRole(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedCalendarRoleFixture(t, db)

	deps := mcp.Deps{DB: db, Queries: generated.New(db), CalendarQueries: calendar.New(db)}
	ctx := context.Background()

	cases := []struct {
		name        string
		actorID     uint32
		calendarPub uuid.UUID
	}{
		{"shared/viewer", fx.viewerID, fx.sharedPub},
		{"shared/editor", fx.editorID, fx.sharedPub},
		{"shared/manager", fx.managerID, fx.sharedPub},
		// The owner of a calendar that names no owner in the column: the
		// grant says owner, calendars.owner_user_id says nothing, and the
		// derived answer was "editor" — a demotion nothing in the product
		// had made, reported to the one member who can delete the thing.
		{"shared/owner", fx.ownerID, fx.sharedPub},
		{"personal/owner", fx.personalID, fx.personalPub},
		{"system/viewer", fx.feedID, fx.systemPub},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := storedCalendarRole(t, db, tc.calendarPub, tc.actorID)
			got := reportedCalendarRole(ctx, t, deps, fx.wsID, tc.actorID, tc.calendarPub)
			require.Equal(t, want, got,
				"list_calendars has to report the calendar_members grant; any other answer is a claim about the caller's authority that the write rule does not honour")
		})
	}
}

// TestMCPListCalendarsRoleAgreesWithTheWriteRule pairs the reported role
// with what the caller can actually do.
//
// Reporting a role is only worth doing if it predicts the refusal. Under
// the derived role the two members below were reported identically and
// only one of them could write, so the field was wrong in a way no caller
// could detect before acting on it.
func TestMCPListCalendarsRoleAgreesWithTheWriteRule(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedCalendarRoleFixture(t, db)

	deps := mcp.Deps{DB: db, Queries: generated.New(db), CalendarQueries: calendar.New(db)}
	ctx := context.Background()
	start := time.Now().UTC().Add(time.Hour).Unix()

	createEvent := func(actorID uint32, title string) error {
		sess := mcp.NewTestSession(actorID, fx.wsID, []string{"write:workspace"})
		_, err := mcp.RunCreateCalendarEvent(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
			"calendarId": fx.sharedPub.String(),
			"title":      title,
			"startAt":    start,
			"endAt":      start + 3600,
		}))
		return err
	}

	require.Equal(t, "viewer", reportedCalendarRole(ctx, t, deps, fx.wsID, fx.viewerID, fx.sharedPub))
	requireSpec(t, createEvent(fx.viewerID, "Not mine to add"), apierrors.CalendarCalendarEditorRoleRequired)

	// The control. Without it, a tool that reported "viewer" for everybody
	// and refused every write would satisfy the pair above.
	require.Equal(t, "editor", reportedCalendarRole(ctx, t, deps, fx.wsID, fx.editorID, fx.sharedPub))
	require.NoError(t, createEvent(fx.editorID, "Sprint planning"))
}
