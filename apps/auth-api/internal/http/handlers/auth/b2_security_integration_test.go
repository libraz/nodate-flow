package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internauth "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth/sessadapter"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/crypto"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/testhelpers"
)

// b2DB lazily boots a shared MySQL testcontainer with the full repo
// schema. The B-2 security regressions need a real DB because they
// exercise the identities / sessions tables and the sqlc query paths
// that persist the TOTP last-step and tear down session families.
var b2DB = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{Database: "nodate_auth_b2_test"})

// requireB2DB skips when integration tests are not enabled and otherwise
// returns the shared *sql.DB. Mirrors the flow-api integration guard so
// `go test -short` and Docker-less CI stay green.
func requireB2DB(t *testing.T) *sql.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping B-2 integration test in -short mode")
	}
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		t.Skip("set NF_TEST_INTEGRATION=1 to run B-2 integration tests")
	}
	inst, err := b2DB.Start(context.Background())
	require.NoError(t, err, "start mysql testcontainer")
	return inst.DB
}

// b2TestCipher returns a deterministic Cipher for encrypting TOTP
// secrets in tests.
func b2TestCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	c, err := crypto.New(key)
	require.NoError(t, err)
	return c
}

// b2Deps assembles a Deps wired to the real DB plus a capturing audit
// sink so tests can assert on emitted audit entries.
func b2Deps(t *testing.T, db *sql.DB) (Deps, *captureSink) {
	t.Helper()
	q := generated.New(db)
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)
	sink := &captureSink{}
	return Deps{
		DB:       db,
		Queries:  q,
		Sessions: sessadapter.NewMySQLStore(db, q),
		JWT:      jwt,
		Cipher:   b2TestCipher(t),
		Audit:    sink,
	}, sink
}

// b2NewUser inserts a fresh user + local identity and returns the
// internal user id, public id, and email. Each call uses a unique email
// so tests stay parallel-safe.
func b2NewUser(t *testing.T, q *generated.Queries) (uint32, types.PublicID, string) {
	t.Helper()
	ctx := context.Background()
	pub := types.New()
	email := "b2-" + pub.String() + "@example.test"
	uid64, err := q.RegisterUser(ctx, generated.RegisterUserParams{
		PublicID:        pub,
		Email:           email,
		DisplayName:     "B2 User",
		Locale:          "en",
		Timezone:        "UTC",
		Country:         sql.NullString{String: "US", Valid: true},
		ThemePreference: generated.UsersThemePreferenceSystem,
	})
	require.NoError(t, err)
	uid := uint32(uid64)
	_, err = q.CreateIdentity(ctx, generated.CreateIdentityParams{
		PublicID:     types.New(),
		UserID:       uid,
		Provider:     generated.IdentitiesProviderLocal,
		Subject:      email,
		PasswordHash: sql.NullString{}, // password not needed for these paths
	})
	require.NoError(t, err)
	return uid, pub, email
}

// b2EnrollTotp encrypts a fresh TOTP secret onto the user's identity and
// confirms it, returning the raw secret so the test can compute codes.
func b2EnrollTotp(t *testing.T, deps Deps, uid uint32) []byte {
	t.Helper()
	ctx := context.Background()
	secret, err := internauth.GenerateTotpSecret()
	require.NoError(t, err)
	blob, err := deps.Cipher.Encrypt(secret)
	require.NoError(t, err)
	ident, err := deps.Queries.FindLocalIdentityByUserId(ctx, uid)
	require.NoError(t, err)
	require.NoError(t, deps.Queries.SetIdentityMfaSecret(ctx, generated.SetIdentityMfaSecretParams{
		MfaSecretCiphertext: sql.NullString{String: string(blob), Valid: true},
		ID:                  ident.ID,
	}))
	require.NoError(t, deps.Queries.ConfirmIdentityMfa(ctx, ident.ID))
	return secret
}

func problemFor(t *testing.T, err error) *handlerutil.ProblemDetails {
	t.Helper()
	require.Error(t, err)
	var problem *handlerutil.ProblemDetails
	require.True(t, errors.As(err, &problem), "expected ProblemDetails, got %T", err)
	return problem
}

