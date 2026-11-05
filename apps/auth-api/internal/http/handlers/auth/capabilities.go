package auth

import (
	"context"

	"github.com/libraz/nodate-flow/packages/go-shared/email"
)

// TotpManagementRegistered reports whether the TOTP management routes
// exist. TOTP is the second factor of a password account (every management
// call reauthenticates with the local password), so they go with password
// login. The challenge endpoint POST /auth/login/totp is independent of
// this, so accounts enrolled earlier stay protected.
func (d Deps) TotpManagementRegistered() bool {
	return d.PasswordLoginEnabled
}

// TotpManagementOffered reports whether enrollment is usable: the routes
// exist and the cipher that seals the secret is configured. The router
// reads TotpManagementRegistered, the capabilities response this, so the
// two cannot drift apart on the password-login dependency.
func (d Deps) TotpManagementOffered() bool {
	return d.Cipher != nil && d.TotpManagementRegistered()
}

// MailerConfigured reports whether EmailSender can actually deliver mail.
// A nil sender and email.NoopSender both mean SMTP is not configured.
// Magic-link route registration and the capabilities response both read
// this, so the two cannot disagree.
func (d Deps) MailerConfigured() bool {
	if d.EmailSender == nil {
		return false
	}
	_, isNoop := d.EmailSender.(email.NoopSender)
	return !isNoop
}

// Capabilities handles GET /auth/capabilities. It returns which
// authentication methods are available on this instance. The response
// is computed once at route-registration time (no DB access, no side
// effects) since these values are immutable during the process
// lifetime.
func Capabilities(deps Deps) func(context.Context, *struct{}) (*CapabilitiesOutput, error) {
	out := &CapabilitiesOutput{
		CacheControl: "public, max-age=3600",
		Body: CapabilitiesBody{
			PasswordLogin:    deps.PasswordLoginEnabled,
			OIDCGoogle:       deps.OIDC != nil,
			OIDCGithub:       deps.OIDCGithub != nil,
			OIDCMicrosoft:    deps.OIDCMicrosoft != nil,
			MagicLink:        deps.MailerConfigured(),
			Totp:             deps.TotpManagementOffered(),
			RegistrationOpen: deps.RegistrationOpen,
		},
	}
	return func(_ context.Context, _ *struct{}) (*CapabilitiesOutput, error) {
		return out, nil
	}
}
