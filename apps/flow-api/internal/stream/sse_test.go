package stream

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/problem"
)

func TestSSEHandler_MissingWorkspaceContext_ReturnsJSONError(t *testing.T) {
	t.Parallel()

	// Create an SSE handler with a no-op notifier and remember func.
	notifier := NewInProcessNotifier()
	handler := SSEHandler(notifier, nil)

	// Request without workspace context set.
	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify the HTTP status code.
	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusForbidden)
	}

	requireProblemEnvelope(t, rec, "WS.WORKSPACE.NOT_FOUND", "workspace context missing")
}

func TestWriteJSONError_EnvelopeStructure(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	writeJSONError(rec, http.StatusBadRequest, "TEST.CODE", "test message")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
	requireProblemEnvelope(t, rec, "TEST.CODE", "test message")
}

// requireProblemEnvelope asserts a recorded response carries the
// canonical problem+json envelope.
//
// A stream that fails before it opens answers as an ordinary HTTP
// error, and the client reading it is the same SDK: it takes the code
// from `type` and the message from `detail`. This handler used to send
// {status, code, message}, none of which the SDK reads, so a rejected
// subscription arrived with neither a code to branch on nor a status.
func requireProblemEnvelope(t *testing.T, rec *httptest.ResponseRecorder, code, detail string) {
	t.Helper()

	if ct := rec.Header().Get("Content-Type"); ct != problem.ContentType {
		t.Errorf("Content-Type: got %q, want %q", ct, problem.ContentType)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode JSON body: %v", err)
	}

	if body["type"] != code {
		t.Errorf("type: got %v, want %q", body["type"], code)
	}
	if body["detail"] != detail {
		t.Errorf("detail: got %v, want %q", body["detail"], detail)
	}
	if statusFloat, ok := body["status"].(float64); !ok || int(statusFloat) != rec.Code {
		t.Errorf("status: got %v, want %d", body["status"], rec.Code)
	}
	if body["title"] != http.StatusText(rec.Code) {
		t.Errorf("title: got %v, want %q", body["title"], http.StatusText(rec.Code))
	}
	for _, gone := range []string{"code", "message"} {
		if _, present := body[gone]; present {
			t.Errorf("%q must be gone from the envelope, not merely unread: %v", gone, body)
		}
	}
}
