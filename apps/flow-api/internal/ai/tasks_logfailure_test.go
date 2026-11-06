package ai

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

// TestLogFailureRedactsProviderError keeps transport details out of the
// bounded ASCII code column while preserving a redacted explanation.
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
	if captured.ErrorCode != "AI.INVOCATION.FAILED" || len(captured.ErrorCode) > 128 {
		t.Fatalf("expected a bounded canonical error code, got %q", captured.ErrorCode)
	}
	for _, char := range captured.ErrorCode {
		if char > 127 {
			t.Fatalf("non-ASCII error code: %q", captured.ErrorCode)
		}
	}
	if strings.Contains(captured.ResponseRedacted, "sk-ant-deadbeefSECRET123") {
		t.Fatalf("secret survived in the redacted explanation: %q", captured.ResponseRedacted)
	}
	if !strings.Contains(captured.ResponseRedacted, "provider returned 401") || !strings.Contains(captured.ResponseRedacted, "[REDACTED") {
		t.Fatalf("missing redacted transport explanation: %q", captured.ResponseRedacted)
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
