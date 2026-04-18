package helpers

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/auth"
	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
)

// TestTenant is the bundle of identifiers and tokens for an isolated test user.
type TestTenant struct {
	BaseURL           string
	UserPublicID      types.PublicID
	UserInternalID    uint32
	Email             string
	DisplayName       string
	WorkspacePublicID types.PublicID
	WorkspaceID       uint32
	AccessToken       string
}

// CreateTestTenant inserts a fresh user and workspace directly into the database
// and signs a JWT. Auth endpoints are stubs in time-api, so we bypass them.
func CreateTestTenant(t *testing.T, srv *TestServer) *TestTenant {
	t.Helper()

	suffix := randomHex(8)
	q := generated.New(srv.DB)
	ctx := context.Background()

	userPub := types.New()
	email := fmt.Sprintf("test+%s@example.test", suffix)
	displayName := "Test User " + suffix

	userID64, err := q.CreateStubUser(ctx, generated.CreateStubUserParams{
		PublicID:        userPub,
		Email:           email,
		DisplayName:     displayName,
		Locale:          "en",
		ThemePreference: generated.UsersThemePreferenceSystem,
	})
	require.NoError(t, err, "create test user")
	userID := uint32(userID64)

	wsPub := types.New()
	wsSlug := "ws-" + suffix
	wsID64, err := q.CreateWorkspace(ctx, generated.CreateWorkspaceParams{
		PublicID: wsPub,
		Slug:     wsSlug,
		Name:     "Test Workspace " + suffix,
	})
	require.NoError(t, err, "create test workspace")
	wsID := uint32(wsID64)

	_, err = q.CreateWorkspaceMember(ctx, generated.CreateWorkspaceMemberParams{
		PublicID:    types.New(),
		WorkspaceID: wsID,
		UserID:      userID,
		Role:        generated.WorkspaceMembersRoleOwner,
		JoinedAt:    sql.NullTime{Time: time.Now().UTC(), Valid: true},
	})
	require.NoError(t, err, "create workspace member")

	token, _, err := srv.JWT.Sign(userPub, types.PublicID{})
	require.NoError(t, err, "sign test jwt")

	return &TestTenant{
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

// CreateExtraMember creates an additional user, adds them to the given workspace
// with the specified calendar subscription role on the given calendar, and
// returns the new tenant.
func CreateExtraMember(
	t *testing.T,
	srv *TestServer,
	wsID uint32,
	wsPub types.PublicID,
	calendarID uint32,
	calSubRole generated.CalendarSubscriptionsRole,
) *TestTenant {
	t.Helper()

	suffix := randomHex(8)
	q := generated.New(srv.DB)
	ctx := context.Background()

	userPub := types.New()
	email := fmt.Sprintf("member+%s@example.test", suffix)
	displayName := "Member " + suffix

	userID64, err := q.CreateStubUser(ctx, generated.CreateStubUserParams{
		PublicID:        userPub,
		Email:           email,
		DisplayName:     displayName,
		Locale:          "en",
		ThemePreference: generated.UsersThemePreferenceSystem,
	})
	require.NoError(t, err)
	userID := uint32(userID64)

	_, err = q.CreateWorkspaceMember(ctx, generated.CreateWorkspaceMemberParams{
		PublicID:    types.New(),
		WorkspaceID: wsID,
		UserID:      userID,
		Role:        generated.WorkspaceMembersRoleMember,
		JoinedAt:    sql.NullTime{Time: time.Now().UTC(), Valid: true},
	})
	require.NoError(t, err)

	_, err = q.CreateCalendarSubscription(ctx, generated.CreateCalendarSubscriptionParams{
		PublicID:     types.New(),
		WorkspaceID:  wsID,
		CalendarID:   calendarID,
		UserID:       userID,
		Role:         calSubRole,
		MemberColor:  "#FF5722",
		DisplayColor: "#FF5722",
	})
	require.NoError(t, err)

	token, _, err := srv.JWT.Sign(userPub, types.PublicID{})
	require.NoError(t, err)

	return &TestTenant{
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

// WsPath builds a URL path like /workspaces/{wsId}/...
func (tt *TestTenant) WsPath(segments ...string) string {
	path := tt.BaseURL + "/workspaces/" + tt.WorkspacePublicID.String()
	for _, s := range segments {
		path += "/" + s
	}
	return path
}

// --- HTTP helpers ---

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

// DoJSONStatus sends a JSON request and returns status + raw body without
// asserting success. Useful for negative-path tests.
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

// ResolveCalendarInternalID looks up the internal ID of a calendar by its
// public UUID string. Used in tests that need to seed extra members.
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

// SignToken creates a JWT for the given user public ID using the test JWT issuer.
func SignToken(t *testing.T, jwt *auth.JWTIssuer, userPub types.PublicID) string {
	t.Helper()
	tok, _, err := jwt.Sign(userPub, types.PublicID{})
	require.NoError(t, err)
	return tok
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
