package integrations

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/crypto"
)

// testCipherKey is a fixed 32-byte key for test Cipher construction.
var testCipherKey = []byte("test-cipher-key-aaaaaaaaaaaaaaaa")

func newTestCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.New(testCipherKey)
	require.NoError(t, err)
	return c
}

// --- LoadUserTokenSet ---

func TestLoadUserTokenSet_NotConnected_NoRow(t *testing.T) {
	t.Parallel()
	q := &fakeLoaderQuerier{findErr: sql.ErrNoRows}
	_, err := LoadUserTokenSet(context.Background(), q, newTestCipher(t), nil, 1, "github")
	require.ErrorIs(t, err, ErrNotConnected,
		"missing DB row must surface as ErrNotConnected, not a raw DB error")
}

func TestLoadUserTokenSet_NotConnected_EmptyCiphertext(t *testing.T) {
	t.Parallel()
	q := &fakeLoaderQuerier{
		findRow: generated.FindUserIntegrationByUserProviderRow{
			ID:                    1,
			AccessTokenCiphertext: nil, // empty
		},
	}
	_, err := LoadUserTokenSet(context.Background(), q, newTestCipher(t), nil, 1, "github")
	require.ErrorIs(t, err, ErrNotConnected,
		"empty access_token_ciphertext must be treated as not connected")
}

func TestLoadUserTokenSet_DecryptsValidToken(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	accessCT, err := c.Encrypt([]byte("my-access-token"))
	require.NoError(t, err)
	refreshCT, err := c.Encrypt([]byte("my-refresh-token"))
	require.NoError(t, err)

	q := &fakeLoaderQuerier{
		findRow: generated.FindUserIntegrationByUserProviderRow{
			ID:                     1,
			PublicID:               types.New(),
			AccessTokenCiphertext:  accessCT,
			RefreshTokenCiphertext: sql.NullString{String: string(refreshCT), Valid: true},
			// No expiry → non-expiring token (GitHub/Slack pattern).
		},
	}
	ts, err := LoadUserTokenSet(context.Background(), q, c, nil, 42, "github")
	require.NoError(t, err)
	assert.Equal(t, "my-access-token", ts.AccessToken)
	assert.Equal(t, "my-refresh-token", ts.RefreshToken)
	assert.True(t, ts.ExpiresAt.IsZero(),
		"non-expiring token must have zero ExpiresAt")
}

func TestLoadUserTokenSet_NonExpiringToken_SkipsRefresh(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	ct, _ := c.Encrypt([]byte("token"))
	q := &fakeLoaderQuerier{
		findRow: generated.FindUserIntegrationByUserProviderRow{
			ID:                    1,
			AccessTokenCiphertext: ct,
			// No expiry.
		},
	}
	// Pass a registry to verify it is NOT called.
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "github"},
			refreshFn: func(ctx context.Context, rt string) (*TokenSet, error) {
				t.Fatal("Refresh must not be called for non-expiring tokens")
				return nil, nil
			},
		}, nil
	})
	ts, err := LoadUserTokenSet(context.Background(), q, c, reg, 1, "github")
	require.NoError(t, err)
	assert.Equal(t, "token", ts.AccessToken)
}

func TestLoadUserTokenSet_ValidToken_NotExpired(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	ct, _ := c.Encrypt([]byte("valid-token"))
	q := &fakeLoaderQuerier{
		findRow: generated.FindUserIntegrationByUserProviderRow{
			ID:                    1,
			AccessTokenCiphertext: ct,
			AccessTokenExpiresAt:  sql.NullTime{Time: time.Now().Add(time.Hour), Valid: true},
		},
	}
	ts, err := LoadUserTokenSet(context.Background(), q, c, nil, 1, "google_calendar")
	require.NoError(t, err)
	assert.Equal(t, "valid-token", ts.AccessToken)
}

