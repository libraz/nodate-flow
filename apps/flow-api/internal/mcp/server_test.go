package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNewHandlerRegistersTools verifies that all tools are registered
// on a freshly-constructed handler. The test avoids exercising the
// transport path that requires a live DB.
func TestNewHandlerRegistersTools(t *testing.T) {
	h := NewHandler(Deps{})
	want := []string{
		"list_projects",
		"list_tasks",
		"get_task",
		"create_task",
		"update_task",
		"add_comment",
		"search_tasks",
		"propose_tasks_from",
		"propose_priority",
	}
	for _, name := range want {
		if _, ok := h.tools[name]; !ok {
			t.Errorf("tool %q not registered", name)
		}
	}
}

// TestGETWithoutAcceptHeaderReturnsError verifies that GET without
// the required Accept: text/event-stream header is rejected.
func TestGETWithoutAcceptHeaderReturnsError(t *testing.T) {
	h := NewHandler(Deps{})
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
	var resp struct {
		Error struct {
			Data struct {
				Code string `json:"code"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Data.Code != "MCP.PROTOCOL.FRAME_MALFORMED" {
		t.Errorf("want MCP.PROTOCOL.FRAME_MALFORMED, got %q", resp.Error.Data.Code)
	}
}

// TestGETSSEWithoutAuthReturnsTokenUnknown verifies that an SSE GET
// with Accept header but no bearer token returns a token error.
func TestGETSSEWithoutAuthReturnsTokenUnknown(t *testing.T) {
	h := NewHandler(Deps{})
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Accept", "text/event-stream")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
	var resp struct {
		Error struct {
			Data struct {
				Code string `json:"code"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Data.Code != "MCP.TOKEN.UNKNOWN" {
		t.Errorf("want MCP.TOKEN.UNKNOWN, got %q", resp.Error.Data.Code)
	}
}

// TestDELETEReturns405 verifies unsupported methods are rejected.
func TestDELETEReturns405(t *testing.T) {
	h := NewHandler(Deps{})
	req := httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rr.Code)
	}
}

// TestSSEHubBroadcast verifies the hub delivers events to connected
// clients for the matching workspace only.
func TestSSEHubBroadcast(t *testing.T) {
	hub := newSSEHub()
	c1 := hub.add(1)
	c2 := hub.add(1)
	c3 := hub.add(2) // different workspace

	evt := sseEvent{EventType: "workspace.event", Data: `{"test":true}`}
	hub.broadcast(1, evt)

	// c1 and c2 should have received the event.
	select {
	case got := <-c1.send:
		if got.Data != evt.Data {
			t.Errorf("c1: want %q, got %q", evt.Data, got.Data)
		}
	default:
		t.Error("c1 did not receive event")
	}
	select {
	case got := <-c2.send:
		if got.Data != evt.Data {
			t.Errorf("c2: want %q, got %q", evt.Data, got.Data)
		}
	default:
		t.Error("c2 did not receive event")
	}
	// c3 should not have received anything.
	select {
	case <-c3.send:
		t.Error("c3 should not receive events for workspace 1")
	default:
		// expected
	}

	hub.remove(c1)
	hub.remove(c2)
	hub.remove(c3)
	if hub.activeCount(1) != 0 {
		t.Errorf("want 0 active for ws 1, got %d", hub.activeCount(1))
	}
}

// TestBuildEventNotification verifies the JSON-RPC notification shape.
func TestBuildEventNotification(t *testing.T) {
	data := buildEventNotification("task.created", 42)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed["jsonrpc"] != "2.0" {
		t.Errorf("want jsonrpc 2.0, got %v", parsed["jsonrpc"])
	}
	if parsed["method"] != "notifications/event" {
		t.Errorf("want method notifications/event, got %v", parsed["method"])
	}
	params, ok := parsed["params"].(map[string]any)
	if !ok {
		t.Fatal("params is not a map")
	}
	if params["type"] != "task.created" {
		t.Errorf("want type task.created, got %v", params["type"])
	}
	if seq, ok := params["seq"].(float64); !ok || int64(seq) != 42 {
		t.Errorf("want seq 42, got %v", params["seq"])
	}
}

// TestHashTokenDeterministic verifies that hashToken produces consistent
// SHA-256 hex output and never returns the raw token.
func TestHashTokenDeterministic(t *testing.T) {
	tok := "mcp_test-token-abc123"
	h1 := hashToken(tok)
	h2 := hashToken(tok)
	if h1 != h2 {
		t.Fatalf("hashToken is not deterministic: %q != %q", h1, h2)
	}
	if h1 == tok {
		t.Fatal("hashToken must not return the raw token")
	}
	// SHA-256 hex is always 64 characters.
	if len(h1) != 64 {
		t.Fatalf("expected 64 hex chars, got %d: %q", len(h1), h1)
	}
	// Different tokens produce different hashes.
	h3 := hashToken("mcp_different-token")
	if h1 == h3 {
		t.Fatal("different tokens should produce different hashes")
	}
}

// TestMalformedFrameReturnsFrameMalformed documents the happy path for
// JSON-RPC frame validation failure.
func TestMalformedFrameReturnsFrameMalformed(t *testing.T) {
	h := NewHandler(Deps{})
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString("not json"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
	var resp struct {
		Error struct {
			Data struct {
				Code string `json:"code"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(resp.Error.Data.Code, "MCP.PROTOCOL.FRAME_MALFORMED") {
		t.Errorf("want FRAME_MALFORMED, got %q", resp.Error.Data.Code)
	}
}
