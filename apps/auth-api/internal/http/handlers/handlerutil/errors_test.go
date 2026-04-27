package handlerutil

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
)

func TestHTTPErr_ReturnsStatusError(t *testing.T) {
	t.Parallel()

	spec := &apierrors.Spec{
		Code:    "AUTH.LOGIN.INVALID_CREDENTIALS",
		Status:  401,
		Message: "Invalid email or password",
	}

	err := HTTPErr(spec)
	if err == nil {
		t.Fatal("expected non-nil error")
	}

	se, ok := err.(huma.StatusError)
	if !ok {
		t.Fatalf("expected huma.StatusError, got %T", err)
	}
	if got := se.GetStatus(); got != 401 {
		t.Errorf("status: got %d, want 401", got)
	}
}

func TestHTTPErr_PropagatesDescriptionAndUserAction(t *testing.T) {
	t.Parallel()

	spec := &apierrors.Spec{
		Code:        "AUTH.MAGIC_LINK.EXPIRED",
		Status:      401,
		Message:     "Magic link has expired",
		Description: "Returned when the user clicks a magic link past its TTL.",
		UserAction:  "Request a new magic link from the sign-in page.",
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

	bare := HTTPErr(&apierrors.Spec{Code: "X.Y.MINIMAL", Status: 400, Message: "bare"})
	bytes, err := json.Marshal(bare)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(bytes)
	if strings.Contains(got, "description") || strings.Contains(got, "userAction") {
		t.Errorf("empty optional fields must be omitted, got %s", got)
	}
}
