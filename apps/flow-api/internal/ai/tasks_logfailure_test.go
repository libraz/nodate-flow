package ai

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

// TestLogFailureRedactsProviderError proves that a provider error
// carrying a secret-prefixed token is scrubbed before it is stored in
// ai_invocations.error_code. Without redaction the raw err.Error()
// (which can echo an upstream 401 body containing an API key) would
// land verbatim in the audit row.
func TestLogFailureRedactsProviderError(t *testing.T) {
	var captured InvocationRecord
	o := &Orchestrator{
		LogInvoke: func(_ context.Context, rec InvocationRecord) {
			captured = rec
		},
	}

	rawErr := errors.New("provider returned 401: invalid key Bearer sk-ant-deadbeefSECRET123")
	o.logFailure(context.Background(), 7, "smart_create", providers.Request{Model: "gpt-x"}, rawErr)

	if captured.Status != "error" {
		t.Fatalf("expected error status, got %q", captured.Status)
	}
	if strings.Contains(captured.ErrorCode, "sk-ant-deadbeefSECRET123") {
		t.Fatalf("secret token survived in error_code: %q", captured.ErrorCode)
	}
	if !strings.Contains(captured.ErrorCode, "[REDACTED") {
		t.Fatalf("expected redaction marker in error_code, got %q", captured.ErrorCode)
	}
	// Non-secret context should still survive so the row is debuggable.
	if !strings.Contains(captured.ErrorCode, "provider returned 401") {
		t.Fatalf("redaction over-scrubbed the error: %q", captured.ErrorCode)
	}
}
