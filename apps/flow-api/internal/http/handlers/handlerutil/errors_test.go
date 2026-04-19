package handlerutil

import (
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

func TestHTTPErr_MessageContainsCodeAndMessage(t *testing.T) {
	t.Parallel()

	spec := &apierrors.Spec{
		Code:    "RATE.LIMIT_EXCEEDED",
		Status:  429,
		Message: "Too many requests",
	}

	err := HTTPErr(spec)
	msg := err.Error()

	// The implementation formats as "CODE: Message".
	want := "RATE.LIMIT_EXCEEDED: Too many requests"
	if msg != want {
		t.Errorf("message: got %q, want %q", msg, want)
	}
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
