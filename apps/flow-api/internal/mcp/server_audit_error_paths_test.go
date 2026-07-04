package mcp

import (
	"os"
	"strings"
	"testing"
)

func TestHandleToolCallAuditsPreExecutionErrorPaths(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	for _, code := range []string{
		"apierrors.McpProtocolFrameMalformed.Code",
		"apierrors.McpToolNotFound.Code",
	} {
		if !strings.Contains(body, code) {
			t.Fatalf("handleToolCall audit path missing %s", code)
		}
	}
	if strings.Count(body, "generated.McpInvocationsStatusError") < 5 {
		t.Fatal("expected pre-execution MCP error paths to audit status=error")
	}
}
