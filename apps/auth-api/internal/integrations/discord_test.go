package integrations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Constructor ---

func TestNewDiscord_RequiresAllThreeFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		id          string
		secret      string
		redirectURI string
	}{
		{"all empty", "", "", ""},
		{"id empty", "", "secret", "https://example.com/cb"},
		{"secret empty", "id", "", "https://example.com/cb"},
		{"redirect empty", "id", "secret", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewDiscord(tc.id, tc.secret, tc.redirectURI)
			require.ErrorIs(t, err, ErrNotConfigured,
				"Discord must be marked unconfigured until client id, secret, AND redirect URI are all set")
		})
	}
}

func TestNewDiscord_ValidCredentialsSucceed(t *testing.T) {
	t.Parallel()
	p, err := NewDiscord("client-id", "client-secret", "https://example.com/oauth/callback/discord")
	require.NoError(t, err)
	assert.Equal(t, "discord", p.Name())
}

// --- AuthURL contract ---

func TestDiscord_AuthURL_ContainsRequiredParams(t *testing.T) {
	t.Parallel()
	p, _ := NewDiscord("my-id", "my-secret", "https://x/cb")
	got := p.AuthURL("state-xyz", "https://example.com/callback")
	assert.Contains(t, got, "client_id=my-id")
	assert.Contains(t, got, "state=state-xyz")
	assert.Contains(t, got, "response_type=code")
	assert.Contains(t, got, "scope=identify+guilds",
		"Discord must request the identify + guilds scopes required by the presence gateway")
	assert.Contains(t, got, "redirect_uri=",
		"redirect_uri must be forwarded so Discord echoes it back on the exchange")
}

// --- Exchange happy path ---

func TestDiscord_Exchange_HappyPath_GlobalName(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/oauth2/token":
			require.Equal(t, http.MethodPost, r.Method)
			require.NoError(t, r.ParseForm())                                    //#nosec G120 -- httptest receives small fixed test bodies
			assert.Equal(t, "authorization_code", r.PostFormValue("grant_type")) //#nosec G120 -- httptest receives small fixed test bodies.
			assert.Equal(t, "test-code", r.PostFormValue("code"))                //#nosec G120 -- httptest receives small fixed test bodies.
			assert.Equal(t, "my-id", r.PostFormValue("client_id"))               //#nosec G120 -- httptest receives small fixed test bodies.
			assert.Equal(t, "my-secret", r.PostFormValue("client_secret"))       //#nosec G120 -- httptest receives small fixed test bodies.
			_, _ = w.Write([]byte(`{
				"access_token":"dtok_abc",
				"refresh_token":"drt_xyz",
				"expires_in":604800,
				"scope":"identify guilds",
				"token_type":"Bearer"
			}`))
		case "/api/users/@me":
			assert.Equal(t, "Bearer dtok_abc", r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{
				"id":"123456789012345678",
				"username":"oldname",
				"discriminator":"0",
				"global_name":"Cool Person"
			}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	p := &DiscordProvider{
		clientID:     "my-id",
		clientSecret: "my-secret",
		redirectURI:  "https://x/cb",
		hc:           rewriteClient(srv.URL, map[string]string{"https://discord.com": ""}),
	}
	tok, acc, err := p.Exchange(context.Background(), "test-code", "https://x/cb")
	require.NoError(t, err)
	assert.Equal(t, "dtok_abc", tok.AccessToken)
	assert.Equal(t, "drt_xyz", tok.RefreshToken,
		"Discord must surface the refresh token for the background refresher")
	assert.False(t, tok.ExpiresAt.IsZero(),
		"Discord tokens must carry an expiry derived from expires_in")
	assert.Contains(t, tok.Scopes, "identify")
	assert.Contains(t, tok.Scopes, "guilds")

	assert.Equal(t, "123456789012345678", acc.ExternalID,
		"ExternalID must equal the Discord snowflake")
	assert.Equal(t, "Cool Person", acc.Label,
		"label must prefer the modern global_name over username#discriminator")
	require.NotNil(t, acc.Metadata, "Discord must populate metadata for the presence gateway")

	var meta map[string]any
	require.NoError(t, json.Unmarshal(acc.Metadata, &meta))
	assert.Equal(t, "123456789012345678", meta["external_user_id"],
		"metadata.external_user_id must equal the snowflake for JSON_EXTRACT lookups")
	verifiedAt, ok := meta["verified_at"].(string)
	require.True(t, ok, "metadata.verified_at must be a string")
	assert.NotEmpty(t, verifiedAt, "metadata.verified_at must be set to the consent timestamp")
	// RFC3339 sanity: must end in Z (UTC) and contain a T separator.
	assert.True(t, strings.HasSuffix(verifiedAt, "Z"), "verified_at must be RFC3339 UTC")
	assert.Contains(t, verifiedAt, "T")
}

func TestDiscord_Exchange_FallsBackToUsernameWhenNoGlobalName(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/oauth2/token":
			_, _ = w.Write([]byte(`{
				"access_token":"dtok",
				"refresh_token":"drt",
				"expires_in":3600,
				"scope":"identify guilds"
			}`))
		case "/api/users/@me":
			_, _ = w.Write([]byte(`{
				"id":"42",
				"username":"legacy",
				"discriminator":"1337",
				"global_name":""
			}`))
		}
	}))
	defer srv.Close()

	p := &DiscordProvider{
		clientID: "id", clientSecret: "secret", redirectURI: "https://x/cb",
		hc: rewriteClient(srv.URL, map[string]string{"https://discord.com": ""}),
	}
	_, acc, err := p.Exchange(context.Background(), "code", "https://x/cb")
	require.NoError(t, err)
	assert.Equal(t, "legacy#1337", acc.Label,
		"with no global_name and a real discriminator, fall back to username#discriminator")
}

