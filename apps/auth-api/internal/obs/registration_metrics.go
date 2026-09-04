package obs

import (
	"github.com/prometheus/client_golang/prometheus"
)

// usersRegisteredTotal counts accounts created by a completed
// registration: a users row that exists together with the identity the
// account signs in with. Both routes to one are counted — a password
// sign-up and a first sign-in through an OIDC provider that no
// existing account holds the address for.
//
// A row an admin provisions while adding a member by address is not
// counted: nobody signed up for it, it carries no identity, and the
// address was never asked for or verified. Counting it would let a
// roster import read as a burst of sign-ups.
//
// It is declared here rather than in packages/go-shared/obs because
// this service is the only one that creates users; a metric name may
// exist once in the repository, and a second declaration elsewhere
// would be two collectors under one name.
//
// It carries no labels. Everything that distinguishes one registration
// from another — the address, the account, the workspace it joins —
// identifies a person, and /metrics is served without authentication.
// The sign-in method would be bounded, but it splits a rate the
// dashboard reads as a single number and answers a question the audit
// trail already answers per account.
var usersRegisteredTotal = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "nf_users_registered_total",
		Help: "Total user accounts created by a completed registration.",
	},
)

func init() {
	prometheus.MustRegister(usersRegisteredTotal)
}

// IncUsersRegistered records one completed registration.
//
// Call it after the account is whole and its rows are committed, never
// before the insert: a sign-up that lost the uniq_users_email race, or
// whose identity row failed, was answered with an error and did not
// register anyone.
func IncUsersRegistered() {
	usersRegisteredTotal.Inc()
}

// UsersRegisteredCounter returns the registration counter. Exposed so
// tests in adjacent packages can call testutil.ToFloat64 on it without
// taking a dependency on the unexported collector.
func UsersRegisteredCounter() prometheus.Counter {
	return usersRegisteredTotal
}