// TestMagicLinkVerify_2FAAccountReturnsChallenge proves B-2(a): a
// magic-link verification on a 2FA-enrolled account must NOT issue
// session tokens. It returns a totp_required step-up challenge instead,
// forcing the second factor.
func TestMagicLinkVerify_2FAAccountReturnsChallenge(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	ctx := context.Background()

	uid, _, _ := b2NewUser(t, deps.Queries)
	b2EnrollTotp(t, deps, uid)

	// Mint a magic-link token for the user.
	plain, hash, err := internauth.GenerateOpaque("ml")
	require.NoError(t, err)
	_, err = deps.Queries.CreateMagicLinkToken(ctx, generated.CreateMagicLinkTokenParams{
		PublicID:  types.New(),
		UserID:    uid,
		TokenHash: hash,
		ExpiresAt: time.Now().Add(15 * time.Minute),
	})
	require.NoError(t, err)

	out, err := MagicLinkVerify(deps)(ctx, &MagicLinkVerifyInput{Token: plain})
	require.NoError(t, err)
	assert.Equal(t, "totp_required", out.Body.Step, "2FA account must be challenged")
	assert.NotEmpty(t, out.Body.ChallengeToken, "a challenge token must be returned")
	assert.Empty(t, out.Body.AccessToken, "no access token before second factor")
	assert.Empty(t, out.SetCookie.Value, "no refresh cookie before second factor")

	// The challenge must be a valid step-up token for this user.
	pubStr, verr := deps.JWT.VerifyTotpChallenge(out.Body.ChallengeToken)
	require.NoError(t, verr)
	assert.NotEmpty(t, pubStr)
}

// TestMagicLinkVerify_NoMFAStillCompletes guards the single-factor path:
// an account without TOTP must still complete via magic link.
func TestMagicLinkVerify_NoMFAStillCompletes(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	ctx := context.Background()

	uid, _, _ := b2NewUser(t, deps.Queries)

	plain, hash, err := internauth.GenerateOpaque("ml")
	require.NoError(t, err)
	_, err = deps.Queries.CreateMagicLinkToken(ctx, generated.CreateMagicLinkTokenParams{
		PublicID:  types.New(),
		UserID:    uid,
		TokenHash: hash,
		ExpiresAt: time.Now().Add(15 * time.Minute),
	})
	require.NoError(t, err)

	out, err := MagicLinkVerify(deps)(ctx, &MagicLinkVerifyInput{Token: plain})
	require.NoError(t, err)
	assert.Equal(t, "complete", out.Body.Step)
	assert.NotEmpty(t, out.Body.AccessToken)
	assert.NotEmpty(t, out.SetCookie.Value, "refresh cookie set on completion")
}

// TestLoginTotp_ReplayedCodeRejected proves B-2(b): a TOTP code that has
// already been accepted (same time-step) is rejected on a second
// presentation, even inside the validation window.
func TestLoginTotp_ReplayedCodeRejected(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	ctx := context.Background()

	uid, pub, _ := b2NewUser(t, deps.Queries)
	secret := b2EnrollTotp(t, deps, uid)

	now := time.Now()
	code := internauth.TotpCode(secret, now)

	// First presentation succeeds.
	challenge1, _, err := deps.JWT.SignTotpChallenge(pub.String())
	require.NoError(t, err)
	_, err = LoginTotp(deps)(ctx, &LoginTotpInput{
		Body: struct {
			ChallengeToken string `json:"challengeToken" minLength:"1"`
			Code           string `json:"code,omitempty" pattern:"^$|^[0-9]{6}$"`
			RecoveryCode   string `json:"recoveryCode,omitempty" pattern:"^$|^[A-Za-z0-9-]{10,20}$"`
		}{ChallengeToken: challenge1, Code: code},
	})
	require.NoError(t, err, "first TOTP submission must succeed")

	// Second presentation of the SAME code (same step) must be rejected.
	challenge2, _, err := deps.JWT.SignTotpChallenge(pub.String())
	require.NoError(t, err)
	_, err = LoginTotp(deps)(ctx, &LoginTotpInput{
		Body: struct {
			ChallengeToken string `json:"challengeToken" minLength:"1"`
			Code           string `json:"code,omitempty" pattern:"^$|^[0-9]{6}$"`
			RecoveryCode   string `json:"recoveryCode,omitempty" pattern:"^$|^[A-Za-z0-9-]{10,20}$"`
		}{ChallengeToken: challenge2, Code: code},
	})
	problem := problemFor(t, err)
	assert.Equal(t, apierrors.AuthTotpCodeMismatch.Code, problem.Type,
		"replayed TOTP code must be rejected as a mismatch")

	// The stored last-step must reflect the accepted code.
	ident, err := deps.Queries.FindLocalIdentityByUserId(ctx, uid)
	require.NoError(t, err)
	assert.True(t, ident.MfaLastStep.Valid, "last step must be persisted after acceptance")
	assert.Equal(t, now.Unix()/30, ident.MfaLastStep.Int64)
}

