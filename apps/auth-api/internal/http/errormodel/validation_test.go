package errormodel

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"

	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

type paramProbeInput struct {
	ID    string `path:"id" pattern:"^[a-z]+$"`
	Limit int    `query:"limit" minimum:"1" maximum:"10"`
}

// newParamProbeAPI registers an operation with a constrained path and
// query parameter next to the body probe.
func newParamProbeAPI(t *testing.T) humatest.TestAPI {
	t.Helper()
	api := newProbeAPI(t)
	huma.Register(api, huma.Operation{
		OperationID: "param-probe",
		Method:      http.MethodGet,
		Path:        "/param-probe/{id}",
	}, func(_ context.Context, _ *paramProbeInput) (*probeOutput, error) {
		return &probeOutput{}, nil
	})
	return api
}

// TestValidationRefusalsCarryACatalogCode holds every refusal Huma raises
// before a handler runs to a type defined in errors/*.yaml, with the
// status that code carries.
func TestValidationRefusalsCarryACatalogCode(t *testing.T) {
	api := newParamProbeAPI(t)

	cases := []struct {
		name string
		do   func() (int, string)
		want *apierr.Spec
	}{
		{"field too long", func() (int, string) {
			r := api.Post("/probe", map[string]any{"sealedMaterial": "long-enough-value", "handle": "far-too-long", "required": "x"})
			return r.Code, r.Body.String()
		}, apierrors.ValidationBodyFieldInvalid},
		{"required field missing", func() (int, string) {
			r := api.Post("/probe", map[string]any{"sealedMaterial": "long-enough-value", "handle": "ok"})
			return r.Code, r.Body.String()
		}, apierrors.ValidationBodyFieldMissing},
		{"body not json", func() (int, string) {
			r := api.Do(http.MethodPost, "/probe", "Content-Type: application/json", strings.NewReader(`{"handle":`))
			return r.Code, r.Body.String()
		}, apierrors.ValidationBodyUnparseable},
		{"query out of range", func() (int, string) {
			r := api.Get("/param-probe/abc?limit=99")
			return r.Code, r.Body.String()
		}, apierrors.ValidationQueryFieldInvalid},
		{"path malformed", func() (int, string) {
			r := api.Get("/param-probe/ABC1")
			return r.Code, r.Body.String()
		}, apierrors.ValidationPathParamInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := tc.do()
			var got struct {
				Type   string `json:"type"`
				Status int    `json:"status"`
			}
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("decode %q: %v", body, err)
			}
			if got.Type != tc.want.Code {
				t.Errorf("type = %q, want %q; body = %s", got.Type, tc.want.Code, body)
			}
			if status != tc.want.Status || got.Status != tc.want.Status {
				t.Errorf("status = %d (body %d), want %d", status, got.Status, tc.want.Status)
			}
		})
	}
}
