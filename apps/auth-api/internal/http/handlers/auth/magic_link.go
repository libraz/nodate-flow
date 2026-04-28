package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	authpkg "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/email"
)

// magicLinkTTL is how long a magic link token stays valid.
const magicLinkTTL = 15 * time.Minute

// magicLinkPrefix is used to generate the opaque token.
const magicLinkPrefix = "ml_"

// MagicLinkRequest handles POST /auth/magic-link/request. It generates
// a one-time token, stores its SHA-256 hash, and sends the link via
// email. The response always returns ok=true to prevent email
// enumeration — even when no account exists for the address.
func MagicLinkRequest(deps Deps) func(context.Context, *MagicLinkRequestInput) (*MagicLinkRequestOutput, error) {
	return func(ctx context.Context, in *MagicLinkRequestInput) (*MagicLinkRequestOutput, error) {
		out := &MagicLinkRequestOutput{}
		out.Body.Ok = true

		addr := strings.ToLower(strings.TrimSpace(in.Body.Email))
		user, err := deps.Queries.FindUserByEmail(ctx, addr)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// Silently succeed to prevent enumeration.
				return out, nil
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		plaintext, hash, err := authpkg.GenerateOpaque(magicLinkPrefix)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		pubID := types.New()
		ip := authn.ClientIPFromContext(ctx)
		var ipAddr sql.NullString
		if ip != "" {
			ipAddr = sql.NullString{String: ip, Valid: true}
		}
		if _, err := deps.Queries.CreateMagicLinkToken(ctx, generated.CreateMagicLinkTokenParams{
			PublicID:  pubID,
			UserID:    user.ID,
			TokenHash: hash,
			ExpiresAt: time.Now().Add(magicLinkTTL),
			IpAddress: ipAddr,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Send the email. If the sender is nil (SMTP not configured),
		// we silently succeed — the token exists in the DB but can't be
		// delivered, matching the "always succeed" contract.
		if deps.EmailSender != nil {
			verifyURL := deps.AccountsWebURL + "/magic-link?token=" + plaintext
			_ = deps.EmailSender.Send(ctx, email.Message{
				To:      []string{addr},
				Subject: "Sign in to nodate-flow",
				Body:    "Click the link below to sign in. This link expires in 15 minutes.\n\n" + verifyURL + "\n\nIf you didn't request this, you can safely ignore this email.",
			})
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "auth.magic_link.requested",
			ActorID:      user.ID,
			ResourceType: "user",
			Metadata:     map[string]any{"email": addr},
		})
		return out, nil
	}
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

		pub, err := deps.Queries.FindUserPublicIdById(ctx, row.UserID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if uerr := deps.Queries.UpdateUserLastLoginAt(ctx, row.UserID); uerr != nil {
			// non-fatal
			_ = uerr
		}
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "auth.magic_link.verified",
			ActorID:      row.UserID,
			ResourceType: "user",
		})
		tokens, refresh, err := issueTokens(ctx, deps, row.UserID, pub, in.UserAgent, authn.ClientIPFromContext(ctx))
		if err != nil {
			return nil, err
		}
		return &MagicLinkVerifyOutput{
			SetCookie: newRefreshCookie(refresh, deps.CookieSecure),
			Body:      tokens,
		}, nil
	}
}
