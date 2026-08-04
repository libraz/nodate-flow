// Calendar-specific test helpers, ported from the pre-merge time-api
// test harness when calendar moved into flow-api (R6 Phase 0). The
// public surface is intentionally narrow: only what calendar e2e tests
// need beyond the standard auth-api register flow used by
// CreateTestTenant.
package helpers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	calgen "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
)

// CalendarTestTenant is the bundle of identifiers calendar e2e tests
// need. It mirrors the time-api TestTenant shape so the migrated tests
// keep their call sites unchanged. Unlike the auth-api-driven
// CreateTestTenant in tenant.go, this tenant is seeded directly into
// the DB via sqlc and bypasses /auth/register; calendar tests
// historically used this stub path because time-api had no auth
// endpoints.
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

// CreateCalendarTestTenant inserts a fresh user + workspace + owner
// membership directly via sqlc and signs a JWT recognized by the merged
// auth middleware. Suitable for calendar tests that need internal IDs
// (CalendarID, WorkspaceID) for cross-tenant probes and direct DB
// assertions.
func CreateCalendarTestTenant(t *testing.T, srv *TestServer) *CalendarTestTenant {
	t.Helper()

	suffix := randomHex(8)
	q := generated.New(srv.DB)
	ctx := context.Background()

	userPub := dbtype.New()
	email := fmt.Sprintf("test+%s@example.test", suffix)
	displayName := "Test User " + suffix

	userID64, err := q.CreateStubUser(ctx, generated.CreateStubUserParams{
		PublicID:        userPub,
		Email:           email,
		DisplayName:     displayName,
		Locale:          "en",
		Timezone:        "UTC",
		Country:         sql.NullString{},
		ThemePreference: generated.UsersThemePreferenceSystem,
	})
	require.NoError(t, err, "create test user")
	userID := uint32(userID64) //#nosec G115 -- LastInsertId for users.id (BIGINT UNSIGNED), fits uint32 in test seed

	wsPub := dbtype.New()
	wsSlug := "ws-" + suffix
	wsID64, err := q.CreateWorkspace(ctx, generated.CreateWorkspaceParams{
		PublicID: wsPub,
		Slug:     wsSlug,
		Name:     "Test Workspace " + suffix,
		Timezone: "UTC",
	})
	require.NoError(t, err, "create test workspace")
	wsID := uint32(wsID64) //#nosec G115 -- LastInsertId for workspaces.id (BIGINT UNSIGNED), fits uint32 in test seed

	_, err = q.CreateWorkspaceMember(ctx, generated.CreateWorkspaceMemberParams{
		PublicID:    dbtype.New(),
		WorkspaceID: wsID,
		UserID:      userID,
		Role:        generated.WorkspaceMembersRoleOwner,
		JoinedAt:    sql.NullTime{Time: time.Now().UTC(), Valid: true},
	})
	require.NoError(t, err, "create workspace member")

	token, _, err := srv.JWT.Sign(userPub, dbtype.PublicID{})
	require.NoError(t, err, "sign test jwt")

	// Auto-register workspace purge so calendar tests get the same
	// cleanup contract as the REST CreateTestTenant in tenant.go.
	t.Cleanup(func() { PurgeWorkspace(t, srv.DB, wsPub.String()) })

	return &CalendarTestTenant{
		BaseURL:           srv.BaseURL,
		UserPublicID:      userPub,
		UserInternalID:    userID,
		Email:             email,
		DisplayName:       displayName,
		WorkspacePublicID: wsPub,
		WorkspaceID:       wsID,
		AccessToken:       token,
	}
}

// CreateExtraCalendarMember creates an additional user, adds them to the
// given workspace, and grants them the given role on the calendar. An
// empty role means editor, which is what most tests want: a second person
// who can add their own events but does not administer the calendar.
func CreateExtraCalendarMember(
	t *testing.T,
	srv *TestServer,
	wsID uint32,
	wsPub dbtype.PublicID,
	calendarID uint32,
	calRole string,
) *CalendarTestTenant {
	t.Helper()

	suffix := randomHex(8)
	q := generated.New(srv.DB)
	cq := calgen.New(srv.DB)
	ctx := context.Background()

	userPub := dbtype.New()
	email := fmt.Sprintf("member+%s@example.test", suffix)
	displayName := "Member " + suffix

	userID64, err := q.CreateStubUser(ctx, generated.CreateStubUserParams{
		PublicID:        userPub,
		Email:           email,
		DisplayName:     displayName,
		Locale:          "en",
		Timezone:        "UTC",
		Country:         sql.NullString{},
		ThemePreference: generated.UsersThemePreferenceSystem,
	})
	require.NoError(t, err)
	userID := uint32(userID64) //#nosec G115 -- LastInsertId for users.id (BIGINT UNSIGNED), fits uint32 in test seed

	_, err = q.CreateWorkspaceMember(ctx, generated.CreateWorkspaceMemberParams{
		PublicID:    dbtype.New(),
		WorkspaceID: wsID,
		UserID:      userID,
		Role:        generated.WorkspaceMembersRoleMember,
		JoinedAt:    sql.NullTime{Time: time.Now().UTC(), Valid: true},
	})
	require.NoError(t, err)

	if calRole == "" {
		calRole = string(calgen.CalendarMembersRoleEditor)
	}
	_, err = cq.UpsertCalendarMember(ctx, calgen.UpsertCalendarMemberParams{
		PublicID:    dbtype.New(),
		WorkspaceID: wsID,
		CalendarID:  calendarID,
		UserID:      userID,
		Role:        calgen.CalendarMembersRole(calRole),
		MemberColor: "#FF5722",
	})
	require.NoError(t, err)

	token, _, err := srv.JWT.Sign(userPub, dbtype.PublicID{})
	require.NoError(t, err)

	return &CalendarTestTenant{
		BaseURL:           srv.BaseURL,
		UserPublicID:      userPub,
		UserInternalID:    userID,
		Email:             email,
		DisplayName:       displayName,
		WorkspacePublicID: wsPub,
		WorkspaceID:       wsID,
		AccessToken:       token,
	}
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
// its public UUID string. Used by tests that need to seed extra members
// or subscriptions directly via DB.
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

// SignToken creates a JWT for the given user public ID using the test
// JWT issuer. Useful when a test needs to mint an additional token for
// a stub user without going through the auth-api register flow.
func SignToken(t *testing.T, jwt *auth.JWTIssuer, userPub dbtype.PublicID) string {
	t.Helper()
	tok, _, err := jwt.Sign(userPub, dbtype.PublicID{})
	require.NoError(t, err)
	return tok
}

// RandomHex returns a random hex string of 2*n characters. Re-exported
// so calendar tests that previously called helpers.RandomHex (in
// time-api) keep working without renaming.
func RandomHex(n int) string {
	return randomHex(n)
}
