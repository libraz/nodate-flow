package sqlviews

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	generated "github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/softdelete"
)

// A grant whose tuple is keyed without a liveness marker outlives its own
// revocation: the revoked row keeps holding the pair, so adding the same
// pair again has to revive that row. Nothing about it shows up in a
// single add-remove pass, which is how the collision survived in several
// tables at once — every test added a member, removed them, and stopped.
//
// So this runs the sequence that fails: add, remove, add. The tables it
// runs it on are the ones softdelete derives from the schema and the
// query tree, and a derived table with no cycle below fails rather than
// going unexercised, because a list of tables written out by hand is the
// thing that fell behind in the first place.

// grantFixture is one tenant's worth of rows: everything the grants
// below need on both ends, plus a second user to grant things to.
type grantFixture struct {
	ctx        context.Context
	q          *generated.Queries
	cq         *calendar.Queries
	wsID       uint32
	projectID  uint32
	ownerID    uint32
	memberID   uint32
	calendarID uint32
	eventID    uint32
	taskID     uint32
	attendeeID uint32
}

func seedGrantFixture(t *testing.T) *grantFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	owner := helpers.CreateTestTenant(t, testSrv.BaseURL)
	t.Cleanup(func() {
		helpers.CleanupTenant(t, owner)
		helpers.PurgeWorkspace(t, testDB, owner.WorkspacePublicID)
	})
	// The second user only has to exist; the grants under test are what
	// brings them into the owner's workspace.
	member := helpers.CreateTestTenant(t, testSrv.BaseURL)
	t.Cleanup(func() {
		helpers.CleanupTenant(t, member)
		helpers.PurgeWorkspace(t, testDB, member.WorkspacePublicID)
	})

	f := &grantFixture{ctx: ctx, q: generated.New(testDB), cq: calendar.New(testDB)}
	f.wsID = helpers.ResolveWorkspaceInternalID(t, testDB, owner.WorkspacePublicID)
	f.ownerID = helpers.ResolveUserInternalID(t, testDB, owner.UserPublicID)
	f.memberID = helpers.ResolveUserInternalID(t, testDB, member.UserPublicID)
	projectPub, err := types.Parse(owner.ProjectPublicID)
	require.NoError(t, err)
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM projects WHERE public_id = ?`, projectPub).Scan(&f.projectID))

	// Creating the workspace provisioned a personal calendar for its
	// owner, which is calendar enough for a membership or an event.
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM calendars WHERE workspace_id = ? AND enabled = TRUE ORDER BY id LIMIT 1`,
		f.wsID).Scan(&f.calendarID))

	eventPub := uuid.Must(uuid.NewV7())
	res, err := testDB.ExecContext(ctx, `
		INSERT INTO calendar_events
			(public_id, workspace_id, calendar_id, title, start_at, end_at, timezone,
			 owner_user_id, created_by_user_id)
		VALUES (?, ?, ?, 'revival cycle', '2030-04-01 09:00:00', '2030-04-01 10:00:00', 'UTC', ?, ?)`,
		eventPub[:], f.wsID, f.calendarID, f.ownerID, f.ownerID)
	require.NoError(t, err)
	f.eventID = lastInsertID(t, res)

	taskPub := uuid.Must(uuid.NewV7())
	res, err = testDB.ExecContext(ctx, `
		INSERT INTO tasks (public_id, workspace_id, project_id, task_number, created_by_user_id, title)
		VALUES (?, ?, ?, 9001, ?, 'revival cycle task')`,
		taskPub[:], f.wsID, f.projectID, f.ownerID)
	require.NoError(t, err)
	f.taskID = lastInsertID(t, res)

	attendeePub := uuid.Must(uuid.NewV7())
	res, err = testDB.ExecContext(ctx, `
		INSERT INTO calendar_event_attendees (public_id, workspace_id, event_id, user_id)
		VALUES (?, ?, ?, ?)`,
		attendeePub[:], f.wsID, f.eventID, f.ownerID)
	require.NoError(t, err)
	f.attendeeID = lastInsertID(t, res)

	return f
}

