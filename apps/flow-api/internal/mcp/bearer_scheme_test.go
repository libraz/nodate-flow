package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// transportRefusalCode drives one request through the MCP transport and
// returns the JSON-RPC error code and HTTP status it answered with.
//
// The handler is built with an empty Deps, so a header that clears the
// bearer gate reaches authenticate, which refuses a nil Queries with
// INTERNAL.UNEXPECTED. That makes "the header parsed" and "the header
// was rejected" two distinguishable answers without a live database:
// only a header the parser refused yields MCP.TOKEN.UNKNOWN before any
// lookup is attempted.
func transportRefusalCode(t *testing.T, method, authHeader string) (string, int) {
	t.Helper()

	h := NewHandler(Deps{})
	var req *http.Request
	switch method {
	case http.MethodPost:
		frame := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
		req = httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(frame))
		req.Header.Set("Content-Type", "application/json")
	default:
		req = httptest.NewRequest(http.MethodGet, "/mcp", nil)
		req.Header.Set("Accept", "text/event-stream")
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	var resp struct {
		Error struct {
			Data struct {
				Code string `json:"code"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response for %q: %v (body=%s)", authHeader, err, rr.Body.String())
	}
	return resp.Error.Data.Code, rr.Code
}

// TestBearerSchemeIsCaseInsensitive pins the auth-scheme match on the
// MCP transport as case-insensitive, which is how RFC 7235 defines the
// scheme token. MCP clients are machine clients written against other
// stacks and routinely send "bearer"; refusing those spells a spelling
// difference as an authentication failure the client cannot diagnose
// from the response.
//
// Both the POST (JSON-RPC) and GET (SSE) entry points are covered
// because each reads the header itself.
func TestBearerSchemeIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	headers := []string{
		"Bearer mcp_scheme_case_probe",
		"bearer mcp_scheme_case_probe",
		"BEARER mcp_scheme_case_probe",
		"BeArEr mcp_scheme_case_probe",
	}
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		for _, header := range headers {
			t.Run(method+"/"+header, func(t *testing.T) {
				code, status := transportRefusalCode(t, method, header)
				if code == "MCP.TOKEN.UNKNOWN" {
					t.Fatalf("header %q was rejected at the bearer gate; want it accepted", header)
				}
				if code != "INTERNAL.UNEXPECTED" {
					t.Fatalf("want INTERNAL.UNEXPECTED past the bearer gate, got %q (status %d)", code, status)
				}
			})
		}
	}
}

// TestBearerLeadingWhitespaceIsTrimmed pins the trim contract the shared
// parser documents: the token is returned trimmed, so padding between
// the scheme and the token does not change the token that is looked up.
func TestBearerLeadingWhitespaceIsTrimmed(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodPost, http.MethodGet} {
		t.Run(method, func(t *testing.T) {
			code, status := transportRefusalCode(t, method, "Bearer   mcp_padded_probe  ")
			if code != "INTERNAL.UNEXPECTED" {
				t.Fatalf("want the padded token accepted past the bearer gate, got %q (status %d)", code, status)
			}
		})
	}
}

// TestMalformedAuthorizationHeaderRefused pins every header shape that
// is not a bearer credential as MCP.TOKEN.UNKNOWN, the same refusal the
// transport gave before the parser was shared. A case-insensitive scheme
// match must not widen what counts as a credential: an empty token, a
// missing separator and a different scheme are all still refused, and so
// is a token whose own "mcp_" prefix is miscased, because the scheme is
// case-insensitive while the token is not.
func TestMalformedAuthorizationHeaderRefused(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		header string
	}{
		{"absent", ""},
		{"scheme only", "Bearer"},
		{"empty token", "Bearer "},
		{"whitespace token", "Bearer    "},
		{"no separator", "Bearermcp_probe"},
		{"other scheme", "Basic bWNwX3Byb2Jl"},
		{"token scheme", "Token mcp_probe"},
		{"miscased token prefix", "bearer MCP_probe"},
		{"non-mcp token", "Bearer pat_probe"},
	}
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		for _, tc := range cases {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				code, status := transportRefusalCode(t, method, tc.header)
				if code != "MCP.TOKEN.UNKNOWN" {
					t.Fatalf("want MCP.TOKEN.UNKNOWN for header %q, got %q (status %d)", tc.header, code, status)
				}
				if status != http.StatusUnauthorized {
					t.Fatalf("want 401 for header %q, got %d", tc.header, status)
				}
			})
		}
	}
}
