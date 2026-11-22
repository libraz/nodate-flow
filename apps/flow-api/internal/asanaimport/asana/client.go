// Package asana is a REST client for the Asana API. It knows nothing about
// the flow database; callers receive plain snapshots of Asana data.
package asana

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultBaseURL is the production Asana API root.
	DefaultBaseURL = "https://app.asana.com/api/1.0"

	pageLimit        = "100"
	maxInFlight      = 8
	maxRetries       = 3  // 5xx and transport failures
	maxRateLimitWait = 10 // 429 waits per request
	defaultRetryWait = 60 * time.Second
	defaultBackoff   = time.Second
	maxBodyBytes     = 32 << 20
)

// APIError is a non-2xx Asana response. It never carries credentials.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("asana: HTTP %d", e.Status)
	}
	return fmt.Sprintf("asana: HTTP %d: %s", e.Status, e.Message)
}

// Client is an Asana REST client. The zero value (plus Token) is usable.
type Client struct {
	BaseURL string       // defaults to DefaultBaseURL
	Token   string       // personal access token, sent as a Bearer header only
	HTTP    *http.Client // defaults to a client with a 60s timeout

	// Sleep waits for d or until ctx ends; tests replace it. Defaults to a timer wait.
	Sleep func(ctx context.Context, d time.Duration) error
	// Backoff is the base delay of the exponential retry backoff. Defaults to 1s.
	Backoff time.Duration

	semOnce sync.Once
	sem     chan struct{}
}

var defaultHTTP = &http.Client{Timeout: 60 * time.Second}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return defaultHTTP
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) backoff(attempt int) time.Duration {
	b := c.Backoff
	if b <= 0 {
		b = defaultBackoff
	}
	return b << attempt
}

type response struct {
	status     int
	body       []byte
	retryAfter string
}

// once performs a single GET holding one of the in-flight slots.
func (c *Client) once(ctx context.Context, rawURL string) (*response, error) {
	c.semOnce.Do(func() { c.sem = make(chan struct{}, maxInFlight) })
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-c.sem }()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, err
	}
	return &response{status: resp.StatusCode, body: body, retryAfter: resp.Header.Get("Retry-After")}, nil
}

// get returns the body of a 2xx response, retrying 429 (Retry-After) and
// 5xx/transport failures (exponential backoff, maxRetries times).
func (c *Client) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	u := c.base() + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	attempt, waits := 0, 0
	for {
		r, err := c.once(ctx, u)
		var apiErr *APIError
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt >= maxRetries {
				return nil, fmt.Errorf("asana: GET %s: %w", path, err)
			}
			if err := c.sleep(ctx, c.backoff(attempt)); err != nil {
				return nil, err
			}
			attempt++
			continue
		case r.status >= 200 && r.status < 300:
			return r.body, nil
		case r.status == http.StatusTooManyRequests:
			apiErr = newAPIError(r)
			if waits >= maxRateLimitWait {
				return nil, apiErr
			}
			wait := defaultRetryWait
			if s, perr := strconv.Atoi(strings.TrimSpace(r.retryAfter)); perr == nil && s >= 0 {
				wait = time.Duration(s) * time.Second
			}
			if err := c.sleep(ctx, wait); err != nil {
				return nil, err
			}
			waits++
		case r.status >= 500:
			apiErr = newAPIError(r)
			if attempt >= maxRetries {
				return nil, apiErr
			}
			if err := c.sleep(ctx, c.backoff(attempt)); err != nil {
				return nil, err
			}
			attempt++
		default:
			return nil, newAPIError(r)
		}
	}
}

func newAPIError(r *response) *APIError {
	var env struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	e := &APIError{Status: r.status}
	if json.Unmarshal(r.body, &env) == nil && len(env.Errors) > 0 {
		e.Message = env.Errors[0].Message
	}
	return e
}

// isOffsetExpired reports whether err is Asana rejecting a stale pagination
// offset: HTTP 400 on a request that carried an offset, with a message that
// mentions the offset or pagination token.
func isOffsetExpired(err error, hadOffset bool) bool {
	var ae *APIError
	if !hadOffset || !errors.As(err, &ae) || ae.Status != http.StatusBadRequest {
		return false
	}
	m := strings.ToLower(ae.Message)
	return strings.Contains(m, "offset") || strings.Contains(m, "pagination")
}

// getOne fetches a single resource and decodes its data envelope into out.
func (c *Client) getOne(ctx context.Context, path string, q url.Values, out any) error {
	body, err := c.get(ctx, path, q)
	if err != nil {
		return err
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("asana: decode %s: %w", path, err)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("asana: decode %s: %w", path, err)
	}
	return nil
}

// list returns every item of a paginated listing. When the offset expires
// mid-listing, the listing restarts once from the beginning; a second expiry
// is an error.
func list[T any](ctx context.Context, c *Client, path string, q url.Values) ([]T, error) {
	restarted := false
	for {
		items, err := listPass[T](ctx, c, path, q)
		if err != nil && !restarted && errors.Is(err, errOffsetExpired) {
			restarted = true
			continue
		}
		return items, err
	}
}

var errOffsetExpired = errors.New("asana: pagination offset expired")

func listPass[T any](ctx context.Context, c *Client, path string, q url.Values) ([]T, error) {
	var all []T
	offset := ""
	for {
		pq := url.Values{}
		for k, v := range q {
			pq[k] = v
		}
		pq.Set("limit", pageLimit)
		if offset != "" {
			pq.Set("offset", offset)
		}
		body, err := c.get(ctx, path, pq)
		if err != nil {
			if isOffsetExpired(err, offset != "") {
				return nil, errors.Join(errOffsetExpired, err)
			}
			return nil, err
		}
		var page struct {
			Data     []T `json:"data"`
			NextPage *struct {
				Offset string `json:"offset"`
			} `json:"next_page"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("asana: decode %s: %w", path, err)
		}
		all = append(all, page.Data...)
		if page.NextPage == nil || page.NextPage.Offset == "" {
			return all, nil
		}
		offset = page.NextPage.Offset
	}
}

// ListWorkspaces returns the workspaces visible to the token.
func (c *Client) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	return list[Workspace](ctx, c, "/workspaces", url.Values{"opt_fields": {nameFields}})
}

// DownloadAttachment re-reads the attachment to obtain a fresh download_url,
// then streams the file to w without the Authorization header. It returns the
// number of bytes written.
func (c *Client) DownloadAttachment(ctx context.Context, gid string, w io.Writer) (int64, error) {
	var a Attachment
	if err := c.getOne(ctx, "/attachments/"+url.PathEscape(gid), url.Values{"opt_fields": {attachmentFields}}, &a); err != nil {
		return 0, err
	}
	if a.DownloadURL == "" {
		return 0, fmt.Errorf("asana: attachment %s has no download_url", gid)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.DownloadURL, nil)
	if err != nil {
		return 0, fmt.Errorf("asana: attachment %s: bad download_url", gid)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		// The signed URL is a credential; report the cause without it.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, fmt.Errorf("asana: download attachment %s: %w", gid, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, &APIError{Status: resp.StatusCode}
	}
	return io.Copy(w, resp.Body)
}