func lastInsertID(t *testing.T, res sql.Result) uint32 {
	t.Helper()
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return uint32(id) //#nosec G115 -- test-scoped LastInsertId fits uint32
}

// liveRows counts the live rows of a table matching a predicate, and is
// how each cycle states what it expects to be true afterwards.
func liveRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var count int
	require.NoError(t, testDB.QueryRow(query, args...).Scan(&count))
	return count
}

func TestRevokedGrantCanBeGrantedAgain(t *testing.T) {
	skipIfNoIntegration(t)

	root, err := softdelete.RepoRoot()
	require.NoError(t, err)
	shaped, err := softdelete.RevivalTables(root)
	require.NoError(t, err)
	queries, err := softdelete.Queries(root)
	require.NoError(t, err)
	derived := softdelete.InScope(shaped, queries)
	require.NotEmpty(t, derived, "no table derived; the schema or query parser has stopped matching")

	covered := map[string]bool{}
	for _, table := range derived {
		cycle, ok := revivalCycles[table.Name]
		if !ok {
			t.Errorf("%s is keyed on a tuple that a revocation leaves in place, and no cycle here "+
				"adds, removes and adds again on it: the writer's revival is unverified", table.Name)
			continue
		}
		covered[table.Name] = true
		t.Run(table.Name, func(t *testing.T) {
			cycle(t, seedGrantFixture(t))
		})
	}
	for name := range revivalCycles {
		if !covered[name] {
			t.Errorf("a cycle exists for %s, which is no longer derived as needing one; "+
				"either the table changed shape or the derivation stopped seeing it", name)
		}
	}
}

