// Package auth provides a GitHub OAuth2 client for login. Unlike
// Google, GitHub does not implement standard OpenID Connect: there is
// no id_token. Instead we exchange the code for an access token and
// then call the /user API to retrieve the profile.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
)

// ErrGithubEmailNotVerified is returned by the GitHub OAuth client when
// the authenticated user has no primary verified email available. The
// handler maps it to AUTH.OIDC.EMAIL_NOT_VERIFIED so the three OIDC
// providers reject unverified accounts uniformly.
var ErrGithubEmailNotVerified = errors.New("github: no primary verified email")

// GithubOAuthConfig configures the GitHub OAuth2 client.
type GithubOAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// GithubClaims holds the profile fields returned by the GitHub /user
// endpoint. The shape intentionally mirrors the OIDC claims struct in
// the Google callback so handler logic stays uniform.
type GithubClaims struct {
	Sub   string `json:"id"`    // GitHub numeric user id (stringified by JSON)
	Login string `json:"login"` // GitHub username
	Name  string `json:"name"`
	Email string `json:"email"`
}

// GithubOAuthClient wraps golang.org/x/oauth2 for GitHub login.
type GithubOAuthClient struct {
	cfg   GithubOAuthConfig
	oauth *oauth2.Config
	// apiBaseURL is the GitHub API root used for /user and /user/emails.
	// Defaults to https://api.github.com; tests override it to point at
	// an httptest server.
	apiBaseURL string
}

// NewGithubOAuth builds a GithubOAuthClient.
func NewGithubOAuth(cfg GithubOAuthConfig) *GithubOAuthClient {
	return &GithubOAuthClient{
		cfg: cfg,
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     github.Endpoint,
			Scopes:       []string{"read:user", "user:email"},
		},
		apiBaseURL: "https://api.github.com",
	}
}

// WithAPIBaseURL overrides the GitHub API root. Intended for tests that
// stand up an httptest.Server in place of api.github.com. Returns the
// receiver for chaining.
func (c *GithubOAuthClient) WithAPIBaseURL(base string) *GithubOAuthClient {
	c.apiBaseURL = base
	return c
}

// AuthCodeURL returns the GitHub authorization redirect URL.
func (c *GithubOAuthClient) AuthCodeURL(state string) string {
	return c.oauth.AuthCodeURL(state)
}

// Exchange swaps an authorization code for an access token and fetches
// the user profile from GitHub's /user endpoint. The nonce parameter
// is accepted for parity with the Google/Microsoft OIDC clients so the
// callback handler can pass it uniformly; GitHub OAuth2 has no
// id_token to bind it to, so the value is currently unused on the
// wire but reserved for the day GitHub ships true OIDC.
func (c *GithubOAuthClient) Exchange(ctx context.Context, code, _ string) (*GithubClaims, error) {
	tok, err := c.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("github: oauth exchange: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBaseURL+"/user", nil)
	if err != nil {
		return nil, fmt.Errorf("github: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: user request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: user request returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("github: read body: %w", err)
	}

	// GitHub returns id as a number; we need it as a string for the
	// identities.subject column.
	var raw struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("github: decode user: %w", err)
	}
	// Always resolve the email through /user/emails so we can require
	// the primary entry to be verified=true. The /user payload only
	// echoes the user's chosen public email and gives us no signal
	// about verification, so trusting it would let an attacker
	// auto-provision an account against any email they have managed
	// to add to GitHub but never verified.
	email, err := c.fetchGithubPrimaryVerifiedEmail(ctx, tok.AccessToken)
	if err != nil {
		return nil, err
	}
	return &GithubClaims{
		Sub:   fmt.Sprintf("%d", raw.ID),
		Login: raw.Login,
		Name:  raw.Name,
		Email: email,
	}, nil
}

// fetchGithubPrimaryVerifiedEmail calls /user/emails and returns the
// primary email iff it is also verified. Returns
// ErrGithubEmailNotVerified when no such address exists so the caller
// can surface AUTH.OIDC.EMAIL_NOT_VERIFIED.
func (c *GithubOAuthClient) fetchGithubPrimaryVerifiedEmail(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBaseURL+"/user/emails", nil)
	if err != nil {
		return "", fmt.Errorf("github: build emails request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: emails request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: emails request returned %d", resp.StatusCode)
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return "", fmt.Errorf("github: decode emails: %w", err)
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}
	return "", ErrGithubEmailNotVerified
}
