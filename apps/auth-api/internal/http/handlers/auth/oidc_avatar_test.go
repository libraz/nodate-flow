package auth

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internauth "github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
)

func TestValidOIDCAvatarURL(t *testing.T) {
	t.Parallel()
	const prefix = "https://example.test/"
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"https", "https://lh3.example.test/a/photo.jpg", true},
		{"uppercase scheme", "HTTPS://lh3.example.test/a.jpg", true},
		{"http rejected", "http://lh3.example.test/a.jpg", false},
		{"empty", "", false},
		{"no host", "https:///x", false},
		{"javascript scheme", "javascript:alert(1)", false},
		{"data scheme", "data:image/png;base64,AAAA", false},
		{"exactly 2048", prefix + strings.Repeat("a", 2048-len(prefix)), true},
		{"2049", prefix + strings.Repeat("a", 2049-len(prefix)), false},
		{"unparsable", "https://exa mple.test/%zz", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, validOIDCAvatarURL(tc.raw))
		})
	}
}

// userAvatar reads the avatar columns of a user row.
func userAvatar(t *testing.T, db *sql.DB, uid uint32) (sql.NullString, sql.NullInt64) {
	t.Helper()
	var url sql.NullString
	var obj sql.NullInt64
	require.NoError(t, db.QueryRowContext(context.Background(),
		"SELECT avatar_url, avatar_storage_object_id FROM users WHERE id = ?", uid).Scan(&url, &obj))
	return url, obj
}

// oidcGoogleSignIn runs a Google callback for the given claims.
func oidcGoogleSignIn(t *testing.T, deps Deps, claims *internauth.GoogleClaims) error {
	t.Helper()
	deps.OIDC = &fakeGoogleExchanger{claims: claims}
	state, stateCookie := signedGoogleState(t, &deps)
	_, err := OIDCGoogleCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code: "auth-code", State: state, StateCookie: stateCookie,
	})
	return err
}

// oidcGithubLinkSignIn signs in through GitHub for email with a fresh
// subject and the given avatar, returning the subject used.
func oidcGithubLinkSignIn(t *testing.T, deps Deps, email, avatar string) string {
	t.Helper()
	sub := "gh-avatar-" + types.New().String()
	deps.OIDCGithub = &fakeGithubExchanger{claims: &internauth.GithubClaims{
		Sub: sub, Login: "avatar-user", Name: "Avatar User", Email: email, AvatarURL: avatar,
	}}
	state, stateCookie := signedGithubState(t, &deps)
	_, err := OIDCGithubCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code: "auth-code", State: state, StateCookie: stateCookie,
	})
	require.NoError(t, err)
	return sub
}

func TestOIDCAvatar_GoogleNewUserSeedsPicture(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	deps.RegistrationOpen = true

	sub := "g-avatar-" + types.New().String()
	const pic = "https://lh3.example.test/a/new-user.jpg"
	require.NoError(t, oidcGoogleSignIn(t, deps, &internauth.GoogleClaims{
		Sub: sub, Email: "avatar-" + sub + "@example.test", EmailVerified: true,
		Name: "Avatar", Picture: pic,
	}))

	uid, ok := findIdentityUserID(t, db, "google", sub)
	require.True(t, ok)
	url, obj := userAvatar(t, db, uid)
	assert.Equal(t, sql.NullString{String: pic, Valid: true}, url)
	assert.False(t, obj.Valid)
}

func TestOIDCAvatar_GithubLinkSeedsEmptyAccount(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	deps.RegistrationOpen = true

	uid, _, email := b2NewUser(t, deps.Queries)
	const pic = "https://avatars.example.test/u/1?v=4"
	oidcGithubLinkSignIn(t, deps, email, pic)

	url, _ := userAvatar(t, db, uid)
	assert.Equal(t, sql.NullString{String: pic, Valid: true}, url)
}

func TestOIDCAvatar_LinkKeepsExistingAvatarURL(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	deps.RegistrationOpen = true

	uid, _, email := b2NewUser(t, deps.Queries)
	const existing = "https://cdn.example.test/mine.png"
	_, err := db.ExecContext(context.Background(), "UPDATE users SET avatar_url = ? WHERE id = ?", existing, uid)
	require.NoError(t, err)

	oidcGithubLinkSignIn(t, deps, email, "https://avatars.example.test/u/2?v=4")

	url, _ := userAvatar(t, db, uid)
	assert.Equal(t, sql.NullString{String: existing, Valid: true}, url)
}

func TestOIDCAvatar_LinkKeepsUploadedAvatar(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	deps.RegistrationOpen = true

	uid, _, email := b2NewUser(t, deps.Queries)
	pub := types.New()
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO storage_objects (public_id, owner_user_id, sha256, byte_size, content_type, storage_key, ref_count)
		 VALUES (?, ?, UNHEX(SHA2(?, 256)), 1, 'image/png', ?, 1)`,
		pub, uid, pub.String(), "user/"+pub.String()+"/avatar")
	require.NoError(t, err)
	objID, err := res.LastInsertId()
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(),
		"UPDATE users SET avatar_storage_object_id = ? WHERE id = ?", objID, uid)
	require.NoError(t, err)

	oidcGithubLinkSignIn(t, deps, email, "https://avatars.example.test/u/3?v=4")

	url, obj := userAvatar(t, db, uid)
	assert.False(t, url.Valid, "an uploaded avatar must not gain an external URL")
	assert.Equal(t, sql.NullInt64{Int64: objID, Valid: true}, obj)
}

func TestOIDCAvatar_ReturningSignInDoesNotReseed(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	deps.RegistrationOpen = true

	sub := "g-avatar-" + types.New().String()
	claims := &internauth.GoogleClaims{
		Sub: sub, Email: "avatar-" + sub + "@example.test", EmailVerified: true,
		Name: "Avatar", Picture: "https://lh3.example.test/a/first.jpg",
	}
	require.NoError(t, oidcGoogleSignIn(t, deps, claims))
	uid, ok := findIdentityUserID(t, db, "google", sub)
	require.True(t, ok)

	_, err := db.ExecContext(context.Background(), "UPDATE users SET avatar_url = NULL WHERE id = ?", uid)
	require.NoError(t, err)

	claims.Picture = "https://lh3.example.test/a/second.jpg"
	require.NoError(t, oidcGoogleSignIn(t, deps, claims))

	url, _ := userAvatar(t, db, uid)
	assert.False(t, url.Valid, "a returning sign-in must not seed the avatar")
}

func TestOIDCAvatar_NonHTTPSPictureIgnored(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	deps.RegistrationOpen = true

	sub := "g-avatar-" + types.New().String()
	require.NoError(t, oidcGoogleSignIn(t, deps, &internauth.GoogleClaims{
		Sub: sub, Email: "avatar-" + sub + "@example.test", EmailVerified: true,
		Name: "Avatar", Picture: "http://lh3.example.test/a/insecure.jpg",
	}))

	uid, ok := findIdentityUserID(t, db, "google", sub)
	require.True(t, ok, "sign-in must still succeed")
	url, _ := userAvatar(t, db, uid)
	assert.False(t, url.Valid)
}
