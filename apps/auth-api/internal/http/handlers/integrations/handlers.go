package integrations

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
	integrationspkg "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/integrations"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
)

// oauthStateTTL is how long a state row remains valid. Short enough
// to be secure, long enough for a user to finish the consent screen.
const oauthStateTTL = 15 * time.Minute

// supportedProviders is the canonical ordered list rendered by the
// catalog endpoint. Keeping this stable lets the UI place the three
// cards in a deterministic left-to-right order.
var supportedProviders = []string{"github", "slack", "google_calendar"}

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// List handles GET /me/integrations. It merges the three supported
// providers with the user's existing rows so the UI can render all
// cards regardless of which ones are connected.
func List(deps Deps) func(context.Context, *struct{}) (*ListIntegrationsOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*ListIntegrationsOutput, error) {
		uid, ok := authn.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		rows, err := deps.Queries.ListUserIntegrations(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		existing := map[string]*ConnectionSummary{}
		for _, r := range rows {
			c := rowToConnectionSummary(r)
			existing[string(r.Provider)] = &c
		}
		out := &ListIntegrationsOutput{}
		out.Body.Providers = make([]ProviderStatus, 0, len(supportedProviders))
		for _, p := range supportedProviders {
			st := ProviderStatus{
				Provider:   p,
				Configured: deps.Registry.Has(p),
			}
			if c, ok := existing[p]; ok {
				st.Connection = c
			}
			out.Body.Providers = append(out.Body.Providers, st)
		}
		return out, nil
	}
}

