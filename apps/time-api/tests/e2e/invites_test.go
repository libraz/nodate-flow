package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/time-api/tests/helpers"
)

// createEventForTenant is a small local helper that creates a single
// calendar event owned by tt and returns the event's public ID. Kept
// local to this file so the invite tests stay self-contained and easy
// to read; callers that need event-shape assertions should fall back
// to the richer helpers in calendar_event_test.go.
func createEventForTenant(t *testing.T, tt *helpers.TestTenant, calID, title string) string {
	t.Helper()
	var evt struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
		"kind":       "event",
		"title":      title,
		"startAt":    time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"endAt":      time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"timezone":   "UTC",
		"visibility": "default",
	}, &evt)
	require.NotEmpty(t, evt.ID)
	return evt.ID
}

// addAttendee adds the given user public ID as an attendee on the event
// and returns the attendee's public ID, matching AttendeeResponse.id.
func addAttendee(t *testing.T, tt *helpers.TestTenant, calID, evtID, userPublicID string) string {
	t.Helper()
	var resp struct {
		Attendees []struct {
			ID     string `json:"id"`
			UserID string `json:"userId"`
		} `json:"attendees"`
	}
	helpers.DoJSON(
		t,
		http.MethodPost,
		tt.WsPath("calendars", calID, "events", evtID, "attendees"),
		tt.AccessToken,
		map[string]any{"userIds": []string{userPublicID}},
		&resp,
	)
	require.Len(t, resp.Attendees, 1, "add-attendees must return exactly one row for one input user")
	require.Equal(t, userPublicID, resp.Attendees[0].UserID)
	require.NotEmpty(t, resp.Attendees[0].ID)
	return resp.Attendees[0].ID
}

// createInvite mints an invite for (event, attendee) and returns the
// create-response fields. Token is only present here.
type createdInvite struct {
	ID        string `json:"id"`
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expiresAt"`
}

func createInvite(t *testing.T, tt *helpers.TestTenant, calID, evtID, attendeeID string) createdInvite {
	t.Helper()
	var out createdInvite
	helpers.DoJSON(
		t,
		http.MethodPost,
		tt.WsPath("calendars", calID, "events", evtID, "attendees", attendeeID, "invite"),
		tt.AccessToken,
		map[string]any{},
		&out,
	)
	require.NotEmpty(t, out.ID, "invite create must return a public id")
	require.NotEmpty(t, out.Token, "invite create must return a plaintext token exactly once")
	require.NotZero(t, out.ExpiresAt, "invite create must return an expiresAt unixtime")
	return out
}

// expireInviteInDB shortcuts the clock-wait loop by directly stamping
// expires_at 1 second in the past for the given invite public_id. The
// task brief calls this out explicitly as the preferred shape.
func expireInviteInDB(t *testing.T, invitePublicID string) {
	t.Helper()
	past := time.Now().UTC().Add(-1 * time.Second)
	res, err := testDB.ExecContext(
		context.Background(),
		`UPDATE calendar_event_invites SET expires_at = ? WHERE public_id = UUID_TO_BIN(?, 0)`,
		past, invitePublicID,
	)
	require.NoError(t, err)
	n, err := res.RowsAffected()
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "expected exactly one invite row to be marked expired")
}

// readAttendeeRsvp returns the rsvp value from the DB for the given
// (event public_id, user public_id) pair. The API surface has no
// attendee-read endpoint, so DB is the pragmatic path for the
// accept-flow assertions.
func readAttendeeRsvp(t *testing.T, eventPublicID, userPublicID string) string {
	t.Helper()
	var rsvp string
	err := testDB.QueryRowContext(
		context.Background(),
		`SELECT a.rsvp
		 FROM calendar_event_attendees a
		 INNER JOIN calendar_events e ON e.id = a.event_id
		 INNER JOIN users u ON u.id = a.user_id
		 WHERE e.public_id = UUID_TO_BIN(?, 0)
		   AND u.public_id = UUID_TO_BIN(?, 0)
		 LIMIT 1`,
		eventPublicID, userPublicID,
	).Scan(&rsvp)
	require.NoError(t, err, "read rsvp for event=%s user=%s", eventPublicID, userPublicID)
	return rsvp
}

