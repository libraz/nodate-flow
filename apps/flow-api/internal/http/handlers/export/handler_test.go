package export

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// TestExportCSVMissingWorkspaceWritesProblemJSON guards the regression
// where the CSV path emitted a plain-text response carrying the raw
// error code in the body (no JSON envelope, no `type` field) when the
// workspace context was missing. The fix routes through
// handlerutil.WriteSpecError so the wire shape matches the JSON route.
func TestExportCSVMissingWorkspaceWritesProblemJSON(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/workspaces/x/export/tasks.csv", nil)

	// Deps left zero — the handler returns before touching them when
	// the workspace is missing from the context.
	ExportCSV(Deps{})(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/problem+json; charset=utf-8" {
		t.Errorf("content-type: got %q want application/problem+json; charset=utf-8", ct)
	}
	var got struct {
		Type   string `json:"type"`
		Status int    `json:"status"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Type != apierrors.WsWorkspaceNotFound.Code {
		t.Errorf("type: got %q want %q", got.Type, apierrors.WsWorkspaceNotFound.Code)
	}
	if got.Status != apierrors.WsWorkspaceNotFound.Status {
		t.Errorf("status field: got %d want %d", got.Status, apierrors.WsWorkspaceNotFound.Status)
	}
}

// TestWriteFetchErrorPropagatesHumaEnvelope asserts that the helper
// preserves the type / status / detail of an error originally built by
// handlerutil.HTTPErr — the path used by fetchForLens / fetchForWorkspace.
func TestWriteFetchErrorPropagatesHumaEnvelope(t *testing.T) {
	t.Parallel()

	spec := apierrors.ExportTaskLensNotFound
	rr := httptest.NewRecorder()
	writeFetchError(rr, handlerutil.HTTPErr(spec))

	if rr.Code != spec.Status {
		t.Errorf("status: got %d want %d", rr.Code, spec.Status)
	}
	var got struct {
		Type   string `json:"type"`
		Status int    `json:"status"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Type != spec.Code {
		t.Errorf("type: got %q want %q", got.Type, spec.Code)
	}
	if got.Detail != spec.Message {
		t.Errorf("detail: got %q want %q", got.Detail, spec.Message)
	}
}

// TestWriteFetchErrorUnknownShapeFallsBackToInternal asserts that an
// arbitrary error that did not come from handlerutil.HTTPErr collapses
// to INTERNAL.UNEXPECTED rather than leaking the raw error string.
func TestWriteFetchErrorUnknownShapeFallsBackToInternal(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	writeFetchError(rr, errString("opaque internal error"))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
	var got struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Type != apierrors.InternalUnexpected.Code {
		t.Errorf("type: got %q want %q", got.Type, apierrors.InternalUnexpected.Code)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
