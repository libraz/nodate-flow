package integrations

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/packages/go-shared/crypto"
)

func newRefresherCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.New(testCipherKey)
	require.NoError(t, err)
	return c
}

// --- RefreshOnce ---

func TestRefreshOnce_NoExpiringRows_IsNoop(t *testing.T) {
	t.Parallel()
	q := &fakeRefresherQuerier{}
	r := &Refresher{
		Queries:  q,
		Cipher:   newRefresherCipher(t),
		Registry: NewRegistry(),
		LeadTime: 15 * time.Minute,
	}
	err := r.RefreshOnce(context.Background())
	require.NoError(t, err)
	assert.True(t, q.listCalled, "must query for expiring rows even if none exist")
}

func TestRefreshOnce_RefreshesExpiringToken(t *testing.T) {
	t.Parallel()
	c := newRefresherCipher(t)
	refreshCT, _ := c.Encrypt([]byte("stored-refresh"))
	newExpiry := time.Now().Add(time.Hour).Truncate(time.Second)

	var updated generated.UpdateConnectionTokensParams
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "google_calendar"},
			refreshFn: func(_ context.Context, rt []byte) (*TokenSet, error) {
				assert.Equal(t, "stored-refresh", string(rt))
				return &TokenSet{
					AccessToken: "refreshed-access",
					ExpiresAt:   newExpiry,
				}, nil
			},
		}, nil
	})
	q := &fakeRefresherQuerier{
		rows: []generated.ListConnectionsExpiringBeforeRow{{
			ID:                     42,
			UserID:                 1,
			Provider:               "google_calendar",
			AccessTokenCiphertext:  nil, // not used by refresher
			RefreshTokenCiphertext: sql.NullString{String: string(refreshCT), Valid: true},
			AccessTokenExpiresAt:   sql.NullTime{Time: time.Now().Add(5 * time.Minute), Valid: true},
		}},
		updateFn: func(params generated.UpdateConnectionTokensParams) error {
			updated = params
			return nil
		},
	}
	r := &Refresher{Queries: q, Cipher: c, Registry: reg, LeadTime: 15 * time.Minute}
	err := r.RefreshOnce(context.Background())
	require.NoError(t, err)

	assert.Equal(t, uint32(42), updated.ID)
	decrypted, err := c.Decrypt(updated.AccessTokenCiphertext)
	require.NoError(t, err)
	assert.Equal(t, "refreshed-access", string(decrypted),
		"must persist the new access token encrypted")
}

// TestRefreshOnce_WipesThePlaintextHandedToRefresh is why Provider.Refresh
// takes a []byte: the slice it receives is the decrypted refresh token, and
// the refresher wipes it as refreshRow returns. A string parameter could
// not hold this — nothing can overwrite an immutable copy.
func TestRefreshOnce_WipesThePlaintextHandedToRefresh(t *testing.T) {
	t.Parallel()
	c := newRefresherCipher(t)
	refreshCT, _ := c.Encrypt([]byte("stored-refresh"))

	// Held deliberately, which is what the doc tells providers not to do.
	// It is the only way to look at the buffer after the wipe.
	var held []byte
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "google_calendar"},
			refreshFn: func(_ context.Context, rt []byte) (*TokenSet, error) {
				require.Equal(t, "stored-refresh", string(rt),
					"the provider must see the plaintext while it runs")
				held = rt
				return &TokenSet{
					AccessToken: "refreshed-access",
					ExpiresAt:   time.Now().Add(time.Hour),
				}, nil
			},
		}, nil
	})
	q := &fakeRefresherQuerier{
		rows: []generated.ListConnectionsExpiringBeforeRow{{
			ID:                     42,
			UserID:                 1,
			Provider:               "google_calendar",
			RefreshTokenCiphertext: sql.NullString{String: string(refreshCT), Valid: true},
			AccessTokenExpiresAt:   sql.NullTime{Time: time.Now().Add(5 * time.Minute), Valid: true},
		}},
	}
	r := &Refresher{Queries: q, Cipher: c, Registry: reg, LeadTime: 15 * time.Minute}
	require.NoError(t, r.RefreshOnce(context.Background()))

	require.NotEmpty(t, held, "the provider must have been handed a token to hold")
	assert.Equal(t, make([]byte, len(held)), held,
		"the refresh token plaintext is still readable after the refresh returned")
}

