// TOTP 2FA handlers for /settings/security. Enrollment is a two-step
// flow:
//
//  1. POST /me/totp/enroll   — after password reverification, server
//     generates a 20-byte secret, encrypts it, writes it to
//     identities.mfa_secret_ciphertext with mfa_confirmed_at = NULL,
//     and returns the otpauth URL.
//  2. POST /me/totp/confirm  — client submits the current 6-digit
//     code plus current password; if both verify, mfa_confirmed_at is
//     stamped.
//
// Until confirm succeeds the account's 2FA state is "pending" and
// login is still single-factor. DELETE /me/totp disables 2FA after
// password reverification.
//
// Login-time 2FA gating (AUTH.TOTP.CODE_REQUIRED on POST /auth/login)
// is intentionally out of scope for this slice and tracked as a
// follow-up; enrollment alone is safe because login ignores the
// secret until the gating lands.
package auth

import (
	"context"
	"database/sql"
	"encoding/base32"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// TotpStatus handles GET /me/totp.
func TotpStatus(deps Deps) func(context.Context, *struct{}) (*TotpStatusOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*TotpStatusOutput, error) {
		uid, ok := authn.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		row, err := loadLocalIdentity(ctx, deps, uid)
		if err != nil {
			return nil, err
		}
		out := &TotpStatusOutput{}
		switch {
		case len(row.MfaSecretCiphertext.String) == 0:
			out.Body.Status = "disabled"
		case !row.MfaConfirmedAt.Valid:
			out.Body.Status = "pending"
		default:
			out.Body.Status = "enabled"
		}
		return out, nil
	}
}