// Connect handles POST /me/integrations/{provider}/connect. It
// creates a CSRF state row and returns the provider authorize URL
// the client must navigate to. The callback handler will complete
// the flow.
func Connect(deps Deps) func(context.Context, *ConnectIntegrationInput) (*ConnectIntegrationOutput, error) {
	return func(ctx context.Context, in *ConnectIntegrationInput) (*ConnectIntegrationOutput, error) {
		uid, ok := authn.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		if !isSupportedProvider(in.Provider) {
			return nil, httpErr(apierrors.IntegrationOauthProviderUnsupported)
		}
		provider, err := deps.Registry.Get(in.Provider)
		if err != nil {
			return nil, httpErr(apierrors.IntegrationOauthProviderNotConfigured)
		}
		if deps.Cipher == nil {
			return nil, httpErr(apierrors.IntegrationOauthProviderNotConfigured)
		}
		state, err := randomState()
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		var redirectTo sql.NullString
		if in.Body.RedirectTo != "" {
			redirectTo = sql.NullString{String: in.Body.RedirectTo, Valid: true}
		}
		if err := deps.Queries.CreateOauthState(ctx, generated.CreateOauthStateParams{
			State:      state,
			UserID:     uid,
			Provider:   generated.OauthStatesProvider(in.Provider),
			RedirectTo: redirectTo,
			ExpiresAt:  time.Now().Add(oauthStateTTL),
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		authURL := provider.AuthURL(state, callbackURL(deps, in.Provider))
		out := &ConnectIntegrationOutput{}
		out.Body.AuthorizeURL = authURL
		return out, nil
	}
}

// Callback handles GET /oauth/callback/{provider}. It is the only
// endpoint in this package that is NOT behind the auth middleware —
// the user arrives here from the provider and carries no bearer
// token. The state row proves who started the flow.
func Callback(deps Deps) func(context.Context, *OAuthCallbackInput) (*OAuthCallbackOutput, error) {
	return func(ctx context.Context, in *OAuthCallbackInput) (*OAuthCallbackOutput, error) {
		if in.Error != "" || in.Code == "" || in.State == "" {
			return redirectWithError(deps, "oauth_denied"), nil
		}
		if !isSupportedProvider(in.Provider) {
			return nil, httpErr(apierrors.IntegrationOauthProviderUnsupported)
		}
		provider, err := deps.Registry.Get(in.Provider)
		if err != nil {
			return nil, httpErr(apierrors.IntegrationOauthProviderNotConfigured)
		}
		if deps.Cipher == nil {
			return nil, httpErr(apierrors.IntegrationOauthProviderNotConfigured)
		}

		// Opportunistic GC before lookup.
		_ = deps.Queries.PurgeExpiredOauthStates(ctx)

		row, err := deps.Queries.ConsumeOauthState(ctx, in.State)
		if err != nil {
			return redirectWithError(deps, "state_invalid"), nil
		}
		_ = deps.Queries.DeleteOauthState(ctx, in.State)
		if row.ExpiresAt.Before(time.Now()) {
			return redirectWithError(deps, "state_expired"), nil
		}
		if string(row.Provider) != in.Provider {
			return redirectWithError(deps, "state_provider_mismatch"), nil
		}

		tokens, acc, err := provider.Exchange(ctx, in.Code, callbackURL(deps, in.Provider))
		if err != nil {
			return redirectWithError(deps, "exchange_failed"), nil
		}

		accessBlob, err := deps.Cipher.Encrypt([]byte(tokens.AccessToken))
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		var refreshBlob []byte
		if tokens.RefreshToken != "" {
			refreshBlob, err = deps.Cipher.Encrypt([]byte(tokens.RefreshToken))
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		}
		var expiresAt sql.NullTime
		if !tokens.ExpiresAt.IsZero() {
			expiresAt = sql.NullTime{Time: tokens.ExpiresAt, Valid: true}
		}
		if _, err := deps.Queries.UpsertUserIntegration(ctx, generated.UpsertUserIntegrationParams{
			PublicID:               types.New(),
			UserID:                 row.UserID,
			Provider:               generated.UserIntegrationsProvider(in.Provider),
			ExternalAccountID:      acc.ExternalID,
			ExternalAccountLabel:   acc.Label,
			Scopes:                 strings.Join(tokens.Scopes, " "),
			AccessTokenCiphertext:  accessBlob,
			RefreshTokenCiphertext: sql.NullString{String: string(refreshBlob), Valid: len(refreshBlob) > 0},
			AccessTokenExpiresAt:   expiresAt,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return redirectOnSuccess(deps, in.Provider, row.RedirectTo), nil
	}
}

// Disconnect handles DELETE /me/integrations/{id}. It best-effort
// revokes the tokens at the provider, then removes the local row.
// A provider revoke failure is logged but does not block deletion:
// a user-initiated disconnect must always succeed locally.
func Disconnect(deps Deps) func(context.Context, *DisconnectInput) (*DisconnectOutput, error) {
	return func(ctx context.Context, in *DisconnectInput) (*DisconnectOutput, error) {
		uid, ok := authn.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		pub, err := types.Parse(in.ID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		row, err := deps.Queries.FindUserIntegrationByPublicId(ctx, generated.FindUserIntegrationByPublicIdParams{
			PublicID: pub,
			UserID:   uid,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.IntegrationConnectionNotFound, apierrors.InternalUnexpected))
		}

		// Best-effort provider-side revoke before local deletion.
		revokeAtProvider(ctx, deps, uid, row.PublicID, row.Provider)

		if err := deps.Queries.DeleteUserIntegration(ctx, generated.DeleteUserIntegrationParams{
			ID:     row.ID,
			UserID: uid,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &DisconnectOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

/* -------- helpers -------- */

func rowToConnectionSummary(r generated.ListUserIntegrationsRow) ConnectionSummary {
	c := ConnectionSummary{
		ID:                   r.PublicID.String(),
		Provider:             string(r.Provider),
		ExternalAccountID:    r.ExternalAccountID,
		ExternalAccountLabel: r.ExternalAccountLabel,
		Scopes:               r.Scopes,
		ConnectedAt:          r.ConnectedAt.Unix(),
	}
	c.LastRefreshedAt = dbtype.UnixSecondsFromNullTime(r.LastRefreshedAt)
	c.AccessTokenExpiresAt = dbtype.UnixSecondsFromNullTime(r.AccessTokenExpiresAt)
	return c
}

func isSupportedProvider(name string) bool {
	for _, p := range supportedProviders {
		if p == name {
			return true
		}
	}
	return false
}

func randomState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func callbackURL(deps Deps, provider string) string {
	return strings.TrimRight(deps.PublicBaseURL, "/") + "/oauth/callback/" + provider
}

func redirectWithError(deps Deps, reason string) *OAuthCallbackOutput {
	q := url.Values{}
	q.Set("integration_error", reason)
	return &OAuthCallbackOutput{
		Status:   http.StatusFound,
		Location: strings.TrimRight(deps.WebBaseURL, "/") + "/settings/security?" + q.Encode(),
	}
}

// revokeAtProvider best-effort invalidates the stored tokens at the
// remote provider. All failures are logged at WARN and swallowed so
// the caller can proceed with local row deletion.
func revokeAtProvider(ctx context.Context, deps Deps, uid uint32, pubID types.PublicID, provider generated.UserIntegrationsProvider) {
	providerName := string(provider)
	p, err := deps.Registry.Get(providerName)
	if err != nil {
		// Provider not configured on this server: nothing to revoke.
		if errors.Is(err, integrationspkg.ErrNotConfigured) {
			return
		}
		slog.WarnContext(ctx, "integrations: revoke provider lookup failed", "provider", providerName, "connection", pubID.String(), "error", err)
		return
	}
	if deps.Cipher == nil {
		slog.WarnContext(ctx, "integrations: revoke skipped: cipher unavailable", "provider", providerName, "connection", pubID.String())
		return
	}
	tokRow, err := deps.Queries.FindUserIntegrationByUserProvider(ctx, generated.FindUserIntegrationByUserProviderParams{
		UserID:   uid,
		Provider: provider,
	})
	if err != nil {
		slog.WarnContext(ctx, "integrations: revoke token lookup failed", "provider", providerName, "connection", pubID.String(), "error", err)
		return
	}
	var tokens integrationspkg.TokenSet
	if len(tokRow.AccessTokenCiphertext) > 0 {
		plain, err := deps.Cipher.Decrypt(tokRow.AccessTokenCiphertext)
		if err != nil {
			slog.WarnContext(ctx, "integrations: revoke access-token decrypt failed", "provider", providerName, "connection", pubID.String(), "error", err)
			return
		}
		tokens.AccessToken = string(plain)
	}
	if tokRow.RefreshTokenCiphertext.Valid && len(tokRow.RefreshTokenCiphertext.String) > 0 {
		plain, err := deps.Cipher.Decrypt([]byte(tokRow.RefreshTokenCiphertext.String))
		if err != nil {
			slog.WarnContext(ctx, "integrations: revoke refresh-token decrypt failed", "provider", providerName, "connection", pubID.String(), "error", err)
			return
		}
		tokens.RefreshToken = string(plain)
	}
	if err := p.Revoke(ctx, tokens); err != nil {
		slog.WarnContext(ctx, "integrations: provider revoke failed", "provider", providerName, "connection", pubID.String(), "error", err)
	}
}

func redirectOnSuccess(deps Deps, provider string, clientRedirect sql.NullString) *OAuthCallbackOutput {
	target := strings.TrimRight(deps.WebBaseURL, "/") + "/settings/integrations"
	if clientRedirect.Valid && clientRedirect.String != "" {
		target = clientRedirect.String
	}
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	return &OAuthCallbackOutput{
		Status:   http.StatusFound,
		Location: target + sep + "connected=" + provider,
	}
}
