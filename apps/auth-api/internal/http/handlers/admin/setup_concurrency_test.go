package admin

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/testhelpers"
)

// setupDB lazily boots a shared MySQL testcontainer with the full repo
// schema. The first-admin TOCTOU regression needs a real DB because it
// races two concurrent INSERTs against the atomic conditional bootstrap
// query and asserts exactly one row lands in instance_admins.
var setupDB = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{Database: "nodate_auth_admin_setup_test"})

// requireSetupDB skips when integration tests are not enabled and
// otherwise returns the shared *sql.DB.
func requireSetupDB(t *testing.T) *sql.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping admin setup integration test in -short mode")
	}
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		t.Skip("set NF_TEST_INTEGRATION=1 to run admin setup integration tests")
	}
	inst, err := setupDB.Start(context.Background())
	require.NoError(t, err, "start mysql testcontainer")
	return inst.DB
}

// setupNewUser inserts a fresh user + local identity and returns the
// internal user id. Each call uses a unique email so the test stays
// parallel-safe.
func setupNewUser(t *testing.T, q *generated.Queries) uint32 {
	t.Helper()
	ctx := context.Background()
	pub := types.New()
	email := "setup-" + pub.String() + "@example.test"
	uid64, err := q.RegisterUser(ctx, generated.RegisterUserParams{
		PublicID:        pub,
		Email:           email,
		DisplayName:     "Setup User",
		Locale:          "en",
		Timezone:        "UTC",
		Country:         sql.NullString{String: "US", Valid: true},
		ThemePreference: generated.UsersThemePreferenceSystem,
	})
	require.NoError(t, err)
	uid := uint32(uid64)
	_, err = q.CreateIdentity(ctx, generated.CreateIdentityParams{
		PublicID:     types.New(),
		UserID:       uid,
		Provider:     generated.IdentitiesProviderLocal,
		Subject:      email,
		PasswordHash: sql.NullString{},
	})
	require.NoError(t, err)
	return uid
}

// TestSetup_ConcurrentCalls_YieldsExactlyOneAdmin proves the first-admin
// bootstrap is atomic: two distinct users hitting POST /admin/setup at
// the same instant must produce exactly one instance admin, with the
// loser receiving the already-initialized error rather than silently
// creating a second admin.
func TestSetup_ConcurrentCalls_YieldsExactlyOneAdmin(t *testing.T) {
	db := requireSetupDB(t)
	q := generated.New(db)
	ctx := context.Background()

	// Clean slate: remove any admin rows a prior test left behind so the
	// "no admin exists yet" precondition holds.
	_, err := db.ExecContext(ctx, "DELETE FROM instance_admins")
	require.NoError(t, err)

	deps := Deps{DB: db, Queries: q, Audit: audit.NoopSink{}}
	handler := Setup(deps)

	const racers = 8
	uids := make([]uint32, racers)
	for i := range uids {
		uids[i] = setupNewUser(t, q)
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		okCount  int
		errCount int
		start    = make(chan struct{})
	)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(uid uint32) {
			defer wg.Done()
			actorCtx := authn.WithActor(ctx, uid)
			<-start // release all goroutines simultaneously
			_, herr := handler(actorCtx, &struct{}{})
			mu.Lock()
			if herr == nil {
				okCount++
			} else {
				errCount++
			}
			mu.Unlock()
		}(uids[i])
	}
	close(start)
	wg.Wait()

	assert.Equal(t, 1, okCount, "exactly one concurrent setup call must succeed")
	assert.Equal(t, racers-1, errCount, "every losing call must get an error, not a silent second admin")

	count, err := q.AdminCountActiveInstanceAdmins(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "exactly one active instance admin row must exist after the race")
}

// TestSetup_SecondCall_AlreadyInitialized proves the sequential happy
// path: the first call promotes the user, a subsequent call by a
// different user is rejected as already-initialized.
func TestSetup_SecondCall_AlreadyInitialized(t *testing.T) {
	db := requireSetupDB(t)
	q := generated.New(db)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, "DELETE FROM instance_admins")
	require.NoError(t, err)

	deps := Deps{DB: db, Queries: q, Audit: audit.NoopSink{}}
	handler := Setup(deps)

	first := setupNewUser(t, q)
	second := setupNewUser(t, q)

	_, err = handler(authn.WithActor(ctx, first), &struct{}{})
	require.NoError(t, err, "first setup must succeed")

	_, err = handler(authn.WithActor(ctx, second), &struct{}{})
	require.Error(t, err, "second setup must be rejected as already initialized")

	count, err := q.AdminCountActiveInstanceAdmins(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "only the first user may become admin")
}
