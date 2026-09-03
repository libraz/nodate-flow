// Calendar-specific test helpers, ported from the pre-merge time-api
// test harness when calendar moved into flow-api. The
// public surface is intentionally narrow: only what calendar e2e tests
// need beyond the standard auth-api register flow used by
// CreateTestTenant.
package helpers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// CalendarTestTenant is the bundle of identifiers calendar e2e tests
// need. It carries the internal workspace / user ids alongside the
// public ones because calendar tests assert against DB rows and probe
// cross-tenant paths that are keyed on the internal ids.
type CalendarTestTenant struct {
	BaseURL           string
	UserPublicID      dbtype.PublicID
	UserInternalID    uint32
	Email             string
	DisplayName       string
	WorkspacePublicID dbtype.PublicID
	WorkspaceID       uint32
	AccessToken       string
}

// CreateCalendarTestTenant registers a user and creates their workspace
// through the API, then resolves the internal ids the calendar tests
// need out of the resulting rows.
//
// It must go through CreateTestTenant rather than INSERT the user, the
// workspace and the owner membership with sqlc and hand-sign a JWT.
// POST /workspaces is where a workspace gets its personal calendar
// (EnsurePersonalCalendar), so a seed that writes the rows directly
// leaves the whole register → create workspace → provision personal
// calendar chain unexercised by every calendar test in this package —
// which is exactly the wiring those tests exist to protect.
func CreateCalendarTestTenant(t *testing.T, srv *TestServer) *CalendarTestTenant {
	t.Helper()

	base := CreateTestTenant(t, srv.BaseURL)

	userPub, err := dbtype.Parse(base.UserPublicID)
	require.NoError(t, err, "parse user public id %q", base.UserPublicID)
	wsPub, err := dbtype.Parse(base.WorkspacePublicID)
	require.NoError(t, err, "parse workspace public id %q", base.WorkspacePublicID)

	return &CalendarTestTenant{
		BaseURL:           srv.BaseURL,
		UserPublicID:      userPub,
		UserInternalID:    ResolveUserInternalID(t, srv.DB, base.UserPublicID),
		Email:             base.Email,
		DisplayName:       base.DisplayName,
		WorkspacePublicID: wsPub,
		WorkspaceID:       ResolveWorkspaceInternalID(t, srv.DB, base.WorkspacePublicID),
		AccessToken:       base.AccessToken,
	}
}

// CreateExtraCalendarMember registers a second user, brings them into
// the owner's workspace through the invite flow, and adds them to the
// calendar at the given role. An empty role means editor, which is what
// most tests want: a second person who can add their own events but
// does not administer the calendar.
//
// Every step is an API call for the same reason CreateCalendarTestTenant
// is: the invite → accept → add-to-calendar chain is product behaviour,
// and seeding the rows directly meant no calendar test ever ran it.
func CreateExtraCalendarMember(
	t *testing.T,
	srv *TestServer,
	owner *CalendarTestTenant,
	calendarID uint32,
	calRole string,
) *CalendarTestTenant {
	t.Helper()

	member := CreateCalendarTestTenant(t, srv)
	wsURL := srv.BaseURL + "/workspaces/" + owner.WorkspacePublicID.String()

	var invite struct {
		Token string `json:"token"`
	}
	DoJSON(t, http.MethodPost, wsURL+"/invites", owner.AccessToken,
		map[string]any{"role": "member"}, &invite)
	require.NotEmpty(t, invite.Token, "workspace invite returned no token")
	DoJSON(t, http.MethodPost, srv.BaseURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	if calRole == "" {
		calRole = "editor"
	}
	calPub := ResolveCalendarPublicID(t, srv.DB, calendarID)
	DoJSON(t, http.MethodPost, wsURL+"/calendars/"+calPub+"/members", owner.AccessToken,
		map[string]any{"email": member.Email, "role": calRole}, nil)

	// The member now acts inside the owner's workspace, so report that
	// workspace rather than the one their own registration created.
	member.WorkspacePublicID = owner.WorkspacePublicID
	member.WorkspaceID = owner.WorkspaceID
	return member
}

// WsPath builds a URL like {BaseURL}/workspaces/{wsId}/{segments...}.
func (tt *CalendarTestTenant) WsPath(segments ...string) string {
	path := tt.BaseURL + "/workspaces/" + tt.WorkspacePublicID.String()
	for _, s := range segments {
		path += "/" + s
	}
	return path
}

// DoJSON sends a JSON request, asserts 2xx, and decodes into out.
func DoJSON(t *testing.T, method, url, bearer string, body any, out any) {
	t.Helper()
	status, raw := DoJSONStatus(t, method, url, bearer, body)
	require.GreaterOrEqualf(t, status, 200, "%s %s -> %d body=%s", method, url, status, string(raw))
	require.Lessf(t, status, 300, "%s %s -> %d body=%s", method, url, status, string(raw))
	if out != nil && len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, out), "decode %s %s body=%s", method, url, string(raw))
	}
}

// DoJSONStatus sends a JSON request and returns status + raw body
// without asserting success. Use it for negative-path tests.
func DoJSONStatus(t *testing.T, method, url, bearer string, body any) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, url, rdr)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, raw
}

// ResolveCalendarInternalID looks up the internal ID of a calendar by
// its public UUID string. Reading an internal id back out of a row the
// API already created is the sanctioned direct-SQL use in tests; what
// is not sanctioned is writing rows the API would have written.
func ResolveCalendarInternalID(t *testing.T, db *sql.DB, calPublicIDStr string) uint32 {
	t.Helper()
	var id uint32
	err := db.QueryRowContext(
		context.Background(),
		`SELECT id FROM calendars WHERE public_id = UUID_TO_BIN(?, 0) AND enabled = TRUE LIMIT 1`,
		calPublicIDStr,
	).Scan(&id)
	require.NoError(t, err, "resolve calendar internal id for %s", calPublicIDStr)
	return id
}

// ResolveCalendarPublicID is the reverse lookup, for helpers that are
// handed an internal calendar id but have to address the calendar over
// the API.
func ResolveCalendarPublicID(t *testing.T, db *sql.DB, calendarID uint32) string {
	t.Helper()
	var pub string
	err := db.QueryRowContext(
		context.Background(),
		`SELECT BIN_TO_UUID(public_id, 0) FROM calendars WHERE id = ? AND enabled = TRUE LIMIT 1`,
		calendarID,
	).Scan(&pub)
	require.NoError(t, err, "resolve calendar public id for %d", calendarID)
	return pub
}

// ResolveUserInternalID looks up users.id by public UUID string.
func ResolveUserInternalID(t *testing.T, db *sql.DB, userPublicIDStr string) uint32 {
	t.Helper()
	var id uint32
	err := db.QueryRowContext(
		context.Background(),
		`SELECT id FROM users WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		userPublicIDStr,
	).Scan(&id)
	require.NoError(t, err, "resolve user internal id for %s", userPublicIDStr)
	return id
}

// ResolveWorkspaceInternalID looks up workspaces.id by public UUID string.
func ResolveWorkspaceInternalID(t *testing.T, db *sql.DB, wsPublicIDStr string) uint32 {
	t.Helper()
	var id uint32
	err := db.QueryRowContext(
		context.Background(),
		`SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0) AND enabled = TRUE LIMIT 1`,
		wsPublicIDStr,
	).Scan(&id)
	require.NoError(t, err, "resolve workspace internal id for %s", wsPublicIDStr)
	return id
}

// RandomHex returns a random hex string of 2*n characters. Exported
// wrapper over the package-private generator, for calendar tests
// outside this package.
func RandomHex(n int) string {
	return randomHex(n)
}