func TestLoadUserTokenSet_Expired_NoRegistry_ReturnsErrTokenExpired(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	ct, _ := c.Encrypt([]byte("stale"))
	q := &fakeLoaderQuerier{
		findRow: generated.FindUserIntegrationByUserProviderRow{
			ID:                    1,
			AccessTokenCiphertext: ct,
			AccessTokenExpiresAt:  sql.NullTime{Time: time.Now().Add(-time.Hour), Valid: true},
		},
	}
	_, err := LoadUserTokenSet(context.Background(), q, c, nil, 1, "google_calendar")
	require.ErrorIs(t, err, ErrTokenExpired,
		"expired token without registry must return ErrTokenExpired")
}

func TestLoadUserTokenSet_Expired_NoRefreshToken_ReturnsErrTokenExpired(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	ct, _ := c.Encrypt([]byte("stale"))
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "google_calendar"},
		}, nil
	})
	q := &fakeLoaderQuerier{
		findRow: generated.FindUserIntegrationByUserProviderRow{
			ID:                    1,
			AccessTokenCiphertext: ct,
			AccessTokenExpiresAt:  sql.NullTime{Time: time.Now().Add(-time.Hour), Valid: true},
			// RefreshTokenCiphertext is empty.
		},
	}
	_, err := LoadUserTokenSet(context.Background(), q, c, reg, 1, "google_calendar")
	require.ErrorIs(t, err, ErrTokenExpired,
		"expired token without refresh token must return ErrTokenExpired")
}

func TestLoadUserTokenSet_Expired_RefreshNotSupported_ReturnsErrTokenExpired(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	accessCT, _ := c.Encrypt([]byte("stale"))
	refreshCT, _ := c.Encrypt([]byte("rt"))
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "github"},
			refreshFn: func(ctx context.Context, rt string) (*TokenSet, error) {
				return nil, ErrRefreshNotSupported
			},
		}, nil
	})
	q := &fakeLoaderQuerier{
		findRow: generated.FindUserIntegrationByUserProviderRow{
			ID:                     1,
			AccessTokenCiphertext:  accessCT,
			RefreshTokenCiphertext: sql.NullString{String: string(refreshCT), Valid: true},
			AccessTokenExpiresAt:   sql.NullTime{Time: time.Now().Add(-time.Hour), Valid: true},
		},
	}
	_, err := LoadUserTokenSet(context.Background(), q, c, reg, 1, "github")
	require.ErrorIs(t, err, ErrTokenExpired)
}

func TestLoadUserTokenSet_Expired_JITRefreshSucceeds(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	accessCT, _ := c.Encrypt([]byte("old-access"))
	refreshCT, _ := c.Encrypt([]byte("my-refresh"))
	newExpiry := time.Now().Add(time.Hour).Truncate(time.Second)

	var updatedParams generated.UpdateConnectionTokensParams
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "google_calendar"},
			refreshFn: func(ctx context.Context, rt string) (*TokenSet, error) {
				assert.Equal(t, "my-refresh", rt,
					"Refresh must receive the decrypted refresh token")
				return &TokenSet{
					AccessToken:  "fresh-access",
					RefreshToken: "my-refresh", // not rotated
					ExpiresAt:    newExpiry,
				}, nil
			},
		}, nil
	})
	q := &fakeLoaderQuerier{
		findRow: generated.FindUserIntegrationByUserProviderRow{
			ID:                     7,
			AccessTokenCiphertext:  accessCT,
			RefreshTokenCiphertext: sql.NullString{String: string(refreshCT), Valid: true},
			AccessTokenExpiresAt:   sql.NullTime{Time: time.Now().Add(-5 * time.Minute), Valid: true},
		},
		updateFn: func(params generated.UpdateConnectionTokensParams) error {
			updatedParams = params
			return nil
		},
	}
	ts, err := LoadUserTokenSet(context.Background(), q, c, reg, 1, "google_calendar")
	require.NoError(t, err)
	assert.Equal(t, "fresh-access", ts.AccessToken,
		"must return the refreshed access token")
	assert.Equal(t, "my-refresh", ts.RefreshToken,
		"must preserve existing refresh token when not rotated")

	// Verify the persisted row received encrypted new tokens.
	assert.Equal(t, uint32(7), updatedParams.ID,
		"must update the correct row ID")
	assert.NotEmpty(t, updatedParams.AccessTokenCiphertext,
		"must persist encrypted new access token")
	// Decrypt to verify correctness.
	decrypted, err := c.Decrypt(updatedParams.AccessTokenCiphertext)
	require.NoError(t, err)
	assert.Equal(t, "fresh-access", string(decrypted))
}

