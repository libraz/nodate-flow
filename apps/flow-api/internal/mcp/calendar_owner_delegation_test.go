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

// sharedCalendarFixture is a calendar that belongs to a group rather than
// to a person: calendars.owner_user_id is NULL, which is what the column
// requires of a calendar meant to outlive any one member.
//
// It carries the three standings the owner rule distinguishes: a manager,
// an editor, and somebody an event can be filed under.
type sharedCalendarFixture struct {
	wsID        uint32
	calendarPub uuid.UUID
	calendarID  uint32
	managerID   uint32
	editorID    uint32
	targetID    uint32
	targetPub   uuid.UUID
}

func seedSharedCalendarFixture(t *testing.T, db *sql.DB) *sharedCalendarFixture {
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
		wsPub[:], "calowner-ws-"+suffix, "CalOwner Workspace")
	require.NoError(t, err)
	wsID64, err := res.LastInsertId()
	require.NoError(t, err)
	wsID := uint32(wsID64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	newMember := func(label, role string) (uint32, uuid.UUID) {
		userPub := uuid.Must(uuid.NewV7())
		r, insErr := tx.ExecContext(ctx,
			`INSERT INTO users (public_id, email, display_name, locale) VALUES (?, ?, ?, 'en')`,
			userPub[:], "calowner-"+label+"-"+suffix+"@example.test", "CalOwner "+label+" "+suffix)
		require.NoError(t, insErr)
		id64, idErr := r.LastInsertId()
		require.NoError(t, idErr)
		id := uint32(id64) //#nosec G115 -- LastInsertId in test seed, fits uint32

		wmPub := uuid.Must(uuid.NewV7())
		_, insErr = tx.ExecContext(ctx,
			`INSERT INTO workspace_members (public_id, workspace_id, user_id, role) VALUES (?, ?, ?, ?)`,
			wmPub[:], wsID, id, role)
		require.NoError(t, insErr)
		return id, userPub
	}

	managerID, _ := newMember("manager", "member")
	editorID, _ := newMember("editor", "member")
	targetID, targetPub := newMember("target", "member")

	// owner_user_id stays NULL: this is the shape of calendar the old
	// lookup could never accept a delegation on.
	calPub := uuid.Must(uuid.NewV7())
	res, err = tx.ExecContext(ctx,
		`INSERT INTO calendars (public_id, workspace_id, kind, name) VALUES (?, ?, 'personal', ?)`,
		calPub[:], wsID, "Team Calendar")
	require.NoError(t, err)
	calID64, err := res.LastInsertId()
	require.NoError(t, err)
	calID := uint32(calID64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	for userID, role := range map[uint32]string{
		managerID: "manager",
		editorID:  "editor",
		targetID:  "editor",
	} {
		cmPub := uuid.Must(uuid.NewV7())
		_, err = tx.ExecContext(ctx,
			`INSERT INTO calendar_members (public_id, workspace_id, calendar_id, user_id, role) VALUES (?, ?, ?, ?, ?)`,
			cmPub[:], wsID, calID, userID, role)
		require.NoError(t, err)
	}

	require.NoError(t, tx.Commit())
	committed = true

	return &sharedCalendarFixture{
		wsID:        wsID,
		calendarPub: calPub,
		calendarID:  calID,
		managerID:   managerID,
		editorID:    editorID,
		targetID:    targetID,
		targetPub:   targetPub,
	}
}

// eventOwner reads back who an event ended up filed under, which is the
// only place the decision is observable after the call returns.
func eventOwner(t *testing.T, db *sql.DB, publicID string) uint32 {
	t.Helper()
	pub := uuid.MustParse(publicID)
	var owner uint32
	require.NoError(t, db.QueryRow(
		`SELECT owner_user_id FROM calendar_events WHERE public_id = ?`, pub[:]).Scan(&owner))
	return owner
}

// TestMCPCalendarEventOwnerRuleIsShared drives create_calendar_event on a
// calendar nobody owns and pins the three answers eventacl.CanSetOwner
// gives, which are the three answers REST gives.
//
// The tool used to decide for itself, by comparing the caller against
// calendars.owner_user_id. That column is NULL on a group calendar, so
// the comparison failed for everyone: on exactly the calendars that have
// managers, no manager could delegate. The manager subtest below is the
// case that used to be refused.
func TestMCPCalendarEventOwnerRuleIsShared(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedSharedCalendarFixture(t, db)

	deps := mcp.Deps{DB: db, Queries: generated.New(db), CalendarQueries: calendar.New(db)}
	ctx := context.Background()
	start := time.Now().UTC().Add(time.Hour).Unix()

	create := func(t *testing.T, actorID uint32, owner string, title string) (any, error) {
		t.Helper()
		sess := mcp.NewTestSession(actorID, fx.wsID, []string{"write:workspace"})
		args := map[string]any{
			"calendarId": fx.calendarPub.String(),
			"title":      title,
			"startAt":    start,
			"endAt":      start + 3600,
		}
		if owner != "" {
			args["ownerUserId"] = owner
		}
		return mcp.RunCreateCalendarEvent(ctx, deps, sess, mcpTrailArgs(t, args))
	}

	t.Run("manager delegates on a calendar nobody owns", func(t *testing.T) {
		out, err := create(t, fx.managerID, fx.targetPub.String(), "Delegated review")
		require.NoError(t, err,
			"a manager may file an event under a colleague; refusing here is the divergence the shared rule removes")
		m, ok := out.(map[string]any)
		require.True(t, ok)
		id, ok := m["id"].(string)
		require.True(t, ok)
		require.Equal(t, fx.targetID, eventOwner(t, db, id),
			"the event has to land on the layer it was delegated to, not on the caller's")
	})

	t.Run("editor may not delegate", func(t *testing.T) {
		_, err := create(t, fx.editorID, fx.targetPub.String(), "Uninvited commitment")
		requireSpec(t, err, apierrors.CalendarEventEditPermissionRequired)
	})

	t.Run("anyone may file their own event", func(t *testing.T) {
		editorPub := userPublicID(t, db, fx.editorID)
		out, err := create(t, fx.editorID, editorPub, "Own focus block")
		require.NoError(t, err)
		m, ok := out.(map[string]any)
		require.True(t, ok)
		id, ok := m["id"].(string)
		require.True(t, ok)
		require.Equal(t, fx.editorID, eventOwner(t, db, id))
	})
}

// userPublicID reads a seeded user's public id, so a test never has to
// carry an internal id across the API boundary.
func userPublicID(t *testing.T, db *sql.DB, userID uint32) string {
	t.Helper()
	var raw []byte
	require.NoError(t, db.QueryRow(`SELECT public_id FROM users WHERE id = ?`, userID).Scan(&raw))
	parsed, err := uuid.FromBytes(raw)
	require.NoError(t, err)
	return parsed.String()
}
