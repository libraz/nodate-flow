package calendar

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// refusedMemberColor is the colour the trigger below refuses. It has to
// be one no other test uses, because the trigger sits on the table the
// whole package writes to; every other colour here comes from the
// palette or from a literal that is not this one.
const refusedMemberColor = "#FA11ED"

// refuseOwnerGrantWrites installs a trigger that fails the
// calendar_members insert for one specific member colour, and removes it
// when the test ends.
//
// A create writes two rows, and only the second can be made to fail on
// demand: both carry the colour from the request body, so no input
// reaches one write without reaching the other. The trigger is the
// smallest thing that tells them apart.
func refuseOwnerGrantWrites(t *testing.T) {
	t.Helper()
	_, err := testDB.ExecContext(context.Background(),
		`CREATE TRIGGER trg_test_refuse_calendar_member
		 BEFORE INSERT ON calendar_members
		 FOR EACH ROW
		 BEGIN
		   IF NEW.member_color = '`+refusedMemberColor+`' THEN
		     SIGNAL SQLSTATE '45000'
		       SET MESSAGE_TEXT = 'calendar member write refused';
		   END IF;
		 END`)
	require.NoError(t, err, "install the trigger that fails the owner grant")
	t.Cleanup(func() {
		_, err := testDB.ExecContext(context.Background(),
			`DROP TRIGGER IF EXISTS trg_test_refuse_calendar_member`)
		require.NoError(t, err, "remove the trigger")
	})
}

// countCalendarsNamed returns how many calendar rows a workspace holds
// under a name, whatever their enabled state.
func countCalendarsNamed(t *testing.T, wsID uint32, name string) int {
	t.Helper()
	var n int
	err := testDB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM calendars WHERE workspace_id = ? AND name = ?`,
		wsID, name).Scan(&n)
	require.NoError(t, err, "count calendars named %q", name)
	return n
}

// TestCreateCalendar_LeavesNoCalendarWhenTheOwnerGrantFails asserts that
// the two rows a create writes land together or not at all.
//
// Access to a calendar is calendar_members, so a calendar row committed
// without the creator's grant is one nobody can open, list or delete —
// no endpoint can reach it again to repair it. The row must not be
// there.
//
// The successful create comes first and is the control: it is the same
// request with the same colour, so a create that had started failing for
// an unrelated reason — a rejected colour, a broken route — cannot pass
// the second half by leaving no row behind for the wrong reason. The
// test is deliberately not parallel; the trigger it installs is visible
// to every writer of calendar_members while it is in place.
func TestCreateCalendar_LeavesNoCalendarWhenTheOwnerGrantFails(t *testing.T) {
	bootstrap(t)

	tt := newTenant(t)

	const grantedName = "Calendar with an owner grant"
	var created struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars"), tt.AccessToken,
		map[string]any{"kind": "personal", "name": grantedName, "color": refusedMemberColor},
		&created)
	require.NotEmpty(t, created.ID)

	calID := helpers.ResolveCalendarInternalID(t, testDB, created.ID)
	var role string
	require.NoError(t, testDB.QueryRowContext(context.Background(),
		`SELECT role FROM calendar_members
		 WHERE calendar_id = ? AND user_id = ? AND enabled = TRUE`,
		calID, tt.UserInternalID).Scan(&role),
		"the creator must hold a grant on the calendar they created")
	assert.Equal(t, "owner", role)

	refuseOwnerGrantWrites(t)

	const refusedName = "Calendar whose grant is refused"
	status, body := helpers.DoJSONStatus(t, http.MethodPost, tt.WsPath("calendars"), tt.AccessToken,
		map[string]any{"kind": "personal", "name": refusedName, "color": refusedMemberColor})
	require.Equal(t, http.StatusInternalServerError, status,
		"the create must report the failed grant write, body=%s", string(body))

	assert.Zero(t, countCalendarsNamed(t, tt.WorkspaceID, refusedName),
		"a create that could not write its owner grant must leave no calendar row")
}
