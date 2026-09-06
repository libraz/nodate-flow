package admin

import (
	"context"
	"database/sql"
	"strings"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	authhandlers "github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/auth"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// allowlistEntryNotFound is the answer to an entry id that names no live
// entry: it was never there, it has already been withdrawn, or it is not a
// well-formed identifier. The three are one answer on purpose — an
// administrator acting on a stale list needs to know the id no longer
// stands, not which of the ways it stopped standing.
var allowlistEntryNotFound = apierrors.InstanceOauthAllowlistNotFound

// ListOAuthSignInAllowlist handles GET /admin/oauth-signin-allowlist.
// Returns a paginated list of every entry, withdrawn ones included: a
// withdrawn entry keeps its claim on its (kind, value) pair and can be
// brought back, so the administrator has to be able to see it.
func ListOAuthSignInAllowlist(deps Deps) func(context.Context, *ListOAuthSignInAllowlistInput) (*ListOAuthSignInAllowlistOutput, error) {
	return func(ctx context.Context, in *ListOAuthSignInAllowlistInput) (*ListOAuthSignInAllowlistOutput, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		rows, err := deps.Queries.ListOauthSigninAllowlistEntries(ctx, generated.ListOauthSigninAllowlistEntriesParams{
			Limit:  limit,
			Offset: in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListOAuthSignInAllowlistOutput{}
		out.Body.Items = make([]OAuthSignInAllowlistEntry, len(rows))
		for i, r := range rows {
			out.Body.Items[i] = listRowToOAuthSignInAllowlistEntry(r)
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// AddOAuthSignInAllowlistEntry handles POST /admin/oauth-signin-allowlist.
// It adds an entry, or revives the withdrawn one that already holds this
// (kind, value) pair, and answers with the entry as it now stands.
func AddOAuthSignInAllowlistEntry(deps Deps) func(context.Context, *AddOAuthSignInAllowlistEntryInput) (*AddOAuthSignInAllowlistEntryOutput, error) {
	return func(ctx context.Context, in *AddOAuthSignInAllowlistEntryInput) (*AddOAuthSignInAllowlistEntryOutput, error) {
		actorID, _ := authn.ActorFromContext(ctx)

		kind, err := parseAllowlistEntryKind(in.Body.Kind)
		if err != nil {
			return nil, err
		}
		value, err := normalizeAllowlistEntryValue(in.Body.Value, kind)
		if err != nil {
			return nil, err
		}

		var notes sql.NullString
		if in.Body.Notes != nil && *in.Body.Notes != "" {
			notes = sql.NullString{String: *in.Body.Notes, Valid: true}
		}

		// The public id is minted here rather than read back: the upsert
		// writes it onto the row it revives too, so it addresses the entry
		// either way.
		pid := types.New()
		if err := deps.Queries.UpsertOauthSigninAllowlistEntry(ctx, generated.UpsertOauthSigninAllowlistEntryParams{
			PublicID:      pid,
			AddedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
			EntryKind:     kind,
			EntryValue:    value,
			Notes:         notes,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Recorded before the read-back, and without consulting an affected
		// row count: an upsert always leaves the entry in the state the
		// request asked for, so the write has taken effect whether the row
		// was created, revived, or already carried these values.
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "admin.oauth_signin_allowlist.add",
			ActorID:      actorID,
			ResourceType: "oauth_signin_allowlist_entry",
			ResourceID:   pid.String(),
		})

		row, err := deps.Queries.FindOauthSigninAllowlistEntry(ctx, pid)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, allowlistEntryNotFound, apierrors.InternalUnexpected))
		}

		out := &AddOAuthSignInAllowlistEntryOutput{Body: rowToOAuthSignInAllowlistEntry(row)}
		return out, nil
	}
}

// WithdrawOAuthSignInAllowlistEntry handles
// DELETE /admin/oauth-signin-allowlist/{entryId}. It clears the entry's
// enabled flag; the row stays, since it is what holds the (kind, value)
// claim that lets the same entry be added back.
func WithdrawOAuthSignInAllowlistEntry(deps Deps) func(context.Context, *WithdrawOAuthSignInAllowlistEntryInput) (*WithdrawOAuthSignInAllowlistEntryOutput, error) {
	return func(ctx context.Context, in *WithdrawOAuthSignInAllowlistEntryInput) (*WithdrawOAuthSignInAllowlistEntryOutput, error) {
		actorID, _ := authn.ActorFromContext(ctx)

		pid, err := types.Parse(in.EntryID)
		if err != nil {
			return nil, httpErr(allowlistEntryNotFound)
		}

		affected, err := deps.Queries.WithdrawOauthSigninAllowlistEntry(ctx, pid)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		// The statement matches only a live entry, so nothing changed means
		// there is no live entry by that id -- never withdrawn, or already
		// withdrawn by another path. Answer as if it were never there and
		// record no audit entry, so the trail never claims a withdrawal that
		// did not happen.
		if affected == 0 {
			return nil, httpErr(allowlistEntryNotFound)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "admin.oauth_signin_allowlist.withdraw",
			ActorID:      actorID,
			ResourceType: "oauth_signin_allowlist_entry",
			ResourceID:   pid.String(),
		})

		out := &WithdrawOAuthSignInAllowlistEntryOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// parseAllowlistEntryKind maps the submitted kind onto the column's enum.
// The schema declares the two accepted values, so an unknown kind only
// reaches here on a call that bypassed validation; it is refused rather
// than guessed, because the kind is what decides how the value is matched.
func parseAllowlistEntryKind(raw string) (generated.OauthSigninAllowlistEntryKind, error) {
	switch generated.OauthSigninAllowlistEntryKind(raw) {
	case generated.OauthSigninAllowlistEntryKindDomain:
		return generated.OauthSigninAllowlistEntryKindDomain, nil
	case generated.OauthSigninAllowlistEntryKindEmail:
		return generated.OauthSigninAllowlistEntryKindEmail, nil
	default:
		return "", httpErr(apierrors.ValidationBodyFieldInvalid)
	}
}

// normalizeAllowlistEntryValue puts the submitted value in the exact form
// the sign-in check compares against, and refuses one that could never
// admit anybody.
//
// Normalization is [authhandlers.NormalizeSignInAllowlistEntry], the
// function the check itself uses: entry_value is stored byte-exact, so an
// entry written under any other rule is an entry the check can never
// match, and the divergence would surface only as a sign-in that never
// works.
//
// The shape rules follow from how the value is matched. A domain entry is
// compared for equality against the part after an address's final "@", so
// it has to be something that part could be: a host name, by the rule in
// [isAllowlistDomain]. That already rules out the "@" of an address
// entered where a domain belongs. An email entry is compared against a
// whole address, so it needs a local part and a domain part around its
// final "@", each holding to the same rule. Any of these mistakes is
// worth reporting now rather than leaving as a sign-in that silently
// never succeeds.
func normalizeAllowlistEntryValue(raw string, kind generated.OauthSigninAllowlistEntryKind) (string, error) {
	value := authhandlers.NormalizeSignInAllowlistEntry(raw, kind == generated.OauthSigninAllowlistEntryKindDomain)
	if value == "" {
		return "", httpErr(apierrors.ValidationBodyFieldInvalid)
	}
	// entry_value is a latin1 column. A rune outside that range is refused
	// by the driver, so it is refused here instead, where the answer can
	// name the field that is wrong.
	for _, r := range value {
		if r > 0xFF {
			return "", httpErr(apierrors.ValidationBodyFieldInvalid)
		}
	}
	switch kind {
	case generated.OauthSigninAllowlistEntryKindDomain:
		if !isAllowlistDomain(value) {
			return "", httpErr(apierrors.ValidationBodyFieldInvalid)
		}
	case generated.OauthSigninAllowlistEntryKindEmail:
		// The final "@" is the split the check itself uses. at <= 0 covers
		// both an address with no "@" and one with no local part.
		at := strings.LastIndex(value, "@")
		if at <= 0 || !isAllowlistLocalPart(value[:at]) || !isAllowlistDomain(value[at+1:]) {
			return "", httpErr(apierrors.ValidationBodyFieldInvalid)
		}
	default:
		return "", httpErr(apierrors.ValidationBodyFieldInvalid)
	}
	return value, nil
}

// isAllowlistDomain reports whether value could be the domain part of an
// address that reaches the sign-in check.
//
// The rule is the host name one: dot-separated labels of ASCII letters,
// digits and hyphens, no label empty, and no label opening or closing on a
// hyphen. Two labels are required, so a bare host name like "localhost" is
// refused -- the addresses this list is matched against come from an
// OAuth/OIDC provider and their domain part is always a registered name.
//
// Letters means ASCII letters, which by this point in normalization means
// lower-case ones. An internationalized domain reaches an address in its
// "xn--" form, so that is the form an entry has to be written in for the
// comparison to ever come out equal.
//
// No length ceiling is imposed here: entry_value is a VARCHAR(255) and the
// column reports anything longer. What this answers is whether the value
// has the shape of a domain at all.
func isAllowlistDomain(value string) bool {
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		// Indexing by byte is enough to reject every non-ASCII rune: each
		// of its bytes is >= 0x80 and so falls to the default.
		for i := 0; i < len(label); i++ {
			switch c := label[i]; {
			case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			default:
				return false
			}
		}
	}
	return true
}

// localPartPunctuation is the punctuation an address's local part may
// carry unquoted, per the dot-atom form: these characters plus letters and
// digits, with "." separating non-empty pieces.
//
// A quoted local part -- the form that would let an address hold a space
// -- is not accepted. Nothing produces one here: the value has already
// been lower-cased, which a quoted local part does not survive, and no
// OAuth/OIDC provider issues one.
const localPartPunctuation = "!#$%&'*+-/=?^_`{|}~"

// isAllowlistLocalPart reports whether value could be the part before an
// address's final "@".
func isAllowlistLocalPart(value string) bool {
	if value == "" || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") ||
		strings.Contains(value, "..") {
		return false
	}
	for i := 0; i < len(value); i++ {
		switch c := value[i]; {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.':
		case strings.IndexByte(localPartPunctuation, c) >= 0:
		default:
			return false
		}
	}
	return true
}
