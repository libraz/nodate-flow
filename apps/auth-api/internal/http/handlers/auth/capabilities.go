package auth

import (
	"context"

	"github.com/libraz/nodate-flow/packages/go-shared/email"
)

// Capabilities handles GET /auth/capabilities. It returns which
// authentication methods are available on this instance. The response
// is computed once at route-registration time (no DB access, no side
// effects) since these values are immutable during the process
// lifetime.
func Capabilities(deps Deps) func(context.Context, *struct{}) (*CapabilitiesOutput, error) {
	// NoopSender satisfies the Sender interface but indicates that
	// SMTP is not configured. Use a type assertion to distinguish
	// it from a real sender.
	_, isNoop := deps.EmailSender.(email.NoopSender)
	hasMail := deps.EmailSender != nil && !isNoop

	out := &CapabilitiesOutput{
		CacheControl: "public, max-age=3600",
		Body: CapabilitiesBody{
			PasswordLogin:    true,
			OIDCGoogle:       deps.OIDC != nil,
			OIDCGithub:       deps.OIDCGithub != nil,
			OIDCMicrosoft:    deps.OIDCMicrosoft != nil,
			MagicLink:        hasMail,
			Totp:             deps.Cipher != nil,
			RegistrationOpen: deps.RegistrationOpen,
		},
	}
	return func(_ context.Context, _ *struct{}) (*CapabilitiesOutput, error) {
		return out, nil
	}
}
