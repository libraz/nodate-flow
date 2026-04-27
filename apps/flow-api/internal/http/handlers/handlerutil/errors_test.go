package handlerutil

import (
	"encoding/json"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
)

func TestHTTPErr_ReturnsHumaStatusError(t *testing.T) {
	t.Parallel()

	spec := &apierrors.Spec{
		Code:    "WS.TASK.NOT_FOUND",
		Status:  404,
		Message: "Task not found",
	}

	err := HTTPErr(spec)
	if err == nil {
		t.Fatal("expected non-nil error")
	}

	se, ok := err.(huma.StatusError)
	if !ok {
		t.Fatalf("expected huma.StatusError, got %T", err)
	}

	if got := se.GetStatus(); got != 404 {
		t.Errorf("status: got %d, want 404", got)
	}

	msg := err.Error()
	if msg == "" {
		t.Fatal("error message should not be empty")
	}
}

// TestHTTPErr_EnvelopeShape verifies that the returned error carries the
// problem+json fields described in RFC 9457: the machine-readable code in
// `type`, the HTTP status text in `title`, and the human-readable message
// in `detail` with NO code prefix. Regression guard for the envelope bug
// where the code was concatenated into `detail` and `type` was never set.
func TestHTTPErr_EnvelopeShape(t *testing.T) {
	t.Parallel()

	spec := &apierrors.Spec{
		Code:    "RATE.LIMIT_EXCEEDED",
		Status:  429,
		Message: "Too many requests",
	}

	err := HTTPErr(spec)

	pd, ok := err.(*ProblemDetails)
	if !ok {
		t.Fatalf("expected *ProblemDetails, got %T", err)
	}

	if pd.Type != "RATE.LIMIT_EXCEEDED" {
		t.Errorf("type: got %q, want %q", pd.Type, "RATE.LIMIT_EXCEEDED")
	}
	if pd.Title != "Too Many Requests" {
		t.Errorf("title: got %q, want %q", pd.Title, "Too Many Requests")
	}
	if pd.Detail != "Too many requests" {
		t.Errorf("detail: got %q, want %q (must NOT include code prefix)", pd.Detail, "Too many requests")
	}
	if pd.Status != 429 {
		t.Errorf("status: got %d, want 429", pd.Status)
	}
}

// TestHTTPErr_PropagatesDescriptionAndUserAction guards the wire shape
// added when ErrorResponse was extended. Description and UserAction
// must be copied from the Spec verbatim so the SDK and frontend see
// the catalog text without round-tripping the registry.
func TestHTTPErr_PropagatesDescriptionAndUserAction(t *testing.T) {
	t.Parallel()

	spec := &apierrors.Spec{
		Code:        "WS.TASK.NOT_FOUND",
		Status:      404,
		Message:     "Task not found",
		Description: "The task does not exist or the actor lacks visibility.",
		UserAction:  "Refresh the list and verify the task is still visible to you.",
	}

	pd, ok := HTTPErr(spec).(*ProblemDetails)
	if !ok {
		t.Fatalf("expected *ProblemDetails")
	}
	if pd.Description != spec.Description {
		t.Errorf("description: got %q, want %q", pd.Description, spec.Description)
	}
	if pd.UserAction != spec.UserAction {
		t.Errorf("userAction: got %q, want %q", pd.UserAction, spec.UserAction)
	}

	// Also marshal to JSON and verify the omitempty tags fire when
	// the catalog has not provided the optional fields.
	bare := HTTPErr(&apierrors.Spec{Code: "X.Y.MINIMAL", Status: 400, Message: "bare"})
	bytes, err := json.Marshal(bare)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(bytes)
	if contains(got, "description") || contains(got, "userAction") {
		t.Errorf("empty description/userAction must be omitted, got %s", got)
	}
}

// contains is a tiny strings.Contains stand-in to avoid a top-level
// import in this single test.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestHTTPErr_DifferentStatusCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		spec   *apierrors.Spec
		status int
	}{
		{
			name:   "400 bad request",
			spec:   &apierrors.Spec{Code: "VALIDATION.INVALID", Status: 400, Message: "Invalid input"},
			status: 400,
		},
		{
			name:   "403 forbidden",
			spec:   &apierrors.Spec{Code: "WS.MEMBER.ROLE_DENIED", Status: 403, Message: "Insufficient role"},
			status: 403,
		},
		{
			name:   "500 internal",
			spec:   &apierrors.Spec{Code: "INTERNAL.UNEXPECTED", Status: 500, Message: "Internal error"},
			status: 500,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := HTTPErr(tc.spec)

			se, ok := err.(huma.StatusError)
			if !ok {
				t.Fatalf("expected huma.StatusError, got %T", err)
			}
			if got := se.GetStatus(); got != tc.status {
				t.Errorf("status: got %d, want %d", got, tc.status)
			}
		})
	}
}