// TotpEnroll handles POST /me/totp/enroll. Calling enroll on an
// already-enabled account returns AUTH.TOTP.ALREADY_ENROLLED so the
// caller must disable 2FA first. Calling enroll while in the
// "pending" state is allowed and rotates the secret.
func TotpEnroll(deps Deps) func(context.Context, *TotpEnrollInput) (*TotpEnrollOutput, error) {
	return func(ctx context.Context, in *TotpEnrollInput) (*TotpEnrollOutput, error) {
		if deps.Cipher == nil {
			return nil, httpErr(apierrors.AuthTotpNotConfigured)
		}
		uid, ok := authn.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		row, err := loadLocalIdentity(ctx, deps, uid)
		if err != nil {
			return nil, err
		}
		if row.MfaConfirmedAt.Valid {
			return nil, httpErr(apierrors.AuthTotpAlreadyEnrolled)
		}
		if err := verifyLocalIdentityPassword(row, in.Body.Password); err != nil {
			return nil, err
		}
		secret, err := auth.GenerateTotpSecret()
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		blob, err := deps.Cipher.Encrypt(secret)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if err := deps.Queries.SetIdentityMfaSecret(ctx, generated.SetIdentityMfaSecretParams{
			MfaSecretCiphertext: sql.NullString{String: string(blob), Valid: true},
			ID:                  row.ID,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// The otpauth URL label is "<issuer>:<email>". We fetch the email
		// from the user profile; any error here is non-fatal — we fall
		// back to the user's public_id.
		account := ""
		if u, uerr := deps.Queries.FindUserProfileById(ctx, uid); uerr == nil {
			account = u.Email
		}
		if account == "" {
			account = "user"
		}
		otpURL := auth.TotpOtpauthURL("nodate-flow", account, secret)
		recordTotpEnrolledAudit(ctx, deps, uid)
		out := &TotpEnrollOutput{}
		out.Body.OtpauthURL = otpURL
		out.Body.Secret = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
		return out, nil
	}
}

// TotpConfirm handles POST /me/totp/confirm.
func TotpConfirm(deps Deps) func(context.Context, *TotpConfirmInput) (*TotpConfirmOutput, error) {
	return func(ctx context.Context, in *TotpConfirmInput) (*TotpConfirmOutput, error) {
		if deps.Cipher == nil {
			return nil, httpErr(apierrors.AuthTotpNotConfigured)
		}
		uid, ok := authn.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		row, err := loadLocalIdentity(ctx, deps, uid)
		if err != nil {
			return nil, err
		}
		if len(row.MfaSecretCiphertext.String) == 0 {
			return nil, httpErr(apierrors.AuthTotpNotEnrolled)
		}
		if row.MfaConfirmedAt.Valid {
			return nil, httpErr(apierrors.AuthTotpAlreadyEnrolled)
		}
		if err := verifyLocalIdentityPassword(row, in.Body.Password); err != nil {
			return nil, err
		}
		secret, err := deps.Cipher.Decrypt([]byte(row.MfaSecretCiphertext.String))
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		step, okCode := auth.VerifyTotpStep(secret, in.Body.Code, time.Now())
		// A pending enrollment has mfa_last_step = NULL, but guard against
		// a replay of the confirmation code anyway in case enrollment was
		// rotated without clearing the step.
		if !okCode || (row.MfaLastStep.Valid && step <= row.MfaLastStep.Int64) {
			return nil, httpErr(apierrors.AuthTotpCodeMismatch)
		}
		if err := deps.Queries.ConfirmIdentityMfa(ctx, row.ID); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		// Burn the confirmation step so the very same code cannot be
		// immediately replayed against POST /auth/login/totp.
		if err := deps.Queries.UpdateIdentityMfaLastStep(ctx, generated.UpdateIdentityMfaLastStepParams{
			Step: sql.NullInt64{Int64: step, Valid: true},
			ID:   row.ID,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		codes, err := issueRecoveryCodes(ctx, deps, uid)
		if err != nil {
			return nil, err
		}
		out := &TotpConfirmOutput{}
		out.Body.Ok = true
		out.Body.RecoveryCodes = codes
		return out, nil
	}
}

// issueRecoveryCodes wipes any existing codes for the user and inserts
// 10 fresh ones, returning the plaintext set.
func issueRecoveryCodes(ctx context.Context, deps Deps, uid uint32) ([]string, error) {
	codes, hashes, err := auth.GenerateRecoveryCodes()
	if err != nil {
		return nil, httpErr(apierrors.InternalUnexpected)
	}
	if err := deps.Queries.DeleteAllRecoveryCodesForUser(ctx, uid); err != nil {
		return nil, httpErr(apierrors.InternalUnexpected)
	}
	for _, h := range hashes {
		if err := deps.Queries.InsertRecoveryCode(ctx, generated.InsertRecoveryCodeParams{UserID: uid, CodeHash: h}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
	}
	return codes, nil
}

// TotpRegenerateRecoveryCodes handles POST /me/totp/recovery-codes.
// Requires password reverification and only works on a fully enabled
// TOTP account.
func TotpRegenerateRecoveryCodes(deps Deps) func(context.Context, *TotpRegenerateRecoveryCodesInput) (*TotpRegenerateRecoveryCodesOutput, error) {
	return func(ctx context.Context, in *TotpRegenerateRecoveryCodesInput) (*TotpRegenerateRecoveryCodesOutput, error) {
		uid, ok := authn.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		row, err := loadLocalIdentity(ctx, deps, uid)
		if err != nil {
			return nil, err
		}
		if !row.MfaConfirmedAt.Valid {
			return nil, httpErr(apierrors.AuthTotpNotEnrolled)
		}
		if err := verifyLocalIdentityPassword(row, in.Body.Password); err != nil {
			return nil, err
		}
		codes, err := issueRecoveryCodes(ctx, deps, uid)
		if err != nil {
			return nil, err
		}
		out := &TotpRegenerateRecoveryCodesOutput{}
		out.Body.RecoveryCodes = codes
		return out, nil
	}
}

// TotpRecoveryCodesStatus handles GET /me/totp/recovery-codes.
func TotpRecoveryCodesStatus(deps Deps) func(context.Context, *struct{}) (*TotpRecoveryCodesStatusOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*TotpRecoveryCodesStatusOutput, error) {
		uid, ok := authn.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		n, err := deps.Queries.CountActiveRecoveryCodes(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &TotpRecoveryCodesStatusOutput{}
		out.Body.Remaining = int(n)
		return out, nil
	}
}

// TotpDisable handles DELETE /me/totp. Requires password
// reverification — removing 2FA is a security-sensitive operation
// and should not be possible with only a stolen access token.
func TotpDisable(deps Deps) func(context.Context, *TotpDisableInput) (*TotpDisableOutput, error) {
	return func(ctx context.Context, in *TotpDisableInput) (*TotpDisableOutput, error) {
		uid, ok := authn.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		row, err := loadLocalIdentity(ctx, deps, uid)
		if err != nil {
			return nil, err
		}
		if len(row.MfaSecretCiphertext.String) == 0 {
			return nil, httpErr(apierrors.AuthTotpNotEnrolled)
		}
		if err := verifyLocalIdentityPassword(row, in.Body.Password); err != nil {
			return nil, err
		}
		if err := deps.Queries.ClearIdentityMfa(ctx, row.ID); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		_ = deps.Queries.DeleteAllRecoveryCodesForUser(ctx, uid)
		recordTotpDisabledAudit(ctx, deps, uid)
		out := &TotpDisableOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// loadLocalIdentity is a small helper that maps sql.ErrNoRows to
// AUTH.PASSWORD.NO_LOCAL_IDENTITY so the handler code stays flat.
func loadLocalIdentity(ctx context.Context, deps Deps, uid uint32) (generated.FindLocalIdentityByUserIdRow, error) {
	row, err := deps.Queries.FindLocalIdentityByUserId(ctx, uid)
	if err != nil {
		return row, httpErr(apierr.SpecForErrNoRows(err, apierrors.AuthPasswordNoLocalIdentity, apierrors.InternalUnexpected))
	}
	return row, nil
}

func verifyLocalIdentityPassword(row generated.FindLocalIdentityByUserIdRow, password string) error {
	if !row.PasswordHash.Valid {
		return httpErr(apierrors.AuthPasswordNoLocalIdentity)
	}
	okPw, err := auth.VerifyPassword(row.PasswordHash.String, password)
	if err != nil || !okPw {
		return httpErr(apierrors.AuthPasswordCurrentMismatch)
	}
	return nil
}

// recordTotpEnrolledAudit emits the audit entry for a successful TOTP
// enrollment. Nil-safe so handlers do not need to guard the call.
func recordTotpEnrolledAudit(ctx context.Context, deps Deps, uid uint32) {
	if deps.Audit == nil {
		return
	}
	deps.Audit.Record(ctx, audit.Entry{
		Action:       "auth.totp_enrolled",
		ActorID:      uid,
		ResourceType: "user",
	})
}

// recordTotpDisabledAudit emits the audit entry for a successful TOTP
// disable. Nil-safe so handlers do not need to guard the call.
func recordTotpDisabledAudit(ctx context.Context, deps Deps, uid uint32) {
	if deps.Audit == nil {
		return
	}
	deps.Audit.Record(ctx, audit.Entry{
		Action:       "auth.totp_disabled",
		ActorID:      uid,
		ResourceType: "user",
	})
}
