package export

import (
	"context"
	"errors"
	"testing"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// TestExportCSVMissingWorkspaceReturnsTypedError guards the regression
// where the CSV path answered with a plain-text body carrying the raw
// error code — no JSON envelope, no `type` field — when the workspace
// context was missing.
//
// The route used to write its own response and therefore its own
// envelope; it is now a Huma operation that returns the error and lets
// the same renderer as every other route shape it. What this test still
// owns is the half the handler decides: that the failure is the typed
// workspace-not-found and not something generic.
func TestExportCSVMissingWorkspaceReturnsTypedError(t *testing.T) {
	t.Parallel()

	// Deps left zero — the handler returns before touching them when
	// the workspace is missing from the context.
	_, err := CSVOperation(Deps{})(context.Background(), &CSVInput{WsID: "x"})
	if err == nil {
		t.Fatal("a request with no workspace context must not produce a file")
	}

	var problem *handlerutil.ProblemDetails
	if !errors.As(err, &problem) {
		t.Fatalf("error = %T; want *handlerutil.ProblemDetails", err)
	}
	if problem.Type != apierrors.WsWorkspaceNotFound.Code {
		t.Errorf("type: got %q want %q", problem.Type, apierrors.WsWorkspaceNotFound.Code)
	}
	if problem.Status != apierrors.WsWorkspaceNotFound.Status {
		t.Errorf("status: got %d want %d", problem.Status, apierrors.WsWorkspaceNotFound.Status)
	}
}
