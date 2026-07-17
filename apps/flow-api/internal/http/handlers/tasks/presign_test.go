package tasks

import (
	"testing"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// TestPresignSharedValidation guards that the task presign surface routes
// its content-type / extension checks through the shared handlerutil
// helpers, keeping the task and calendar upload flows consistent.
func TestPresignSharedValidation(t *testing.T) {
	t.Parallel()
	if !handlerutil.IsAllowedContentType("image/png") {
		t.Error("expected image/png to be allowed")
	}
	if handlerutil.IsAllowedContentType("application/octet-stream") {
		t.Error("expected application/octet-stream to be blocked")
	}
	if !handlerutil.HasBlockedExtension("malware.exe") {
		t.Error("expected .exe to be a blocked extension")
	}
}
