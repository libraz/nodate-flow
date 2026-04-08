// Package integrations implements the personal OAuth flow for
// per-user GitHub / Slack / Google Calendar connections used by
// /settings/integrations. Workspace-level credentials managed by
// @mcp are a separate concern and do not share this package.
//
// The Provider interface is narrow on purpose: each implementation
// only needs to build the provider-specific authorize URL, run the
// code→token exchange, and fetch the account profile. Token
// refresh is optional — GitHub OAuth apps do not issue refresh
// tokens at all.
package integrations

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// TokenSet is the normalised result of a token exchange or refresh.
// Providers that do not return an expiry leave ExpiresAt zero.
type TokenSet struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Scopes       []string
}

// Account is the minimal identity profile fetched after the token
// exchange so the user can see which account they just connected.
type Account struct {
	// ExternalID is the provider-stable subject (GH numeric id as
	// string, Slack user id, Google sub).
	ExternalID string
	// Label is the human-readable display form (email, @handle, name).
	Label string
}

// Provider is the per-service OAuth driver.
type Provider interface {
	// Name returns the canonical provider identifier stored in the
	// user_integrations.provider column.
	Name() string
	// AuthURL builds the "send the user here" URL for the consent
	// screen. Scope selection is hard-coded inside each impl so the
	// caller does not need to know which scopes this app requires.
	AuthURL(state, redirectURI string) string
	// Exchange swaps the ?code=... returned by the callback handler
	// for an access token + account profile.
	Exchange(ctx context.Context, code, redirectURI string) (*TokenSet, *Account, error)
	// Refresh exchanges a stored refresh token for a fresh access
	// token. Providers that do not issue refresh tokens (GitHub OAuth
	// Apps) or whose user tokens are long-lived (Slack) return
	// ErrRefreshNotSupported and the refresher skips them.
	Refresh(ctx context.Context, refreshToken string) (*TokenSet, error)
	// Revoke asks the provider to invalidate the given tokens. It is
	// best-effort: "already gone" responses are treated as success.
	// Called on user-initiated disconnect.
	Revoke(ctx context.Context, tokens TokenSet) error
}

// ErrNotConfigured is returned by a Provider constructor when the
// required client id or secret is empty. The handler layer maps it
// to INTEGRATION.OAUTH.PROVIDER_NOT_CONFIGURED.
var ErrNotConfigured = errors.New("integrations: provider not configured")

// ErrRefreshNotSupported is returned by Provider.Refresh for
// providers that do not support (or do not need) a refresh token
// flow. Callers treat this as a soft skip rather than an error.
var ErrRefreshNotSupported = errors.New("integrations: refresh not supported")

// Registry owns the concrete Provider instances and resolves them
// by name. Providers that are not configured (missing env vars)
// are intentionally absent so Get returns a clean error.
type Registry struct {
	providers map[string]Provider
}

// NewRegistry builds a Registry from a slice of candidate Provider
// constructors. Each constructor returns (Provider, nil) when
// configured and (nil, ErrNotConfigured) otherwise; the latter are
// silently dropped so the registry only contains usable providers.
func NewRegistry(candidates ...func() (Provider, error)) *Registry {
	r := &Registry{providers: map[string]Provider{}}
	for _, fn := range candidates {
		p, err := fn()
		if err != nil || p == nil {
			continue
		}
		r.providers[p.Name()] = p
	}
	return r
}

// Get returns the Provider for the given name or ErrNotConfigured
// if none is registered.
func (r *Registry) Get(name string) (Provider, error) {
	if r == nil {
		return nil, ErrNotConfigured
	}
	p, ok := r.providers[name]
	if !ok {
		return nil, ErrNotConfigured
	}
	return p, nil
}

// Has reports whether a Provider with this name is configured.
func (r *Registry) Has(name string) bool {
	if r == nil {
		return false
	}
	_, ok := r.providers[name]
	return ok
}

// Names returns the configured provider names in a stable order.
// Used by the /me/integrations list view so the UI can always show
// all three cards even when some are not configured on this server.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.providers))
	for k := range r.providers {
		out = append(out, k)
	}
	return out
}

// fmt shim used by providers to wrap parser errors.
func wrapExchange(err error) error {
	return fmt.Errorf("integrations: token exchange: %w", err)
}