// TestRefresh_ReusedTokenRevokesFamilyAndAudits proves B-2(c): replaying
// a rotated (revoked) refresh token tears down every session for the
// user and writes an auth.refresh_reuse_detected audit entry.
func TestRefresh_ReusedTokenRevokesFamilyAndAudits(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, sink := b2Deps(t, db)
	ctx := context.Background()

	uid, _, _ := b2NewUser(t, deps.Queries)

	// Create a session, then rotate it past the benign grace window so
	// the original refresh hash now points at a revoked row. We simulate
	// rotation by inserting the original session already revoked.
	origPlain, origHash, err := internauth.GenerateRefresh()
	require.NoError(t, err)
	_, err = deps.Queries.CreateSession(ctx, generated.CreateSessionParams{
		PublicID:    types.New(),
		UserID:      uid,
		RefreshHash: origHash,
		ExpiresAt:   time.Now().Add(30 * 24 * time.Hour),
	})
	require.NoError(t, err)

	// A second, still-active session in the same family.
	_, activeHash, err := internauth.GenerateRefresh()
	require.NoError(t, err)
	activePub := types.New()
	_, err = deps.Queries.CreateSession(ctx, generated.CreateSessionParams{
		PublicID:    activePub,
		UserID:      uid,
		RefreshHash: activeHash,
		ExpiresAt:   time.Now().Add(30 * 24 * time.Hour),
	})
	require.NoError(t, err)

	// Revoke the original session well outside the grace window.
	_, err = db.ExecContext(ctx,
		"UPDATE sessions SET revoked_at = ?, enabled = FALSE WHERE refresh_hash = ?",
		time.Now().Add(-time.Hour), origHash)
	require.NoError(t, err)

	// Replay the (now revoked) original refresh token.
	_, err = Refresh(deps)(ctx, &RefreshInput{
		RefreshCookie: http.Cookie{Name: "nd_rt", Value: origPlain},
	})
	problem := problemFor(t, err)
	assert.Equal(t, apierrors.AuthTokenRefreshInvalid.Code, problem.Type)

	// The whole family must now be revoked, including the active session.
	var activeCount int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sessions WHERE user_id = ? AND enabled = TRUE AND revoked_at IS NULL", uid,
	).Scan(&activeCount))
	assert.Equal(t, 0, activeCount, "reuse must revoke the entire session family")

	// An audit entry must record the reuse.
	found := false
	for _, e := range sink.snapshot() {
		if e.Action == "auth.refresh_reuse_detected" {
			found = true
			assert.Equal(t, uid, e.ActorID)
		}
	}
	assert.True(t, found, "auth.refresh_reuse_detected audit entry must be written")
}

// TestRefresh_GraceWindowDoesNotRevokeFamily confirms a benign
// double-submit (the rotated token replayed within the grace window) is
// rejected but does NOT tear down the session family.
func TestRefresh_GraceWindowDoesNotRevokeFamily(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, sink := b2Deps(t, db)
	ctx := context.Background()

	uid, _, _ := b2NewUser(t, deps.Queries)

	origPlain, origHash, err := internauth.GenerateRefresh()
	require.NoError(t, err)
	_, err = deps.Queries.CreateSession(ctx, generated.CreateSessionParams{
		PublicID:    types.New(),
		UserID:      uid,
		RefreshHash: origHash,
		ExpiresAt:   time.Now().Add(30 * 24 * time.Hour),
	})
	require.NoError(t, err)

	_, activeHash, err := internauth.GenerateRefresh()
	require.NoError(t, err)
	_, err = deps.Queries.CreateSession(ctx, generated.CreateSessionParams{
		PublicID:    types.New(),
		UserID:      uid,
		RefreshHash: activeHash,
		ExpiresAt:   time.Now().Add(30 * 24 * time.Hour),
	})
	require.NoError(t, err)

	// Revoke the original "just now" — inside the grace window.
	_, err = db.ExecContext(ctx,
		"UPDATE sessions SET revoked_at = ?, enabled = FALSE WHERE refresh_hash = ?",
		time.Now(), origHash)
	require.NoError(t, err)

	_, err = Refresh(deps)(ctx, &RefreshInput{
		RefreshCookie: http.Cookie{Name: "nd_rt", Value: origPlain},
	})
	problem := problemFor(t, err)
	assert.Equal(t, apierrors.AuthTokenRefreshInvalid.Code, problem.Type)

	// The active session must survive a benign double-submit.
	var activeCount int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sessions WHERE user_id = ? AND enabled = TRUE AND revoked_at IS NULL", uid,
	).Scan(&activeCount))
	assert.Equal(t, 1, activeCount, "benign double-submit must not revoke the family")

	for _, e := range sink.snapshot() {
		assert.NotEqual(t, "auth.refresh_reuse_detected", e.Action,
			"grace-window replay must not raise the reuse alarm")
	}
}
