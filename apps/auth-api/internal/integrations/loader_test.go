package integrations

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/crypto"
)

// testCipherKey is a fixed 32-byte key for test Cipher construction.
var testCipherKey = []byte("test-cipher-key-aaaaaaaaaaaaaaaa")

func newTestCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.New(testCipherKey)
	require.NoError(t, err)
	return c
}

// captured records what the callback saw, as strings, so assertions do
// not hold the borrowed slices past the call.
type captured struct {
	calls   int
	access  string
	refresh string
	expires time.Time
}

// capture returns a callback that records the tokens it was handed.
func capture(got *captured) func(context.Context, Tokens) error {
	return func(_ context.Context, tk Tokens) error {
		got.calls++
		got.access = string(tk.AccessToken)
		got.refresh = string(tk.RefreshToken)
		got.expires = tk.ExpiresAt
		return nil
	}
}

// mustNotRun is a callback that fails the test if the loader reaches it.
func mustNotRun(t *testing.T) func(context.Context, Tokens) error {
	t.Helper()
	return func(context.Context, Tokens) error {
		t.Fatal("the callback must not run on a path that returns an error")
		return nil
	}
}

// --- WithUserTokens ---

func TestWithUserTokens_NotConnected_NoRow(t *testing.T) {
	t.Parallel()
	q := &fakeLoaderQuerier{findErr: sql.ErrNoRows}
	err := WithUserTokens(context.Background(), q, newTestCipher(t), nil, 1, "github", mustNotRun(t))
	require.ErrorIs(t, err, ErrNotConnected,
		"missing DB row must surface as ErrNotConnected, not a raw DB error")
}

func TestWithUserTokens_NotConnected_EmptyCiphertext(t *testing.T) {
	t.Parallel()
	q := &fakeLoaderQuerier{
		findRow: generated.FindUserIntegrationByUserProviderRow{
			ID:                    1,
			AccessTokenCiphertext: nil, // empty
		},
	}
	err := WithUserTokens(context.Background(), q, newTestCipher(t), nil, 1, "github", mustNotRun(t))
	require.ErrorIs(t, err, ErrNotConnected,
		"empty access_token_ciphertext must be treated as not connected")
}

func TestWithUserTokens_DecryptsValidToken(t *testing.T) {
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
	var got captured
	require.NoError(t, WithUserTokens(context.Background(), q, c, nil, 42, "github", capture(&got)))
	assert.Equal(t, 1, got.calls)
	assert.Equal(t, "my-access-token", got.access)
	assert.Equal(t, "my-refresh-token", got.refresh)
	assert.True(t, got.expires.IsZero(),
		"non-expiring token must have zero ExpiresAt")
}

// TestWithUserTokens_WipesThePlaintextAfterTheCallback is the property
// the callback shape exists for: once the work that needed the tokens is
// done, neither plaintext is still in the buffer the loader decrypted
// into. A helper that returned the tokens instead could not hold this —
// nothing would know when the caller was finished with them.
func TestWithUserTokens_WipesThePlaintextAfterTheCallback(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	accessCT, err := c.Encrypt([]byte("my-access-token"))
	require.NoError(t, err)
	refreshCT, err := c.Encrypt([]byte("my-refresh-token"))
	require.NoError(t, err)

	q := &fakeLoaderQuerier{
		findRow: generated.FindUserIntegrationByUserProviderRow{
			ID:                     1,
			AccessTokenCiphertext:  accessCT,
			RefreshTokenCiphertext: sql.NullString{String: string(refreshCT), Valid: true},
		},
	}
	// Held deliberately, which is what the doc tells callers not to do.
	// It is the only way to look at the buffer after the wipe.
	var heldAccess, heldRefresh []byte
	require.NoError(t, WithUserTokens(context.Background(), q, c, nil, 1, "github",
		func(_ context.Context, tk Tokens) error {
			require.Equal(t, "my-access-token", string(tk.AccessToken),
				"the callback must see the plaintext while it runs")
			require.Equal(t, "my-refresh-token", string(tk.RefreshToken))
			heldAccess = tk.AccessToken
			heldRefresh = tk.RefreshToken
			return nil
		}))

	require.NotEmpty(t, heldAccess, "the callback must have been handed a token to hold")
	assert.Equal(t, bytes.Repeat([]byte{0}, len(heldAccess)), heldAccess,
		"the access token plaintext is still readable after the callback returned")
	assert.Equal(t, bytes.Repeat([]byte{0}, len(heldRefresh)), heldRefresh,
		"the refresh token plaintext is still readable after the callback returned")
}

