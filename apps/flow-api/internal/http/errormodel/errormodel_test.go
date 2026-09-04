package errormodel

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
)

// probeInput exercises the three grounds the model suppresses on. The
// field names are deliberately not the ones the log redactor already
// knows, so what the tests below observe is the mechanism rather than a
// name that happened to be listed somewhere.
type probeInput struct {
	Body struct {
		// SealedMaterial is marked write-only and named nothing like a
		// credential: it stands for the field that gains the protection
		// by declaring what it is.
		SealedMaterial string `json:"sealedMaterial" writeOnly:"true" minLength:"8"`
		// Handle is an ordinary field. Its value must survive.
		Handle string `json:"handle" maxLength:"4"`
		// NewPassword is a compound name no key list spells out.
		NewPassword string `json:"newPassword,omitempty" minLength:"8"`
		Required    string `json:"required"`
	}
}

type probeOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// newProbeAPI registers probeInput on a fresh API with the sanitiser
// installed and the API's own write-only members learned.
func newProbeAPI(t *testing.T) humatest.TestAPI {
	t.Helper()
	Install()
	_, api := humatest.New(t, huma.DefaultConfig("errormodel-probe", "1.0.0"))
	huma.Register(api, huma.Operation{
		OperationID: "probe",
		Method:      http.MethodPost,
		Path:        "/probe",
	}, func(_ context.Context, _ *probeInput) (*probeOutput, error) {
		return &probeOutput{}, nil
	})
	LearnWriteOnlyFields([]huma.API{api})
	return api
}

// TestWriteOnlyFieldValueIsNotEchoed holds the structural marker: a
// member the schema says is never returned in a response is not
// returned in an error response either.
func TestWriteOnlyFieldValueIsNotEchoed(t *testing.T) {
	api := newProbeAPI(t)

	const submitted = "short"
	resp := api.Post("/probe", map[string]any{
		"sealedMaterial": submitted,
		"handle":         "ok",
		"required":       "x",
	})
	body := resp.Body.String()

	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", resp.Code, body)
	}
	if strings.Contains(body, submitted) {
		t.Errorf("write-only value came back in the refusal: %s", body)
	}
	if !strings.Contains(body, "body.sealedMaterial") {
		t.Errorf("refusal does not name the field: %s", body)
	}
}

// TestCompoundCredentialNameIsNotEchoed holds the part-by-part name
// check: `newPassword` is as much a password as `password`.
func TestCompoundCredentialNameIsNotEchoed(t *testing.T) {
	api := newProbeAPI(t)

	const submitted = "shrt"
	resp := api.Post("/probe", map[string]any{
		"sealedMaterial": "long-enough-value",
		"handle":         "ok",
		"newPassword":    submitted,
		"required":       "x",
	})
	body := resp.Body.String()

	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", resp.Code, body)
	}
	if strings.Contains(body, `"`+submitted+`"`) {
		t.Errorf("compound-named credential came back in the refusal: %s", body)
	}
	if !strings.Contains(body, "body.newPassword") {
		t.Errorf("refusal does not name the field: %s", body)
	}
}

// TestOrdinaryFieldValueSurvives is the counterweight. Without it the
// suppression could pass by emptying every value.
func TestOrdinaryFieldValueSurvives(t *testing.T) {
	api := newProbeAPI(t)

	const submitted = "far-too-long"
	resp := api.Post("/probe", map[string]any{
		"sealedMaterial": "long-enough-value",
		"handle":         submitted,
		"required":       "x",
	})
	body := resp.Body.String()

	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", resp.Code, body)
	}
	if !strings.Contains(body, submitted) {
		t.Errorf("ordinary field lost its echoed value: %s", body)
	}
}

// TestSecretPrefixInAnOrdinaryFieldIsRedacted covers the value that no
// field name predicts: a token pasted into a field nobody thought of as
// carrying one.
func TestSecretPrefixInAnOrdinaryFieldIsRedacted(t *testing.T) {
	api := newProbeAPI(t)

	const submitted = "sk-ant-0123456789abcdef"
	resp := api.Post("/probe", map[string]any{
		"sealedMaterial": "long-enough-value",
		"handle":         submitted,
		"required":       "x",
	})
	body := resp.Body.String()

	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", resp.Code, body)
	}
	if strings.Contains(body, submitted) {
		t.Errorf("secret-prefixed value came back in the refusal: %s", body)
	}
	if !strings.Contains(body, "body.handle") {
		t.Errorf("refusal does not name the field: %s", body)
	}
}

// TestEnclosingObjectIsNotEchoed covers the failure reported against the
// object rather than the field: the echoed value is every sibling that
// was sent, write-only ones included.
func TestEnclosingObjectIsNotEchoed(t *testing.T) {
	api := newProbeAPI(t)

	const submitted = "long-enough-value"
	resp := api.Post("/probe", map[string]any{
		"sealedMaterial": submitted,
		"handle":         "ok",
	})
	body := resp.Body.String()

	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", resp.Code, body)
	}
	if strings.Contains(body, submitted) {
		t.Errorf("the request object was echoed back whole: %s", body)
	}
	if !strings.Contains(body, "required") {
		t.Errorf("refusal does not say which property was missing: %s", body)
	}
}

// TestRawPayloadIsNotEchoed covers the parse-failure path, where the
// echoed value is the request payload rather than a member of it.
func TestRawPayloadIsNotEchoed(t *testing.T) {
	api := newProbeAPI(t)

	const submitted = "long-enough-value"
	resp := api.Do(http.MethodPost, "/probe", "Content-Type: application/json",
		strings.NewReader(`{"sealedMaterial":"`+submitted+`"`))
	body := resp.Body.String()

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", resp.Code, body)
	}
	if strings.Contains(body, submitted) {
		t.Errorf("the raw payload was echoed back: %s", body)
	}
}

func TestLeafField(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"body.apiKey":              "apiKey",
		"body":                     "body",
		"path.wsId":                "wsId",
		"body.rules[2].signalKind": "signalKind",
		"body.tags[0]":             "tags",
		"":                         "",
	}
	for loc, want := range cases {
		if got := leafField(loc); got != want {
			t.Errorf("leafField(%q) = %q, want %q", loc, got, want)
		}
	}
}

func TestMustNotReflect(t *testing.T) {
	t.Parallel()

	suppressed := []string{
		"password", "newPassword", "currentPassword",
		"apiKey", "api_key", "clientSecret", "refreshToken",
		"idToken", "code", "authorization",
	}
	for _, name := range suppressed {
		if !mustNotReflect(name) {
			t.Errorf("mustNotReflect(%q) = false, want true", name)
		}
	}

	kept := []string{"title", "displayName", "signalKind", "baseUrl", "locale", "timezone"}
	for _, name := range kept {
		if mustNotReflect(name) {
			t.Errorf("mustNotReflect(%q) = true, want false", name)
		}
	}
}
