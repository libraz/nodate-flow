package auth

import (
	"context"
	"fmt"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// OIDCConfig configures a Google OIDC client.
type OIDCConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// OIDCClient lazily constructs an oidc.Provider on first use so that
// process startup never fails when the upstream issuer is unreachable.
type OIDCClient struct {
	cfg OIDCConfig

	once     sync.Once
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
	initErr  error
}

// NewOIDCClient builds an unconfigured OIDCClient. The provider is
// fetched on the first call to Provider/Verifier/OAuth2.
func NewOIDCClient(cfg OIDCConfig) *OIDCClient {
	return &OIDCClient{cfg: cfg}
}

func (c *OIDCClient) ensure(ctx context.Context) error {
	c.once.Do(func() {
		p, err := oidc.NewProvider(ctx, "https://accounts.google.com")
		if err != nil {
			c.initErr = fmt.Errorf("auth: oidc provider: %w", err)
			return
		}
		c.provider = p
		c.verifier = p.Verifier(&oidc.Config{ClientID: c.cfg.ClientID})
		c.oauth = &oauth2.Config{
			ClientID:     c.cfg.ClientID,
			ClientSecret: c.cfg.ClientSecret,
			RedirectURL:  c.cfg.RedirectURL,
			Endpoint:     google.Endpoint,
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		}
	})
	return c.initErr
}

// AuthCodeURL returns the redirect URL the user should be sent to.
func (c *OIDCClient) AuthCodeURL(ctx context.Context, state, nonce string) (string, error) {
	if err := c.ensure(ctx); err != nil {
		return "", err
	}
	return c.oauth.AuthCodeURL(state, oidc.Nonce(nonce)), nil
}

// Exchange swaps an authorization code for tokens and verifies the id_token.
// When expectedNonce is non-empty, the id_token's nonce claim is validated
// against it to prevent token replay attacks.
func (c *OIDCClient) Exchange(ctx context.Context, code, expectedNonce string) (*oidc.IDToken, error) {
	if err := c.ensure(ctx); err != nil {
		return nil, err
	}
	tok, err := c.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("auth: oidc exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("auth: oidc response missing id_token")
	}
	idTok, err := c.verifier.Verify(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("auth: oidc verify: %w", err)
	}
	if expectedNonce != "" && idTok.Nonce != expectedNonce {
		return nil, fmt.Errorf("auth: oidc nonce mismatch")
	}
	return idTok, nil
}