func TestDiscord_Exchange_FallsBackToBareUsernameWhenDiscriminatorIsZero(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/oauth2/token":
			_, _ = w.Write([]byte(`{
				"access_token":"dtok",
				"refresh_token":"drt",
				"expires_in":3600,
				"scope":"identify"
			}`))
		case "/api/users/@me":
			_, _ = w.Write([]byte(`{
				"id":"7",
				"username":"newstyle",
				"discriminator":"0",
				"global_name":""
			}`))
		}
	}))
	defer srv.Close()

	p := &DiscordProvider{
		clientID: "id", clientSecret: "secret", redirectURI: "https://x/cb",
		hc: rewriteClient(srv.URL, map[string]string{"https://discord.com": ""}),
	}
	_, acc, err := p.Exchange(context.Background(), "code", "https://x/cb")
	require.NoError(t, err)
	assert.Equal(t, "newstyle", acc.Label,
		"discriminator '0' is the migrated-account sentinel — must not print the dead #0 suffix")
}

func TestDiscord_Exchange_RejectsMissingAccessToken(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()

	p := &DiscordProvider{
		clientID: "id", clientSecret: "secret", redirectURI: "https://x/cb",
		hc: rewriteClient(srv.URL, map[string]string{"https://discord.com": ""}),
	}
	_, _, err := p.Exchange(context.Background(), "bad", "https://x/cb")
	require.Error(t, err, "missing access_token must surface as a token-exchange error")
	assert.Contains(t, err.Error(), "invalid_grant")
}

// --- Refresh ---

func TestDiscord_Refresh_EmptyTokenReturnsErrRefreshNotSupported(t *testing.T) {
	t.Parallel()
	p, _ := NewDiscord("id", "secret", "https://x/cb")
	_, err := p.Refresh(context.Background(), "")
	require.ErrorIs(t, err, ErrRefreshNotSupported,
		"empty refresh token must be treated as unsupported, not a transport error")
}

func TestDiscord_Refresh_RotatedTokenIsAdopted(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())                               //#nosec G120 -- httptest receives small fixed test bodies
		assert.Equal(t, "refresh_token", r.PostFormValue("grant_type")) //#nosec G120 -- httptest receives small fixed test bodies.
		assert.Equal(t, "old-rt", r.PostFormValue("refresh_token"))     //#nosec G120 -- httptest receives small fixed test bodies.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token":"new-access",
			"refresh_token":"rotated-rt",
			"expires_in":3600,
			"scope":"identify guilds"
		}`))
	}))
	defer srv.Close()

	p := &DiscordProvider{
		clientID: "id", clientSecret: "secret", redirectURI: "https://x/cb",
		hc: rewriteClient(srv.URL, map[string]string{"https://discord.com": ""}),
	}
	tok, err := p.Refresh(context.Background(), "old-rt")
	require.NoError(t, err)
	assert.Equal(t, "new-access", tok.AccessToken)
	assert.Equal(t, "rotated-rt", tok.RefreshToken,
		"Discord rotates refresh tokens — caller must adopt the new value")
}

func TestDiscord_Refresh_PreservesOriginalWhenResponseOmitsRefresh(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token":"new-access",
			"expires_in":3600,
			"scope":"identify"
		}`))
	}))
	defer srv.Close()

	p := &DiscordProvider{
		clientID: "id", clientSecret: "secret", redirectURI: "https://x/cb",
		hc: rewriteClient(srv.URL, map[string]string{"https://discord.com": ""}),
	}
	tok, err := p.Refresh(context.Background(), "old-rt")
	require.NoError(t, err)
	assert.Equal(t, "old-rt", tok.RefreshToken,
		"when Discord omits refresh_token, preserve the stored one so the row keeps its refresh capability")
}

