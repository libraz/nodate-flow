package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/outbound"
)

// OutboundClient posts messages back into Slack via the Web API.
// Only the operations the MCP surface needs are exposed.
type OutboundClient struct {
	Token   string
	BaseURL string // defaults to https://slack.com/api
	HTTP    *http.Client
	Limiter outbound.RateLimiter
}

// ErrOutboundUnauthorized is returned when slack rejects the token.
var ErrOutboundUnauthorized = errors.New("slack outbound: unauthorized")

// maxResponseBytes bounds how much of a Slack response this client
// holds in memory. chat.postMessage answers with a small JSON envelope;
// a failure body is quoted into an error string that reaches the logs.
// Neither justifies reading a response whose length the other side
// picks.
const maxResponseBytes = 64 << 10 // 64 KiB

func (c *OutboundClient) base() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return "https://slack.com/api"
}

// requestTimeout bounds one call to the Slack Web API. The fallback used
// to be http.DefaultClient, which has no deadline at all: an upstream that
// accepts the connection and then stops talking pins the caller's
// goroutine for as long as the peer keeps the socket open.
const requestTimeout = 15 * time.Second

// defaultClient is the shared fallback for callers that did not supply
// their own. One client, so the transport's connection pool is reused.
var defaultClient = &http.Client{Timeout: requestTimeout}

func (c *OutboundClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return defaultClient
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
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
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
