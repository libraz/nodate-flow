package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

var grantDB = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{Database: "nodate_auth_grant_admin_test"})

func requireGrantDB(t *testing.T) *sql.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		t.Skip("set NF_TEST_INTEGRATION=1 to run integration tests")
	}
	inst, err := grantDB.Start(context.Background())
	require.NoError(t, err, "start mysql testcontainer")
	return inst.DB
}

func newUser(t *testing.T, q *generated.Queries) (uint32, string) {
	t.Helper()
	pub := types.New()
	email := "grant-" + pub.String() + "@example.test"
	id, err := q.RegisterUser(context.Background(), generated.RegisterUserParams{
		PublicID:        pub,
		Email:           email,
		DisplayName:     "Grant Admin User",
		Locale:          "en",
		Timezone:        "UTC",
		Country:         sql.NullString{String: "US", Valid: true},
		ThemePreference: generated.UsersThemePreferenceSystem,
	})
	require.NoError(t, err)
	return uint32(id), email //#nosec G115 -- test fixture id from LastInsertId
}

func TestGrantAdmin_GrantsThenIsIdempotent(t *testing.T) {
	db := requireGrantDB(t)
	q := generated.New(db)
	ctx := context.Background()
	uid, email := newUser(t, q)

	granted, err := grantAdmin(ctx, q, audit.NoopSink{}, "  "+email+" ")
	require.NoError(t, err)
	assert.True(t, granted)
	_, err = q.AdminFindInstanceAdminByUserId(ctx, uid)
	require.NoError(t, err, "the grant must be active")

	granted, err = grantAdmin(ctx, q, audit.NoopSink{}, email)
	require.NoError(t, err)
	assert.False(t, granted, "second run must be a no-op")

	var n int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM instance_admins WHERE user_id = ?", uid).Scan(&n))
	assert.Equal(t, 1, n)
}

func TestGrantAdmin_UnknownUserFails(t *testing.T) {
	db := requireGrantDB(t)
	_, err := grantAdmin(context.Background(), generated.New(db), audit.NoopSink{}, "nobody-"+types.New().String()+"@example.test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must sign in once")
}

func TestRun_RequiresEmailFlag(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	assert.Equal(t, 2, run(nil, &out, &errOut))
	assert.Contains(t, errOut.String(), "-email is required")
}
