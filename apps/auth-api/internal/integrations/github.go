package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	githubAuthorizeURL = "https://github.com/login/oauth/authorize"
	githubTokenURL     = "https://github.com/login/oauth/access_token" //#nosec G101 -- public OAuth endpoint URL, not a credential
	githubUserURL      = "https://api.github.com/user"
)

// githubScopes is the minimal scope set needed by the existing
// workspace GitHub integration, mirrored for personal connections so
// the downstream handlers can reuse the same client code.
var githubScopes = []string{"read:user", "repo"}

// GithubProvider implements [Provider] against github.com. OAuth
// Apps do not issue refresh tokens so RefreshToken is always empty;
// GitHub Apps do but we intentionally target the simpler OAuth App
// flow for v1.
type GithubProvider struct {
	clientID     string
	clientSecret string
	hc           *http.Client
}

// NewGithub constructs a GithubProvider from env-provided credentials.
// Returns ErrNotConfigured when either value is empty.
func NewGithub(clientID, clientSecret string) (Provider, error) {
	if clientID == "" || clientSecret == "" {
		return nil, ErrNotConfigured
	}
	return &GithubProvider{
		clientID:     clientID,
		clientSecret: clientSecret,
		hc:           &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// Name implements [Provider].
func (p *GithubProvider) Name() string { return "github" }

// AuthURL implements [Provider].
func (p *GithubProvider) AuthURL(state, redirectURI string) string {
	q := url.Values{}
	q.Set("client_id", p.clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("scope", strings.Join(githubScopes, " "))
	q.Set("allow_signup", "false")
	return githubAuthorizeURL + "?" + q.Encode()
}

// Exchange implements [Provider].
func (p *GithubProvider) Exchange(ctx context.Context, code, redirectURI string) (*TokenSet, *Account, error) {
	form := url.Values{}
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, githubTokenURL, strings.NewReader(form.Encode()))
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
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, nil, wrapExchange(fmt.Errorf("status %d: %s", resp.StatusCode, body))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, nil, wrapExchange(err)
	}
	if tok.Error != "" || tok.AccessToken == "" {
		return nil, nil, wrapExchange(fmt.Errorf("github: %s", tok.Error))
	}

	// Fetch /user to discover the login + numeric id for the label.
	userReq, err := http.NewRequestWithContext(ctx, http.MethodGet, githubUserURL, nil)
	if err != nil {
		return nil, nil, wrapExchange(err)
	}
	userReq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	userReq.Header.Set("Accept", "application/vnd.github+json")
	userResp, err := p.hc.Do(userReq)
	if err != nil {
		return nil, nil, wrapExchange(err)
	}
	defer userResp.Body.Close()
	if userResp.StatusCode/100 != 2 {
		return nil, nil, fmt.Errorf("integrations/github: user fetch status %d", userResp.StatusCode)
	}
	var u struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(userResp.Body).Decode(&u); err != nil {
		return nil, nil, wrapExchange(err)
	}
	label := "@" + u.Login
	if u.Email != "" {
		label = u.Email
	}
	return &TokenSet{
			AccessToken: tok.AccessToken,
			Scopes:      strings.Split(tok.Scope, ","),
		}, &Account{
			ExternalID: strconv.FormatInt(u.ID, 10),
			Label:      label,
		}, nil
}

// Refresh implements [Provider]. GitHub OAuth Apps do not issue
// refresh tokens; access tokens are long-lived until revoked.
func (p *GithubProvider) Refresh(ctx context.Context, refreshToken string) (*TokenSet, error) {
	return nil, ErrRefreshNotSupported
}

// Revoke implements [Provider]. Uses the OAuth App token revocation
// endpoint which requires HTTP Basic auth with client credentials.
func (p *GithubProvider) Revoke(ctx context.Context, tokens TokenSet) error {
	if tokens.AccessToken == "" {
		return nil
	}
	payload, err := json.Marshal(map[string]string{"access_token": tokens.AccessToken})
	if err != nil {
		return fmt.Errorf("integrations/github: revoke marshal: %w", err)
	}
	url := "https://api.github.com/applications/" + p.clientID + "/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, strings.NewReader(string(payload)))
	if err != nil {
		return fmt.Errorf("integrations/github: revoke: %w", err)
	}
	req.SetBasicAuth(p.clientID, p.clientSecret)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.hc.Do(req)
	if err != nil {
		return fmt.Errorf("integrations/github: revoke: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("integrations/github: revoke status %d: %s", resp.StatusCode, body)
}
