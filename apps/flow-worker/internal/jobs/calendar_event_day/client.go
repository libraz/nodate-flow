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

// PostSignalBody is the JSON shape the worker sends to flow-api. The
// field names and casing must match
// apps/flow-api/internal/http/handlers/signals/types.go
// (SignalCreateInputBody) exactly — Huma rejects unknown / mistyped
// fields with a 422 envelope that would surface as a per-tick warn loop.
//
// SubjectID is the calendar_events.public_id as a canonical UUID v7
// string; the worker never sends the internal numeric id (CLAUDE.md
// rule 18). Payload is a raw JSON document so the worker can include
// the all-day flag and start instant without coupling the wire shape to
// any Go struct on the flow-api side.
type PostSignalBody struct {
	WorkspaceID string          `json:"workspaceId"`
	Source      string          `json:"source"`
	Kind        string          `json:"kind"`
	ExternalID  string          `json:"externalId,omitempty"`
	SubjectType string          `json:"subjectType,omitempty"`
	SubjectID   string          `json:"subjectId,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	ExpiresAt   *int64          `json:"expiresAt,omitempty"`
}

// PostSignal sends one POST /signals request and translates the
// response into a Go error.
//
// Status handling:
//   - 2xx                 → success.
//   - 409 Conflict        → flow-api detected the external_id dedupe
//     and collapsed the row; treated as success and
//     logged at debug.
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
	case resp.StatusCode == http.StatusConflict:
		// flow-api collapsed the dedupe — equivalent outcome to a fresh
		// insert, so the tick is still healthy.
		c.Logger.DebugContext(ctx, "calendar_event_day: signal deduplicated by flow-api",
			slog.String("external_id", body.ExternalID),
			slog.String("subject_id", body.SubjectID),
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