// listEventInvites calls the owner-only list endpoint and returns the
// response shape defined by the handler.
type eventInviteListItem struct {
	ID               string `json:"id"`
	AttendeePublicID string `json:"attendeePublicId"`
	Email            string `json:"email"`
	Token            string `json:"token,omitempty"`
	ExpiresAt        int64  `json:"expiresAt"`
	AcceptedAt       *int64 `json:"acceptedAt,omitempty"`
	CreatedAt        int64  `json:"createdAt"`
}

func listEventInvites(t *testing.T, tt *helpers.TestTenant, calID, evtID string) []eventInviteListItem {
	t.Helper()
	var resp struct {
		Invites []eventInviteListItem `json:"invites"`
	}
	helpers.DoJSON(
		t,
		http.MethodGet,
		tt.WsPath("calendars", calID, "events", evtID, "invites"),
		tt.AccessToken,
		nil,
		&resp,
	)
	return resp.Invites
}

// listMyInvites calls the authenticated inbox endpoint and returns the
// response shape defined by ListMyInvites.
type myInviteListItem struct {
	ID                string `json:"id"`
	EventPublicID     string `json:"eventPublicId"`
	EventTitle        string `json:"eventTitle"`
	CalendarPublicID  string `json:"calendarPublicId"`
	WorkspacePublicID string `json:"workspacePublicId"`
	ExpiresAt         int64  `json:"expiresAt"`
}

func listMyInvites(t *testing.T, bearer string) []myInviteListItem {
	t.Helper()
	var resp struct {
		Invites []myInviteListItem `json:"invites"`
	}
	helpers.DoJSON(t, http.MethodGet, testSrv.BaseURL+"/me/invites", bearer, nil, &resp)
	return resp.Invites
}

// acceptInvite fires the unauthenticated accept endpoint and returns
// the raw status + body so negative-path tests can assert on it.
func acceptInviteStatus(t *testing.T, token, rsvp string) (int, []byte) {
	t.Helper()
	return helpers.DoJSONStatus(t, http.MethodPost, testSrv.BaseURL+"/public/invites/accept", "", map[string]any{
		"token": token,
		"rsvp":  rsvp,
	})
}

// --- Tests ---

func TestCreateInvite_ReturnsTokenOnce(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)
	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	member := helpers.CreateExtraMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")
	evtID := createEventForTenant(t, owner, calID, "Invite Token Once")
	attendeeID := addAttendee(t, owner, calID, evtID, member.UserPublicID.String())

	created := createInvite(t, owner, calID, evtID, attendeeID)

	require.NotEmpty(t, created.ID, "create response must carry the public invite id")
	require.NotEmpty(t, created.Token, "create response must carry the plaintext token exactly once")
	require.Greater(t, created.ExpiresAt, time.Now().Unix(), "expiresAt must be in the future")

	// List endpoint must not re-expose the token.
	invites := listEventInvites(t, owner, calID, evtID)
	require.Len(t, invites, 1)
	assert.Equal(t, created.ID, invites[0].ID)
	assert.Equal(t, member.Email, invites[0].Email)
	assert.Equal(t, attendeeID, invites[0].AttendeePublicID)
	assert.Empty(t, invites[0].Token, "list response must never re-expose the plaintext token")
}

func TestCreateInvite_RotatesExistingOnResend(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)
	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	member := helpers.CreateExtraMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")
	evtID := createEventForTenant(t, owner, calID, "Invite Rotate")
	attendeeID := addAttendee(t, owner, calID, evtID, member.UserPublicID.String())

	first := createInvite(t, owner, calID, evtID, attendeeID)
	second := createInvite(t, owner, calID, evtID, attendeeID)

	assert.NotEqual(t, first.Token, second.Token, "resend must mint a new token")
	assert.Equal(t, first.ID, second.ID, "resend must rotate the existing invite row in place")

	invites := listEventInvites(t, owner, calID, evtID)
	require.Len(t, invites, 1, "only one active invite should exist per (event, attendee)")
	assert.Equal(t, first.ID, invites[0].ID)

	// Sanity: the old token must no longer accept; the new one must.
	oldStatus, _ := acceptInviteStatus(t, first.Token, "accepted")
	assert.Equal(t, http.StatusNotFound, oldStatus, "rotated-out token must be rejected")
	newStatus, _ := acceptInviteStatus(t, second.Token, "accepted")
	assert.Equal(t, http.StatusOK, newStatus, "freshly rotated token must still accept")
}

