package auth

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// GET /me reports whether the account holds a local password, so the web
// apps can hide the password and TOTP sections from an OIDC-only account.
func TestMe_HasPassword(t *testing.T) {
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	ctx := context.Background()

	// b2NewUser leaves the hash NULL: an identity row without a password.
	noPwUID, _, _ := b2NewUser(t, deps.Queries)
	out, err := Me(deps)(authn.WithActor(ctx, noPwUID), &struct{}{})
	require.NoError(t, err)
	assert.False(t, out.Body.HasPassword, "an identity without a hash is not a password account")

	pwUID, _, _ := b2NewUser(t, deps.Queries)
	_, err = db.ExecContext(ctx,
		"UPDATE identities SET password_hash = ? WHERE user_id = ? AND provider = 'local'",
		"$2a$10$abcdefghijklmnopqrstuuabcdefghijklmnopqrstuvwxyz01234", pwUID)
	require.NoError(t, err)
	out, err = Me(deps)(authn.WithActor(ctx, pwUID), &struct{}{})
	require.NoError(t, err)
	assert.True(t, out.Body.HasPassword)

	// An account that only ever signed in through a provider has no local row.
	pub := types.New()
	id, err := deps.Queries.RegisterUser(ctx, generated.RegisterUserParams{
		PublicID:        pub,
		Email:           "oidc-" + pub.String() + "@example.test",
		DisplayName:     "OIDC User",
		Locale:          "en",
		Timezone:        "UTC",
		Country:         sql.NullString{String: "US", Valid: true},
		ThemePreference: generated.UsersThemePreferenceSystem,
	})
	require.NoError(t, err)
	out, err = Me(deps)(authn.WithActor(ctx, uint32(id)), &struct{}{}) //#nosec G115 -- test fixture id
	require.NoError(t, err)
	assert.False(t, out.Body.HasPassword)
}
