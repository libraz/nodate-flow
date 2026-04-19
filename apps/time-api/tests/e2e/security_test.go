package e2e

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/time-api/tests/helpers"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// createInviteViaAPI creates a calendar invite through the API and returns the token.
func createInviteViaAPI(t *testing.T, tt *helpers.TestTenant, calID string, body map[string]any) string {
	t.Helper()
	var resp struct {
		Token string `json:"token"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "invites"), tt.AccessToken, body, &resp)
	require.NotEmpty(t, resp.Token)
	return resp.Token
}

// createExpiredInviteDirectly inserts an invite with an already-passed expiry into the DB.
// The token parameter is the plaintext token; it is hashed before storage.
func createExpiredInviteDirectly(t *testing.T, db *sql.DB, wsID, calID, createdByUserID uint32, token string) {
	t.Helper()
	pubID := types.New()
	q := generated.New(db)
	_, err := q.CreateCalendarInvite(context.Background(), generated.CreateCalendarInviteParams{
		PublicID:        pubID,
		WorkspaceID:     wsID,
		CalendarID:      calID,
		CreatedByUserID: createdByUserID,
		TokenHash:       authn.HashOpaque(token),
		Role:            generated.CalendarInvitesRoleViewer,
		ExpiresAt:       sql.NullTime{Time: time.Now().Add(-24 * time.Hour), Valid: true},
	})
	require.NoError(t, err)
}

// createExhaustedInviteDirectly inserts an invite that has max_uses=1 and use_count=1.
// The token parameter is the plaintext token; it is hashed before storage.
func createExhaustedInviteDirectly(t *testing.T, db *sql.DB, wsID, calID, createdByUserID uint32, token string) {
	t.Helper()
	pubID := types.New()
	q := generated.New(db)
	tokenHash := authn.HashOpaque(token)
	_, err := q.CreateCalendarInvite(context.Background(), generated.CreateCalendarInviteParams{
		PublicID:        pubID,
		WorkspaceID:     wsID,
		CalendarID:      calID,
		CreatedByUserID: createdByUserID,
		TokenHash:       tokenHash,
		Role:            generated.CalendarInvitesRoleViewer,
		MaxUses:         sql.NullInt32{Int32: 1, Valid: true},
	})
	require.NoError(t, err)

	// Set use_count to 1 directly.
	_, err = db.ExecContext(context.Background(),
		`UPDATE calendar_invites SET use_count = 1 WHERE token_hash = ?`, tokenHash)
	require.NoError(t, err)
}

func TestExpiredInviteCannotExportICS(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)
	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)

	token := "expired-export-" + helpers.RandomHex(8)
	createExpiredInviteDirectly(t, testDB, tt.WorkspaceID, calInternalID, tt.UserInternalID, token)

	status, _ := helpers.DoJSONStatus(t, http.MethodGet,
		testSrv.BaseURL+"/share/"+token+"/export.ics", "", nil)
	assert.Equal(t, http.StatusNotFound, status, "expired invite should not export ICS")
}

func TestExhaustedInviteCannotExportICS(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)
	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)

	token := "exhausted-export-" + helpers.RandomHex(8)
	createExhaustedInviteDirectly(t, testDB, tt.WorkspaceID, calInternalID, tt.UserInternalID, token)

	status, _ := helpers.DoJSONStatus(t, http.MethodGet,
		testSrv.BaseURL+"/share/"+token+"/export.ics", "", nil)
	assert.Equal(t, http.StatusNotFound, status, "exhausted invite should not export ICS")
}

func TestExpiredInviteCannotGetShareEvents(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)
	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)

	token := "expired-events-" + helpers.RandomHex(8)
	createExpiredInviteDirectly(t, testDB, tt.WorkspaceID, calInternalID, tt.UserInternalID, token)

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	url := testSrv.BaseURL + "/share/" + token + "/events" +
		"?start=" + start.Format(time.RFC3339) +
		"&end=" + end.Format(time.RFC3339)

	status, _ := helpers.DoJSONStatus(t, http.MethodGet, url, "", nil)
	assert.Equal(t, http.StatusNotFound, status, "expired invite should not list events")
}

func TestViewerCannotAddAttendees(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)
	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)

	viewer := helpers.CreateExtraMember(t, testSrv, owner.WorkspaceID,
		owner.WorkspacePublicID, calInternalID,
		generated.CalendarSubscriptionsRoleViewer)

	// Owner creates an event.
	var evt struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, owner.WsPath("calendars", calID, "events"), owner.AccessToken, map[string]any{
		"kind":     "event",
		"title":    "Owner Event",
		"startAt":  time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"endAt":    time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"timezone": "UTC",
	}, &evt)

	// Viewer tries to add attendees.
	status, _ := helpers.DoJSONStatus(t, http.MethodPost,
		viewer.WsPath("calendars", calID, "events", evt.ID, "attendees"),
		viewer.AccessToken,
		map[string]any{"userIds": []string{owner.UserPublicID.String()}})
	assert.Equal(t, http.StatusForbidden, status, "viewer should not be able to add attendees")
}

func TestViewerCannotCreateEventFromTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)
	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)

	viewer := helpers.CreateExtraMember(t, testSrv, owner.WorkspaceID,
		owner.WorkspacePublicID, calInternalID,
		generated.CalendarSubscriptionsRoleViewer)

	// Use a fake task UUID (we only care about the permission check, which
	// should fire before the task lookup).
	fakeTaskID := types.New().String()

	status, _ := helpers.DoJSONStatus(t, http.MethodPost,
		viewer.WsPath("calendars", calID, "events", "from-task"),
		viewer.AccessToken,
		map[string]any{"taskId": fakeTaskID})
	assert.Equal(t, http.StatusForbidden, status, "viewer should not be able to create event from task")
}

func TestSmartCreateWithInvalidCalId(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Use a non-existent calendar UUID.
	fakeCalID := types.New().String()

	status, _ := helpers.DoJSONStatus(t, http.MethodPost,
		tt.WsPath("calendars", fakeCalID, "events", "smart-create"),
		tt.AccessToken,
		map[string]any{"text": "Meeting tomorrow at 10am"})
	assert.Equal(t, http.StatusNotFound, status, "smart create with invalid calId should return 404")
}

func TestAcceptInviteAlreadyMember(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)

	token := createInviteViaAPI(t, owner, calID, map[string]any{
		"role": "editor",
	})

	// Owner tries to accept their own invite (already a member).
	status, _ := helpers.DoJSONStatus(t, http.MethodPost,
		testSrv.BaseURL+"/invites/"+token+"/accept",
		owner.AccessToken, nil)
	assert.Equal(t, http.StatusConflict, status, "already-member should get 409")
}