func TestLoadUserTokenSet_Expired_JITRefreshFails_ReturnsStaleFallback(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	accessCT, _ := c.Encrypt([]byte("stale-access"))
	refreshCT, _ := c.Encrypt([]byte("rt"))
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "google_calendar"},
			refreshFn: func(ctx context.Context, rt string) (*TokenSet, error) {
				return nil, errors.New("provider unavailable")
			},
		}, nil
	})
	q := &fakeLoaderQuerier{
		findRow: generated.FindUserIntegrationByUserProviderRow{
			ID:                     1,
			AccessTokenCiphertext:  accessCT,
			RefreshTokenCiphertext: sql.NullString{String: string(refreshCT), Valid: true},
			AccessTokenExpiresAt:   sql.NullTime{Time: time.Now().Add(-time.Minute), Valid: true},
		},
	}
	ts, err := LoadUserTokenSet(context.Background(), q, c, reg, 1, "google_calendar")
	require.NoError(t, err,
		"transient refresh failure must not be fatal — caller gets the stale token to try")
	assert.Equal(t, "stale-access", ts.AccessToken)
}

func TestLoadUserTokenSet_Expired_JITRefreshRotatesRefreshToken(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	accessCT, _ := c.Encrypt([]byte("old-access"))
	refreshCT, _ := c.Encrypt([]byte("old-refresh"))

	var updatedParams generated.UpdateConnectionTokensParams
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "google_calendar"},
			refreshFn: func(ctx context.Context, rt string) (*TokenSet, error) {
				return &TokenSet{
					AccessToken:  "new-access",
					RefreshToken: "rotated-refresh", // different from stored
					ExpiresAt:    time.Now().Add(time.Hour),
				}, nil
			},
		}, nil
	})
	q := &fakeLoaderQuerier{
		findRow: generated.FindUserIntegrationByUserProviderRow{
			ID:                     3,
			AccessTokenCiphertext:  accessCT,
			RefreshTokenCiphertext: sql.NullString{String: string(refreshCT), Valid: true},
			AccessTokenExpiresAt:   sql.NullTime{Time: time.Now().Add(-time.Minute), Valid: true},
		},
		updateFn: func(params generated.UpdateConnectionTokensParams) error {
			updatedParams = params
			return nil
		},
	}
	ts, err := LoadUserTokenSet(context.Background(), q, c, reg, 1, "google_calendar")
	require.NoError(t, err)
	assert.Equal(t, "new-access", ts.AccessToken)
	assert.Equal(t, "rotated-refresh", ts.RefreshToken,
		"caller must receive the rotated refresh token")

	// Persisted refresh token must be the new one, encrypted.
	require.True(t, updatedParams.RefreshTokenCiphertext.Valid,
		"rotated refresh token must be persisted")
	decrypted, err := c.Decrypt([]byte(updatedParams.RefreshTokenCiphertext.String))
	require.NoError(t, err)
	assert.Equal(t, "rotated-refresh", string(decrypted),
		"persisted ciphertext must decrypt to the rotated token")
}

