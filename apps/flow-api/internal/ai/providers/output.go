package providers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Termination describes the provider's explicit generation-stop evidence.
// The empty zero value is interpreted as TerminationUnknown.
type Termination string

const (
	TerminationUnknown    Termination = "unknown"
	TerminationComplete   Termination = "complete"
	TerminationIncomplete Termination = "incomplete"
	TerminationRefused    Termination = "refused"
)

// OutputMode records whether a request used provider-native schema output,
// retained the text path for local validation, or did not request a schema.
type OutputMode string

const (
	OutputModeNone          OutputMode = "none"
	OutputModeNativeSchema  OutputMode = "native_schema"
	OutputModeValidatedText OutputMode = "validated_text"
)

// ErrInvalidOutputSpec reports that a static output contract could not be
// represented as one canonical JSON Schema document.
var ErrInvalidOutputSpec = errors.New("ai/providers: invalid output specification")

// OutputSpec is an immutable, server-owned JSON output contract. Its schema
// bytes are copied and canonicalized by [NewOutputSpec]; accessors return
// copies so a request cannot be changed after construction. Call sites must
// build these values from package-owned schema constants, never request data.
type OutputSpec struct {
	contract string
	schema   []byte
}

// NewOutputSpec copies and canonicalizes a static JSON Schema document.
// Schemas are sent only to providers with a verified native dialect; the
// complete original bytes remain available for local validation.
func NewOutputSpec(contract string, schemaJSON []byte) (*OutputSpec, error) {
	contract = strings.TrimSpace(contract)
	if !validContractName(contract) || len(schemaJSON) == 0 {
		return nil, ErrInvalidOutputSpec
	}
	dec := json.NewDecoder(bytes.NewReader(schemaJSON))
	dec.UseNumber()
	var schema any
	if err := dec.Decode(&schema); err != nil {
		return nil, ErrInvalidOutputSpec
	}
	if err := requireJSONEOF(dec); err != nil {
		return nil, ErrInvalidOutputSpec
	}
	canonical, err := json.Marshal(schema)
	if err != nil {
		return nil, ErrInvalidOutputSpec
	}
	return &OutputSpec{contract: contract, schema: canonical}, nil
}

// Contract returns the stable contract identifier.
func (s *OutputSpec) Contract() string {
	if s == nil {
		return ""
	}
	return s.contract
}

// SchemaJSON returns a copy of the canonical original schema.
func (s *OutputSpec) SchemaJSON() []byte {
	if s == nil {
		return nil
	}
	return append([]byte(nil), s.schema...)
}

// Clone returns an independent copy suitable for carrying through a request
// builder without exposing mutable schema storage.
func (s *OutputSpec) Clone() *OutputSpec {
	if s == nil {
		return nil
	}
	return &OutputSpec{contract: s.contract, schema: s.SchemaJSON()}
}

func validContractName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func requireJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

// RequireComplete is the fail-closed boundary for decoded provider results.
// HTTP success and nil error only mean that an upstream exchange completed;
// they do not prove that the model finished a usable answer.
func RequireComplete(resp *Response) error {
	if resp == nil {
		return ErrResponseSchemaMismatch
	}
	switch resp.Termination {
	case TerminationComplete:
		if strings.TrimSpace(resp.Text) == "" {
			return ErrResponseSchemaMismatch
		}
		return nil
	case TerminationIncomplete:
		return ErrGenerationIncomplete
	case TerminationRefused:
		return ErrModelRefused
	default:
		// Without a recognized stop signal the output is not proven complete.
		return fmt.Errorf("%w: unrecognized stop reason", ErrGenerationIncomplete)
	}
}

// maxStopReasonLength bounds provider-controlled diagnostic data retained in
// a response or invocation record.
const maxStopReasonLength = 64

