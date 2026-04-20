// Package auth provides a GitHub OAuth2 client for login. Unlike
// Google, GitHub does not implement standard OpenID Connect: there is
// no id_token. Instead we exchange the code for an access token and
// then call the /user API to retrieve the profile.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
)

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
	}
}

// AuthCodeURL returns the GitHub authorization redirect URL.
func (c *GithubOAuthClient) AuthCodeURL(state string) string {
	return c.oauth.AuthCodeURL(state)
}

// Exchange swaps an authorization code for an access token and fetches
// the user profile from GitHub's /user endpoint.
func (c *GithubOAuthClient) Exchange(ctx context.Context, code string) (*GithubClaims, error) {
	tok, err := c.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("github: oauth exchange: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
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
	// If primary email is private, fetch from /user/emails endpoint.
	email := raw.Email
	if email == "" {
		email, _ = fetchGithubPrimaryEmail(ctx, tok.AccessToken)
	}
	return &GithubClaims{
		Sub:   fmt.Sprintf("%d", raw.ID),
		Login: raw.Login,
		Name:  raw.Name,
		Email: email,
	}, nil
}

// fetchGithubPrimaryEmail calls /user/emails to find the user's
// primary verified email when the /user response has it empty.
func fetchGithubPrimaryEmail(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user/emails", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return "", err
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}
	return "", fmt.Errorf("github: no primary verified email found")
}
