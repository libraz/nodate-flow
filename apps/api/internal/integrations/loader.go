package integrations

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/crypto"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
)

// ErrNotConnected is returned by LoadUserTokenSet when the user has
// no enabled user_integrations row for the requested provider, or
// when the stored ciphertext is empty. Callers should treat this as
// "user has not linked this account yet" and surface a connect CTA
// rather than a 500.
var ErrNotConnected = errors.New("integrations: user not connected")

// ErrTokenExpired is returned by LoadUserTokenSet when the stored
// access token is past its expiry AND a live refresh was not
// possible (refresh unsupported, missing refresh token, or a
// refresh attempt failed hard). Callers can map this to a
// re-auth prompt.
var ErrTokenExpired = errors.New("integrations: access token expired")

// LoaderQuerier is the narrow sqlc surface LoadUserTokenSet needs.
// Declared as an interface so tests can substitute a fake without
// standing up a real database.
type LoaderQuerier interface {
	FindUserIntegrationByUserProvider(ctx context.Context, arg generated.FindUserIntegrationByUserProviderParams) (generated.FindUserIntegrationByUserProviderRow, error)
	UpdateConnectionTokens(ctx context.Context, arg generated.UpdateConnectionTokensParams) error
}

// LoadUserTokenSet resolves the decrypted OAuth credentials for a
// given (userID, provider) pair so the @mcp and @ai layers can make
// provider-specific API calls on behalf of the user.
//
// Intended usage: handlers / tools in internal/mcp and internal/ai
// that need to talk to GitHub, Slack, or Google Calendar on behalf
// of the current user pull the userID from middleware.ActorFromContext,
// call this helper with a *crypto.Cipher + sqlc Queries + the
// optional *Registry, and then build a provider-specific HTTP
// client from the returned TokenSet. The helper centralises three
// concerns the caller should not have to re-implement:
//
//  1. Row lookup + ErrNotConnected mapping.
//  2. Decryption of access/refresh ciphertext.
//  3. Best-effort "just-in-time" refresh when the background
//     Refresher has not yet caught up (e.g. a foreground request
//     arrives seconds after the token actually expired).
//
// The registry argument is optional: pass nil in call sites that
// never need JIT refresh (e.g. pure GitHub, where Refresh is not
// supported anyway). When registry is non-nil and the stored token
// is expired, LoadUserTokenSet will attempt one synchronous refresh
// via the Provider and, on success, persist the new ciphertext
// before returning. Refresh failures are logged and downgraded:
// the caller gets the stale token and will fail naturally against
// the provider, which keeps this helper best-effort rather than
// strictly fatal.
func LoadUserTokenSet(
	ctx context.Context,
	q LoaderQuerier,
	cipher *crypto.Cipher,
	registry *Registry,
	userID uint32,
	provider string,
) (TokenSet, error) {
	row, err := q.FindUserIntegrationByUserProvider(ctx, generated.FindUserIntegrationByUserProviderParams{
		UserID:   userID,
		Provider: generated.UserIntegrationsProvider(provider),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TokenSet{}, ErrNotConnected
		}
		return TokenSet{}, err
	}
	if len(row.AccessTokenCiphertext) == 0 {
		return TokenSet{}, ErrNotConnected
	}

	accessPlain, err := cipher.Decrypt(row.AccessTokenCiphertext)
	if err != nil {
		return TokenSet{}, err
	}
	var refreshPlain []byte
	if len(row.RefreshTokenCiphertext) > 0 {
		refreshPlain, err = cipher.Decrypt(row.RefreshTokenCiphertext)
		if err != nil {
			return TokenSet{}, err
		}
	}

	ts := TokenSet{
		AccessToken:  string(accessPlain),
		RefreshToken: string(refreshPlain),
	}
	if row.AccessTokenExpiresAt.Valid {
		ts.ExpiresAt = row.AccessTokenExpiresAt.Time
	}

	// If the token is still within its lifetime (or has no expiry
	// at all — GitHub/Slack user tokens), we are done.
	if ts.ExpiresAt.IsZero() || time.Now().Before(ts.ExpiresAt) {
		return ts, nil
	}

	// Expired. Try a one-shot refresh if we have the machinery.
	if registry == nil || len(refreshPlain) == 0 {
		return TokenSet{}, ErrTokenExpired
	}
	p, err := registry.Get(provider)
	if err != nil {
		return TokenSet{}, ErrTokenExpired
	}
	refreshed, refreshErr := p.Refresh(ctx, string(refreshPlain))
	if refreshErr != nil {
		if errors.Is(refreshErr, ErrRefreshNotSupported) {
			return TokenSet{}, ErrTokenExpired
		}
		// Best-effort: log and fall through with the stale token so
		// the caller can attempt the request and surface the natural
		// 401 if the provider actually rejects it.
		slog.Default().Warn("integrations: jit refresh failed",
			"provider", provider, "user_id", userID, "err", refreshErr)
		return ts, nil
	}
	if refreshed == nil || refreshed.AccessToken == "" {
		return ts, nil
	}

	// Persist the new tokens. Failure to persist is non-fatal: the
	// background refresher will pick the row up on its next pass.
	newAccessCipher, encErr := cipher.Encrypt([]byte(refreshed.AccessToken))
	if encErr != nil {
		slog.Default().Warn("integrations: jit refresh encrypt failed",
			"provider", provider, "user_id", userID, "err", encErr)
		return *refreshed, nil
	}
	newRefreshCipher := row.RefreshTokenCiphertext
	if refreshed.RefreshToken != "" && refreshed.RefreshToken != string(refreshPlain) {
		enc, encErr2 := cipher.Encrypt([]byte(refreshed.RefreshToken))
		if encErr2 != nil {
			slog.Default().Warn("integrations: jit refresh encrypt refresh failed",
				"provider", provider, "user_id", userID, "err", encErr2)
			return *refreshed, nil
		}
		newRefreshCipher = enc
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
	// Return the refreshed set regardless of persistence outcome —
	// the caller should use the fresh token for the current request.
	out := *refreshed
	if out.RefreshToken == "" {
		out.RefreshToken = string(refreshPlain)
	}
	return out, nil
}