// TestWithUserTokens_WipesThePlaintextWhenTheCallbackFails holds the
// same property on the error path: a callback that returns an error is
// the case where the plaintext is most likely to be forgotten.
func TestWithUserTokens_WipesThePlaintextWhenTheCallbackFails(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	accessCT, err := c.Encrypt([]byte("my-access-token"))
	require.NoError(t, err)
	q := &fakeLoaderQuerier{
		findRow: generated.FindUserIntegrationByUserProviderRow{
			ID:                    1,
			AccessTokenCiphertext: accessCT,
		},
	}
	useErr := errors.New("provider call failed")
	var held []byte
	err = WithUserTokens(context.Background(), q, c, nil, 1, "github",
		func(_ context.Context, tk Tokens) error {
			held = tk.AccessToken
			return useErr
		})
	require.ErrorIs(t, err, useErr, "the callback's error must reach the caller unchanged")
	require.NotEmpty(t, held)
	assert.Equal(t, bytes.Repeat([]byte{0}, len(held)), held,
		"the plaintext survived a callback that returned an error")
}

func TestWithUserTokens_NonExpiringToken_SkipsRefresh(t *testing.T) {
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
			refreshFn: func(_ context.Context, _ []byte) (*TokenSet, error) {
				t.Fatal("Refresh must not be called for non-expiring tokens")
				return nil, nil
			},
		}, nil
	})
	var got captured
	require.NoError(t, WithUserTokens(context.Background(), q, c, reg, 1, "github", capture(&got)))
	assert.Equal(t, "token", got.access)
}

func TestWithUserTokens_ValidToken_NotExpired(t *testing.T) {
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
	var got captured
	require.NoError(t, WithUserTokens(context.Background(), q, c, nil, 1, "google_calendar", capture(&got)))
	assert.Equal(t, "valid-token", got.access)
}

func TestWithUserTokens_Expired_NoRegistry_ReturnsErrTokenExpired(t *testing.T) {
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
	err := WithUserTokens(context.Background(), q, c, nil, 1, "google_calendar", mustNotRun(t))
	require.ErrorIs(t, err, ErrTokenExpired,
		"expired token without registry must return ErrTokenExpired")
}

func TestWithUserTokens_Expired_NoRefreshToken_ReturnsErrTokenExpired(t *testing.T) {
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
	err := WithUserTokens(context.Background(), q, c, reg, 1, "google_calendar", mustNotRun(t))
	require.ErrorIs(t, err, ErrTokenExpired,
		"expired token without refresh token must return ErrTokenExpired")
}

func TestWithUserTokens_Expired_RefreshNotSupported_ReturnsErrTokenExpired(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	accessCT, _ := c.Encrypt([]byte("stale"))
	refreshCT, _ := c.Encrypt([]byte("rt"))
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "github"},
			refreshFn: func(_ context.Context, _ []byte) (*TokenSet, error) {
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
	err := WithUserTokens(context.Background(), q, c, reg, 1, "github", mustNotRun(t))
	require.ErrorIs(t, err, ErrTokenExpired)
}

