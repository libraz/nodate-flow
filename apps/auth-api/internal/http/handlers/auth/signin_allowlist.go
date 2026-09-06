package auth

import (
	"context"
	"log/slog"
	"strings"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
)

// isSignInEmailAllowed reports whether the given verified email may sign
// in under the instance's opt-in OAuth/OIDC allowlist. It is the single
// enforcement point: the three OIDC callbacks decide nothing themselves.
//
// The effective allowlist is the union of the configured entries
// (OAuthAllowedDomains / OAuthAllowedEmails) and the enabled rows of
// oauth_signin_allowlist. The union is what makes the environment a floor
// an operator can always set: a row adds to the allowlist and can never
// take away what the environment grants, and adding the first row does
// not silently discard the configured entries.
//
// An empty union means open. With nothing in the environment and no
// enabled row, every verified address may sign in.
//
// The read runs per sign-in and is deliberately not cached: an entry an
// administrator withdraws stops admitting on the next attempt rather than
// at the end of some interval.
//
// A failed read refuses the sign-in, including on an instance whose
// environment names nothing. An allowlist maintained through the
// administrator screens leaves the environment empty, so reading "no
// environment entry" as "no allowlist to enforce" would turn a
// locked-down instance open at exactly the moment its database is
// unreachable. Refusing costs an instance that opted into nothing
// nothing: resolving the sign-in to a user needs the same database a few
// lines later, so no otherwise-reachable sign-in is lost here. The
// refusal surfaces as an internal error rather than
// AUTH.OIDC.DOMAIN_NOT_ALLOWED so an outage is never reported to a user
// as a rejected address.
func (d Deps) isSignInEmailAllowed(ctx context.Context, email string) (bool, error) {
	rows, err := d.Queries.ListEnabledOauthSigninAllowlistEntries(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "sign-in allowlist read failed; refusing sign-in",
			slog.String("err", err.Error()))
		return false, httpErr(apierrors.InternalUnexpected)
	}
	domains, emails := signInAllowlistUnion(d.OAuthAllowedDomains, d.OAuthAllowedEmails, rows)
	return signInAllowlistAdmits(domains, emails, email), nil
}

// signInAllowlistUnion merges the configured entries with the enabled
// rows into the two lists the match runs over.
//
// Both sources go through NormalizeSignInAllowlistEntry. entry_value is
// stored latin1_bin, i.e. byte-exact, so a row written as "Example.COM"
// or "@example.com" through any path is only comparable to an address
// after Go normalizes it; the environment entries are normalized at load
// and normalizing them again is idempotent. Fresh slices are built rather
// than appended onto the caller's, so concurrent sign-ins never share a
// backing array.
//
// An entry of an unknown kind is ignored: a value whose kind cannot be
// read is a value whose meaning is unknown, and admitting on it would
// widen access on a guess.
func signInAllowlistUnion(
	envDomains, envEmails []string,
	rows []generated.ListEnabledOauthSigninAllowlistEntriesRow,
) (domains, emails []string) {
	domains = make([]string, 0, len(envDomains)+len(rows))
	emails = make([]string, 0, len(envEmails)+len(rows))
	for _, raw := range envDomains {
		if v := NormalizeSignInAllowlistEntry(raw, true); v != "" {
			domains = append(domains, v)
		}
	}
	for _, raw := range envEmails {
		if v := NormalizeSignInAllowlistEntry(raw, false); v != "" {
			emails = append(emails, v)
		}
	}
	for _, row := range rows {
		switch row.EntryKind {
		case generated.OauthSigninAllowlistEntryKindDomain:
			if v := NormalizeSignInAllowlistEntry(row.EntryValue, true); v != "" {
				domains = append(domains, v)
			}
		case generated.OauthSigninAllowlistEntryKindEmail:
			if v := NormalizeSignInAllowlistEntry(row.EntryValue, false); v != "" {
				emails = append(emails, v)
			}
		default:
		}
	}
	return domains, emails
}

// NormalizeSignInAllowlistEntry lower-cases and trims one allowlist entry
// and, for a domain, strips a single leading "@" so "example.com" and
// "@example.com" are the same entry. An entry carrying no value comes
// back empty and the caller drops it: kept in the list it would count as
// an active allowlist while matching only a malformed address.
//
// It is exported because the administrator's write path must normalize an
// entry exactly the way this check does. entry_value is stored byte-exact,
// so a row written under any other rule is a row this check can never
// match, and the divergence would surface only as a sign-in that never
// works. One definition is what keeps the two sides in step.
func NormalizeSignInAllowlistEntry(raw string, isDomain bool) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	if isDomain {
		v = strings.TrimPrefix(v, "@")
	}
	return v
}

// signInAllowlistAdmits reports whether an address is admitted by the
// effective allowlist.
//
// An empty allowlist admits everyone. Otherwise the address is admitted
// when its lower-cased form equals an email entry, or when the part after
// its final "@" equals a domain entry. An address with no "@", or one
// ending in "@", is refused once the allowlist is non-empty. Domain
// matching is exact: a subdomain is not the apex domain.
func signInAllowlistAdmits(domains, emails []string, email string) bool {
	if len(domains) == 0 && len(emails) == 0 {
		return true
	}
	normalized := strings.ToLower(strings.TrimSpace(email))
	for _, allowed := range emails {
		if normalized == allowed {
			return true
		}
	}
	at := strings.LastIndex(normalized, "@")
	if at < 0 || at == len(normalized)-1 {
		return false
	}
	domain := normalized[at+1:]
	for _, allowed := range domains {
		if domain == allowed {
			return true
		}
	}
	return false
}
