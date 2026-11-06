package outputcontract

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

func outputSpec(t *testing.T, schema string) *providers.OutputSpec {
	t.Helper()
	spec, err := providers.NewOutputSpec("test-contract", []byte(schema))
	if err != nil {
		t.Fatalf("NewOutputSpec: %v", err)
	}
	return spec
}

func TestValidateChecksStaticJSONSchema(t *testing.T) {
	spec := outputSpec(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"name":{"type":"string","minLength":2}},"required":["name"],"additionalProperties":false}`)
	for _, tc := range []struct {
		name    string
		payload string
		wantErr bool
	}{
		{"valid object", `{"name":"Ada"}`, false},
		{"missing required property", `{}`, true},
		{"constraint violation", `{"name":"A"}`, true},
		{"additional property", `{"name":"Ada","role":"admin"}`, true},
		{"wrong type", `{"name":42}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(spec, []byte(tc.payload))
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate(%s) err = %v, wantErr=%t", tc.payload, err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, ErrValidationFailed) {
				t.Fatalf("failure was not sanitized with ErrValidationFailed: %v", err)
			}
		})
	}
}

func TestValidateConsumesExactlyOneJSONValue(t *testing.T) {
	spec := outputSpec(t, `{"type":"array","items":{"type":"integer"}}`)
	for _, payload := range []string{"", "[] {}", "[] trailing", `{"x":`, `"unterminated`} {
		t.Run(payload, func(t *testing.T) {
			if err := Validate(spec, []byte(payload)); !errors.Is(err, ErrInvalidJSON) {
				t.Fatalf("Validate err = %v, want ErrInvalidJSON", err)
			}
		})
	}
	if err := Validate(spec, []byte(`[1,2,3]`)); err != nil {
		t.Fatalf("valid top-level array rejected: %v", err)
	}
}

func TestValidateRejectsRemoteSchemaReferences(t *testing.T) {
	spec := outputSpec(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","$ref":"https://attacker.invalid/schema.json"}`)
	if err := Validate(spec, []byte(`{}`)); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("Validate err = %v, want ErrInvalidSpec; the validator must never load external schemas", err)
	}
}

func TestValidateNeverLoadsExternalSchemaURLs(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"type":"object"}`))
	}))
	t.Cleanup(srv.Close)

	spec := outputSpec(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","$ref":"`+srv.URL+`/schema.json"}`)
	if err := Validate(spec, []byte(`{}`)); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("Validate err = %v, want ErrInvalidSpec", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("schema validation made %d external request(s), want none", calls.Load())
	}
}

func TestValidateDoesNotTreatPropertyNamesAsSchemaKeywords(t *testing.T) {
	spec := outputSpec(t, `{"type":"object","properties":{"$ref":{"type":"string"}},"required":["$ref"],"additionalProperties":false}`)
	if err := Validate(spec, []byte(`{"$ref":"literal data"}`)); err != nil {
		t.Fatalf("literal property named $ref was treated as a remote reference: %v", err)
	}
}

func TestValidateSupportsLocalReferences(t *testing.T) {
	spec := outputSpec(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","$defs":{"name":{"type":"string","minLength":1}},"type":"object","properties":{"name":{"$ref":"#/$defs/name"}},"required":["name"],"additionalProperties":false}`)
	if err := Validate(spec, []byte(`{"name":"Ada"}`)); err != nil {
		t.Fatalf("valid local reference rejected: %v", err)
	}
	if err := Validate(spec, []byte(`{"name":""}`)); !errors.Is(err, ErrValidationFailed) {
		t.Fatalf("local reference constraint err = %v, want ErrValidationFailed", err)
	}
}
