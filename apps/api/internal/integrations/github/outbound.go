package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/outbound"
)

// OutboundClient is the minimal GitHub REST client used by the
// outbound MCP tool (4.OUT-1). It exposes only the operations the
// MCP surface needs and routes every request through the package
// rate limiter so a runaway agent cannot flood github.com.
type OutboundClient struct {
	Token    string
	BaseURL  string // defaults to https://api.github.com
	HTTP     *http.Client
	Limiter  outbound.RateLimiter
	UserAgent string
}

// ErrOutboundUnauthorized is returned when the upstream rejects the
// configured token.
var ErrOutboundUnauthorized = errors.New("github outbound: unauthorized")

func (c *OutboundClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *OutboundClient) base() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return "https://api.github.com"
}

// CreateIssueComment posts a comment on the given issue. owner / repo
// / number address the issue; body is the markdown content. Returns
// the created comment id on success.
func (c *OutboundClient) CreateIssueComment(ctx context.Context, owner, repo string, number int, body string) (int64, error) {
	if c.Limiter != nil {
		if err := c.Limiter.Wait(ctx); err != nil {
			return 0, err
		}
	}
	payload, _ := json.Marshal(map[string]string{"body": body})
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments", c.base(), owner, repo, number)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		return 0, ErrOutboundUnauthorized
	}
	if resp.StatusCode/100 != 2 {
		return 0, fmt.Errorf("github outbound: status %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(raw, &out)
	return out.ID, nil
}
