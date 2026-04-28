package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// mcpCallRaw sends a single JSON-RPC 2.0 request frame to /mcp with the
// given bearer token and returns the raw HTTP status + response body
// without asserting 2xx. Use this when the test expects the transport
// to reject the request before reaching JSON-RPC dispatch (token
// unknown, token expired) so the HTTP status is meaningful.
func mcpCallRaw(t *testing.T, token, method string, params any) (int, []byte) {
	t.Helper()
	frame := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
	}
	if params != nil {
		frame["params"] = params
	}
	buf, err := json.Marshal(frame)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, testServerURL+"/mcp", bytes.NewReader(buf))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, raw
}

// mcpErrorCode decodes a JSON-RPC error envelope and returns the stable
// nodate-flow string error code that lives in error.data.code (per
// writeRPCError in apps/flow-api/internal/mcp/server.go). When the
// envelope does not contain an error, it returns "" so the caller can
// fail with a descriptive assertion.
func mcpErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    struct {
				Code string `json:"code"`
			} `json:"data"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &env), "decode jsonrpc envelope: %s", string(body))
	if env.Error == nil {
		return ""
	}
	return env.Error.Data.Code
}
