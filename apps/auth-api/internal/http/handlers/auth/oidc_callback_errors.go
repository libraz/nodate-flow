package auth

import (
	"context"
	"log/slog"

	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
)

// oidcProviderRejection builds the AUTH.OIDC.PROVIDER_REJECTED error
// surface for an OIDC callback whose query string carries an `error`
// slug from the identity provider.
//
// The IdP redirects with `?error=...&error_description=...` whenever it
// declines the sign-in (user denied consent, scopes rejected, the
// relying-party app misconfigured at the IdP, etc.). Without checking
// these query parameters the handler would fail later with a generic
// "id_token invalid" code that obscures the real cause; this helper is
// the canonical entry point so all three providers share the same
// diagnostic shape.
//
// The slug and (optional) human-readable description are forwarded
// through ProblemDetails extensions so the SDK can surface them for
// diagnostics. The description is never logged verbatim because it
// can carry IdP-controlled or sensitive content; only the slug
// (`provider_error`) and the IdP identifier (`provider`) are emitted
// at slog.Warn.
func oidcProviderRejection(ctx context.Context, provider, errorSlug, errorDescription string) error {
	slog.WarnContext(ctx, "oidc: provider rejected callback",
		slog.String("provider", provider),
		slog.String("provider_error", errorSlug),
	)
	details := map[string]any{
		"provider":       provider,
		"provider_error": errorSlug,
	}
	if errorDescription != "" {
		details["provider_error_description"] = errorDescription
	}
	return handlerutil.HTTPErrWithDetails(apierrors.AuthOidcProviderRejected, details)
}
