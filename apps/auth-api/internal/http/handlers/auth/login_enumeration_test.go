package auth

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internauth "github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
)

// newPasswordUser inserts a fresh user with a local password identity and
// returns the login email. The password hash is real so VerifyPassword
// runs its full argon2id path, matching production timing.
func newPasswordUser(t *testing.T, q *generated.Queries, password string) string {
	t.Helper()
	ctx := context.Background()
	pub := types.New()
	email := "enum-" + pub.String() + "@example.test"
	uid64, err := q.RegisterUser(ctx, generated.RegisterUserParams{
		PublicID:        pub,
		Email:           email,
		DisplayName:     "Enum User",
		Locale:          "en",
		Timezone:        "UTC",
		Country:         sql.NullString{String: "US", Valid: true},
		ThemePreference: generated.UsersThemePreferenceSystem,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, uid64, int64(0))
	uid := uint32(uid64) //#nosec G115 -- test fixture LastInsertId is asserted non-negative and fits users.id.
	hash, err := internauth.HashPassword(password)
	require.NoError(t, err)
	_, err = q.CreateIdentity(ctx, generated.CreateIdentityParams{
		PublicID:     types.New(),
		UserID:       uid,
		Provider:     generated.IdentitiesProviderLocal,
		Subject:      email,
		PasswordHash: sql.NullString{String: hash, Valid: true},
	})
	require.NoError(t, err)
	return email
}

// loginFailure records the wire-visible outcome of a single failed login
// so a known-but-wrong-password account can be compared against an unknown
// email for indistinguishability.
type loginFailure struct {
	status int
	code   string
}

func attemptLogin(t *testing.T, deps Deps, email, password string) loginFailure {
	t.Helper()
	_, err := Login(deps)(context.Background(), &LoginInput{
		Body: struct {
			Email    string `json:"email" format:"email"`
			Password string `json:"password"`
		}{Email: email, Password: password},
	})
	problem := problemFor(t, err)
	return loginFailure{status: problem.Status, code: problem.Type}
}

// TestLogin_LockoutIsNotAnEnumerationOracle proves L-5 is closed: a
// known email with a wrong password and an unknown email must return the
// identical status + error code on every attempt across the lockout
// threshold. Before the fix the real account would eventually flip to
// AUTH.LOGIN.RATE_LIMITED_AFTER_RETRIES and then AUTH.LOGIN.ACCOUNT_LOCKED,
// while the unknown email stayed on AUTH.LOGIN.INVALID_CREDENTIALS —
// letting an attacker distinguish real accounts.
func TestLogin_LockoutIsNotAnEnumerationOracle(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)

	const goodPassword = "correct-horse-battery-staple"
	knownEmail := newPasswordUser(t, deps.Queries, goodPassword)
	unknownEmail := "does-not-exist-" + types.New().String() + "@example.test"

	// Drive both identities well past the lockout threshold so the locked
	// branch (subsequent attempts on an already-locked account) is also
	// exercised for the known email.
	attempts := int(maxFailedBeforeLock) + 3
	for i := 0; i < attempts; i++ {
		known := attemptLogin(t, deps, knownEmail, "wrong-password")
		unknown := attemptLogin(t, deps, unknownEmail, "wrong-password")

		assert.Equal(t, apierrors.AuthLoginInvalidCredentials.Code, known.code,
			"attempt %d: known-but-wrong-password must stay invalid credentials", i)
		assert.Equal(t, unknown, known,
			"attempt %d: known and unknown email must be indistinguishable", i)
	}

	// The lockout is still enforced internally: even the correct password
	// cannot authenticate once the account is locked, and the caller still
	// sees only invalid credentials (no locked oracle).
	locked := attemptLogin(t, deps, knownEmail, goodPassword)
	assert.Equal(t, apierrors.AuthLoginInvalidCredentials.Code, locked.code,
		"locked account must not reveal a distinct locked response")

	// And the lock is actually recorded on the real identity.
	ident, err := deps.Queries.FindLocalIdentityByEmail(context.Background(), knownEmail)
	require.NoError(t, err)
	assert.True(t, ident.LockedUntilAt.Valid,
		"the real account must actually be locked after repeated failures")
}
