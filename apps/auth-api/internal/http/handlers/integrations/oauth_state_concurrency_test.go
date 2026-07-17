package integrations

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
	integrationspkg "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/integrations"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/testhelpers"
)

// oauthRaceDB lazily boots a shared MySQL testcontainer with the full
// repo schema. The OAuth-state single-use regression needs a real DB
// because it races two concurrent callbacks against the atomic guarded
// DELETE (ClaimOauthState) and asserts exactly one wins.
var oauthRaceDB = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{Database: "nodate_auth_oauth_race_test"})

// requireOauthRaceDB skips when integration tests are not enabled and
// otherwise returns the shared *sql.DB.
func requireOauthRaceDB(t *testing.T) *sql.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping oauth state race integration test in -short mode")
	}
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		t.Skip("set NF_TEST_INTEGRATION=1 to run oauth state race integration tests")
	}
	inst, err := oauthRaceDB.Start(context.Background())
	require.NoError(t, err, "start mysql testcontainer")
	return inst.DB
}

// oauthRaceUser inserts a fresh user and returns the internal user id
// so the oauth_states / user_integrations foreign keys resolve.
func oauthRaceUser(t *testing.T, q *generated.Queries) uint32 {
	t.Helper()
	ctx := context.Background()
	pub := types.New()
	uid64, err := q.RegisterUser(ctx, generated.RegisterUserParams{
		PublicID:        pub,
		Email:           "oauth-race-" + pub.String() + "@example.test",
		DisplayName:     "OAuth Race User",
		Locale:          "en",
		Timezone:        "UTC",
		Country:         sql.NullString{String: "US", Valid: true},
		ThemePreference: generated.UsersThemePreferenceSystem,
	})
	require.NoError(t, err)
	return uint32(uid64) //#nosec G115 -- test fixture LastInsertId fits users.id
}

// TestCallback_ConcurrentSameState_SingleUse proves the OAuth state
// single-use guarantee under concurrency. Two callbacks race the same
// state through GET /oauth/callback/{provider}; the atomic claim
// (ClaimOauthState, a guarded DELETE) must elect exactly one winner.
// The loser must be bounced with state_invalid and never reach the
// provider token exchange. Before the atomic consume a read-then-delete
// sequence let both requests observe the state row and both complete
// the link — the double-spend this test pins.
func TestCallback_ConcurrentSameState_SingleUse(t *testing.T) {
	t.Parallel()
	db := requireOauthRaceDB(t)
	q := generated.New(db)
	ctx := context.Background()

	uid := oauthRaceUser(t, q)
	state := "race-" + types.New().String()
	require.NoError(t, q.CreateOauthState(ctx, generated.CreateOauthStateParams{
		State:     state,
		UserID:    uid,
		Provider:  generated.OauthStatesProviderGithub,
		ExpiresAt: time.Now().Add(oauthStateTTL),
	}))

	// exchanges counts provider Exchange invocations: the race loser
	// must never reach the token exchange.
	var (
		exchangeMu sync.Mutex
		exchanges  int
	)
	reg := integrationspkg.NewRegistry(
		func() (integrationspkg.Provider, error) {
			return &stubExchangeProvider{
				stubProvider: stubProvider{name: "github"},
				tokens:       &integrationspkg.TokenSet{AccessToken: "gho_race", Scopes: []string{"read:user"}},
				account:      &integrationspkg.Account{ExternalID: "42", Label: "octocat"},
				onCall: func() {
					exchangeMu.Lock()
					exchanges++
					exchangeMu.Unlock()
				},
			}, nil
		},
	)
	deps := Deps{
		Queries:       q,
		Registry:      reg,
		Cipher:        newTestCipher(t),
		PublicBaseURL: "https://auth.example.com",
		WebBaseURL:    "https://app.example.com",
	}
	handler := Callback(deps)

	const racers = 2
	var (
		wg            sync.WaitGroup
		mu            sync.Mutex
		okCount       int
		invalidCount  int
		unexpectedLoc string
		unexpectedErr error
		start         = make(chan struct{})
	)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all goroutines simultaneously
			out, herr := handler(ctx, &OAuthCallbackInput{
				Provider: "github",
				Code:     "authcode",
				State:    state,
			})
			mu.Lock()
			defer mu.Unlock()
			if herr != nil {
				unexpectedErr = herr
				return
			}
			switch {
			case strings.Contains(out.Location, "connected=github"):
				okCount++
			case strings.Contains(out.Location, "integration_error=state_invalid"):
				invalidCount++
			default:
				unexpectedLoc = out.Location
			}
		}()
	}
	close(start)
	wg.Wait()

	require.NoError(t, unexpectedErr, "racing callbacks must redirect, not error")
	require.Empty(t, unexpectedLoc, "every racer must land on success or state_invalid")
	assert.Equal(t, 1, okCount, "exactly one concurrent callback may consume the state")
	assert.Equal(t, racers-1, invalidCount,
		"every other racer must be bounced with state_invalid")
	assert.Equal(t, 1, exchanges,
		"the provider token exchange must run exactly once")

	// Authoritative DB checks: the state row must be gone and exactly
	// one integration row linked.
	var stateCount int64
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM oauth_states WHERE state = ?", state,
	).Scan(&stateCount))
	assert.Zero(t, stateCount, "the consumed state row must be deleted")

	var linkCount int64
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM user_integrations WHERE user_id = ?", uid,
	).Scan(&linkCount))
	assert.Equal(t, int64(1), linkCount, "exactly one integration row may be linked")
}
