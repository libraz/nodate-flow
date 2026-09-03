package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	discordAuthorizeURL = "https://discord.com/oauth2/authorize"
	discordTokenURL     = "https://discord.com/api/oauth2/token"        //#nosec G101 -- public OAuth endpoint URL, not a credential
	discordRevokeURL    = "https://discord.com/api/oauth2/token/revoke" //#nosec G101 -- public OAuth endpoint URL, not a credential
	discordUserURL      = "https://discord.com/api/users/@me"
)

// discordScopes is the minimal scope set needed by the
// presence-discord gateway: identify gives us the user snowflake we
// bind to the workspace user, guilds lets the gateway recognise the
// shared guild membership that gates presence events.
var discordScopes = []string{"identify", "guilds"}

// DiscordProvider implements [Provider] against Discord's OAuth2
// flow. Personal Discord connections are presence-binding only —
// the access token unlocks identity + guild membership for the
// gateway and is not used to mutate tasks.
type DiscordProvider struct {
	clientID     string
	clientSecret string
	redirectURI  string
	hc           *http.Client
}

// NewDiscord constructs a DiscordProvider from env-provided
// credentials. Returns ErrNotConfigured when any of client id,
// client secret, or redirect URI is empty so the provider card on
// /settings/integrations renders as "not available" without ever
// being registered.
func NewDiscord(clientID, clientSecret, redirectURI string) (Provider, error) {
	if clientID == "" || clientSecret == "" || redirectURI == "" {
		return nil, ErrNotConfigured
	}
	return &DiscordProvider{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURI:  redirectURI,
		hc:           &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// Name implements [Provider].
func (p *DiscordProvider) Name() string { return "discord" }

// AuthURL implements [Provider]. We always pass response_type=code
// and prompt=consent so the user explicitly re-authorises whenever
// they reconnect (Discord otherwise returns the access token
// silently and we miss a chance to refresh the verified_at stamp).
func (p *DiscordProvider) AuthURL(state, redirectURI string) string {
	q := url.Values{}
	q.Set("client_id", p.clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("response_type", "code")
	q.Set("prompt", "consent")
	q.Set("scope", strings.Join(discordScopes, " "))
	return discordAuthorizeURL + "?" + q.Encode()
}

// Exchange implements [Provider]. The token endpoint returns an
// access token, refresh token, and expires_in (seconds); we then
// hit /users/@me to resolve the friendly label. Discord's modern
// global_name supersedes the legacy username#discriminator pair —
// we prefer it when set so labels match what users see in the
// Discord client today.
func (p *DiscordProvider) Exchange(ctx context.Context, code, redirectURI string) (*TokenSet, *Account, error) {
	form := url.Values{}
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, discordTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, nil, wrapExchange(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, nil, wrapExchange(err)
	}
	defer resp.Body.Close()
	body, err := readProviderBody(resp.Body)
	if err != nil {
		return nil, nil, wrapExchange(err)
	}
	if resp.StatusCode/100 != 2 {
		// Never echo the raw body — Discord may include tokens in
		// some response variants and the slog redaction layer does
		// not key on "body". Parse the well-defined OAuth error
		// envelope and surface only the error code.
		return nil, nil, wrapExchange(fmt.Errorf("status %d: %s", resp.StatusCode, discordErrorCode(body)))
	}
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
		TokenType    string `json:"token_type"`
		Error        string `json:"error"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, nil, wrapExchange(err)
	}
	if tok.AccessToken == "" {
		return nil, nil, wrapExchange(fmt.Errorf("discord: %s", tok.Error))
	}
	var expiresAt time.Time
	if tok.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}

	// Fetch /users/@me for the snowflake + friendly label.
	uReq, err := http.NewRequestWithContext(ctx, http.MethodGet, discordUserURL, nil)
	if err != nil {
		return nil, nil, wrapExchange(err)
	}
	uReq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	uReq.Header.Set("Accept", "application/json")
	uResp, err := p.hc.Do(uReq)
	if err != nil {
		return nil, nil, wrapExchange(err)
	}
	defer uResp.Body.Close()
	if uResp.StatusCode/100 != 2 {
		return nil, nil, fmt.Errorf("integrations/discord: user fetch status %d", uResp.StatusCode)
	}
	var u struct {
		ID            string `json:"id"`
		Username      string `json:"username"`
		Discriminator string `json:"discriminator"`
		GlobalName    string `json:"global_name"`
	}
	if err := json.NewDecoder(uResp.Body).Decode(&u); err != nil {
		return nil, nil, wrapExchange(err)
	}
	if u.ID == "" {
		return nil, nil, wrapExchange(fmt.Errorf("discord: user response missing id"))
	}
	label := discordLabel(u.GlobalName, u.Username, u.Discriminator)

	// metadata_json carries the snowflake + verified_at so the
	// presence-discord gateway can resolve incoming events
	// via JSON_EXTRACT(metadata_json, '$.external_user_id') without
	// branching on the provider column. The snowflake is duplicated
	// into external_account_id for the standard reverse lookup; the
	// duplication is intentional (ADR 0008 D6) so the gateway query
	// stays uniform across present and future presence providers.
	meta, err := json.Marshal(map[string]string{
		"external_user_id": u.ID,
		"verified_at":      time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return nil, nil, wrapExchange(err)
	}

	scopes := strings.Fields(tok.Scope)
	return &TokenSet{
			AccessToken:  tok.AccessToken,
			RefreshToken: tok.RefreshToken,
			ExpiresAt:    expiresAt,
			Scopes:       scopes,
		}, &Account{
			ExternalID: u.ID,
			Label:      label,
			Metadata:   meta,
		}, nil
}

// Refresh implements [Provider]. Discord issues refresh tokens and
// accepts grant_type=refresh_token at the same token endpoint. The
// response shape mirrors Exchange so the parsing path is shared.
func (p *DiscordProvider) Refresh(ctx context.Context, refreshToken string) (*TokenSet, error) {
	if refreshToken == "" {
		return nil, ErrRefreshNotSupported
	}
	form := url.Values{}
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, discordTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, wrapExchange(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, wrapExchange(err)
	}
	defer resp.Body.Close()
	body, err := readProviderBody(resp.Body)
	if err != nil {
		return nil, wrapExchange(err)
	}
	if resp.StatusCode/100 != 2 {
		// See Exchange — strip the raw body so token-shaped error
		// payloads never reach slog.
		return nil, wrapExchange(fmt.Errorf("status %d: %s", resp.StatusCode, discordErrorCode(body)))
	}
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
		Error        string `json:"error"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, wrapExchange(err)
	}
	if tok.AccessToken == "" {
		return nil, wrapExchange(fmt.Errorf("discord: %s", tok.Error))
	}
	rt := tok.RefreshToken
	if rt == "" {
		// Discord usually rotates the refresh token; preserve the
		// existing one only if the response omits it so the stored
		// row never loses its refresh capability.
		rt = refreshToken
	}
	var expiresAt time.Time
	if tok.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	return &TokenSet{
		AccessToken:  tok.AccessToken,
		RefreshToken: rt,
		ExpiresAt:    expiresAt,
		Scopes:       strings.Fields(tok.Scope),
	}, nil
}

// Revoke implements [Provider]. Discord's revoke endpoint accepts
// the token + client credentials as a form post and answers 200 on
// success. Best-effort: a non-2xx response is logged and swallowed
// so the local row deletion always proceeds.
func (p *DiscordProvider) Revoke(ctx context.Context, tokens TokenSet) error {
	token := tokens.RefreshToken
	if token == "" {
		token = tokens.AccessToken
	}
	if token == "" {
		return nil
	}
	form := url.Values{}
	form.Set("token", token)
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, discordRevokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("integrations/discord: revoke: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.hc.Do(req)
	if err != nil {
		return fmt.Errorf("integrations/discord: revoke: %w", err)
	}
	defer resp.Body.Close()
	// Revoke is best-effort, so an over-long body is not itself a
	// failure: the size error is dropped and the truncated bytes fall
	// through to the parse below, which reports them as unparseable.
	body, _ := readProviderBody(resp.Body)
	if resp.StatusCode/100 == 2 {
		return nil
	}
	// Parse the response body as Discord's well-defined OAuth error
	// envelope. We deliberately never log the raw body: a non-2xx
	// response can occasionally echo back the submitted token (or a
	// new short-lived token in malformed-response scenarios), and the
	// slog redaction layer does not key on "body". Logging only the
	// parsed error field keeps diagnostics useful without leaking
	// secret material.
	var parsed struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	parseErr := json.Unmarshal(body, &parsed)
	// Discord returns 400 invalid_token when the token is already
	// gone; treat as success so disconnect is idempotent.
	if resp.StatusCode == http.StatusBadRequest && parseErr == nil && parsed.Error == "invalid_token" {
		return nil
	}
	if parseErr != nil {
		slog.WarnContext(ctx, "integrations/discord: revoke non-2xx",
			"status", resp.StatusCode,
			"body_unparseable", true)
		return fmt.Errorf("integrations/discord: revoke status %d", resp.StatusCode)
	}
	slog.WarnContext(ctx, "integrations/discord: revoke non-2xx",
		"status", resp.StatusCode,
		"discord_error", parsed.Error)
	return fmt.Errorf("integrations/discord: revoke status %d: %s", resp.StatusCode, parsed.Error)
}

// discordErrorCode returns the OAuth error code from a Discord
// non-2xx response body, or "unparseable" if the body is not the
// expected JSON envelope. The goal is to surface enough diagnostic
// info for operators without ever echoing token-shaped fields the
// raw body might contain.
func discordErrorCode(body []byte) string {
	var r struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &r); err != nil || r.Error == "" {
		return "unparseable"
	}
	return r.Error
}

// discordLabel picks the friendliest representation of a Discord
// user. global_name is what the official client now shows for users
// who have migrated off discriminators; the legacy username + "#0"
// case (no discriminator) is handled by falling back to username
// alone rather than printing the dead "#0" suffix.
func discordLabel(globalName, username, discriminator string) string {
	if globalName != "" {
		return globalName
	}
	if username != "" && discriminator != "" && discriminator != "0" {
		return username + "#" + discriminator
	}
	return username
}
