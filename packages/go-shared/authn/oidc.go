package authn

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// googleIssuer is the discovery root for Google sign-in.
const googleIssuer = "https://accounts.google.com"

// OIDCConfig configures a Google OIDC client.
type OIDCConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// OIDCClient lazily discovers the issuer on first use so that process
// startup never fails when it is unreachable. [Discovery] carries the
// retry and single-flight behaviour; this type only says what to build
// once the provider is in hand.
type OIDCClient struct {
	cfg OIDCConfig

	// issuer is the discovery root. Empty means [googleIssuer]; tests
	// point it at a local server.
	issuer string

	discovery Discovery
	provider  *oidc.Provider
	verifier  *oidc.IDTokenVerifier
	oauth     *oauth2.Config
}

// NewOIDCClient builds an unconfigured OIDCClient. The provider is
// fetched on the first call to AuthCodeURL or Exchange.
func NewOIDCClient(cfg OIDCConfig) *OIDCClient {
	return &OIDCClient{cfg: cfg}
}

func (c *OIDCClient) issuerURL() string {
	if c.issuer != "" {
		return c.issuer
	}
	return googleIssuer
}

func (c *OIDCClient) ensure(ctx context.Context) error {
	return c.discovery.Do(ctx, c.issuerURL(), func(p *oidc.Provider) {
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
	ctx = WithOutboundHTTPClient(ctx)
	tok, err := c.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("authn: oidc exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("authn: oidc response missing id_token")
	}
	idTok, err := c.verifier.Verify(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("authn: oidc verify: %w", err)
	}
	if expectedNonce != "" && idTok.Nonce != expectedNonce {
		return nil, fmt.Errorf("authn: oidc nonce mismatch")
	}
	return idTok, nil
}
