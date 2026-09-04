package integrations

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/packages/go-shared/crypto"
)

// ErrNotConnected is returned by WithUserTokens and WithStoredTokens
// when the user has no enabled user_integrations row for the requested
// provider, or when the stored ciphertext is empty. Callers should
// treat this as "user has not linked this account yet" and surface a
// connect CTA rather than a 500.
var ErrNotConnected = errors.New("integrations: user not connected")

// ErrTokenExpired is returned by WithUserTokens when the stored
// access token is past its expiry AND a live refresh was not
// possible (refresh unsupported, missing refresh token, or a
// refresh attempt failed hard). Callers can map this to a
// re-auth prompt.
var ErrTokenExpired = errors.New("integrations: access token expired")

// ConnectionQuerier is the read-only half of [LoaderQuerier]: the
// lookup WithStoredTokens needs and nothing more. Declared as an
// interface so tests can substitute a fake without standing up a real
// database.
type ConnectionQuerier interface {
	FindUserIntegrationByUserProvider(ctx context.Context, arg generated.FindUserIntegrationByUserProviderParams) (generated.FindUserIntegrationByUserProviderRow, error)
}

// LoaderQuerier is the narrow sqlc surface WithUserTokens needs. The
// write is for persisting a just-in-time refresh.
type LoaderQuerier interface {
	ConnectionQuerier
	UpdateConnectionTokens(ctx context.Context, arg generated.UpdateConnectionTokensParams) error
}

// Tokens is a borrowed view of one user's decrypted OAuth credentials.
//
// The slices belong to whichever helper decrypted them — [WithUserTokens]
// or [WithStoredTokens] — and are wiped as that helper returns. Build the
// outbound request inside the callback and let it go; a value copied out
// of here outlives the wipe and puts the plaintext back in the heap the
// wipe exists to clear. Keeping a secret in a []byte rather than a string
// is what makes wiping possible at all.
type Tokens struct {
	// AccessToken is the plaintext OAuth access token.
	AccessToken []byte
	// RefreshToken is the plaintext OAuth refresh token, empty when the
	// provider issued none.
	RefreshToken []byte
	// ExpiresAt is the access token's expiry, zero for the providers
	// that issue non-expiring user tokens (GitHub, Slack).
	ExpiresAt time.Time
}

// WithUserTokens resolves the decrypted OAuth credentials for a given
// (userID, provider) pair, hands them to use, and wipes them before it
// returns. The error from use is returned unchanged.
//
// Intended usage: handlers / tools in internal/mcp and internal/ai
// that need to talk to GitHub, Slack, or Google Calendar on behalf of
// the current user pull the userID from middleware.ActorFromContext and
// build the provider request inside the callback. The helper centralises
// four concerns the caller should not have to re-implement:
//
//  1. Row lookup + ErrNotConnected mapping.
//  2. Decryption of access/refresh ciphertext.
//  3. Best-effort "just-in-time" refresh when the background
//     Refresher has not yet caught up (e.g. a foreground request
//     arrives seconds after the token actually expired).
//  4. Wiping every plaintext it decrypted, on every path out.
//
// The callback shape is what bounds the plaintext's lifetime. Returning
// the tokens instead would leave them live in the caller's frame for as
// long as it runs, which is what [crypto.Cipher.Decrypt] tells callers
// not to do.
//
// The registry argument is optional: pass nil in call sites that
// never need JIT refresh (e.g. pure GitHub, where Refresh is not
// supported anyway). When registry is non-nil and the stored token
// is expired, WithUserTokens will attempt one synchronous refresh
// via the Provider and, on success, persist the new ciphertext
// before invoking the callback. Refresh failures are logged and
// downgraded: the callback gets the stale token and will fail naturally
// against the provider, which keeps this helper best-effort rather than
// strictly fatal.
func WithUserTokens(
	ctx context.Context,
	q LoaderQuerier,
	cipher *crypto.Cipher,
	registry *Registry,
	userID uint32,
	provider string,
	use func(context.Context, Tokens) error,
) error {
	row, err := findConnection(ctx, q, userID, provider)
	if err != nil {
		return err
	}
	return withRowTokens(ctx, cipher, row, func(ctx context.Context, stored Tokens) error {
		// If the token is still within its lifetime (or has no expiry
		// at all — GitHub/Slack user tokens), we are done.
		if stored.ExpiresAt.IsZero() || time.Now().Before(stored.ExpiresAt) {
			return use(ctx, stored)
		}

		// Expired. Try a one-shot refresh if we have the machinery.
		if registry == nil || len(stored.RefreshToken) == 0 {
			return ErrTokenExpired
		}
		p, err := registry.Get(provider)
		if err != nil {
			return ErrTokenExpired
		}
		refreshed, refreshErr := p.Refresh(ctx, stored.RefreshToken)
		if refreshErr != nil {
			if errors.Is(refreshErr, ErrRefreshNotSupported) {
				return ErrTokenExpired
			}
			// Best-effort: log and fall through with the stale token so
			// the caller can attempt the request and surface the natural
			// 401 if the provider actually rejects it.
			slog.Default().Warn("integrations: jit refresh failed",
				"provider", provider, "user_id", userID, "err", refreshErr)
			return use(ctx, stored)
		}
		if refreshed == nil || refreshed.AccessToken == "" {
			return use(ctx, stored)
		}

		fresh := Tokens{
			AccessToken:  []byte(refreshed.AccessToken),
			RefreshToken: stored.RefreshToken,
			ExpiresAt:    refreshed.ExpiresAt,
		}
		defer crypto.Zero(fresh.AccessToken)
		rotated := refreshed.RefreshToken != "" &&
			!bytes.Equal(stored.RefreshToken, []byte(refreshed.RefreshToken))
		if refreshed.RefreshToken != "" {
			fresh.RefreshToken = []byte(refreshed.RefreshToken)
			defer crypto.Zero(fresh.RefreshToken)
		}

		// Persist the new tokens. Failure to persist is non-fatal: the
		// background refresher will pick the row up on its next pass.
		newAccessCipher, encErr := cipher.Encrypt(fresh.AccessToken)
		if encErr != nil {
			slog.Default().Warn("integrations: jit refresh encrypt failed",
				"provider", provider, "user_id", userID, "err", encErr)
			return use(ctx, fresh)
		}
		newRefreshCipher := row.RefreshTokenCiphertext
		if rotated {
			enc, encErr2 := cipher.Encrypt(fresh.RefreshToken)
			if encErr2 != nil {
				slog.Default().Warn("integrations: jit refresh encrypt refresh failed",
					"provider", provider, "user_id", userID, "err", encErr2)
				return use(ctx, fresh)
			}
			newRefreshCipher = sql.NullString{String: string(enc), Valid: true}
		}
		var expires sql.NullTime
		if !refreshed.ExpiresAt.IsZero() {
			expires = sql.NullTime{Time: refreshed.ExpiresAt, Valid: true}
		}
		if updErr := q.UpdateConnectionTokens(ctx, generated.UpdateConnectionTokensParams{
			AccessTokenCiphertext:  newAccessCipher,
			RefreshTokenCiphertext: newRefreshCipher,
			AccessTokenExpiresAt:   expires,
			ID:                     row.ID,
		}); updErr != nil {
			slog.Default().Warn("integrations: jit refresh persist failed",
				"provider", provider, "user_id", userID, "err", updErr)
		}
		// Hand over the refreshed set regardless of persistence outcome —
		// the callback should use the fresh token for the current request.
		return use(ctx, fresh)
	})
}

