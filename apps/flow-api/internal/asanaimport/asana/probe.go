package asana

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// ProbeResult is the outcome of a single-resource GET.
type ProbeResult int

const (
	// Error is any outcome other than the three below (transport, 5xx after retries, ...).
	Error ProbeResult = iota
	// Exists means the resource is visible to the token.
	Exists
	// NotFound means HTTP 404.
	NotFound
	// Forbidden means HTTP 403.
	Forbidden
)

func (r ProbeResult) String() string {
	switch r {
	case Exists:
		return "exists"
	case NotFound:
		return "not_found"
	case Forbidden:
		return "forbidden"
	default:
		return "error"
	}
}

// ProbeTask fetches a task by gid and classifies the result.
func (c *Client) ProbeTask(ctx context.Context, gid string) ProbeResult {
	return c.probe(ctx, "/tasks/"+url.PathEscape(gid))
}

// ProbeStory fetches a story by gid and classifies the result.
func (c *Client) ProbeStory(ctx context.Context, gid string) ProbeResult {
	return c.probe(ctx, "/stories/"+url.PathEscape(gid))
}

func (c *Client) probe(ctx context.Context, path string) ProbeResult {
	_, err := c.get(ctx, path, url.Values{"opt_fields": {"gid"}})
	if err == nil {
		return Exists
	}
	var ae *APIError
	if errors.As(err, &ae) {
		switch ae.Status {
		case http.StatusNotFound:
			return NotFound
		case http.StatusForbidden:
			return Forbidden
		}
	}
	return Error
}
