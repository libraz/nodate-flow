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
	googleAuthorizeURL = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL     = "https://oauth2.googleapis.com/token"
	googleUserinfoURL  = "https://www.googleapis.com/oauth2/v3/userinfo"
)

// googleCalendarScopes is the scope set needed to list calendars and
// read upcoming events for the personal schedule-ingest feature.
// openid + email are required so we can fetch a stable sub for the
// external_account_id column.
var googleCalendarScopes = []string{
	"openid",
	"email",
	"https://www.googleapis.com/auth/calendar.readonly",
}

// GoogleCalendarProvider implements [Provider] against Google's
// OAuth 2.0 web flow. We request offline access so a refresh token
// is issued the first time the user consents; subsequent consents
// return only the access token and we leave the stored refresh
// token untouched.
type GoogleCalendarProvider struct {
	clientID     string
	clientSecret string
	hc           *http.Client
}

// NewGoogleCalendar constructs a GoogleCalendarProvider.
func NewGoogleCalendar(clientID, clientSecret string) (Provider, error) {
	if clientID == "" || clientSecret == "" {
		return nil, ErrNotConfigured
	}
	return &GoogleCalendarProvider{
		clientID:     clientID,
		clientSecret: clientSecret,
		hc:           &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// Name implements [Provider].
func (p *GoogleCalendarProvider) Name() string { return "google_calendar" }

// AuthURL implements [Provider]. access_type=offline and
// prompt=consent together guarantee a refresh token on first
// authorization; without prompt=consent Google silently omits
// the refresh token on subsequent consents and breaks refresh.
func (p *GoogleCalendarProvider) AuthURL(state, redirectURI string) string {
	q := url.Values{}
	q.Set("client_id", p.clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("response_type", "code")
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	q.Set("include_granted_scopes", "true")
	q.Set("scope", strings.Join(googleCalendarScopes, " "))
	return googleAuthorizeURL + "?" + q.Encode()
}

// Exchange implements [Provider].
func (p *GoogleCalendarProvider) Exchange(ctx context.Context, code, redirectURI string) (*TokenSet, *Account, error) {
	form := url.Values{}
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("grant_type", "authorization_code")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenURL, strings.NewReader(form.Encode()))
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
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
		Error        string `json:"error"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, nil, wrapExchange(err)
	}
	if tok.AccessToken == "" {
		return nil, nil, wrapExchange(fmt.Errorf("google: %s", tok.Error))
	}
	expiresAt := time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)

	// Fetch userinfo for the stable sub + email label.
	uReq, err := http.NewRequestWithContext(ctx, http.MethodGet, googleUserinfoURL, nil)
	if err != nil {
		return nil, nil, wrapExchange(err)
	}
	uReq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	uResp, err := p.hc.Do(uReq)
	if err != nil {
		return nil, nil, wrapExchange(err)
	}
	defer uResp.Body.Close()
	if uResp.StatusCode/100 != 2 {
		return nil, nil, fmt.Errorf("integrations/google: userinfo status %d", uResp.StatusCode)
	}
	var u struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(uResp.Body).Decode(&u); err != nil {
		return nil, nil, wrapExchange(err)
	}
	label := u.Email
	if label == "" {
		label = u.Sub
	}
	return &TokenSet{
			AccessToken:  tok.AccessToken,
			RefreshToken: tok.RefreshToken,
			ExpiresAt:    expiresAt,
			Scopes:       strings.Split(tok.Scope, " "),
		}, &Account{
			ExternalID: u.Sub,
			Label:      label,
		}, nil
}