// TestRefreshOnce_WipesThePlaintextWhenRefreshFails holds the same
// property on the error path, which is the one where a plaintext is most
// likely to be forgotten.
func TestRefreshOnce_WipesThePlaintextWhenRefreshFails(t *testing.T) {
	t.Parallel()
	c := newRefresherCipher(t)
	refreshCT, _ := c.Encrypt([]byte("stored-refresh"))

	var held []byte
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "google_calendar"},
			refreshFn: func(_ context.Context, rt []byte) (*TokenSet, error) {
				held = rt
				return nil, errors.New("provider down")
			},
		}, nil
	})
	q := &fakeRefresherQuerier{
		rows: []generated.ListConnectionsExpiringBeforeRow{{
			ID:                     42,
			UserID:                 1,
			Provider:               "google_calendar",
			RefreshTokenCiphertext: sql.NullString{String: string(refreshCT), Valid: true},
			AccessTokenExpiresAt:   sql.NullTime{Time: time.Now().Add(5 * time.Minute), Valid: true},
		}},
		updateFn: func(generated.UpdateConnectionTokensParams) error {
			t.Fatal("a failed refresh must not write tokens back")
			return nil
		},
	}
	r := &Refresher{Queries: q, Cipher: c, Registry: reg, LeadTime: 15 * time.Minute}
	require.NoError(t, r.RefreshOnce(context.Background()),
		"one failing connection must not fail the pass")

	require.NotEmpty(t, held)
	assert.Equal(t, make([]byte, len(held)), held,
		"the plaintext survived a provider refresh that returned an error")
}

func TestRefreshOnce_SkipsRowWithoutRefreshToken(t *testing.T) {
	t.Parallel()
	c := newRefresherCipher(t)
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "github"},
			refreshFn: func(_ context.Context, _ []byte) (*TokenSet, error) {
				t.Fatal("must not call Refresh for row without refresh token")
				return nil, nil
			},
		}, nil
	})
	q := &fakeRefresherQuerier{
		rows: []generated.ListConnectionsExpiringBeforeRow{{
			ID:                     1,
			UserID:                 1,
			Provider:               "github",
			RefreshTokenCiphertext: sql.NullString{Valid: false}, // no refresh token
		}},
	}
	r := &Refresher{Queries: q, Cipher: c, Registry: reg}
	err := r.RefreshOnce(context.Background())
	require.NoError(t, err)
}

func TestRefreshOnce_SkipsProviderReturningErrRefreshNotSupported(t *testing.T) {
	t.Parallel()
	c := newRefresherCipher(t)
	refreshCT, _ := c.Encrypt([]byte("rt"))
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "github"},
			refreshFn: func(_ context.Context, _ []byte) (*TokenSet, error) {
				return nil, ErrRefreshNotSupported
			},
		}, nil
	})
	q := &fakeRefresherQuerier{
		rows: []generated.ListConnectionsExpiringBeforeRow{{
			ID:                     1,
			UserID:                 1,
			Provider:               "github",
			RefreshTokenCiphertext: sql.NullString{String: string(refreshCT), Valid: true},
		}},
		updateFn: func(_ generated.UpdateConnectionTokensParams) error {
			t.Fatal("must not persist when refresh is unsupported")
			return nil
		},
	}
	r := &Refresher{Queries: q, Cipher: c, Registry: reg}
	err := r.RefreshOnce(context.Background())
	require.NoError(t, err,
		"ErrRefreshNotSupported must be a silent skip, not a pass-level error")
}

func TestRefreshOnce_OneRowFailure_DoesNotBlockOthers(t *testing.T) {
	t.Parallel()
	c := newRefresherCipher(t)
	badCT := []byte("not-valid-ciphertext")
	goodRefreshCT, _ := c.Encrypt([]byte("good-refresh"))

	var updatedIDs []uint32
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "google_calendar"},
			refreshFn: func(_ context.Context, rt []byte) (*TokenSet, error) {
				return &TokenSet{
					AccessToken: "new-" + string(rt),
					ExpiresAt:   time.Now().Add(time.Hour),
				}, nil
			},
		}, nil
	})
	q := &fakeRefresherQuerier{
		rows: []generated.ListConnectionsExpiringBeforeRow{
			{
				ID:                     10,
				UserID:                 1,
				Provider:               "google_calendar",
				RefreshTokenCiphertext: sql.NullString{String: string(badCT), Valid: true},
			},
			{
				ID:                     20,
				UserID:                 2,
				Provider:               "google_calendar",
				RefreshTokenCiphertext: sql.NullString{String: string(goodRefreshCT), Valid: true},
			},
		},
		updateFn: func(params generated.UpdateConnectionTokensParams) error {
			updatedIDs = append(updatedIDs, params.ID)
			return nil
		},
	}
	r := &Refresher{Queries: q, Cipher: c, Registry: reg}
	err := r.RefreshOnce(context.Background())
	require.NoError(t, err, "individual row failures must not crash the pass")
	assert.Equal(t, []uint32{20}, updatedIDs,
		"only the healthy row must be refreshed; the bad row must be skipped")
}

