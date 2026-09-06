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
// entry. The catalog carries no code for this table, so the closest
// instance-level not-found stands in; the status and the "the id you named
// is not there" meaning are what the caller branches on.
var allowlistEntryNotFound = apierrors.InstanceUserNotFound

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
// compared against the part after an address's final "@" and so contains
// no "@" of its own -- the leading one is already stripped, and another
// means an address was entered where a domain belongs. An email entry is
// compared against a whole address and so needs a local part and a domain
// part around its final "@". Either mistake is worth reporting now rather
// than leaving as a sign-in that silently never succeeds.
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
		if strings.Contains(value, "@") {
			return "", httpErr(apierrors.ValidationBodyFieldInvalid)
		}
	case generated.OauthSigninAllowlistEntryKindEmail:
		at := strings.LastIndex(value, "@")
		if at <= 0 || at == len(value)-1 {
			return "", httpErr(apierrors.ValidationBodyFieldInvalid)
		}
	default:
		return "", httpErr(apierrors.ValidationBodyFieldInvalid)
	}
	return value, nil
}
