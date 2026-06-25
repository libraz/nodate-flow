package calendar_event_day

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// silentLogger returns a slog.Logger that discards all records. The
// client tests assert HTTP behaviour, not log output; the lifecycle
// integration tests in tests/ cover the slog stream end-to-end.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// newClient is a one-line constructor for tests. It panics on
// configuration error because every caller hard-codes valid args.
func newClient(t *testing.T, baseURL, token string) *SignalsClient {
	t.Helper()
	c, err := NewSignalsClient(baseURL, token, "flow-worker/test", silentLogger())
	require.NoError(t, err)
	return c
}

// sampleBody returns a representative PostSignalBody so each test can
// override one or two fields without restating the boilerplate.
func sampleBody() PostSignalBody {
	expires := int64(1_800_000_000)
	return PostSignalBody{
		WorkspaceID: "01923456-7890-7abc-9def-0123456789ab",
		Source:      "calendar",
		Kind:        "calendar.event_day_arrived",
		ExternalID:  "calendar_event_day:01923456-7890-7abc-9def-0123456789ab:2026-05-17",
		SubjectType: "calendar_event",
		SubjectID:   "01923456-7890-7abc-9def-0123456789ab",
		Payload:     json.RawMessage(`{"allDay":false}`),
		ExpiresAt:   &expires,
	}
}

// TestNewSignalsClientRejectsEmptyBaseURL asserts the constructor refuses
// to build a client that cannot send requests — the worker would
// otherwise log per-tick failures forever.
func TestNewSignalsClientRejectsEmptyBaseURL(t *testing.T) {
	t.Parallel()
	_, err := NewSignalsClient("", "token", "ua", silentLogger())
	require.Error(t, err)
	require.Contains(t, err.Error(), "BaseURL")
}

// TestNewSignalsClientRejectsEmptyToken pins the security guard: the
// service-token bearer is required so the worker cannot accidentally
// ship an anonymous POST to flow-api.
func TestNewSignalsClientRejectsEmptyToken(t *testing.T) {
	t.Parallel()
	_, err := NewSignalsClient("http://localhost", "", "ua", silentLogger())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Token")
}

// TestNewSignalsClientRequiresLogger ensures DebugContext / WarnContext
// callers in PostSignal cannot nil-deref.
func TestNewSignalsClientRequiresLogger(t *testing.T) {
	t.Parallel()
	_, err := NewSignalsClient("http://localhost", "tok", "ua", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Logger")
}

// TestPostSignalHeaders verifies the auth bearer, content type, and
// user agent reach the wire as configured. This is the security-
// sensitive assertion: a regression in header building would surface as
// a 401 storm in production but a silent test if it is not pinned here.
func TestPostSignalHeaders(t *testing.T) {
	t.Parallel()

	var (
		gotAuth        string
		gotContentType string
		gotUA          string
		gotPath        string
		gotMethod      string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		gotUA = r.Header.Get("User-Agent")
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL, "secret-token")
	require.NoError(t, c.PostSignal(context.Background(), sampleBody()))

	require.Equal(t, "Bearer secret-token", gotAuth)
	require.Equal(t, "application/json", gotContentType)
	require.Equal(t, "flow-worker/test", gotUA)
	require.Equal(t, "/signals", gotPath)
	require.Equal(t, http.MethodPost, gotMethod)
}

// TestPostSignalBaseURLTrailingSlashIsTolerated documents that callers
// can hand in either form without producing "//signals".
func TestPostSignalBaseURLTrailingSlashIsTolerated(t *testing.T) {
	t.Parallel()

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL+"/", "tok")
	require.NoError(t, c.PostSignal(context.Background(), sampleBody()))
	require.Equal(t, "/signals", gotPath)
}

// TestPostSignalBodyMatchesDTO confirms the JSON shape on the wire
// matches the fields flow-api SignalCreateInputBody declares. Drift
// here would cause Huma to reject the request with a 422 envelope.
func TestPostSignalBodyMatchesDTO(t *testing.T) {
	t.Parallel()

	var bodyBytes []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		bodyBytes = b
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL, "tok")
	require.NoError(t, c.PostSignal(context.Background(), sampleBody()))

	// Decode into a generic map and assert keys/values rather than
	// re-marshalling — that way an accidental snake_case slip-through
	// (workspace_id, external_id) is caught even when Go field tags hide
	// it from local struct inspection.
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(bodyBytes, &decoded))
	require.Equal(t, "01923456-7890-7abc-9def-0123456789ab", decoded["workspaceId"])
	require.Equal(t, "calendar", decoded["source"])
	require.Equal(t, "calendar.event_day_arrived", decoded["kind"])
	require.Equal(t, "calendar_event_day:01923456-7890-7abc-9def-0123456789ab:2026-05-17", decoded["externalId"])
	require.Equal(t, "calendar_event", decoded["subjectType"])
	require.Equal(t, "01923456-7890-7abc-9def-0123456789ab", decoded["subjectId"])
	require.InEpsilon(t, float64(1_800_000_000), decoded["expiresAt"], 0)

	// snake_case fields must never appear on the wire — flow-api's DTO
	// uses camelCase exclusively.
	for _, k := range []string{"workspace_id", "external_id", "subject_type", "subject_id", "payload_json", "expires_at"} {
		_, present := decoded[k]
		require.Falsef(t, present, "wire body must not contain snake_case key %q", k)
	}
}