func TestRefreshOnce_UnavailableProvider_SkipsRow(t *testing.T) {
	t.Parallel()
	c := newRefresherCipher(t)
	refreshCT, _ := c.Encrypt([]byte("rt"))
	reg := NewRegistry() // empty — no providers configured

	q := &fakeRefresherQuerier{
		rows: []generated.ListConnectionsExpiringBeforeRow{{
			ID:                     1,
			UserID:                 1,
			Provider:               "google_calendar",
			RefreshTokenCiphertext: sql.NullString{String: string(refreshCT), Valid: true},
		}},
		updateFn: func(_ generated.UpdateConnectionTokensParams) error {
			t.Fatal("must not persist when provider is unavailable")
			return nil
		},
	}
	r := &Refresher{Queries: q, Cipher: c, Registry: reg}
	err := r.RefreshOnce(context.Background())
	require.NoError(t, err)
}

func TestRefreshOnce_PreservesRefreshTokenWhenNotRotated(t *testing.T) {
	t.Parallel()
	c := newRefresherCipher(t)
	originalRefreshCT, _ := c.Encrypt([]byte("original-rt"))

	var updated generated.UpdateConnectionTokensParams
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "google_calendar"},
			refreshFn: func(_ context.Context, _ []byte) (*TokenSet, error) {
				return &TokenSet{
					AccessToken: "new-access",
					// RefreshToken empty or same as input → no rotation.
					ExpiresAt: time.Now().Add(time.Hour),
				}, nil
			},
		}, nil
	})
	q := &fakeRefresherQuerier{
		rows: []generated.ListConnectionsExpiringBeforeRow{{
			ID:                     1,
			UserID:                 1,
			Provider:               "google_calendar",
			RefreshTokenCiphertext: sql.NullString{String: string(originalRefreshCT), Valid: true},
		}},
		updateFn: func(params generated.UpdateConnectionTokensParams) error {
			updated = params
			return nil
		},
	}
	r := &Refresher{Queries: q, Cipher: c, Registry: reg}
	err := r.RefreshOnce(context.Background())
	require.NoError(t, err)

	assert.Equal(t, string(originalRefreshCT), updated.RefreshTokenCiphertext.String,
		"when provider does not rotate, the original ciphertext must be preserved verbatim")
}

func TestRefreshOnce_RotatesRefreshTokenWhenProviderReturnsNew(t *testing.T) {
	t.Parallel()
	c := newRefresherCipher(t)
	originalRefreshCT, _ := c.Encrypt([]byte("old-rt"))

	var updated generated.UpdateConnectionTokensParams
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "google_calendar"},
			refreshFn: func(_ context.Context, _ []byte) (*TokenSet, error) {
				return &TokenSet{
					AccessToken:  "new-access",
					RefreshToken: "rotated-rt",
					ExpiresAt:    time.Now().Add(time.Hour),
				}, nil
			},
		}, nil
	})
	q := &fakeRefresherQuerier{
		rows: []generated.ListConnectionsExpiringBeforeRow{{
			ID:                     1,
			UserID:                 1,
			Provider:               "google_calendar",
			RefreshTokenCiphertext: sql.NullString{String: string(originalRefreshCT), Valid: true},
		}},
		updateFn: func(params generated.UpdateConnectionTokensParams) error {
			updated = params
			return nil
		},
	}
	r := &Refresher{Queries: q, Cipher: c, Registry: reg}
	err := r.RefreshOnce(context.Background())
	require.NoError(t, err)

	require.True(t, updated.RefreshTokenCiphertext.Valid)
	decrypted, err := c.Decrypt([]byte(updated.RefreshTokenCiphertext.String))
	require.NoError(t, err)
	assert.Equal(t, "rotated-rt", string(decrypted),
		"rotated refresh token must be encrypted and persisted")
}