func TestCreateInvite_NonOwnerForbidden(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)
	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	member := helpers.CreateExtraMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")
	evtID := createEventForTenant(t, owner, calID, "Non-Owner Invite")
	attendeeID := addAttendee(t, owner, calID, evtID, member.UserPublicID.String())

	// Non-owner (the extra member) attempts to mint an invite for the
	// same event. The owner gate in CreateEventInvite should 403.
	status, body := helpers.DoJSONStatus(
		t,
		http.MethodPost,
		owner.WsPath("calendars", calID, "events", evtID, "attendees", attendeeID, "invite"),
		member.AccessToken,
		map[string]any{},
	)
	assert.Equal(t, http.StatusForbidden, status, "non-owner must be forbidden from minting invites; body=%s", string(body))
	assert.Contains(
		t,
		string(body),
		"CALENDAR.CALENDAR.OWNER_ROLE_REQUIRED",
		"error body must carry the owner-gate error code",
	)
}

func TestAcceptInvite_HappyPath(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)
	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	member := helpers.CreateExtraMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")
	evtID := createEventForTenant(t, owner, calID, "Accept Happy Path")
	attendeeID := addAttendee(t, owner, calID, evtID, member.UserPublicID.String())

	created := createInvite(t, owner, calID, evtID, attendeeID)

	status, body := acceptInviteStatus(t, created.Token, "accepted")
	assert.Equal(t, http.StatusOK, status, "accept must succeed; body=%s", string(body))

	// After accept, /me/invites as the member must not list the row —
	// ListMyCalendarEventInvites filters accepted_at IS NULL.
	inbox := listMyInvites(t, member.AccessToken)
	for _, inv := range inbox {
		assert.NotEqual(t, created.ID, inv.ID, "accepted invite must not appear in the inbox")
	}

	// The attendee row's rsvp must now reflect "accepted".
	rsvp := readAttendeeRsvp(t, evtID, member.UserPublicID.String())
	assert.Equal(t, "accepted", rsvp, "accept must stamp the attendee rsvp")
}

func TestAcceptInvite_UpdatesRsvpIdempotent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)
	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	member := helpers.CreateExtraMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")
	evtID := createEventForTenant(t, owner, calID, "Accept Idempotent")
	attendeeID := addAttendee(t, owner, calID, evtID, member.UserPublicID.String())

	created := createInvite(t, owner, calID, evtID, attendeeID)

	// First accept as "accepted".
	status1, body1 := acceptInviteStatus(t, created.Token, "accepted")
	require.Equal(t, http.StatusOK, status1, "first accept must succeed; body=%s", string(body1))

	// Second accept, same token, "declined" — handler is spec'd to be
	// idempotent on RSVP so the recipient can change their mind.
	status2, body2 := acceptInviteStatus(t, created.Token, "declined")
	require.Equal(t, http.StatusOK, status2, "second accept must succeed; body=%s", string(body2))

	rsvp := readAttendeeRsvp(t, evtID, member.UserPublicID.String())
	assert.Equal(t, "declined", rsvp, "second accept must overwrite the rsvp")
}

func TestAcceptInvite_InvalidToken(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	// newTenant is called purely to guarantee the server is up; no
	// tenant state is needed for the 404 path.
	_ = newTenant(t)

	bogus := helpers.RandomHex(32) // 64-hex-char string matching the real token shape
	status, body := acceptInviteStatus(t, bogus, "accepted")
	assert.Equal(t, http.StatusNotFound, status, "unknown token must 404; body=%s", string(body))
	assert.Contains(
		t,
		string(body),
		"CALENDAR.INVITE.NOT_FOUND",
		"error body must carry the invite-not-found code",
	)
}

func TestAcceptInvite_ExpiredToken(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)
	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	member := helpers.CreateExtraMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")
	evtID := createEventForTenant(t, owner, calID, "Accept Expired")
	attendeeID := addAttendee(t, owner, calID, evtID, member.UserPublicID.String())

	created := createInvite(t, owner, calID, evtID, attendeeID)
	expireInviteInDB(t, created.ID)

	status, body := acceptInviteStatus(t, created.Token, "accepted")
	assert.Equal(t, http.StatusNotFound, status, "expired invite must collapse to 404; body=%s", string(body))
	assert.Contains(
		t,
		string(body),
		"CALENDAR.INVITE.NOT_FOUND",
		"expired invite must surface the not-found code",
	)
}

