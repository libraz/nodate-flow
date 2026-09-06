package admin

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
	sharedacl "github.com/libraz/nodate-flow/packages/go-shared/acl"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// allowlistFixture empties the allowlist and returns an actor to attribute
// the writes to. Every case starts from an empty table because entries are
// unique on (kind, value) across the whole instance.
func allowlistFixture(t *testing.T, db *sql.DB, q *generated.Queries) uint32 {
	t.Helper()

	_, err := db.ExecContext(context.Background(), "DELETE FROM oauth_signin_allowlist")
	require.NoError(t, err)
	return setupNewUser(t, q)
}

// addAllowlistInput builds the add body.
func addAllowlistInput(kind, value string, notes *string) *AddOAuthSignInAllowlistEntryInput {
	in := &AddOAuthSignInAllowlistEntryInput{}
	in.Body.Kind = kind
	in.Body.Value = value
	in.Body.Notes = notes
	return in
}

// TestAddAllowlistEntry_StoresTheNormalizedValue pins that an entry is
// written in the form the sign-in check compares against. entry_value is
// stored byte-exact, so an entry written in any other form is one that
// check can never match.
func TestAddAllowlistEntry_StoresTheNormalizedValue(t *testing.T) {
	db := requireSetupDB(t)
	q := generated.New(db)
	ctx := context.Background()

	actorID := allowlistFixture(t, db, q)
	sink := &recordingSink{}
	deps := Deps{DB: db, Queries: q, Audit: sink}
	actorCtx := authn.WithActor(ctx, actorID)

	cases := []struct {
		name  string
		kind  string
		value string
		want  string
	}{
		{"domain keeps neither case nor its leading at-sign", "domain", "  @Example.COM ", "example.com"},
		{"address is lower-cased and trimmed", "email", " Someone@Example.COM  ", "someone@example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := AddOAuthSignInAllowlistEntry(deps)(actorCtx, addAllowlistInput(tc.kind, tc.value, nil))
			require.NoError(t, err)
			assert.Equal(t, tc.want, out.Body.Value, "the response must show the value as it was stored")
			assert.True(t, out.Body.Enabled)

			pid, err := types.Parse(out.Body.ID)
			require.NoError(t, err)
			var stored string
			require.NoError(t, db.QueryRowContext(ctx,
				"SELECT entry_value FROM oauth_signin_allowlist WHERE public_id = ?", pid).Scan(&stored))
			assert.Equal(t, tc.want, stored, "the column holds the normalized value, not the submitted one")
		})
	}

	assert.Equal(t,
		[]string{"admin.oauth_signin_allowlist.add", "admin.oauth_signin_allowlist.add"},
		sink.actions(), "each add must be audited exactly once")
}

// TestAddAllowlistEntry_RevivesAWithdrawnEntry pins the reason the write
// is an upsert: a withdrawn entry keeps its claim on (kind, value), so an
// administrator adding the same domain back must get it back rather than a
// duplicate-key failure on an ordinary request.
func TestAddAllowlistEntry_RevivesAWithdrawnEntry(t *testing.T) {
	db := requireSetupDB(t)
	q := generated.New(db)
	ctx := context.Background()

	actorID := allowlistFixture(t, db, q)
	sink := &recordingSink{}
	deps := Deps{DB: db, Queries: q, Audit: sink}
	actorCtx := authn.WithActor(ctx, actorID)

	first, err := AddOAuthSignInAllowlistEntry(deps)(actorCtx, addAllowlistInput("domain", "example.com", nil))
	require.NoError(t, err)

	_, err = WithdrawOAuthSignInAllowlistEntry(deps)(actorCtx,
		&WithdrawOAuthSignInAllowlistEntryInput{EntryID: first.Body.ID})
	require.NoError(t, err)

	notes := "back for the migration"
	second, err := AddOAuthSignInAllowlistEntry(deps)(actorCtx, addAllowlistInput("domain", "  @EXAMPLE.com", &notes))
	require.NoError(t, err, "re-adding a withdrawn entry must succeed, not collide on its unique key")
	assert.True(t, second.Body.Enabled, "the revived entry must admit sign-ins again")
	require.NotNil(t, second.Body.Notes)
	assert.Equal(t, notes, *second.Body.Notes, "re-adding states the entry afresh")
	assert.NotEqual(t, first.Body.ID, second.Body.ID,
		"the revived entry answers to the id the response carries")

	var rows int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM oauth_signin_allowlist WHERE entry_kind = 'domain' AND entry_value = 'example.com'").Scan(&rows))
	assert.Equal(t, 1, rows, "reviving must reuse the row that holds the claim, not add a second one")

	live, err := q.ListEnabledOauthSigninAllowlistEntries(ctx)
	require.NoError(t, err)
	require.Len(t, live, 1)
	assert.Equal(t, "example.com", live[0].EntryValue, "the sign-in check must see the revived entry")
}

