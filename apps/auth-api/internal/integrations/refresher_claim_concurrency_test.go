package integrations

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/crypto"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// refreshRaceDB lazily boots a shared MySQL testcontainer with the full
// repo schema. The claim is a compare-and-swap expressed in SQL, so an
// in-process fake would only exercise the Go around it; the property
// under test is that the database elects one winner.
var refreshRaceDB = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{Database: "nodate_auth_refresh_race_test"})

func requireRefreshRaceDB(t *testing.T) *sql.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping refresher claim race integration test in -short mode")
	}
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		t.Skip("set NF_TEST_INTEGRATION=1 to run refresher claim race integration tests")
	}
	inst, err := refreshRaceDB.Start(context.Background())
	require.NoError(t, err, "start mysql testcontainer")
	return inst.DB
}

// rotatingProvider models a provider that rotates the refresh token on
// every use and treats a retired token as replay — Discord's behaviour,
// and the reason a duplicate refresh is destructive rather than merely
// wasteful. Once it sees a token it has already retired it marks the
// grant revoked, which is exactly the state a user experiences as "the
// integration stopped working and nothing said why".
type rotatingProvider struct {
	stubProvider

	mu      sync.Mutex
	current string
	issued  int
	revoked bool
}

var errGrantRevoked = errors.New("integrations: refresh token reuse detected, grant revoked")

func (p *rotatingProvider) Refresh(_ context.Context, refreshToken []byte) (*TokenSet, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.revoked {
		return nil, errGrantRevoked
	}
	if string(refreshToken) != p.current {
		// A token this provider already rotated away. Real providers
		// respond by killing the whole grant.
		p.revoked = true
		return nil, errGrantRevoked
	}
	p.issued++
	p.current = "rotated-" + types.New().String()
	return &TokenSet{
		AccessToken:  "access-" + types.New().String(),
		RefreshToken: p.current,
		ExpiresAt:    time.Now().Add(time.Hour),
	}, nil
}

func (p *rotatingProvider) snapshot() (issued int, revoked bool, current string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.issued, p.revoked, p.current
}

// refreshRaceUser inserts a user so the user_integrations FK resolves.
func refreshRaceUser(t *testing.T, q *generated.Queries) uint32 {
	t.Helper()
	pub := types.New()
	uid, err := q.RegisterUser(context.Background(), generated.RegisterUserParams{
		PublicID:        pub,
		Email:           "refresh-race-" + pub.String() + "@example.test",
		DisplayName:     "Refresh Race User",
		Locale:          "en",
		Timezone:        "UTC",
		Country:         sql.NullString{String: "US", Valid: true},
		ThemePreference: generated.UsersThemePreferenceSystem,
	})
	require.NoError(t, err)
	return uint32(uid) //#nosec G115 -- test fixture LastInsertId fits users.id
}

