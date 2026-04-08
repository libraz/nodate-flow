package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNewHandlerRegistersPhase1Tools verifies that all Phase 1 tools
// are registered on a freshly-constructed handler. The test avoids
// exercising the transport path that requires a live DB.
func TestNewHandlerRegistersPhase1Tools(t *testing.T) {
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

// TestGETReturns405 documents that SSE is deferred in Phase 1.
func TestGETReturns405(t *testing.T) {
	h := NewHandler(Deps{})
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rr.Code)
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
