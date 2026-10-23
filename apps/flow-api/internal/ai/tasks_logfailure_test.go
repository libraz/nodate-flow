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

// TestInvocationRecordCarriesTheQuotedTask holds both log paths to the
// task the prompt quotes: without task_id the workspace invocation list
// has no row to apply task visibility to.
func TestInvocationRecordCarriesTheQuotedTask(t *testing.T) {
	var got []InvocationRecord
	o := &Orchestrator{
		LogInvoke: func(_ context.Context, rec InvocationRecord) {
			got = append(got, rec)
		},
	}
	ctx := WithTaskID(context.Background(), 42)
	o.logSuccess(ctx, 7, "propose_steps", providers.Request{Model: "m"}, &providers.Response{Text: "[]"})
	o.logFailure(ctx, 7, "propose_steps", providers.Request{Model: "m"}, errors.New("boom"))
	o.logSuccess(context.Background(), 7, "propose_tasks_from", providers.Request{Model: "m"}, &providers.Response{Text: "[]"})

	if len(got) != 3 {
		t.Fatalf("logged %d records, want 3", len(got))
	}
	if got[0].TaskID != 42 || got[1].TaskID != 42 {
		t.Fatalf("task ids = %d, %d; want 42 on both paths of a task-tagged call", got[0].TaskID, got[1].TaskID)
	}
	if got[2].TaskID != 0 {
		t.Fatalf("an untagged call recorded task %d", got[2].TaskID)
	}
}
