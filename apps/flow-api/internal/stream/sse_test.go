package stream

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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

	// Verify Content-Type is JSON.
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", ct, "application/json")
	}

	// Parse and verify JSON envelope structure.
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode JSON body: %v", err)
	}

	// Check status field.
	status, ok := body["status"]
	if !ok {
		t.Fatal("JSON body missing 'status' field")
	}
	if statusFloat, ok := status.(float64); !ok || int(statusFloat) != http.StatusForbidden {
		t.Errorf("status field: got %v, want %d", status, http.StatusForbidden)
	}

	// Check code field.
	code, ok := body["code"]
	if !ok {
		t.Fatal("JSON body missing 'code' field")
	}
	if code != "WS.WORKSPACE.NOT_FOUND" {
		t.Errorf("code field: got %v, want %q", code, "WS.WORKSPACE.NOT_FOUND")
	}

	// Check message field.
	message, ok := body["message"]
	if !ok {
		t.Fatal("JSON body missing 'message' field")
	}
	if message != "workspace context missing" {
		t.Errorf("message field: got %v, want %q", message, "workspace context missing")
	}
}

func TestWriteJSONError_EnvelopeStructure(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	writeJSONError(rec, http.StatusBadRequest, "TEST.CODE", "test message")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", ct, "application/json")
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode JSON body: %v", err)
	}

	// Verify exactly 3 fields.
	if len(body) != 3 {
		t.Errorf("expected 3 fields in JSON envelope, got %d: %v", len(body), body)
	}

	if statusFloat, ok := body["status"].(float64); !ok || int(statusFloat) != http.StatusBadRequest {
		t.Errorf("status: got %v, want %d", body["status"], http.StatusBadRequest)
	}
	if body["code"] != "TEST.CODE" {
		t.Errorf("code: got %v, want %q", body["code"], "TEST.CODE")
	}
	if body["message"] != "test message" {
		t.Errorf("message: got %v, want %q", body["message"], "test message")
	}
}