// WithStoredTokens resolves the stored OAuth credentials for a given
// (userID, provider) pair, hands them to use, and wipes them before it
// returns — the same borrow contract as [WithUserTokens], without the
// expiry handling.
//
// Revocation is what needs that difference. A refresh token outlives
// the access tokens it mints, so a connection whose access token has
// already expired is exactly the one whose grant still has to be
// invalidated at the provider; WithUserTokens would answer
// ErrTokenExpired and never run the callback.
func WithStoredTokens(
	ctx context.Context,
	q ConnectionQuerier,
	cipher *crypto.Cipher,
	userID uint32,
	provider string,
	use func(context.Context, Tokens) error,
) error {
	row, err := findConnection(ctx, q, userID, provider)
	if err != nil {
		return err
	}
	return withRowTokens(ctx, cipher, row, use)
}

// findConnection resolves the user's row for one provider, mapping a
// missing row or empty ciphertext to ErrNotConnected.
func findConnection(
	ctx context.Context,
	q ConnectionQuerier,
	userID uint32,
	provider string,
) (generated.FindUserIntegrationByUserProviderRow, error) {
	row, err := q.FindUserIntegrationByUserProvider(ctx, generated.FindUserIntegrationByUserProviderParams{
		UserID:   userID,
		Provider: generated.UserIntegrationsProvider(provider),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return row, ErrNotConnected
		}
		return row, err
	}
	if len(row.AccessTokenCiphertext) == 0 {
		return row, ErrNotConnected
	}
	return row, nil
}

// withRowTokens decrypts a connection row into borrowed views, hands
// them to use, and wipes them on every path out. Both exported entry
// points go through here so there is one place that owns the wipe.
func withRowTokens(
	ctx context.Context,
	cipher *crypto.Cipher,
	row generated.FindUserIntegrationByUserProviderRow,
	use func(context.Context, Tokens) error,
) error {
	accessPlain, err := cipher.Decrypt(row.AccessTokenCiphertext)
	if err != nil {
		return err
	}
	defer crypto.Zero(accessPlain)
	var refreshPlain []byte
	if row.RefreshTokenCiphertext.Valid && len(row.RefreshTokenCiphertext.String) > 0 {
		refreshPlain, err = cipher.Decrypt([]byte(row.RefreshTokenCiphertext.String))
		if err != nil {
			return err
		}
		defer crypto.Zero(refreshPlain)
	}
	var expiresAt time.Time
	if row.AccessTokenExpiresAt.Valid {
		expiresAt = row.AccessTokenExpiresAt.Time
	}
	return use(ctx, Tokens{
		AccessToken:  accessPlain,
		RefreshToken: refreshPlain,
		ExpiresAt:    expiresAt,
	})
}
