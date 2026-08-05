package auth

import (
	"context"
	"log/slog"
	"time"

	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// startOIDCState mints a state parameter for provider together with the
// cookie that binds it to the caller's browser. Every /start handler
// goes through here so no provider can be wired up without the binding.
func (d Deps) startOIDCState(ctx context.Context, out *OIDCStartOutput, nonce, provider string) (string, error) {
	binding, err := d.JWT.NewOIDCStateBinding(nonce, provider)
	if err != nil {
		slog.ErrorContext(ctx, "oidc start: failed to sign state",
			slog.String("provider", provider), slog.String("err", err.Error()))
		return "", httpErr(apierrors.InternalUnexpected)
	}
	out.SetCookie = newOIDCStateCookie(binding.CookieValue, binding.ExpiresAt, d.CookieSecure)
	out.Body.State = binding.State
	return binding.State, nil
}

// consumeOIDCState is the single gate every OIDC callback passes before
// it will exchange an authorization code. It checks, in order:
//
//   - the state's signature, expiry, and audience;
//   - that it was minted for this provider's redirect URI;
//   - that it is bound to the verifier cookie this browser presented;
//   - that it has not been redeemed already.
//
// The cookie check is the CSRF defence: an attacker can sign in at the
// provider themselves and hand a victim a working code plus state, but
// cannot write the verifier cookie into the victim's browser, so the
// victim can no longer be silently signed in to the attacker's account.
// The single-use claim then stops a state recovered from history, a
// referer header, or a proxy log from being redeemed a second time
// while its signature is still valid.
//
// Returns the nonce the id_token must carry.
func (d Deps) consumeOIDCState(ctx context.Context, in *OIDCCallbackInput, provider string) (string, error) {
	verified, err := d.JWT.VerifyOIDCStateBinding(in.State, in.StateCookie.Value, provider)
	if err != nil {
		slog.WarnContext(ctx, "oidc callback: state rejected",
			slog.String("provider", provider), slog.String("err", err.Error()))
		return "", httpErr(apierrors.AuthOidcStateMismatch)
	}
	store := d.SingleUse
	if store == nil {
		// A nil store would silently drop the replay defence, so it is
		// treated as a wiring failure rather than an opt-out.
		slog.ErrorContext(ctx, "oidc callback: single-use store not configured",
			slog.String("provider", provider))
		return "", httpErr(apierrors.InternalUnexpected)
	}
	ttl := time.Until(verified.ExpiresAt)
	claimed, err := store.Consume(ctx, oidcStateUseKey(verified.ID), ttl)
	if err != nil {
		slog.ErrorContext(ctx, "oidc callback: single-use claim failed",
			slog.String("provider", provider), slog.String("err", err.Error()))
		return "", httpErr(apierrors.InternalUnexpected)
	}
	if !claimed {
		slog.WarnContext(ctx, "oidc callback: state replayed",
			slog.String("provider", provider))
		return "", httpErr(apierrors.AuthOidcStateMismatch)
	}
	return verified.Nonce, nil
}

// oidcStateUseKey namespaces the single-use record so state jtis can
// never collide with another token kind sharing the same store.
func oidcStateUseKey(id string) string { return "oidc-state:" + id }

// Compile-time assurance that the store contract used above is the
// shared one, so a future backend swap (a claimed row in MySQL, Redis
// SET NX) only has to satisfy this interface.
var _ authn.SingleUseStore = (*authn.MemorySingleUseStore)(nil)