// TestRefresher_ConcurrentReplicas_DoNotRevokeTheGrant proves the claim
// does what it exists for: with every API replica running the refresher
// loop, one connection due for refresh must be exchanged exactly once
// per pass, and the user's authorization must survive.
//
// The shape is rounds rather than one burst on purpose. A single burst
// can pass by luck — one goroutine finishing before the others start —
// while a scheduler that interleaves them differently on the next run
// fails. Each round re-arms the row so the whole population is eligible
// again, and the assertion is per round, so an implementation that wins
// the race occasionally is still red.
//
// The assertions are deliberately about the connection, not about
// counters internal to the refresher: `revoked` is the user-visible
// failure (the integration silently stops working and needs re-consent)
// and `issued == 1 per round` is the mechanism that prevents it.
//
// Nothing here asserts on instance-wide row counts: the suite runs in
// parallel against a shared database, so every query is scoped to the
// row this test created.
func TestRefresher_ConcurrentReplicas_DoNotRevokeTheGrant(t *testing.T) {
	t.Parallel()
	db := requireRefreshRaceDB(t)
	q := generated.New(db)
	ctx := context.Background()

	cipher, err := crypto.New(testCipherKey)
	require.NoError(t, err)

	provider := &rotatingProvider{
		stubProvider: stubProvider{name: "discord"},
		current:      "seed-refresh-token",
	}
	reg := NewRegistry(func() (Provider, error) { return provider, nil })

	uid := refreshRaceUser(t, q)
	connPub := types.New()
	seedAccess, err := cipher.Encrypt([]byte("seed-access-token"))
	require.NoError(t, err)
	seedRefresh, err := cipher.Encrypt([]byte(provider.current))
	require.NoError(t, err)

	connID, err := q.UpsertUserIntegration(ctx, generated.UpsertUserIntegrationParams{
		PublicID:               connPub,
		UserID:                 uid,
		Provider:               generated.UserIntegrationsProviderDiscord,
		ExternalAccountID:      "race-" + connPub.String(),
		ExternalAccountLabel:   "race@example.test",
		Scopes:                 "identify",
		AccessTokenCiphertext:  seedAccess,
		RefreshTokenCiphertext: sql.NullString{String: string(seedRefresh), Valid: true},
		AccessTokenExpiresAt:   sql.NullTime{Time: time.Now().Add(time.Minute), Valid: true},
	})
	require.NoError(t, err)
	require.NotZero(t, connID)

	// Silence the per-row warnings the losing replicas would otherwise
	// print; the test asserts on outcomes, not on log volume.
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	const (
		rounds   = 40
		replicas = 8
	)
	for round := 1; round <= rounds; round++ {
		// Re-arm the row into the state a later tick would find it in:
		// inside the refresh window, holding the token the provider
		// currently considers valid, and last refreshed long enough ago
		// that the claim lease has lapsed. Production reaches this state
		// by waiting out the ticker; the test states it directly so the
		// rounds can run back to back.
		_, _, currentToken := provider.snapshot()
		armed, encErr := cipher.Encrypt([]byte(currentToken))
		require.NoError(t, encErr)
		_, execErr := db.ExecContext(ctx,
			`UPDATE user_integrations
			    SET refresh_token_ciphertext = ?,
			        access_token_expires_at = ?,
			        last_refreshed_at = ?
			  WHERE id = ?`,
			string(armed), time.Now().Add(time.Minute),
			time.Now().Add(-time.Hour), connID)
		require.NoError(t, execErr)

		before, _, _ := provider.snapshot()

		var wg sync.WaitGroup
		wg.Add(replicas)
		start := make(chan struct{})
		for i := 0; i < replicas; i++ {
			go func() {
				defer wg.Done()
				<-start
				r := &Refresher{
					Queries:    q,
					Cipher:     cipher,
					Registry:   reg,
					Logger:     quiet,
					LeadTime:   15 * time.Minute,
					ClaimLease: 30 * time.Second,
				}
				// A failure here is a database error, not a lost race;
				// the race outcome is asserted from the provider.
				_ = r.RefreshOnce(ctx)
			}()
		}
		close(start)
		wg.Wait()

		after, revoked, _ := provider.snapshot()
		require.Falsef(t, revoked,
			"round %d: a second replica presented a rotated refresh token and the provider killed the grant; "+
				"the connection is now permanently broken with no signal to the user", round)
		assert.Equalf(t, 1, after-before,
			"round %d: %d replicas listed the same connection and %d of them exchanged its refresh token; "+
				"exactly one claim must win", round, replicas, after-before)
	}

	// The surviving connection must still be usable: a refresh with the
	// token now stored has to be accepted by the provider.
	stored, err := q.FindUserIntegrationByUserProvider(ctx, generated.FindUserIntegrationByUserProviderParams{
		UserID:   uid,
		Provider: generated.UserIntegrationsProviderDiscord,
	})
	require.NoError(t, err)
	require.True(t, stored.RefreshTokenCiphertext.Valid, "the connection lost its refresh token")
	plain, err := cipher.Decrypt([]byte(stored.RefreshTokenCiphertext.String))
	require.NoError(t, err)
	_, err = provider.Refresh(ctx, plain)
	require.NoError(t, err, "the stored refresh token is no longer the one the provider will accept")
}

// TestClaimConnectionForRefresh_SecondClaimOnSameStateLoses pins the
// claim itself, independent of the refresher loop. Two callers that read
// the same last_refreshed_at may not both win, and the winner's own next
// claim on the stale value must lose too — that second half is what
// stops a replica from re-claiming a row it already refreshed within the
// same pass.
func TestClaimConnectionForRefresh_SecondClaimOnSameStateLoses(t *testing.T) {
	t.Parallel()
	db := requireRefreshRaceDB(t)
	q := generated.New(db)
	ctx := context.Background()

	uid := refreshRaceUser(t, q)
	connPub := types.New()
	connID, err := q.UpsertUserIntegration(ctx, generated.UpsertUserIntegrationParams{
		PublicID:               connPub,
		UserID:                 uid,
		Provider:               generated.UserIntegrationsProviderGoogleCalendar,
		ExternalAccountID:      "claim-" + connPub.String(),
		ExternalAccountLabel:   "claim@example.test",
		Scopes:                 "calendar",
		AccessTokenCiphertext:  []byte("ct"),
		RefreshTokenCiphertext: sql.NullString{String: "rt", Valid: true},
		AccessTokenExpiresAt:   sql.NullTime{Time: time.Now().Add(time.Minute), Valid: true},
	})
	require.NoError(t, err)
	id := uint32(connID) //#nosec G115 -- test fixture LastInsertId fits user_integrations.id

	var observed sql.NullTime
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT last_refreshed_at FROM user_integrations WHERE id = ?`, id).Scan(&observed))

	first, err := q.ClaimConnectionForRefresh(ctx, generated.ClaimConnectionForRefreshParams{
		ID:              id,
		LastRefreshedAt: observed,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 1, first, "the first claim on the observed state must win")

	second, err := q.ClaimConnectionForRefresh(ctx, generated.ClaimConnectionForRefreshParams{
		ID:              id,
		LastRefreshedAt: observed,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 0, second,
		"a claim on a last_refreshed_at that has already been swapped must lose; "+
			"otherwise every replica in the pass refreshes the same row")
}
