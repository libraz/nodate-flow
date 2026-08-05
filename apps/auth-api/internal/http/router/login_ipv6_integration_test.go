package router

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// ipv6ClientAddr is a global-scope IPv6 address in text form. It is 36
// characters wide, well past the 16-byte sessions.ip_address column, so
// storing it as text makes MySQL reject the INSERT in STRICT mode and
// turns every login from an IPv6 client into a 500.
const (
	ipv6ClientAddr   = "2001:db8:85a3:8d3:1319:8a2e:370:7348"
	ipv6ClientRemote = "[" + ipv6ClientAddr + "]:51234"
)

// ipv6DB is the shared MySQL testcontainer for this file.
var ipv6DB = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{Database: "nodate_auth_ipv6_test"})

// requireIPv6DB skips unless integration mode is on, mirroring the guard
// used by the other DB-backed auth-api tests.
func requireIPv6DB(t *testing.T) *sql.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		t.Skip("set NF_TEST_INTEGRATION=1 to run integration tests")
	}
	inst, err := ipv6DB.Start(context.Background())
	require.NoError(t, err, "start mysql testcontainer")
	return inst.DB
}

// ipv6Deps wires a router against the real database. Rate limiting is
// off because every request in this file shares one client address.
func ipv6Deps(t *testing.T, db *sql.DB) Deps {
	t.Helper()
	issuer, err := auth.NewJWTIssuer(nil, "nodate-auth", "api", 15*time.Minute)
	require.NoError(t, err)
	return Deps{
		DB:                db,
		Queries:           generated.New(db),
		JWT:               issuer,
		DisableRateLimit:  true,
		MinPasswordLength: 8,
	}
}

// ipv6NewUser inserts a user with a local password identity and returns
// the email. Each call uses a unique address so the test stays
// parallel-safe.
func ipv6NewUser(t *testing.T, q *generated.Queries, password string) string {
	t.Helper()
	ctx := context.Background()
	pub := types.New()
	email := "ipv6-" + pub.String() + "@example.test"
	uid64, err := q.RegisterUser(ctx, generated.RegisterUserParams{
		PublicID:        pub,
		Email:           email,
		DisplayName:     "IPv6 User",
		Locale:          "en",
		Timezone:        "UTC",
		Country:         sql.NullString{String: "US", Valid: true},
		ThemePreference: generated.UsersThemePreferenceSystem,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, uid64, int64(0))
	hash, err := auth.HashPassword(password)
	require.NoError(t, err)
	_, err = q.CreateIdentity(ctx, generated.CreateIdentityParams{
		PublicID:     types.New(),
		UserID:       uint32(uid64), //#nosec G115 -- test fixture LastInsertId is asserted non-negative and fits users.id.
		Provider:     generated.IdentitiesProviderLocal,
		Subject:      email,
		PasswordHash: sql.NullString{String: hash, Valid: true},
	})
	require.NoError(t, err)
	return email
}

// TestLoginFromIPv6ClientIssuesSession drives a password login whose
// RemoteAddr is a real global IPv6 address, all the way through the
// ClientIP middleware and the sessions INSERT. It asserts three things
// that a text-encoded IP breaks: the login succeeds, the stored column
// holds the 16-byte packed address, and the sessions API renders it back
// as the address the client came from rather than raw bytes.
func TestLoginFromIPv6ClientIssuesSession(t *testing.T) {
	t.Parallel()
	db := requireIPv6DB(t)
	deps := ipv6Deps(t, db)
	h := Build(deps)

	const password = "correct horse battery"
	email := ipv6NewUser(t, deps.Queries, password)

	body := `{"email":"` + email + `","password":"` + password + `"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ipv6ClientRemote
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equalf(t, http.StatusOK, rec.Code,
		"login from an IPv6 client must succeed; body=%s", rec.Body.String())

	var login struct {
		Step        string `json:"step"`
		AccessToken string `json:"accessToken"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &login))
	require.Equal(t, "complete", login.Step)
	require.NotEmpty(t, login.AccessToken)

	// The column is VARBINARY(16): the row must hold the packed form,
	// not the 36-character text the client connected from.
	var stored sql.NullString
	err := db.QueryRow(
		`SELECT s.ip_address FROM sessions s
		 JOIN users u ON u.id = s.user_id
		 WHERE u.email = ?
		 ORDER BY s.id DESC LIMIT 1`, email).Scan(&stored)
	require.NoError(t, err)
	require.True(t, stored.Valid, "sessions.ip_address must be populated")
	require.Len(t, stored.String, 16, "sessions.ip_address must be the packed 16-byte form")
	require.Equal(t, ipv6ClientAddr, dbtype.IPStringFromNullString(stored))

	// The read path has to undo the packing, otherwise the security
	// panel shows the operator a mangled binary string.
	listReq := httptest.NewRequest(http.MethodGet, "/me/sessions", nil)
	listReq.Header.Set("Authorization", "Bearer "+login.AccessToken)
	listReq.RemoteAddr = ipv6ClientRemote
	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, listReq)
	require.Equalf(t, http.StatusOK, listRec.Code, "body=%s", listRec.Body.String())

	var sessions struct {
		Items []struct {
			IPAddress string `json:"ipAddress"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &sessions))
	require.NotEmpty(t, sessions.Items)
	require.Equal(t, ipv6ClientAddr, sessions.Items[0].IPAddress)
}

// TestMagicLinkRequestFromIPv6Client covers the other write of a client
// IP on an unauthenticated path: magic_link_tokens.ip_address is the
// same VARBINARY(16) column, and the request fails at token creation
// when the address is stored as text.
func TestMagicLinkRequestFromIPv6Client(t *testing.T) {
	t.Parallel()
	db := requireIPv6DB(t)
	deps := ipv6Deps(t, db)
	h := Build(deps)

	email := ipv6NewUser(t, deps.Queries, "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/auth/magic-link/request",
		strings.NewReader(`{"email":"`+email+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ipv6ClientRemote
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equalf(t, http.StatusOK, rec.Code,
		"magic-link request from an IPv6 client must succeed; body=%s", rec.Body.String())

	var stored sql.NullString
	err := db.QueryRow(
		`SELECT t.ip_address FROM magic_link_tokens t
		 JOIN users u ON u.id = t.user_id
		 WHERE u.email = ?
		 ORDER BY t.id DESC LIMIT 1`, email).Scan(&stored)
	require.NoError(t, err)
	require.True(t, stored.Valid, "magic_link_tokens.ip_address must be populated")
	require.Len(t, stored.String, 16)
	require.Equal(t, ipv6ClientAddr, dbtype.IPStringFromNullString(stored))
}