func TestRefreshOnce_RespectsContextCancellation(t *testing.T) {
	t.Parallel()
	c := newRefresherCipher(t)
	refreshCT, _ := c.Encrypt([]byte("rt"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	q := &fakeRefresherQuerier{
		rows: []generated.ListConnectionsExpiringBeforeRow{{
			ID:                     1,
			UserID:                 1,
			Provider:               "google_calendar",
			RefreshTokenCiphertext: sql.NullString{String: string(refreshCT), Valid: true},
		}},
		updateFn: func(_ generated.UpdateConnectionTokensParams) error {
			t.Fatal("must not persist when context is cancelled")
			return nil
		},
	}
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{stubProvider: stubProvider{name: "google_calendar"}}, nil
	})
	r := &Refresher{Queries: q, Cipher: c, Registry: reg}
	err := r.RefreshOnce(ctx)
	require.ErrorIs(t, err, context.Canceled,
		"must honour context cancellation between row iterations")
}

func TestRefreshOnce_DBListError_Propagates(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("connection reset")
	q := &fakeRefresherQuerier{listErr: dbErr}
	r := &Refresher{
		Queries:  q,
		Cipher:   newRefresherCipher(t),
		Registry: NewRegistry(),
	}
	err := r.RefreshOnce(context.Background())
	require.ErrorIs(t, err, dbErr,
		"DB errors from listing must propagate so the caller can log them")
}

func TestRefreshOnce_EmptyRefreshResult_SkipsUpdate(t *testing.T) {
	t.Parallel()
	c := newRefresherCipher(t)
	refreshCT, _ := c.Encrypt([]byte("rt"))
	reg := NewRegistry(func() (Provider, error) {
		return &stubRefreshProvider{
			stubProvider: stubProvider{name: "google_calendar"},
			refreshFn: func(_ context.Context, _ []byte) (*TokenSet, error) {
				return &TokenSet{AccessToken: ""}, nil // empty result
			},
		}, nil
	})
	q := &fakeRefresherQuerier{
		rows: []generated.ListConnectionsExpiringBeforeRow{{
			ID:                     1,
			UserID:                 1,
			Provider:               "google_calendar",
			RefreshTokenCiphertext: sql.NullString{String: string(refreshCT), Valid: true},
		}},
		updateFn: func(_ generated.UpdateConnectionTokensParams) error {
			t.Fatal("must not persist an empty access token")
			return nil
		},
	}
	r := &Refresher{Queries: q, Cipher: c, Registry: reg}
	err := r.RefreshOnce(context.Background())
	require.NoError(t, err)
}

// --- Run lifecycle ---

func TestRun_StopsOnContextCancel(t *testing.T) {
	t.Parallel()
	q := &fakeRefresherQuerier{}
	r := &Refresher{
		Queries:  q,
		Cipher:   newRefresherCipher(t),
		Registry: NewRegistry(),
		Interval: 50 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()
	// Let at least one tick fire.
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after context cancellation")
	}
	assert.True(t, q.listCalled, "Run must execute at least one pass")
}

func TestRefresher_DefaultsAreReasonable(t *testing.T) {
	t.Parallel()
	r := &Refresher{}
	assert.Equal(t, 15*time.Minute, r.leadTime(),
		"default lead time must be 15 minutes to preempt expiring tokens")
}

// --- test doubles ---

type fakeRefresherQuerier struct {
	rows       []generated.ListConnectionsExpiringBeforeRow
	listErr    error
	listCalled bool
	updateFn   func(generated.UpdateConnectionTokensParams) error
	// claimFn overrides the claim outcome. Nil means every claim
	// succeeds, which is what a single refresher in a single process
	// sees and what these unit tests are about; the contention the claim
	// exists for is covered against a real database in
	// refresher_claim_concurrency_test.go.
	claimFn     func(generated.ClaimConnectionForRefreshParams) (int64, error)
	claimParams []generated.ClaimConnectionForRefreshParams
}

func (f *fakeRefresherQuerier) ListConnectionsExpiringBefore(
	_ context.Context, _ sql.NullTime,
) ([]generated.ListConnectionsExpiringBeforeRow, error) {
	f.listCalled = true
	return f.rows, f.listErr
}

func (f *fakeRefresherQuerier) ClaimConnectionForRefresh(
	_ context.Context, arg generated.ClaimConnectionForRefreshParams,
) (int64, error) {
	f.claimParams = append(f.claimParams, arg)
	if f.claimFn != nil {
		return f.claimFn(arg)
	}
	return 1, nil
}

func (f *fakeRefresherQuerier) UpdateConnectionTokens(
	_ context.Context, arg generated.UpdateConnectionTokensParams,
) error {
	if f.updateFn != nil {
		return f.updateFn(arg)
	}
	return nil
}
