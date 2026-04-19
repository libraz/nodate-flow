package auth

import "github.com/nodate-flow/nodate-flow/packages/go-shared/authn"

// OIDCConfig configures a Google OIDC client.
type OIDCConfig = authn.OIDCConfig

// OIDCClient lazily constructs an oidc.Provider on first use so that
// process startup never fails when the upstream issuer is unreachable.
type OIDCClient = authn.OIDCClient

// NewOIDCClient builds an unconfigured OIDCClient. The provider is
// fetched on the first call to AuthCodeURL or Exchange.
func NewOIDCClient(cfg OIDCConfig) *OIDCClient {
	return authn.NewOIDCClient(cfg)
}
