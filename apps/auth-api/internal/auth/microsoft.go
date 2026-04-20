package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/microsoft"
)

// MicrosoftOIDCConfig configures a Microsoft Entra ID OIDC client.
type MicrosoftOIDCConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// MicrosoftOIDCClient wraps the standard OIDC discovery flow for
// Microsoft Entra ID (Azure AD) using the "common" tenant so that
// any Microsoft account can sign in.
type MicrosoftOIDCClient struct {
	cfg MicrosoftOIDCConfig

	once     sync.Once
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
	initErr  error
}

// NewMicrosoftOIDC builds a MicrosoftOIDCClient. The provider discovery
// is deferred to the first call to AuthCodeURL or Exchange.
func NewMicrosoftOIDC(cfg MicrosoftOIDCConfig) *MicrosoftOIDCClient {
	return &MicrosoftOIDCClient{cfg: cfg}
}

func (c *MicrosoftOIDCClient) ensure(ctx context.Context) error {
	c.once.Do(func() {
		// Microsoft uses the "common" tenant for multi-tenant apps.
		p, err := oidc.NewProvider(ctx, "https://login.microsoftonline.com/common/v2.0")
		if err != nil {
			c.initErr = fmt.Errorf("microsoft: oidc provider: %w", err)
			return
		}
		// Skip issuer check because "common" returns a tenant-specific
		// issuer in the id_token that differs from the discovery URL.
		c.verifier = p.Verifier(&oidc.Config{
			ClientID:        c.cfg.ClientID,
			SkipIssuerCheck: true,
		})
		c.oauth = &oauth2.Config{
			ClientID:     c.cfg.ClientID,
			ClientSecret: c.cfg.ClientSecret,
			RedirectURL:  c.cfg.RedirectURL,
			Endpoint:     microsoft.AzureADEndpoint("common"),
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		}
	})
	return c.initErr
}

// AuthCodeURL returns the redirect URL the user should be sent to.
func (c *MicrosoftOIDCClient) AuthCodeURL(ctx context.Context, state, nonce string) (string, error) {
	if err := c.ensure(ctx); err != nil {
		return "", err
	}
	return c.oauth.AuthCodeURL(state, oidc.Nonce(nonce)), nil
}

// Exchange swaps an authorization code for tokens and verifies the id_token.
func (c *MicrosoftOIDCClient) Exchange(ctx context.Context, code, expectedNonce string) (*oidc.IDToken, error) {
	if err := c.ensure(ctx); err != nil {
		return nil, err
	}
	tok, err := c.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("microsoft: oidc exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("microsoft: oidc response missing id_token")
	}
	idTok, err := c.verifier.Verify(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("microsoft: oidc verify: %w", err)
	}
	if expectedNonce != "" && subtle.ConstantTimeCompare([]byte(idTok.Nonce), []byte(expectedNonce)) != 1 {
		return nil, fmt.Errorf("microsoft: oidc nonce mismatch")
	}
	return idTok, nil
}
