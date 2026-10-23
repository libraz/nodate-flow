package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	authpkg "github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/email"
	sharedtoken "github.com/libraz/nodate-flow/packages/go-shared/token"
)

// magicLinkTTL is how long a magic link token stays valid.
const magicLinkTTL = 15 * time.Minute

// magicLinkPrefix is the user-visible prefix on the opaque token.
// Sourced from packages/go-shared/token so the prefix catalogue cannot
// drift between auth-api and flow-api.
const magicLinkPrefix = sharedtoken.PrefixMagicLink

// magicLinkIssueTimeout bounds the issuance a request hands off to the
// background, delivery included.
const magicLinkIssueTimeout = time.Minute

// MagicLinkRequest handles POST /auth/magic-link/request. It answers
// ok=true at once and issues the link in the background, so neither the
// response nor its latency depends on whether the address has an
// account; anything slower on the known-address path (token write, mail
// delivery, audit) would otherwise enumerate it.
func MagicLinkRequest(deps Deps) func(context.Context, *MagicLinkRequestInput) (*MagicLinkRequestOutput, error) {
	return func(ctx context.Context, in *MagicLinkRequestInput) (*MagicLinkRequestOutput, error) {
		out := &MagicLinkRequestOutput{}
		out.Body.Ok = true

		addr := strings.ToLower(strings.TrimSpace(in.Body.Email))
		issueCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), magicLinkIssueTimeout)
		go func() {
			defer cancel()
			if err := issueMagicLink(issueCtx, deps, addr); err != nil {
				slog.ErrorContext(issueCtx, "magic link: issuance failed", slog.Any("err", err))
			}
		}()
		return out, nil
	}
}

// issueMagicLink creates a one-time token for the account holding addr,
// stores its SHA-256 hash and mails the link. An address with no account
// is not an error. With no sender configured the token is stored but
// cannot be delivered.
func issueMagicLink(ctx context.Context, deps Deps, addr string) error {
	user, err := deps.Queries.FindUserByEmail(ctx, addr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("find user: %w", err)
	}

	plaintext, hash, err := authpkg.GenerateOpaque(magicLinkPrefix)
	if err != nil {
		return fmt.Errorf("generate token: %w", err)
	}
	// magic_link_tokens.ip_address is VARBINARY(16), so the address
	// is packed rather than stored as text: an IPv6 caller would
	// otherwise overflow the column and fail the request outright.
	ipAddr := dbtype.NullStringFromIP(authn.ClientIPFromContext(ctx))
	if _, err := deps.Queries.CreateMagicLinkToken(ctx, generated.CreateMagicLinkTokenParams{
		PublicID:  types.New(),
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: time.Now().Add(magicLinkTTL),
		IpAddress: ipAddr,
	}); err != nil {
		return fmt.Errorf("create token: %w", err)
	}

	deps.Audit.Record(ctx, audit.Entry{
		Action:       "auth.magic_link.requested",
		ActorID:      user.ID,
		ResourceType: "user",
		Metadata:     map[string]any{"email": addr},
	})

	if deps.EmailSender == nil {
		return nil
	}
	verifyURL := deps.AccountsWebURL + "/magic-link?token=" + plaintext
	if err := deps.EmailSender.Send(ctx, email.Message{
		To:      []string{addr},
		Subject: "Sign in to nodate-flow",
		Body:    "Click the link below to sign in. This link expires in 15 minutes.\n\n" + verifyURL + "\n\nIf you didn't request this, you can safely ignore this email.",
	}); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	return nil
}

// MagicLinkVerify handles GET /auth/magic-link/verify?token=. It
// validates the token, marks it as used, and issues session tokens.
func MagicLinkVerify(deps Deps) func(context.Context, *MagicLinkVerifyInput) (*MagicLinkVerifyOutput, error) {
	return func(ctx context.Context, in *MagicLinkVerifyInput) (*MagicLinkVerifyOutput, error) {
		if in.Token == "" {
			return nil, httpErr(apierrors.AuthMagicLinkMalformed)
		}
		hash := authpkg.HashOpaque(in.Token)
		row, err := deps.Queries.FindMagicLinkByTokenHash(ctx, hash)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.AuthMagicLinkMalformed, apierrors.InternalUnexpected))
		}
		if row.UsedAt.Valid {
			return nil, httpErr(apierrors.AuthMagicLinkAlreadyUsed)
		}
		if row.ExpiresAt.Before(time.Now()) {
			return nil, httpErr(apierrors.AuthMagicLinkExpired)
		}

		// A suspended account is refused before the link is burned.
		pub, err := deps.Queries.FindUserPublicIdById(ctx, row.UserID)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.AuthLoginAccountDisabled, apierrors.InternalUnexpected))
		}

		// Atomic compare-and-set on used_at: the SQL UPDATE matches only
		// when used_at IS NULL, so two concurrent verify requests racing
		// on the same token cannot both win. Exactly one will see
		// RowsAffected == 1; the loser sees 0 and is rejected with
		// ALREADY_USED so we never mint two sessions from one link.
		affected, err := deps.Queries.MarkMagicLinkUsed(ctx, row.ID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if affected == 0 {
			return nil, httpErr(apierrors.AuthMagicLinkAlreadyUsed)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "auth.magic_link.verified",
			ActorID:      row.UserID,
			ResourceType: "user",
		})

		// Step-up: if the account has confirmed TOTP, the magic link
		// alone is not sufficient. Mirror the password login path and
		// return a short-lived totp challenge instead of session tokens;
		// the client must finish at POST /auth/login/totp. The magic-link
		// token has already been burned above, so this cannot be replayed.
		// last_login_at is deferred to the TOTP happy path. A missing
		// local identity (e.g. OIDC-only account) is not an error here —
		// it simply means no TOTP gate applies.
		ident, ierr := deps.Queries.FindLocalIdentityByUserId(ctx, row.UserID)
		if ierr == nil && ident.MfaConfirmedAt.Valid && len(ident.MfaSecretCiphertext.String) > 0 {
			challenge, _, cerr := deps.JWT.SignTotpChallenge(pub.String())
			if cerr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			return &MagicLinkVerifyOutput{
				Body: LoginBody{
					Step:           "totp_required",
					ChallengeToken: challenge,
				},
			}, nil
		} else if ierr != nil && !errors.Is(ierr, sql.ErrNoRows) {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if uerr := deps.Queries.UpdateUserLastLoginAt(ctx, row.UserID); uerr != nil {
			// non-fatal
			_ = uerr
		}
		tokens, refresh, err := IssueTokens(ctx, deps, row.UserID, pub, in.UserAgent, authn.ClientIPFromContext(ctx))
		if err != nil {
			return nil, err
		}
		return &MagicLinkVerifyOutput{
			SetCookie: newRefreshCookie(refresh, deps.CookieSecure),
			Body: LoginBody{
				Step:        "complete",
				AccessToken: tokens.AccessToken,
				ExpiresAt:   tokens.ExpiresAt,
				UserID:      tokens.UserID,
			},
		}, nil
	}
}