// TestPostSignal201IsSuccess covers the happy path: flow-api responded
// 201 Created, the client returns nil.
func TestPostSignal201IsSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	c := newClient(t, srv.URL, "tok")
	require.NoError(t, c.PostSignal(context.Background(), sampleBody()))
}

// TestPostSignal200IsSuccess covers the actual flow-api response shape:
// POST /signals returns 200 OK with the created Signal body. Pinned
// separately from the 201 case so a regression on either side is loud.
func TestPostSignal200IsSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"01923456-7890-7abc-9def-0123456789ab"}`))
	}))
	defer srv.Close()
	c := newClient(t, srv.URL, "tok")
	require.NoError(t, c.PostSignal(context.Background(), sampleBody()))
}

// TestPostSignalDuplicate200IsSuccess pins the real dedupe-collapse
// contract: flow-api does an INSERT IGNORE on the (workspace_id, source,
// external_id) UNIQUE and returns 200 with the existing row even when the
// external_id was already present. The worker therefore sees a plain 200
// for a duplicate emit — never a 409 — and must treat it as success.
func TestPostSignalDuplicate200IsSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Same response shape flow-api returns for a fresh insert: the
		// existing signal row. There is no distinguishing 409.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"01923456-7890-7abc-9def-0123456789ab"}`))
	}))
	defer srv.Close()
	c := newClient(t, srv.URL, "tok")
	require.NoError(t, c.PostSignal(context.Background(), sampleBody()),
		"a duplicate emit returns 200 (INSERT IGNORE), which must be treated as success")
}

// TestPostSignal409IsError documents that a 409 is no longer a special
// success case. flow-api never returns 409 for POST /signals, so if one
// ever surfaces it is an unexpected client error the worker must report
// rather than silently swallow.
func TestPostSignal409IsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"conflict"}`))
	}))
	defer srv.Close()
	c := newClient(t, srv.URL, "tok")
	err := c.PostSignal(context.Background(), sampleBody())
	require.Error(t, err)
	require.Contains(t, err.Error(), "409")
}

// TestPostSignal400IsError covers permanent rejection: a 400 means the
// worker sent a body flow-api refuses, which the runner records as an
// errored tick. The Logger receives a warn so operators see the cause.
func TestPostSignal400IsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer srv.Close()
	c := newClient(t, srv.URL, "tok")
	err := c.PostSignal(context.Background(), sampleBody())
	require.Error(t, err)
	require.Contains(t, err.Error(), "400")
}

// TestPostSignal500IsError mirrors the 4xx case for transient server
// failures: returning a non-nil error so the runner ticks again.
func TestPostSignal500IsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()
	c := newClient(t, srv.URL, "tok")
	err := c.PostSignal(context.Background(), sampleBody())
	require.Error(t, err)
	require.Contains(t, err.Error(), "500")
}

// TestPostSignalNetworkErrorIsError verifies the transport-failure path:
// when the upstream connection cannot be made, the client wraps the
// underlying error so the runner sees an explicit failure rather than a
// nil-return on an unsent request.
func TestPostSignalNetworkErrorIsError(t *testing.T) {
	t.Parallel()
	// A server that closes the listener immediately — the client gets a
	// connection-refused error on Do.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	addr := srv.URL
	srv.Close()

	c := newClient(t, addr, "tok")
	err := c.PostSignal(context.Background(), sampleBody())
	require.Error(t, err)
	require.True(t,
		strings.Contains(err.Error(), "post signal") || strings.Contains(err.Error(), "connect"),
		"expected wrapped transport error, got %q", err.Error(),
	)
}

// TestPostSignalRespectsContextCancellation pins that cancelling the
// caller context aborts the in-flight HTTP attempt rather than blocking
// the tick to its 10s timeout.
func TestPostSignalRespectsContextCancellation(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-r.Context().Done()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL, "tok")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := c.PostSignal(ctx, sampleBody())
	require.Error(t, err)
}
