package auth

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internauth "github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// allowlistDB is a container of its own: an enabled allowlist row is
// instance-wide, so one in the shared b2 database would refuse every
// other OIDC test's sign-in.
var allowlistDB = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{Database: "nodate_auth_allowlist_test"})

func requireAllowlistDB(t *testing.T) *sql.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		t.Skip("set NF_TEST_INTEGRATION=1 to run integration tests")
	}
	inst, err := allowlistDB.Start(context.Background())
	require.NoError(t, err, "start mysql testcontainer")
	return inst.DB
}

// failingReadDBTX fails every read, standing for an unreachable database.
type failingReadDBTX struct{}

func (failingReadDBTX) ExecContext(context.Context, string, ...interface{}) (sql.Result, error) {
	return nil, errors.New("db unreachable")
}

func (failingReadDBTX) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, errors.New("db unreachable")
}

func (failingReadDBTX) QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error) {
	return nil, errors.New("db unreachable")
}

func (failingReadDBTX) QueryRowContext(context.Context, string, ...interface{}) *sql.Row {
	return nil
}

// allowlistCallback signs in through one OIDC provider with the given
// verified email and a fresh subject.
type allowlistCallback func(t *testing.T, deps Deps, email string) error

func githubAllowlistCallback(t *testing.T, deps Deps, email string) error {
	t.Helper()
	deps.OIDCGithub = &fakeGithubExchanger{
		claims: &internauth.GithubClaims{Sub: "gh-" + types.New().String(), Login: "u", Name: "U", Email: email},
	}
	state, stateCookie := signedGithubState(t, &deps)
	_, err := OIDCGithubCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code: "auth-code", State: state, StateCookie: stateCookie,
	})
	return err
}

func microsoftAllowlistCallback(t *testing.T, deps Deps, email string) error {
	t.Helper()
	deps.MicrosoftAllowedTenantIDs = []string{testMicrosoftTenantID}
	deps.OIDCMicrosoft = &fakeMicrosoftExchanger{
		claims: &internauth.MicrosoftClaims{
			Sub:                      "ms-" + types.New().String(),
			Email:                    email,
			Name:                     "U",
			TenantID:                 testMicrosoftTenantID,
			EmailVerified:            true,
			EmailDomainOwnerVerified: true,
		},
	}
	state, stateCookie := signedMicrosoftState(t, &deps)
	_, err := OIDCMicrosoftCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code: "auth-code", State: state, StateCookie: stateCookie,
	})
	return err
}

func googleAllowlistCallback(t *testing.T, deps Deps, email string) error {
	t.Helper()
	deps.OIDC = &fakeGoogleExchanger{
		claims: &internauth.GoogleClaims{Sub: "g-" + types.New().String(), Email: email, Name: "U", EmailVerified: true},
	}
	state, stateCookie := signedGoogleState(t, &deps)
	_, err := OIDCGoogleCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code: "auth-code", State: state, StateCookie: stateCookie,
	})
	return err
}

var allowlistCallbacks = map[string]allowlistCallback{
	"google":    googleAllowlistCallback,
	"github":    githubAllowlistCallback,
	"microsoft": microsoftAllowlistCallback,
}

// TestOIDCCallbacks_AllowlistReadFailureRefuses proves each callback
// refuses, as an internal error, when the allowlist cannot be read,
// even with nothing configured in the environment.
func TestOIDCCallbacks_AllowlistReadFailureRefuses(t *testing.T) {
	t.Parallel()
	for name, callback := range allowlistCallbacks {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
			require.NoError(t, err)
			deps := Deps{JWT: jwt, Queries: generated.New(failingReadDBTX{}), RegistrationOpen: true}

			problem := problemFor(t, callback(t, deps, "someone@example.test"))
			assert.Equal(t, apierrors.InternalUnexpected.Code, problem.Type)
		})
	}
}

// TestOIDCCallbacks_AllowlistIsEnforced drives each callback through the
// allowlist: an environment entry that does not match refuses, a database
// row admits, and withdrawing the row stops admitting on the next attempt.
// The steps share instance-wide state, so they run in sequence.
func TestOIDCCallbacks_AllowlistIsEnforced(t *testing.T) {
	db := requireAllowlistDB(t)
	for name, callback := range allowlistCallbacks {
		t.Run(name, func(t *testing.T) {
			deps, _ := b2Deps(t, db)
			deps.RegistrationOpen = true
			address := func() string { return name + "-" + types.New().String() + "@allowed.example" }

			deps.OAuthAllowedDomains = []string{"corp.example"}
			problem := problemFor(t, callback(t, deps, address()))
			assert.Equal(t, apierrors.AuthOidcDomainNotAllowed.Code, problem.Type,
				"an address outside the environment allowlist must be refused")
			deps.OAuthAllowedDomains = nil

			rowID := types.New()
			require.NoError(t, deps.Queries.UpsertOauthSigninAllowlistEntry(context.Background(),
				generated.UpsertOauthSigninAllowlistEntryParams{
					PublicID:   rowID,
					EntryKind:  generated.OauthSigninAllowlistEntryKindDomain,
					EntryValue: "allowed.example",
				}))
			require.NoError(t, callback(t, deps, address()), "an enabled row must admit its domain")

			n, err := deps.Queries.WithdrawOauthSigninAllowlistEntry(context.Background(), rowID)
			require.NoError(t, err)
			require.EqualValues(t, 1, n)
			// Withdrawing the only row leaves the allowlist empty, which is
			// open; a second row keeps it active so the withdrawal shows.
			require.NoError(t, deps.Queries.UpsertOauthSigninAllowlistEntry(context.Background(),
				generated.UpsertOauthSigninAllowlistEntryParams{
					PublicID:   types.New(),
					EntryKind:  generated.OauthSigninAllowlistEntryKindDomain,
					EntryValue: "other-" + name + ".example",
				}))
			problem = problemFor(t, callback(t, deps, address()))
			assert.Equal(t, apierrors.AuthOidcDomainNotAllowed.Code, problem.Type,
				"a withdrawn row must stop admitting on the next attempt")

			_, err = db.ExecContext(context.Background(), "UPDATE oauth_signin_allowlist SET enabled = FALSE")
			require.NoError(t, err)
		})
	}
}