// --- Revoke ---

func TestDiscord_Revoke_EmptyTokenIsNoop(t *testing.T) {
	t.Parallel()
	p, _ := NewDiscord("id", "secret", "https://x/cb")
	err := p.Revoke(context.Background(), TokenSet{})
	require.NoError(t, err, "revoking empty tokens must succeed silently")
}

func TestDiscord_Revoke_PrefersRefreshTokenAndPostsClientCreds(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())                     //#nosec G120 -- httptest receives small fixed test bodies
		assert.Equal(t, "rt-value", r.PostFormValue("token"), //#nosec G120 -- httptest receives small fixed test bodies.
			"Discord revoke must prefer the refresh token over the access token")
		assert.Equal(t, "my-id", r.PostFormValue("client_id"))         //#nosec G120 -- httptest receives small fixed test bodies.
		assert.Equal(t, "my-secret", r.PostFormValue("client_secret")) //#nosec G120 -- httptest receives small fixed test bodies.
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &DiscordProvider{
		clientID: "my-id", clientSecret: "my-secret", redirectURI: "https://x/cb",
		hc: rewriteClient(srv.URL, map[string]string{"https://discord.com": ""}),
	}
	err := p.Revoke(context.Background(), TokenSet{
		AccessToken:  "at-value",
		RefreshToken: "rt-value",
	})
	require.NoError(t, err, "HTTP 200 from Discord means token was revoked successfully")
}

func TestDiscord_Revoke_FallsBackToAccessTokenWhenNoRefresh(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())                        //#nosec G120 -- httptest receives small fixed test bodies
		assert.Equal(t, "access-only", r.PostFormValue("token"), //#nosec G120 -- httptest receives small fixed test bodies.
			"must revoke the access token when no refresh token is stored")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &DiscordProvider{
		clientID: "id", clientSecret: "secret", redirectURI: "https://x/cb",
		hc: rewriteClient(srv.URL, map[string]string{"https://discord.com": ""}),
	}
	err := p.Revoke(context.Background(), TokenSet{AccessToken: "access-only"})
	require.NoError(t, err)
}

func TestDiscord_Revoke_InvalidTokenIsSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
	}))
	defer srv.Close()

	p := &DiscordProvider{
		clientID: "id", clientSecret: "secret", redirectURI: "https://x/cb",
		hc: rewriteClient(srv.URL, map[string]string{"https://discord.com": ""}),
	}
	err := p.Revoke(context.Background(), TokenSet{RefreshToken: "already-gone"})
	require.NoError(t, err,
		"Discord 400 + invalid_token means token is already revoked — must be treated as success")
}

func TestDiscord_Revoke_ServerErrorPropagates(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	p := &DiscordProvider{
		clientID: "id", clientSecret: "secret", redirectURI: "https://x/cb",
		hc: rewriteClient(srv.URL, map[string]string{"https://discord.com": ""}),
	}
	err := p.Revoke(context.Background(), TokenSet{RefreshToken: "rt"})
	require.Error(t, err, "non-success non-invalid_token responses must propagate so the caller can log them")
}

// --- discordLabel helper unit ---

func TestDiscordLabel_PrefersGlobalName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "Display", discordLabel("Display", "fallback", "1234"))
}

func TestDiscordLabel_UsesDiscriminatorWhenNonZero(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "user#0001", discordLabel("", "user", "0001"))
}

func TestDiscordLabel_DropsZeroDiscriminator(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "user", discordLabel("", "user", "0"))
}

func TestDiscordLabel_EmptyEverything(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", discordLabel("", "", ""))
}
