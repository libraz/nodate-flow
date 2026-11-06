// Package outputcontract validates model output against static, server-owned
// JSON Schema contracts carried by providers.Request.
package outputcontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var (
	// ErrInvalidJSON reports an empty, malformed, or multi-value response.
	ErrInvalidJSON = errors.New("ai/outputcontract: response is not exactly one JSON value")
	// ErrInvalidSpec reports an invalid static contract or a schema that would
	// require loading an external resource.
	ErrInvalidSpec = errors.New("ai/outputcontract: invalid static schema")
	// ErrValidationFailed reports a structural mismatch without echoing model
	// output, schema contents, or validator details into logs and API errors.
	ErrValidationFailed = errors.New("ai/outputcontract: response does not satisfy output contract")
)

const localResourcePrefix = "urn:nodate-flow:output-contract:"

var schemaCache = struct {
	sync.RWMutex
	byKey map[string]*jsonschema.Schema
}{byKey: make(map[string]*jsonschema.Schema)}

// Validate checks exactly one JSON value against the original full schema.
// OutputSpec values are intended to originate from package-owned constants;
// remote references are rejected and the compiler has a deny-all loader so
// schema validation can never read files or make network requests.
func Validate(spec *providers.OutputSpec, payload []byte) error {
	if spec == nil || spec.Contract() == "" || len(spec.SchemaJSON()) == 0 {
		return ErrInvalidSpec
	}
	compiled, err := compile(spec)
	if err != nil {
		return ErrInvalidSpec
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var instance any
	if err := dec.Decode(&instance); err != nil {
		return ErrInvalidJSON
	}
	if err := requireEOF(dec); err != nil {
		return ErrInvalidJSON
	}
	if err := compiled.Validate(instance); err != nil {
		return ErrValidationFailed
	}
	return nil
}

func compile(spec *providers.OutputSpec) (*jsonschema.Schema, error) {
	schemaBytes := spec.SchemaJSON()
	if !offlineSchema(schemaBytes) {
		return nil, ErrInvalidSpec
	}
	keyHash := sha256.Sum256(schemaBytes)
	key := spec.Contract() + ":" + fmt.Sprintf("%x", keyHash[:])

	schemaCache.Lock()
	defer schemaCache.Unlock()
	compiled := schemaCache.byKey[key]
	if compiled != nil {
		return compiled, nil
	}

	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
	if err != nil {
		return nil, err
	}
	location := localResourcePrefix + key
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	c.AssertContent()
	c.UseLoader(denyExternalLoader{})
	if err := c.AddResource(location, doc); err != nil {
		return nil, err
	}
	compiled, err = c.Compile(location)
	if err != nil {
		return nil, err
	}

	schemaCache.byKey[key] = compiled
	return compiled, nil
}

type denyExternalLoader struct{}

func (denyExternalLoader) Load(string) (any, error) {
	return nil, errors.New("external schema loading disabled")
}

func offlineSchema(schemaJSON []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(schemaJSON))
	dec.UseNumber()
	var schema any
	if dec.Decode(&schema) != nil || requireEOF(dec) != nil {
		return false
	}
	return localRefsOnly(schema)
}

func localRefsOnly(value any) bool {
	if _, ok := value.(bool); ok {
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
					if !localRefsOnly(nested) {
						return false
					}
				}
			}
		case "items", "additionalItems", "additionalProperties", "unevaluatedItems", "unevaluatedProperties", "contains", "propertyNames", "not", "if", "then", "else", "contentSchema":
			if nested, ok := child.(map[string]any); ok && !localRefsOnly(nested) {
				return false
			}
		case "allOf", "anyOf", "oneOf", "prefixItems":
			if schemas, ok := child.([]any); ok {
				for _, nested := range schemas {
					if !localRefsOnly(nested) {
						return false
					}
				}
			}
		}
	}
	return true
}

func requireEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}