// TestWithdrawAllowlistEntry_AbsentIsNotFoundAndUnaudited pins that a
// withdrawal which changed nothing is reported as not found and leaves no
// audit entry: the trail may not claim something happened to a resource
// that it did not.
func TestWithdrawAllowlistEntry_AbsentIsNotFoundAndUnaudited(t *testing.T) {
	db := requireSetupDB(t)
	q := generated.New(db)
	ctx := context.Background()

	actorID := allowlistFixture(t, db, q)
	actorCtx := authn.WithActor(ctx, actorID)

	added, err := AddOAuthSignInAllowlistEntry(Deps{DB: db, Queries: q, Audit: &recordingSink{}})(
		actorCtx, addAllowlistInput("email", "someone@example.com", nil))
	require.NoError(t, err)

	// A sink that has seen nothing yet, so every assertion below is about
	// what the withdrawals recorded.
	sink := &recordingSink{}
	deps := Deps{DB: db, Queries: q, Audit: sink}

	cases := []struct {
		name    string
		entryID string
	}{
		{"an id that names nothing", types.New().String()},
		{"an id that is not a uuid", "not-a-uuid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := WithdrawOAuthSignInAllowlistEntry(deps)(actorCtx,
				&WithdrawOAuthSignInAllowlistEntryInput{EntryID: tc.entryID})

			var prob *handlerutil.ProblemDetails
			require.ErrorAs(t, err, &prob)
			assert.Equal(t, http.StatusNotFound, prob.Status)
			assert.Equal(t, "INSTANCE.OAUTH_ALLOWLIST.NOT_FOUND", prob.Type,
				"the refusal must name the allowlist entry, not some other missing resource")
			assert.Empty(t, sink.actions(), "no audit entry may claim a withdrawal this call did not perform")
		})
	}

	// The same entry twice: the second call changes nothing, so it reads
	// exactly like an entry that was never there.
	_, err = WithdrawOAuthSignInAllowlistEntry(deps)(actorCtx,
		&WithdrawOAuthSignInAllowlistEntryInput{EntryID: added.Body.ID})
	require.NoError(t, err)
	assert.Equal(t, []string{"admin.oauth_signin_allowlist.withdraw"}, sink.actions(),
		"a withdrawal that landed must be audited exactly once")

	_, err = WithdrawOAuthSignInAllowlistEntry(deps)(actorCtx,
		&WithdrawOAuthSignInAllowlistEntryInput{EntryID: added.Body.ID})
	var prob *handlerutil.ProblemDetails
	require.ErrorAs(t, err, &prob)
	assert.Equal(t, http.StatusNotFound, prob.Status)
	assert.Equal(t, "INSTANCE.OAUTH_ALLOWLIST.NOT_FOUND", prob.Type,
		"an already-withdrawn entry reads as the same missing entry")
	assert.Len(t, sink.actions(), 1, "the repeated withdrawal must add nothing to the trail")

	live, err := q.ListEnabledOauthSigninAllowlistEntries(ctx)
	require.NoError(t, err)
	assert.Empty(t, live, "a withdrawn entry must stop admitting sign-ins")
}

// TestAddAllowlistEntry_MalformedValueIsRefused pins the shape rules. A
// value that cannot match anything is a mistake the administrator hears
// about now rather than discovering as a sign-in that never works, so the
// refusal happens before any statement runs -- which is why this case
// needs no database.
func TestAddAllowlistEntry_MalformedValueIsRefused(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	handler := AddOAuthSignInAllowlistEntry(Deps{Audit: sink})
	ctx := authn.WithActor(context.Background(), 7)

	cases := []struct {
		name  string
		kind  string
		value string
	}{
		{"an address where a domain belongs", "domain", "someone@example.com"},
		{"a domain where an address belongs", "email", "example.com"},
		{"an address with no local part", "email", "@example.com"},
		{"an address with no domain part", "email", "someone@"},
		{"a value that is only whitespace", "domain", "   "},
		{"a value the latin1 column cannot hold", "domain", "例え.example"},
		{"a kind the column does not define", "ip", "203.0.113.7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := handler(ctx, addAllowlistInput(tc.kind, tc.value, nil))

			var prob *handlerutil.ProblemDetails
			require.ErrorAs(t, err, &prob)
			assert.Equal(t, "VALIDATION.BODY.FIELD_INVALID", prob.Type)
		})
	}
	assert.Empty(t, sink.actions(), "a refused add must leave no audit entry")
}

// TestAllowlistRoutes_RefuseANonAdmin pins that the three operations are
// registered by [Register], the registrar the router mounts behind the
// instance-admin gate, and so are unreachable for a caller who is signed
// in but not an administrator.
func TestAllowlistRoutes_RefuseANonAdmin(t *testing.T) {
	t.Parallel()

	denied := 0
	gate := sharedacl.RequireInstanceAdmin(sharedacl.Config{
		IsInstanceAdmin: func(context.Context, uint32) (bool, error) { return false, nil },
		ExtractActor:    func(*http.Request) (uint32, bool) { return 7, true },
		WriteError: func(w http.ResponseWriter, _ *http.Request, status int, _, _ string) {
			denied++
			w.WriteHeader(status)
		},
	})

	r := chi.NewRouter()
	r.Group(func(sub chi.Router) {
		sub.Use(gate)
		// Nil queries: reaching a handler at all is the failure this test
		// is looking for, and the gate is what must stop it.
		Register(humachi.New(sub, huma.DefaultConfig("test", "1.0.0")), Deps{})
	})

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/admin/oauth-signin-allowlist"},
		{http.MethodPost, "/admin/oauth-signin-allowlist"},
		{http.MethodDelete, "/admin/oauth-signin-allowlist/" + types.New().String()},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, http.NoBody))
			assert.Equal(t, http.StatusForbidden, rec.Code,
				"a signed-in non-administrator must not reach the allowlist")
		})
	}
	assert.Equal(t, len(cases), denied, "every call must be refused by the gate, not by a handler")
}