func boundedStopReason(reason string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	if len(reason) > maxStopReasonLength {
		reason = reason[:maxStopReasonLength]
	}
	var b strings.Builder
	b.Grow(len(reason))
	for _, r := range reason {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func outputModeFor(spec *OutputSpec) OutputMode {
	if spec == nil {
		return OutputModeNone
	}
	return OutputModeValidatedText
}

// nativeSchemaFor decides the mode before any HTTP request is sent. If the
// dialect cannot safely represent the schema, it returns validated-text mode
// rather than making a request and retrying it with weaker settings.
func nativeSchemaFor(kind Kind, model, endpoint string, spec *OutputSpec) (OutputMode, json.RawMessage) {
	mode := outputModeFor(spec)
	if spec == nil || spec.contract == "" || len(spec.schema) == 0 {
		return mode, nil
	}
	capability := NativeOutputCapability(kind, model, endpoint)
	if !capability.Verified {
		return mode, nil
	}
	if capability.Dialect == DialectOllamaGenerate {
		if !schemaHasOnlyLocalReferences(spec.schema) {
			return mode, nil
		}
		return OutputModeNativeSchema, json.RawMessage(spec.SchemaJSON())
	}
	projected, ok := projectNativeSchema(spec, capability.Dialect)
	if !ok {
		return mode, nil
	}
	return OutputModeNativeSchema, json.RawMessage(projected)
}

// projectNativeSchema creates the deliberately small JSON Schema subset
// verified for the native response-format dialects. Constraints omitted here
// are still enforced against the original schema by outputcontract.Validate.
func projectNativeSchema(spec *OutputSpec, dialect OutputDialect) ([]byte, bool) {
	var root any
	dec := json.NewDecoder(bytes.NewReader(spec.schema))
	dec.UseNumber()
	if dec.Decode(&root) != nil {
		return nil, false
	}
	if !schemaHasOnlyLocalReferences(spec.schema) {
		return nil, false
	}
	projected, ok := projectSchemaNode(root, true, dialect)
	if !ok {
		return nil, false
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

var strippedNativeSchemaKeywords = map[string]struct{}{
	"$schema": {}, "$id": {}, "$anchor": {}, "$comment": {},
	"title": {}, "description": {},
	"minimum": {}, "maximum": {}, "exclusiveMinimum": {}, "exclusiveMaximum": {}, "multipleOf": {},
	"minLength": {}, "maxLength": {}, "pattern": {}, "format": {},
	"minItems": {}, "maxItems": {}, "uniqueItems": {},
	"minProperties": {}, "maxProperties": {},
	"default": {}, "examples": {}, "deprecated": {}, "readOnly": {}, "writeOnly": {},
}

func projectSchemaNode(node any, root bool, dialect OutputDialect) (any, bool) {
	obj, ok := node.(map[string]any)
	if !ok {
		return nil, false
	}
	projected := make(map[string]any, len(obj)+1)
	for key, value := range obj {
		if _, strip := strippedNativeSchemaKeywords[key]; strip {
			continue
		}
		switch key {
		case "type":
			if !validNativeType(value) {
				return nil, false
			}
			projected[key] = value
		case "properties":
			props, ok := value.(map[string]any)
			if !ok {
				return nil, false
			}
			outProps := make(map[string]any, len(props))
			for name, child := range props {
				out, ok := projectSchemaNode(child, false, dialect)
				if !ok {
					return nil, false
				}
				outProps[name] = out
			}
			projected[key] = outProps
		case "required":
			if !validRequired(value) {
				return nil, false
			}
			projected[key] = value
		case "additionalProperties":
			allowed, ok := value.(bool)
			if !ok || allowed {
				return nil, false // maps and true are dynamic object schemas.
			}
			projected[key] = false
		case "items":
			out, ok := projectSchemaNode(value, false, dialect)
			if !ok {
				return nil, false
			}
			projected[key] = out
		case "enum", "const":
			projected[key] = value
		default:
			return nil, false // references, unions and unknown keywords stay on text fallback.
		}
	}

	typeName, isStringType := obj["type"].(string)
	if root && (!isStringType || typeName != "object") {
		return nil, false // OpenAI strict structured outputs require an object root.
	}
	if _, hasProperties := obj["properties"]; hasProperties && !schemaTypeContains(obj["type"], "object") {
		return nil, false
	}
	if _, hasRequired := obj["required"]; hasRequired && !schemaTypeContains(obj["type"], "object") {
		return nil, false
	}
	if _, hasAdditionalProperties := obj["additionalProperties"]; hasAdditionalProperties && !schemaTypeContains(obj["type"], "object") {
		return nil, false
	}
	if _, hasItems := obj["items"]; hasItems && !schemaTypeContains(obj["type"], "array") {
		return nil, false
	}
	if schemaTypeContains(obj["type"], "object") {
		props, hasProps := obj["properties"].(map[string]any)
		if !hasProps {
			if _, closed := obj["additionalProperties"]; !closed {
				return nil, false // a free-form object would be projected to accept only {}.
			}
			projected["properties"] = map[string]any{}
			props = map[string]any{}
		}
		if allowed, exists := obj["additionalProperties"]; exists {
			if flag, ok := allowed.(bool); !ok || flag {
				return nil, false
			}
		} else {
			projected["additionalProperties"] = false
		}
		required := map[string]struct{}{}
		if list, ok := obj["required"].([]any); ok {
			for _, item := range list {
				name, ok := item.(string)
				if !ok {
					return nil, false
				}
				if _, ok := props[name]; !ok {
					return nil, false
				}
				if _, duplicate := required[name]; duplicate {
					return nil, false
				}
				required[name] = struct{}{}
			}
		}
		if dialect == DialectOpenAIChatCompletions {
			for name := range props {
				if _, ok := required[name]; !ok {
					return nil, false // strict OpenAI schemas require every declared property.
				}
			}
		}
	}
	if schemaTypeContains(obj["type"], "array") {
		if _, ok := obj["items"]; !ok {
			return nil, false
		}
	}
	return projected, true
}

func schemaTypeContains(value any, want string) bool {
	if name, ok := value.(string); ok {
		return name == want
	}
	list, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range list {
		if name, ok := item.(string); ok && name == want {
			return true
		}
	}
	return false
}

func validNativeType(value any) bool {
	if name, ok := value.(string); ok {
		return name == "object" || name == "array" || name == "string" || name == "integer" || name == "number" || name == "boolean" || name == "null"
	}
	list, ok := value.([]any)
	if !ok || len(list) == 0 {
		return false
	}
	for _, item := range list {
		name, ok := item.(string)
		if !ok || !validNativeTypeName(name) {
			return false
		}
	}
	return true
}

func validNativeTypeName(name string) bool {
	switch name {
	case "object", "array", "string", "integer", "number", "boolean", "null":
		return true
	default:
		return false
	}
}

func validRequired(value any) bool {
	list, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range list {
		if _, ok := item.(string); !ok {
			return false
		}
	}
	return true
}

func schemaHasOnlyLocalReferences(schemaJSON []byte) bool {
	var schema any
	dec := json.NewDecoder(bytes.NewReader(schemaJSON))
	dec.UseNumber()
	if dec.Decode(&schema) != nil {
		return false
	}
	return localSchemaReferencesOnly(schema)
}

func localSchemaReferencesOnly(value any) bool {
	if _, ok := value.(bool); ok { // boolean schemas cannot load resources.
		return true
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return false
	}
	for key, child := range obj {
		if key == "$ref" || key == "$dynamicRef" || key == "$recursiveRef" {
			ref, ok := child.(string)
			if !ok || !strings.HasPrefix(ref, "#") {
				return false
			}
		}
		if key == "$schema" {
			url, ok := child.(string)
			if !ok || (url != "https://json-schema.org/draft/2020-12/schema" && url != "https://json-schema.org/draft/2020-12/schema#") {
				return false
			}
		}
		switch key {
		case "$defs", "definitions", "properties", "patternProperties", "dependentSchemas":
			if schemas, ok := child.(map[string]any); ok {
				for _, nested := range schemas {
					if !localSchemaReferencesOnly(nested) {
						return false
					}
				}
			}
		case "items", "additionalItems", "additionalProperties", "unevaluatedItems", "unevaluatedProperties", "contains", "propertyNames", "not", "if", "then", "else", "contentSchema":
			if nested, isSchema := child.(map[string]any); isSchema {
				if !localSchemaReferencesOnly(nested) {
					return false
				}
			}
		case "allOf", "anyOf", "oneOf", "prefixItems":
			if schemas, ok := child.([]any); ok {
				for _, nested := range schemas {
					if !localSchemaReferencesOnly(nested) {
						return false
					}
				}
			}
		}
	}
	return true
}
