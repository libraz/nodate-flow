package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	slackAuthorizeURL = "https://slack.com/oauth/v2/authorize"
	slackTokenURL     = "https://slack.com/api/oauth.v2.access" //#nosec G101 -- public OAuth endpoint URL, not a credential
)

// slackScopes is the minimal user-scope set needed to post as the
// user and read their DMs for the inbox integration.
var slackScopes = []string{"chat:write", "im:history", "users:read"}

// SlackProvider implements [Provider] against Slack's OAuth v2 flow.
// The user token (authed_user.access_token) is what we persist — NOT
// the bot token returned in the same response — because personal
// integrations act as the user, not the workspace bot.
type SlackProvider struct {
	clientID     string
	clientSecret string
	hc           *http.Client
}

// NewSlack constructs a SlackProvider. Returns ErrNotConfigured when
// credentials are missing.
func NewSlack(clientID, clientSecret string) (Provider, error) {
	if clientID == "" || clientSecret == "" {
		return nil, ErrNotConfigured
	}
	return &SlackProvider{
		clientID:     clientID,
		clientSecret: clientSecret,
		hc:           &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// Name implements [Provider].
func (p *SlackProvider) Name() string { return "slack" }

// AuthURL implements [Provider]. Slack treats user_scope as distinct
// from scope (bot scopes); we only request user scopes so the
// installation stays account-level with no bot footprint.
func (p *SlackProvider) AuthURL(state, redirectURI string) string {
	q := url.Values{}
	q.Set("client_id", p.clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("user_scope", strings.Join(slackScopes, ","))
	return slackAuthorizeURL + "?" + q.Encode()
}

// Exchange implements [Provider]. Slack's oauth.v2.access returns a
// nested authed_user object containing the user token; we read that
// rather than the top-level access_token (which is the bot token).
func (p *SlackProvider) Exchange(ctx context.Context, code, redirectURI string) (*TokenSet, *Account, error) {
	form := url.Values{}
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, slackTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, nil, wrapExchange(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, nil, wrapExchange(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, nil, wrapExchange(fmt.Errorf("status %d: %s", resp.StatusCode, body))
	}
	var tok struct {
		OK         bool   `json:"ok"`
		Error      string `json:"error"`
		AuthedUser struct {
			ID          string `json:"id"`
			Scope       string `json:"scope"`
			AccessToken string `json:"access_token"`
		} `json:"authed_user"`
		Team struct {
			Name string `json:"name"`
		} `json:"team"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, nil, wrapExchange(err)
	}
	if !tok.OK || tok.AuthedUser.AccessToken == "" {
		return nil, nil, wrapExchange(fmt.Errorf("slack: %s", tok.Error))
	}
	label := tok.AuthedUser.ID
	if tok.Team.Name != "" {
		label = tok.Team.Name + " / " + tok.AuthedUser.ID
	}
	return &TokenSet{
			AccessToken: tok.AuthedUser.AccessToken,
			Scopes:      strings.Split(tok.AuthedUser.Scope, ","),
		}, &Account{
			ExternalID: tok.AuthedUser.ID,
			Label:      label,
		}, nil
}

// Refresh implements [Provider]. Slack user tokens (xoxp-) do not
// expire by default; token rotation is opt-in per-app and we do
// not enable it, so there is nothing to refresh.
func (p *SlackProvider) Refresh(ctx context.Context, refreshToken string) (*TokenSet, error) {
	return nil, ErrRefreshNotSupported
}

// Revoke implements [Provider]. Calls auth.revoke with the user
// token as bearer. Slack responds 200 even on failure and signals
// success through the JSON body.
func (p *SlackProvider) Revoke(ctx context.Context, tokens TokenSet) error {
	if tokens.AccessToken == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/auth.revoke", nil)
	if err != nil {
		return fmt.Errorf("integrations/slack: revoke: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	resp, err := p.hc.Do(req)
	if err != nil {
		return fmt.Errorf("integrations/slack: revoke: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("integrations/slack: revoke status %d: %s", resp.StatusCode, body)
	}
	var r struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("integrations/slack: revoke decode: %w", err)
	}
	if r.OK {
		return nil
	}
	switch r.Error {
	case "not_authed", "token_revoked", "invalid_auth":
		return nil
	}
	return fmt.Errorf("integrations/slack: revoke: %s", r.Error)
}
