package auth

import (
	"context"
	"fmt"

	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// OIDCConfig configures a Google OIDC client.
type OIDCConfig = authn.OIDCConfig

// GoogleClaims holds the id_token claims the Google callback consumes.
type GoogleClaims struct {
	Email         string `json:"email"`
	Sub           string `json:"sub"`
	Name          string `json:"name"`
	Locale        string `json:"locale"`
	EmailVerified bool   `json:"email_verified"`
}

// GoogleOIDCClient adapts the shared OIDC client, which lazily
// constructs its oidc.Provider on first use so that process startup
// never fails when the upstream issuer is unreachable, to the claims
// shape the Google handlers consume.
type GoogleOIDCClient struct {
	client *authn.OIDCClient
}

// NewGoogleOIDC builds an unconfigured GoogleOIDCClient. The provider is
// fetched on the first call to AuthCodeURL or Exchange.
func NewGoogleOIDC(cfg OIDCConfig) *GoogleOIDCClient {
	return &GoogleOIDCClient{client: authn.NewOIDCClient(cfg)}
}

// AuthCodeURL returns the redirect URL the user should be sent to.
func (c *GoogleOIDCClient) AuthCodeURL(ctx context.Context, state, nonce string) (string, error) {
	return c.client.AuthCodeURL(ctx, state, nonce)
}

// Exchange swaps an authorization code for a verified id_token and
// returns its claims.
func (c *GoogleOIDCClient) Exchange(ctx context.Context, code, expectedNonce string) (*GoogleClaims, error) {
	idTok, err := c.client.Exchange(ctx, code, expectedNonce)
	if err != nil {
		return nil, err
	}
	var claims GoogleClaims
	if err := idTok.Claims(&claims); err != nil {
		return nil, fmt.Errorf("google: decode id_token claims: %w", err)
	}
	return &claims, nil
}