// revivalCycles holds one add / remove / add sequence per table. The map
// is keyed by table name but is not the inventory: the derivation is,
// and a table missing from here is reported above.
var revivalCycles = map[string]func(*testing.T, *grantFixture){
	"workspace_members": func(t *testing.T, f *grantFixture) {
		add := func() error {
			_, err := f.q.CreateWorkspaceMember(f.ctx, generated.CreateWorkspaceMemberParams{
				PublicID:    types.New(),
				WorkspaceID: f.wsID,
				UserID:      f.memberID,
				Role:        generated.WorkspaceMembersRoleMember,
				InvitedAt:   sql.NullTime{Time: time.Now(), Valid: true},
				JoinedAt:    sql.NullTime{Time: time.Now(), Valid: true},
			})
			return err
		}
		require.NoError(t, add())
		require.NoError(t, f.q.RemoveWorkspaceMemberByUserId(f.ctx, generated.RemoveWorkspaceMemberByUserIdParams{
			WorkspaceID: f.wsID,
			UserID:      f.memberID,
		}))
		require.NoError(t, add(), "re-adding a removed workspace member")
		require.Equal(t, 1, liveRows(t,
			`SELECT COUNT(*) FROM workspace_members WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE`,
			f.wsID, f.memberID))
	},

	"project_members": func(t *testing.T, f *grantFixture) {
		add := func() error {
			_, err := f.q.AddProjectMember(f.ctx, generated.AddProjectMemberParams{
				PublicID:    types.New(),
				WorkspaceID: f.wsID,
				ProjectID:   f.projectID,
				UserID:      f.memberID,
				Role:        generated.ProjectMembersRoleEditor,
				AddedAt:     sql.NullTime{Time: time.Now(), Valid: true},
			})
			return err
		}
		require.NoError(t, add())
		require.NoError(t, f.q.RemoveProjectMemberByUserId(f.ctx, generated.RemoveProjectMemberByUserIdParams{
			ProjectID: f.projectID,
			UserID:    f.memberID,
		}))
		require.NoError(t, add(), "re-adding a removed project member")
		require.Equal(t, 1, liveRows(t,
			`SELECT COUNT(*) FROM project_members WHERE project_id = ? AND user_id = ? AND enabled = TRUE`,
			f.projectID, f.memberID))
	},

	"calendar_members": func(t *testing.T, f *grantFixture) {
		add := func() error {
			_, err := f.cq.UpsertCalendarMember(f.ctx, calendar.UpsertCalendarMemberParams{
				PublicID:    types.New(),
				WorkspaceID: f.wsID,
				CalendarID:  f.calendarID,
				UserID:      f.memberID,
				Role:        calendar.CalendarMembersRoleEditor,
				MemberColor: "#4285F4",
			})
			return err
		}
		require.NoError(t, add())
		_, err := f.cq.DisableCalendarMember(f.ctx, calendar.DisableCalendarMemberParams{
			CalendarID: f.calendarID,
			UserID:     f.memberID,
		})
		require.NoError(t, err)
		require.NoError(t, add(), "re-granting a revoked calendar membership")
		require.Equal(t, 1, liveRows(t,
			`SELECT COUNT(*) FROM calendar_members WHERE calendar_id = ? AND user_id = ? AND enabled = TRUE`,
			f.calendarID, f.memberID))
	},

	"calendar_subscriptions": func(t *testing.T, f *grantFixture) {
		add := func() error {
			_, err := f.cq.CreateCalendarSubscription(f.ctx, calendar.CreateCalendarSubscriptionParams{
				PublicID:     types.New(),
				WorkspaceID:  f.wsID,
				CalendarID:   f.calendarID,
				UserID:       f.memberID,
				DisplayColor: "#4285F4",
			})
			return err
		}
		require.NoError(t, add())
		require.NoError(t, f.cq.DisableCalendarSubscription(f.ctx, calendar.DisableCalendarSubscriptionParams{
			CalendarID: f.calendarID,
			UserID:     f.memberID,
		}))
		require.NoError(t, add(), "subscribing again after unsubscribing")
		require.Equal(t, 1, liveRows(t,
			`SELECT COUNT(*) FROM calendar_subscriptions WHERE calendar_id = ? AND user_id = ? AND enabled = TRUE`,
			f.calendarID, f.memberID))
	},

	"calendar_event_attendees": func(t *testing.T, f *grantFixture) {
		add := func() error {
			_, err := f.cq.CreateCalendarEventAttendee(f.ctx, calendar.CreateCalendarEventAttendeeParams{
				PublicID:    types.New(),
				WorkspaceID: f.wsID,
				EventID:     sql.NullInt32{Int32: int32(f.eventID), Valid: true}, //#nosec G115 -- test-scoped id
				UserID:      f.memberID,
				Rsvp:        calendar.CalendarEventAttendeesRsvpPending,
				CanEdit:     false,
			})
			return err
		}
		require.NoError(t, add())

		// The answer this attendee gave belongs to the invitation they
		// were removed from, not to the next one.
		_, err := testDB.ExecContext(f.ctx,
			`UPDATE calendar_event_attendees SET rsvp = 'accepted' WHERE event_id = ? AND user_id = ?`,
			f.eventID, f.memberID)
		require.NoError(t, err)

		require.NoError(t, f.cq.DisableCalendarEventAttendee(f.ctx, calendar.DisableCalendarEventAttendeeParams{
			EventID: sql.NullInt32{Int32: int32(f.eventID), Valid: true}, //#nosec G115 -- test-scoped id
			UserID:  f.memberID,
		}))
		require.NoError(t, add(), "inviting a removed attendee back to the event")

		row, err := f.cq.FindCalendarEventAttendee(f.ctx, calendar.FindCalendarEventAttendeeParams{
			EventID: sql.NullInt32{Int32: int32(f.eventID), Valid: true}, //#nosec G115 -- test-scoped id
			UserID:  f.memberID,
		})
		require.NoError(t, err)
		require.Equal(t, calendar.CalendarEventAttendeesRsvpPending, row.Rsvp,
			"a re-invited attendee answers the new invitation, so the RSVP they gave before being removed must not come back")
		require.False(t, row.CanEdit, "edit permission taken away with the attendee must not return with them")
	},

	"calendar_event_invites": func(t *testing.T, f *grantFixture) {
		// The invite carries a token, so its writer is the other
		// accepted shape: find the row whatever its state, then revive
		// it with a new capability rather than insert a second one.
		eventID := sql.NullInt32{Int32: int32(f.eventID), Valid: true}       //#nosec G115 -- test-scoped id
		attendeeID := sql.NullInt32{Int32: int32(f.attendeeID), Valid: true} //#nosec G115 -- test-scoped id
		// token_hash is the raw BINARY(32) digest of the link token.
		firstToken := []byte("a1b2c3d4e5f60718293a4b5c6d7e8f90")
		secondToken := []byte("0f9e8d7c6b5a493827160f5e4d3c2b1a")

		_, err := f.cq.CreateCalendarEventInvite(f.ctx, calendar.CreateCalendarEventInviteParams{
			PublicID:    types.New(),
			WorkspaceID: f.wsID,
			CalendarID:  f.calendarID,
			EventID:     eventID,
			AttendeeID:  attendeeID,
			Email:       "invitee@example.test",
			TokenHash:   firstToken,
			ExpiresAt:   time.Now().Add(24 * time.Hour),
		})
		require.NoError(t, err)

		existing, err := f.cq.FindCalendarEventInviteForAttendee(f.ctx, calendar.FindCalendarEventInviteForAttendeeParams{
			EventID:    eventID,
			AttendeeID: attendeeID,
		})
		require.NoError(t, err)
		require.NoError(t, f.cq.DisableCalendarEventInvite(f.ctx, existing.ID))

		revoked, err := f.cq.FindCalendarEventInviteForAttendee(f.ctx, calendar.FindCalendarEventInviteForAttendeeParams{
			EventID:    eventID,
			AttendeeID: attendeeID,
		})
		require.NoError(t, err, "the lookup that precedes an invite has to see the revoked row, "+
			"or the caller inserts beside it and collides")
		require.False(t, revoked.Enabled)

		require.NoError(t, f.cq.ReviveCalendarEventInvite(f.ctx, calendar.ReviveCalendarEventInviteParams{
			TokenHash: secondToken,
			ExpiresAt: time.Now().Add(24 * time.Hour),
			ID:        revoked.ID,
		}))
		require.Equal(t, 1, liveRows(t,
			`SELECT COUNT(*) FROM calendar_event_invites WHERE event_id = ? AND attendee_id = ? AND enabled = TRUE`,
			f.eventID, f.attendeeID))
		require.Equal(t, 0, liveRows(t,
			`SELECT COUNT(*) FROM calendar_event_invites WHERE id = ? AND token_hash = ?`,
			revoked.ID, string(firstToken)),
			"a revived invite mints a new token; restoring the old one would undo the revocation for anyone still holding the link")
	},

	"task_actors": func(t *testing.T, f *grantFixture) {
		var actorPublicID types.PublicID
		add := func() error {
			actorPublicID = types.New()
			_, err := f.q.AddActor(f.ctx, generated.AddActorParams{
				PublicID:    actorPublicID,
				WorkspaceID: f.wsID,
				TaskID:      f.taskID,
				UserID:      sql.NullInt32{Int32: int32(f.memberID), Valid: true}, //#nosec G115 -- test-scoped id
				Role:        generated.TaskActorsRoleAssignee,
			})
			return err
		}
		require.NoError(t, add())
		_, err := f.q.RemoveActor(f.ctx, generated.RemoveActorParams{
			WorkspaceID: f.wsID,
			PublicID:    actorPublicID,
		})
		require.NoError(t, err)
		require.NoError(t, add(), "re-attaching a removed actor to the task")
		require.Equal(t, 1, liveRows(t,
			`SELECT COUNT(*) FROM task_actors WHERE task_id = ? AND user_id = ? AND role = 'assignee' AND enabled = TRUE`,
			f.taskID, f.memberID))
	},
}
