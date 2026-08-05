package signals

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// TestWebhookErrEnvelopeShape locks in the C8 contract: webhook
// failure responses ride the same RFC 9457 problem+json envelope as
// every Huma-pipelined endpoint. Without this guarantee, signature
// rejection would emit a status code with no machine-readable `type`
// for the SDK to branch on, leaving the receiver to scrape free-form
// text.
func TestWebhookErrEnvelopeShape(t *testing.T) {
	rec := httptest.NewRecorder()
	webhookErr(rec, apierrors.IntegrationGhWebhookInvalidSignature)

	if rec.Code != apierrors.IntegrationGhWebhookInvalidSignature.Status {
		t.Fatalf("status = %d; want %d", rec.Code, apierrors.IntegrationGhWebhookInvalidSignature.Status)
	}

	gotCT := rec.Header().Get("Content-Type")
	if gotCT != "application/problem+json; charset=utf-8" {
		t.Fatalf("Content-Type = %q; want application/problem+json", gotCT)
	}

	var envelope struct {
		Type        string `json:"type"`
		Title       string `json:"title"`
		Status      int    `json:"status"`
		Detail      string `json:"detail"`
		Description string `json:"description,omitempty"`
		UserAction  string `json:"userAction,omitempty"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response is not JSON: %v\nbody: %s", err, rec.Body.String())
	}
	if envelope.Type != apierrors.IntegrationGhWebhookInvalidSignature.Code {
		t.Fatalf("envelope.type = %q; want %q", envelope.Type, apierrors.IntegrationGhWebhookInvalidSignature.Code)
	}
	if envelope.Status != apierrors.IntegrationGhWebhookInvalidSignature.Status {
		t.Fatalf("envelope.status = %d; want %d", envelope.Status, apierrors.IntegrationGhWebhookInvalidSignature.Status)
	}
}
