package auth

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	internauth "github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/obs"
	"github.com/libraz/nodate-flow/packages/go-shared/sessionstore"
)

// None of the tests below calls t.Parallel, deliberately. The
// registration counter is a package-level collector shared by the whole
// binary, and the integration tests in this package register users of
// their own; a sequential test never overlaps a paused parallel one, so
// running sequentially is what makes a delta of exactly one meaningful.
// Every assertion is a delta for the same reason — the absolute value
// carries whatever ran before.

// registerSessions answers the one session write a successful
// registration performs. fakeSessions panics on Create because the
// logout tests never reach it; this path does.
type registerSessions struct{ *fakeSessions }

func (registerSessions) Create(context.Context, sessionstore.CreateParams) (uint32, error) {
	return 1, nil
}

// registerDeps builds the handler dependencies a registration needs on
// top of the given stub database: a real token issuer and a session
// store that accepts the row.
func registerDeps(t *testing.T, stub *registerStub) Deps {
	t.Helper()

	db := openRegisterStubDB(t, stub)
	t.Cleanup(func() { _ = db.Close() })

	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)

	return Deps{
		Queries:          generated.New(db),
		Sessions:         registerSessions{},
		JWT:              jwt,
		RegistrationOpen: true,
	}
}

// TestRegistrationCounterCountsACompletedSignUp pins the sign-up rate to
// what actually happened: one account created, one increment.
func TestRegistrationCounterCountsACompletedSignUp(t *testing.T) {
	before := testutil.ToFloat64(obs.UsersRegisteredCounter())

	out, err := Register(registerDeps(t, &registerStub{}))(context.Background(), registerInput())
	require.NoError(t, err)
	require.NotEmpty(t, out.Body.AccessToken)

	require.Equal(t, 1.0, testutil.ToFloat64(obs.UsersRegisteredCounter())-before,
		"a completed registration must count exactly once")
}

// TestRegistrationCounterIgnoresATakenAddress covers the answer the
// counter has to give for a sign-up that registered nobody. Both unique
// keys are reachable: users.email decides first, and
// identities(provider, subject) is the same race one step later. In
// either case the caller is told the address is taken, so a counter
// that moved would report sign-ups that never happened — and the
// identity case is the one that looks like a success from the users
// insert alone.
func TestRegistrationCounterIgnoresATakenAddress(t *testing.T) {
	for _, tc := range []struct {
		name string
		on   string
	}{
		{"users.email", "INSERT INTO users"},
		{"identities.provider_subject", "INSERT INTO identities"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := testutil.ToFloat64(obs.UsersRegisteredCounter())

			_, err := Register(registerDeps(t, &registerStub{duplicateOn: tc.on}))(context.Background(), registerInput())
			require.Error(t, err)

			require.Zero(t, testutil.ToFloat64(obs.UsersRegisteredCounter())-before,
				"a sign-up refused as already taken registered nobody")
		})
	}
}

// TestRegistrationCounterCountsAnOIDCFirstSignIn covers the second way
// an account comes into existence. A provider sign-in for an address no
// user holds provisions a user and an identity, which is a registration
// whatever button the person pressed; instrumenting only the password
// form would report a fraction of sign-ups as the whole.
func TestRegistrationCounterCountsAnOIDCFirstSignIn(t *testing.T) {
	before := testutil.ToFloat64(obs.UsersRegisteredCounter())

	deps := registerDeps(t, &registerStub{})
	_, _, err := deps.resolveOIDCUser(context.Background(), oidcProvisionParams{
		Provider:       generated.IdentitiesProvider("github"),
		Subject:        "gh-metrics-subject",
		Email:          "oidc-new@example.test",
		DisplayName:    "First Arrival",
		AllowEmailLink: true,
	})
	require.NoError(t, err)

	require.Equal(t, 1.0, testutil.ToFloat64(obs.UsersRegisteredCounter())-before,
		"a provider sign-in that provisioned an account must count once")
}

// TestRegistrationCounterIgnoresAClosedInstance is the boundary on that
// path: with registration closed no row is written, so nothing may be
// counted.
func TestRegistrationCounterIgnoresAClosedInstance(t *testing.T) {
	before := testutil.ToFloat64(obs.UsersRegisteredCounter())

	deps := registerDeps(t, &registerStub{})
	deps.RegistrationOpen = false
	_, _, err := deps.resolveOIDCUser(context.Background(), oidcProvisionParams{
		Provider:       generated.IdentitiesProvider("github"),
		Subject:        "gh-closed-subject",
		Email:          "oidc-closed@example.test",
		DisplayName:    "Refused Arrival",
		AllowEmailLink: true,
	})
	require.Error(t, err)

	require.Zero(t, testutil.ToFloat64(obs.UsersRegisteredCounter())-before,
		"a refused sign-in provisioned nothing")
}