func TestRevokeInvite_RemovesFromList(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)
	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	member := helpers.CreateExtraMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")
	evtID := createEventForTenant(t, owner, calID, "Revoke")
	attendeeID := addAttendee(t, owner, calID, evtID, member.UserPublicID.String())

	created := createInvite(t, owner, calID, evtID, attendeeID)

	var revoked struct {
		Revoked bool `json:"revoked"`
	}
	helpers.DoJSON(
		t,
		http.MethodDelete,
		owner.WsPath("calendars", calID, "events", evtID, "invites", created.ID),
		owner.AccessToken,
		nil,
		&revoked,
	)
	assert.True(t, revoked.Revoked, "revoke must confirm success")

	invites := listEventInvites(t, owner, calID, evtID)
	assert.Len(t, invites, 0, "revoked invite must drop out of the list")

	status, _ := acceptInviteStatus(t, created.Token, "accepted")
	assert.Equal(t, http.StatusNotFound, status, "revoked token must no longer accept")
}

func TestListMyInvites_ScopedToCallerEmail(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)
	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	memberA := helpers.CreateExtraMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")
	memberB := helpers.CreateExtraMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")

	// Separate events so each invite targets a distinct (event,
	// attendee) pair; keeps the list semantics clean.
	evtA := createEventForTenant(t, owner, calID, "Invite To A")
	attendeeAID := addAttendee(t, owner, calID, evtA, memberA.UserPublicID.String())
	inviteA := createInvite(t, owner, calID, evtA, attendeeAID)

	evtB := createEventForTenant(t, owner, calID, "Invite To B")
	attendeeBID := addAttendee(t, owner, calID, evtB, memberB.UserPublicID.String())
	inviteB := createInvite(t, owner, calID, evtB, attendeeBID)

	inboxA := listMyInvites(t, memberA.AccessToken)
	require.Len(t, inboxA, 1, "member A inbox must contain exactly one invite")
	assert.Equal(t, inviteA.ID, inboxA[0].ID)
	assert.Equal(t, evtA, inboxA[0].EventPublicID)
	assert.Equal(t, owner.WorkspacePublicID.String(), inboxA[0].WorkspacePublicID)

	inboxB := listMyInvites(t, memberB.AccessToken)
	require.Len(t, inboxB, 1, "member B inbox must contain exactly one invite")
	assert.Equal(t, inviteB.ID, inboxB[0].ID)
	assert.Equal(t, evtB, inboxB[0].EventPublicID)

	assert.NotEqual(t, inboxA[0].ID, inboxB[0].ID, "invites must not leak between recipients")
}

func TestListMyInvites_ExcludesAcceptedAndExpired(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)
	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	member := helpers.CreateExtraMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")

	// Three independent events so each can hold its own invite row for
	// the same attendee (UNIQUE(event_id, attendee_id) allows this as
	// long as the event differs).
	acceptedEvt := createEventForTenant(t, owner, calID, "Inbox Accepted")
	acceptedAttendee := addAttendee(t, owner, calID, acceptedEvt, member.UserPublicID.String())
	acceptedInv := createInvite(t, owner, calID, acceptedEvt, acceptedAttendee)

	expiredEvt := createEventForTenant(t, owner, calID, "Inbox Expired")
	expiredAttendee := addAttendee(t, owner, calID, expiredEvt, member.UserPublicID.String())
	expiredInv := createInvite(t, owner, calID, expiredEvt, expiredAttendee)

	pendingEvt := createEventForTenant(t, owner, calID, "Inbox Pending")
	pendingAttendee := addAttendee(t, owner, calID, pendingEvt, member.UserPublicID.String())
	pendingInv := createInvite(t, owner, calID, pendingEvt, pendingAttendee)

	// Mark acceptedInv as accepted via the public accept endpoint.
	status, body := acceptInviteStatus(t, acceptedInv.Token, "accepted")
	require.Equal(t, http.StatusOK, status, "seed accept must succeed; body=%s", string(body))

	// Expire expiredInv by direct DB UPDATE.
	expireInviteInDB(t, expiredInv.ID)

	inbox := listMyInvites(t, member.AccessToken)
	require.Len(t, inbox, 1, "inbox must exclude accepted + expired and leave only pending")
	assert.Equal(t, pendingInv.ID, inbox[0].ID, "only the pending invite should surface")
	assert.Equal(t, pendingEvt, inbox[0].EventPublicID)
}