func TestWithUserTokens_Expired_JITRefreshSucceeds(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	accessCT, _ := c.Encrypt([]byte("old-access"))
	refreshCT, _ := c.Encrypt([]byte("my-refresh"))
	newExpiry := time.Now().Add(time.Hour).Truncate(time.Second)

	var updatedParams generated.UpdateConnectionTokensParams
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "google_calendar"},
			refreshFn: func(_ context.Context, rt []byte) (*TokenSet, error) {
				assert.Equal(t, "my-refresh", string(rt),
					"Refresh must receive the decrypted refresh token")
				return &TokenSet{
					AccessToken: "fresh-access",
					// Empty RefreshToken is how a provider reports
					// "not rotated"; the stored one stays in force.
					ExpiresAt: newExpiry,
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
	var got captured
	require.NoError(t, WithUserTokens(context.Background(), q, c, reg, 1, "google_calendar", capture(&got)))
	assert.Equal(t, "fresh-access", got.access,
		"must hand over the refreshed access token")
	assert.Equal(t, "my-refresh", got.refresh,
		"a provider that did not rotate must leave the stored refresh token in force")

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

// TestWithUserTokens_WipesTheRefreshedPlaintext holds the wipe on the
// JIT-refresh path, where the plaintext handed to the callback is the
// one that came back from the provider rather than the one decrypted
// from the row.
func TestWithUserTokens_WipesTheRefreshedPlaintext(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	accessCT, _ := c.Encrypt([]byte("old-access"))
	refreshCT, _ := c.Encrypt([]byte("old-refresh"))
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "google_calendar"},
			refreshFn: func(_ context.Context, _ []byte) (*TokenSet, error) {
				return &TokenSet{
					AccessToken:  "fresh-access",
					RefreshToken: "rotated-refresh",
					ExpiresAt:    time.Now().Add(time.Hour),
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
	}
	var heldAccess, heldRefresh []byte
	require.NoError(t, WithUserTokens(context.Background(), q, c, reg, 1, "google_calendar",
		func(_ context.Context, tk Tokens) error {
			heldAccess = tk.AccessToken
			heldRefresh = tk.RefreshToken
			return nil
		}))
	assert.Equal(t, bytes.Repeat([]byte{0}, len(heldAccess)), heldAccess,
		"the refreshed access token is still readable after the callback returned")
	assert.Equal(t, bytes.Repeat([]byte{0}, len(heldRefresh)), heldRefresh,
		"the rotated refresh token is still readable after the callback returned")
}

func TestWithUserTokens_Expired_JITRefreshFails_ReturnsStaleFallback(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	accessCT, _ := c.Encrypt([]byte("stale-access"))
	refreshCT, _ := c.Encrypt([]byte("rt"))
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "google_calendar"},
			refreshFn: func(_ context.Context, _ []byte) (*TokenSet, error) {
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
	var got captured
	require.NoError(t, WithUserTokens(context.Background(), q, c, reg, 1, "google_calendar", capture(&got)),
		"transient refresh failure must not be fatal — caller gets the stale token to try")
	assert.Equal(t, "stale-access", got.access)
}

func TestWithUserTokens_Expired_JITRefreshRotatesRefreshToken(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	accessCT, _ := c.Encrypt([]byte("old-access"))
	refreshCT, _ := c.Encrypt([]byte("old-refresh"))

	var updatedParams generated.UpdateConnectionTokensParams
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "google_calendar"},
			refreshFn: func(_ context.Context, _ []byte) (*TokenSet, error) {
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
	var got captured
	require.NoError(t, WithUserTokens(context.Background(), q, c, reg, 1, "google_calendar", capture(&got)))
	assert.Equal(t, "new-access", got.access)
	assert.Equal(t, "rotated-refresh", got.refresh,
		"the callback must receive the rotated refresh token")

	// Persisted refresh token must be the new one, encrypted.
	require.True(t, updatedParams.RefreshTokenCiphertext.Valid,
		"rotated refresh token must be persisted")
	decrypted, err := c.Decrypt([]byte(updatedParams.RefreshTokenCiphertext.String))
	require.NoError(t, err)
	assert.Equal(t, "rotated-refresh", string(decrypted),
		"persisted ciphertext must decrypt to the rotated token")
}

func TestWithUserTokens_Expired_JITRefreshPersistFails_StillReturnsFresh(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	accessCT, _ := c.Encrypt([]byte("old-access"))
	refreshCT, _ := c.Encrypt([]byte("rt"))
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "google_calendar"},
			refreshFn: func(_ context.Context, _ []byte) (*TokenSet, error) {
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
	var got captured
	require.NoError(t, WithUserTokens(context.Background(), q, c, reg, 1, "google_calendar", capture(&got)),
		"DB persist failure must be non-fatal — the background refresher will catch up")
	assert.Equal(t, "fresh", got.access,
		"the callback must receive the fresh token regardless of persistence outcome")
}

func TestWithUserTokens_Expired_JITRefreshReturnsNil_FallsBack(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	accessCT, _ := c.Encrypt([]byte("stale"))
	refreshCT, _ := c.Encrypt([]byte("rt"))
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "google_calendar"},
			refreshFn: func(_ context.Context, _ []byte) (*TokenSet, error) {
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
	var got captured
	require.NoError(t, WithUserTokens(context.Background(), q, c, reg, 1, "google_calendar", capture(&got)))
	assert.Equal(t, "stale", got.access,
		"nil refresh result must fall back to the stale token")
}

func TestWithUserTokens_DBError_PropagatesUnwrapped(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("connection refused")
	q := &fakeLoaderQuerier{findErr: dbErr}
	err := WithUserTokens(context.Background(), q, newTestCipher(t), nil, 1, "github", mustNotRun(t))
	require.ErrorIs(t, err, dbErr,
		"non-ErrNoRows DB errors must propagate directly, not masked as ErrNotConnected")
}

// --- WithStoredTokens ---

// TestWithStoredTokens_WipesThePlaintextAfterTheCallback holds the borrow
// contract for the expiry-free entry point, which revocation uses.
func TestWithStoredTokens_WipesThePlaintextAfterTheCallback(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	accessCT, err := c.Encrypt([]byte("my-access-token"))
	require.NoError(t, err)
	refreshCT, err := c.Encrypt([]byte("my-refresh-token"))
	require.NoError(t, err)

	q := &fakeLoaderQuerier{
		findRow: generated.FindUserIntegrationByUserProviderRow{
			ID:                     1,
			AccessTokenCiphertext:  accessCT,
			RefreshTokenCiphertext: sql.NullString{String: string(refreshCT), Valid: true},
		},
	}
	// Held deliberately, which is what the doc tells callers not to do.
	// It is the only way to look at the buffer after the wipe.
	var heldAccess, heldRefresh []byte
	require.NoError(t, WithStoredTokens(context.Background(), q, c, 1, "google_calendar",
		func(_ context.Context, tk Tokens) error {
			require.Equal(t, "my-access-token", string(tk.AccessToken),
				"the callback must see the plaintext while it runs")
			require.Equal(t, "my-refresh-token", string(tk.RefreshToken))
			heldAccess = tk.AccessToken
			heldRefresh = tk.RefreshToken
			return nil
		}))

	require.NotEmpty(t, heldAccess, "the callback must have been handed a token to hold")
	assert.Equal(t, bytes.Repeat([]byte{0}, len(heldAccess)), heldAccess,
		"the access token plaintext is still readable after the callback returned")
	assert.Equal(t, bytes.Repeat([]byte{0}, len(heldRefresh)), heldRefresh,
		"the refresh token plaintext is still readable after the callback returned")
}

// TestWithStoredTokens_WipesThePlaintextWhenTheCallbackFails holds the
// same property on the error path — a provider revoke that fails is the
// case where the plaintext is most likely to be forgotten.
func TestWithStoredTokens_WipesThePlaintextWhenTheCallbackFails(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	accessCT, err := c.Encrypt([]byte("my-access-token"))
	require.NoError(t, err)
	q := &fakeLoaderQuerier{
		findRow: generated.FindUserIntegrationByUserProviderRow{
			ID:                    1,
			AccessTokenCiphertext: accessCT,
		},
	}
	useErr := errors.New("provider call failed")
	var held []byte
	err = WithStoredTokens(context.Background(), q, c, 1, "github",
		func(_ context.Context, tk Tokens) error {
			held = tk.AccessToken
			return useErr
		})
	require.ErrorIs(t, err, useErr, "the callback's error must reach the caller unchanged")
	require.NotEmpty(t, held)
	assert.Equal(t, bytes.Repeat([]byte{0}, len(held)), held,
		"the plaintext survived a callback that returned an error")
}

// TestWithStoredTokens_ExpiredAccessToken_StillHandsOverTokens is the
// reason this entry point exists alongside WithUserTokens. A grant whose
// access token has expired still has to be revocable: the refresh token
// outlives the access tokens it mints, and skipping it would leave the
// grant live at the provider after the user disconnected.
func TestWithStoredTokens_ExpiredAccessToken_StillHandsOverTokens(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	accessCT, _ := c.Encrypt([]byte("expired-access"))
	refreshCT, _ := c.Encrypt([]byte("still-valid-refresh"))
	q := &fakeLoaderQuerier{
		findRow: generated.FindUserIntegrationByUserProviderRow{
			ID:                     1,
			AccessTokenCiphertext:  accessCT,
			RefreshTokenCiphertext: sql.NullString{String: string(refreshCT), Valid: true},
			AccessTokenExpiresAt:   sql.NullTime{Time: time.Now().Add(-time.Hour), Valid: true},
		},
	}
	var got captured
	require.NoError(t, WithStoredTokens(context.Background(), q, c, 1, "google_calendar", capture(&got)))
	assert.Equal(t, 1, got.calls, "an expired access token must not skip the callback")
	assert.Equal(t, "still-valid-refresh", got.refresh)
}

func TestWithStoredTokens_NotConnected_NoRow(t *testing.T) {
	t.Parallel()
	q := &fakeLoaderQuerier{findErr: sql.ErrNoRows}
	err := WithStoredTokens(context.Background(), q, newTestCipher(t), 1, "github", mustNotRun(t))
	require.ErrorIs(t, err, ErrNotConnected,
		"missing DB row must surface as ErrNotConnected, not a raw DB error")
}

// --- test doubles ---

type fakeLoaderQuerier struct {
	findRow generated.FindUserIntegrationByUserProviderRow
	findErr error

	updateFn func(generated.UpdateConnectionTokensParams) error
}

func (f *fakeLoaderQuerier) FindUserIntegrationByUserProvider(
	_ context.Context, _ generated.FindUserIntegrationByUserProviderParams,
) (generated.FindUserIntegrationByUserProviderRow, error) {
	return f.findRow, f.findErr
}

func (f *fakeLoaderQuerier) UpdateConnectionTokens(
	_ context.Context, arg generated.UpdateConnectionTokensParams,
) error {
	if f.updateFn != nil {
		return f.updateFn(arg)
	}
	return nil
}

// stubRefreshProvider extends stubProvider with a customisable Refresh.
type stubRefreshProvider struct {
	stubProvider
	refreshFn func(ctx context.Context, refreshToken []byte) (*TokenSet, error)
}

func (s *stubRefreshProvider) Refresh(ctx context.Context, refreshToken []byte) (*TokenSet, error) {
	if s.refreshFn != nil {
		return s.refreshFn(ctx, refreshToken)
	}
	return nil, ErrRefreshNotSupported
}
