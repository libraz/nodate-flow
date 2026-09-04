package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/microsoft"

	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// microsoftIssuer is the discovery root for multi-tenant Microsoft apps.
const microsoftIssuer = "https://login.microsoftonline.com/common/v2.0"

// MicrosoftOIDCConfig configures a Microsoft Entra ID OIDC client.
type MicrosoftOIDCConfig struct {
	ClientID         string
	ClientSecret     string
	RedirectURL      string
	AllowedTenantIDs []string
}

// MicrosoftClaims holds the subset of id_token claims the callback
// handler consumes. Returning a flat struct from Exchange (rather than
// the raw *oidc.IDToken) keeps the handler boundary testable: tests
// can build a MicrosoftClaims value directly without needing to forge
// a verified id_token through the go-oidc library's private fields.
type MicrosoftClaims struct {
	Sub               string `json:"sub"`
	Email             string `json:"email"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	TenantID          string `json:"tid"`
	// EmailVerified reflects the generic email_verified claim when
	// Microsoft emits it. It is not the trust signal used by this
	// provider: Entra v2.0 may omit it, so validation relies on the
	// tenant allowlist plus xms_edov instead.
	EmailVerified bool `json:"email_verified"`
	// EmailDomainOwnerVerified reflects Microsoft's xms_edov claim.
	// It proves the domain owner has verified the email domain with
	// Microsoft and is required before trusting Microsoft email claims
	// from the multi-tenant "common" endpoint.
	EmailDomainOwnerVerified bool `json:"xms_edov"`
}

// MicrosoftOIDCClient wraps the standard OIDC discovery flow for
// Microsoft Entra ID (Azure AD) using the "common" tenant so that
// any Microsoft account can sign in.
//
// authn.Discovery carries the deadline, the single-flight and the retry
// behaviour of the discovery call; this type only says what to build once
// the provider is in hand.
type MicrosoftOIDCClient struct {
	cfg MicrosoftOIDCConfig

	// issuer is the discovery root. Empty means [microsoftIssuer]; tests
	// point it at a local server.
	issuer string

	discovery authn.Discovery
	verifier  *oidc.IDTokenVerifier
	oauth     *oauth2.Config
}

// NewMicrosoftOIDC builds a MicrosoftOIDCClient. The provider discovery
// is deferred to the first call to AuthCodeURL or Exchange.
func NewMicrosoftOIDC(cfg MicrosoftOIDCConfig) *MicrosoftOIDCClient {
	cfg.AllowedTenantIDs = normalizeTenantIDs(cfg.AllowedTenantIDs)
	return &MicrosoftOIDCClient{cfg: cfg}
}

func (c *MicrosoftOIDCClient) issuerURL() string {
	if c.issuer != "" {
		return c.issuer
	}
	return microsoftIssuer
}

func (c *MicrosoftOIDCClient) ensure(ctx context.Context) error {
	return c.discovery.Do(ctx, c.issuerURL(), func(p *oidc.Provider) {
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
}

// AuthCodeURL returns the redirect URL the user should be sent to.
func (c *MicrosoftOIDCClient) AuthCodeURL(ctx context.Context, state, nonce string) (string, error) {
	if err := c.ensure(ctx); err != nil {
		return "", err
	}
	return c.oauth.AuthCodeURL(state, oidc.Nonce(nonce)), nil
}

// Exchange swaps an authorization code for tokens, verifies the
// id_token, and returns the parsed claims the callback handler needs.
func (c *MicrosoftOIDCClient) Exchange(ctx context.Context, code, expectedNonce string) (*MicrosoftClaims, error) {
	if err := c.ensure(ctx); err != nil {
		return nil, err
	}
	ctx = authn.WithOutboundHTTPClient(ctx)
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
	var claims MicrosoftClaims
	if err := idTok.Claims(&claims); err != nil {
		return nil, fmt.Errorf("microsoft: decode claims: %w", err)
	}
	if err := ValidateMicrosoftClaims(&claims, c.cfg.AllowedTenantIDs); err != nil {
		return nil, err
	}
	return &claims, nil
}

// ValidateMicrosoftClaims enforces the tenant and email-domain proof
// required before the auth-api trusts a Microsoft id_token from the
// multi-tenant "common" endpoint.
func ValidateMicrosoftClaims(claims *MicrosoftClaims, allowedTenantIDs []string) error {
	if claims == nil {
		return fmt.Errorf("microsoft: claims missing")
	}
	allowed := normalizeTenantIDs(allowedTenantIDs)
	if len(allowed) == 0 {
		return fmt.Errorf("microsoft: no allowed tenant ids configured")
	}
	tid := strings.ToLower(strings.TrimSpace(claims.TenantID))
	if tid == "" {
		return fmt.Errorf("microsoft: tid claim missing")
	}
	for _, allowedID := range allowed {
		if tid == allowedID {
			if !claims.EmailDomainOwnerVerified {
				return fmt.Errorf("microsoft: xms_edov claim missing or false")
			}
			return nil
		}
	}
	return fmt.Errorf("microsoft: tenant %q is not allowed", claims.TenantID)
}

func normalizeTenantIDs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, raw := range in {
		v := strings.ToLower(strings.TrimSpace(raw))
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	return out
}
