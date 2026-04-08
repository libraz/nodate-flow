package slack

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

// OutboundClient posts messages back into Slack via the Web API
// (4.OUT-2). Only the operations the MCP surface needs are exposed.
type OutboundClient struct {
	Token   string
	BaseURL string // defaults to https://slack.com/api
	HTTP    *http.Client
	Limiter outbound.RateLimiter
}

// ErrOutboundUnauthorized is returned when slack rejects the token.
var ErrOutboundUnauthorized = errors.New("slack outbound: unauthorized")

func (c *OutboundClient) base() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return "https://slack.com/api"
}

func (c *OutboundClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// PostMessage posts a markdown message to a channel and returns the
// resulting message ts (Slack's message id).
func (c *OutboundClient) PostMessage(ctx context.Context, channel, text string) (string, error) {
	if c.Limiter != nil {
		if err := c.Limiter.Wait(ctx); err != nil {
			return "", err
		}
	}
	payload, _ := json.Marshal(map[string]string{"channel": channel, "text": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base()+"/chat.postMessage", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		return "", ErrOutboundUnauthorized
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("slack outbound: status %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		OK    bool   `json:"ok"`
		Ts    string `json:"ts"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &out)
	if !out.OK {
		return "", fmt.Errorf("slack outbound: %s", out.Error)
	}
	return out.Ts, nil
}