func TestLoadUserTokenSet_Expired_JITRefreshPersistFails_StillReturnsFresh(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	accessCT, _ := c.Encrypt([]byte("old-access"))
	refreshCT, _ := c.Encrypt([]byte("rt"))
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "google_calendar"},
			refreshFn: func(ctx context.Context, rt string) (*TokenSet, error) {
				return &TokenSet{
					AccessToken: "fresh",
					ExpiresAt:   time.Now().Add(time.Hour),
				}, nil
			},
		}, nil
	})
	q := &fakeLoaderQuerier{
		findRow: generated.FindUserIntegrationByUserProviderRow{
			ID:                     1,
			AccessTokenCiphertext:  accessCT,
			RefreshTokenCiphertext: sql.NullString{String: string(refreshCT), Valid: true},
			AccessTokenExpiresAt:   sql.NullTime{Time: time.Now().Add(-time.Minute), Valid: true},
		},
		updateFn: func(_ generated.UpdateConnectionTokensParams) error {
			return errors.New("db write failed")
		},
	}
	ts, err := LoadUserTokenSet(context.Background(), q, c, reg, 1, "google_calendar")
	require.NoError(t, err,
		"DB persist failure must be non-fatal — the background refresher will catch up")
	assert.Equal(t, "fresh", ts.AccessToken,
		"caller must receive the fresh token regardless of persistence outcome")
}

func TestLoadUserTokenSet_Expired_JITRefreshReturnsNil_FallsBack(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	accessCT, _ := c.Encrypt([]byte("stale"))
	refreshCT, _ := c.Encrypt([]byte("rt"))
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "google_calendar"},
			refreshFn: func(ctx context.Context, rt string) (*TokenSet, error) {
				return nil, nil // nil result, nil error
			},
		}, nil
	})
	q := &fakeLoaderQuerier{
		findRow: generated.FindUserIntegrationByUserProviderRow{
			ID:                     1,
			AccessTokenCiphertext:  accessCT,
			RefreshTokenCiphertext: sql.NullString{String: string(refreshCT), Valid: true},
			AccessTokenExpiresAt:   sql.NullTime{Time: time.Now().Add(-time.Minute), Valid: true},
		},
	}
	ts, err := LoadUserTokenSet(context.Background(), q, c, reg, 1, "google_calendar")
	require.NoError(t, err)
	assert.Equal(t, "stale", ts.AccessToken,
		"nil refresh result must fall back to the stale token")
}

func TestLoadUserTokenSet_DBError_PropagatesUnwrapped(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("connection refused")
	q := &fakeLoaderQuerier{findErr: dbErr}
	_, err := LoadUserTokenSet(context.Background(), q, newTestCipher(t), nil, 1, "github")
	require.ErrorIs(t, err, dbErr,
		"non-ErrNoRows DB errors must propagate directly, not masked as ErrNotConnected")
}

// --- test doubles ---

type fakeLoaderQuerier struct {
	findRow generated.FindUserIntegrationByUserProviderRow
	findErr error

	updateFn func(generated.UpdateConnectionTokensParams) error
}

func (f *fakeLoaderQuerier) FindUserIntegrationByUserProvider(
	ctx context.Context, arg generated.FindUserIntegrationByUserProviderParams,
) (generated.FindUserIntegrationByUserProviderRow, error) {
	return f.findRow, f.findErr
}

func (f *fakeLoaderQuerier) UpdateConnectionTokens(
	ctx context.Context, arg generated.UpdateConnectionTokensParams,
) error {
	if f.updateFn != nil {
		return f.updateFn(arg)
	}
	return nil
}

// stubRefreshProvider extends stubProvider with a customisable Refresh.
type stubRefreshProvider struct {
	stubProvider
	refreshFn func(ctx context.Context, refreshToken string) (*TokenSet, error)
}

func (s *stubRefreshProvider) Refresh(ctx context.Context, refreshToken string) (*TokenSet, error) {
	if s.refreshFn != nil {
		return s.refreshFn(ctx, refreshToken)
	}
	return nil, ErrRefreshNotSupported
}
