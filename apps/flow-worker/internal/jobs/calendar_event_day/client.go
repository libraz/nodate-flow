package calendar_event_day

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/signalwire"
)

// SignalsClient is the thin HTTP wrapper the worker uses to POST a
// single signal to flow-api on each scanned event. The client is
// stateless and safe for concurrent use as long as HTTPClient is.
//
// SignalsClient deliberately speaks the public `POST /signals` contract
// rather than reaching into the database, so the worker never bypasses
// the audit log, judge-enqueue hook, or future signal-level validation.
type SignalsClient struct {
	// BaseURL is the flow-api base URL (e.g. "http://localhost:8080").
	// The trailing /signals path is appended internally; a trailing
	// slash on BaseURL is tolerated.
	BaseURL string
	// Token is the shared bearer the flow-api service-token middleware
	// (RequireSignalsAuth) compares against NF_FLOW_API_SIGNAL_TOKEN.
	// An empty Token disables the worker: PostSignal returns an error
	// rather than emitting an unauthenticated request.
	Token string
	// UserAgent is sent as the HTTP User-Agent header so flow-api access
	// logs can attribute requests to the worker version. When empty the
	// client falls back to "flow-worker/unknown".
	UserAgent string
	// HTTPClient is the underlying transport. When nil the constructor
	// installs a client with a 10s overall timeout — never use
	// http.DefaultClient because it has no timeout (CLAUDE.md test
	// conventions).
	HTTPClient *http.Client
	// Logger receives structured warn/debug records on per-signal HTTP
	// outcomes. Required.
	Logger *slog.Logger
}

// NewSignalsClient validates the construction inputs and installs the
// default HTTP client when one was not supplied. Returns an error when
// the configuration is unusable so cmd/worker can fail boot cleanly
// rather than logging per-tick auth failures forever.
func NewSignalsClient(baseURL, token, userAgent string, logger *slog.Logger) (*SignalsClient, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("calendar_event_day: SignalsClient BaseURL is required")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("calendar_event_day: SignalsClient Token is required (set NF_FLOW_API_SIGNAL_TOKEN)")
	}
	if logger == nil {
		return nil, errors.New("calendar_event_day: SignalsClient Logger is required")
	}
	ua := strings.TrimSpace(userAgent)
	if ua == "" {
		ua = "flow-worker/unknown"
	}
	return &SignalsClient{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Token:      token,
		UserAgent:  ua,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		Logger:     logger,
	}, nil
}

// PostSignalBody is the JSON shape the worker sends to flow-api. It is a
// type alias for signalwire.CreateRequest — the single shared wire body
// owned by packages/go-shared/signalwire — so the worker, flow-api, and
// presence-discord can never diverge on field names or casing. Huma
// rejects unknown / mistyped fields with a 422 envelope that would
// surface as a per-tick warn loop, so sharing the struct turns wire
// drift into a compile error.
//
// SubjectID is the calendar_events.public_id as a canonical UUID v7
// string; the worker never sends the internal numeric id (CLAUDE.md
// rule 18). Payload is a raw JSON document so the worker can include
// the all-day flag and start instant. The worker leaves the TaskID and
// other unused fields zero-valued (omitempty drops them from the wire).
type PostSignalBody = signalwire.CreateRequest

// PostSignal sends one POST /signals request and translates the
// response into a Go error.
//
// Status handling:
//   - 2xx                 → success. flow-api returns 200 even when the
//     external_id already exists: POST /signals does an INSERT IGNORE on
//     the (workspace_id, source, external_id) UNIQUE and returns the
//     existing (or freshly inserted) row, so a duplicate emit is a 200,
//     not a 409.
//   - 4xx (other)         → permanent error: the worker logs at warn
//     and returns a non-nil error. Retrying on the
//     next tick will not help — the dedupe key
//     keeps it from compounding, and a bad request
//     shape is a bug for @api to fix.
//   - 5xx                 → transient error: returned as an error so the
//     runner records the tick as failed; the next
//     tick re-attempts and dedupe collapses on
//     success.
//   - transport / encoding → returned as an error wrapping the cause.
//
// PostSignal never panics on a nil HTTPClient or empty Token; the
// constructor guarantees both, and direct struct construction outside
// NewSignalsClient is treated as a programming error documented on the
// struct fields.
func (c *SignalsClient) PostSignal(ctx context.Context, body PostSignalBody) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("calendar_event_day: marshal signal body: %w", err)
	}

	endpoint := c.BaseURL + "/signals"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("calendar_event_day: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("calendar_event_day: post signal: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Drain a bounded chunk of the body for diagnostics. Avoid io.ReadAll
	// on an unbounded server response to keep the worker memory bound
	// predictable when flow-api misbehaves.
	const errPreviewBytes = 1 << 10 // 1 KiB
	preview, _ := io.ReadAll(io.LimitReader(resp.Body, errPreviewBytes))

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		c.Logger.DebugContext(ctx, "calendar_event_day: signal posted",
			slog.String("kind", body.Kind),
			slog.String("subject_id", body.SubjectID),
			slog.Int("status", resp.StatusCode),
		)
		return nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		c.Logger.WarnContext(ctx, "calendar_event_day: flow-api rejected signal",
			slog.Int("status", resp.StatusCode),
			slog.String("kind", body.Kind),
			slog.String("external_id", body.ExternalID),
			slog.String("body_preview", string(preview)),
		)
		return fmt.Errorf("calendar_event_day: flow-api returned %d: %s", resp.StatusCode, string(preview))
	default:
		// 5xx and other unexpected statuses — log and bubble up so the
		// tick is marked failed; next tick will retry and dedupe.
		c.Logger.WarnContext(ctx, "calendar_event_day: flow-api 5xx posting signal",
			slog.Int("status", resp.StatusCode),
			slog.String("kind", body.Kind),
			slog.String("external_id", body.ExternalID),
			slog.String("body_preview", string(preview)),
		)
		return fmt.Errorf("calendar_event_day: flow-api returned %d: %s", resp.StatusCode, string(preview))
	}
}
