package mcp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// A tool's inputSchema is the contract the server publishes through
// tools/list, and it is the only thing an MCP client has to decide what
// to send. Until the check below existed it was advertisement and
// nothing else: the server measured argument sizes and handed whatever
// arrived to the tool body. create_task{priority: 999} was stored
// verbatim against a column the UI renders as "none" and the list query
// orders by DESC, so the task silently outranked every real priority;
// a colour longer than the column overflowed; a calendar enum surfaced
// as an opaque execution failure; a negative offset went straight into
// LIMIT ? OFFSET ?.
//
// The rule is derived from the schema rather than written per tool on
// purpose. A hand-written check has to be remembered by whoever adds
// the next tool, and the same omission returns one tool later — the
// shape this repository already refuses in taskcreate and airequest.
// Deriving it makes "what a tool declares" and "what the server
// enforces" the same sentence, so a tool that declares nothing is
// visibly unconstrained instead of quietly so.
//
// Only the keyword subset the schema helpers in tools.go can emit is
// interpreted (type, required, enum, pattern, minLength, maxLength,
// minimum, maximum, minItems, maxItems, properties, items). An
// unrecognised keyword is ignored rather than treated as a failure, so
// adding one to a schema never rejects previously valid calls by
// accident — but it also never silently starts enforcing something.
// [TestToolSchemaKeywordsAreEnforced]
// closes that gap from the other side by failing when a schema uses a
// keyword this validator does not implement.

// validationKeywords is the set of JSON Schema keywords validateValue
// understands. It is exported to the package's tests so a schema can
// never advertise a constraint the server does not check.
var validationKeywords = map[string]bool{
	"type":        true,
	"description": true,
	"properties":  true,
	"required":    true,
	"items":       true,
	"enum":        true,
	"pattern":     true,
	"minLength":   true,
	"maxLength":   true,
	"minimum":     true,
	"maximum":     true,
	"minItems":    true,
	"maxItems":    true,
}

// patternCache memoises compiled schema patterns. Schemas are built once
// at registration and never change, so the cache is bounded by the tool
// table.
var patternCache sync.Map // string -> *regexp.Regexp

// compilePattern returns the compiled form of a schema pattern. A
// pattern that does not compile yields nil, which callers treat as
// "unconstrained": a broken pattern in a schema is a bug to catch in
// tests ([TestToolSchemaPatternsCompile]), not a reason to reject every
// call at runtime.
func compilePattern(pat string) *regexp.Regexp {
	if v, ok := patternCache.Load(pat); ok {
		re, _ := v.(*regexp.Regexp)
		return re
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		patternCache.Store(pat, (*regexp.Regexp)(nil))
		return nil
	}
	patternCache.Store(pat, re)
	return re
}

// argumentsInvalid builds the caller-facing rejection. The field path is
// included because an agent that cannot see which argument was refused
// retries the same call.
func argumentsInvalid(path, format string, args ...any) error {
	detail := fmt.Sprintf(format, args...)
	if path == "" {
		return apierrors.Newf(apierrors.McpToolArgumentsInvalid, "arguments: %s", detail)
	}
	return apierrors.Newf(apierrors.McpToolArgumentsInvalid, "argument %q: %s", path, detail)
}

// validateArgsAgainstSchema checks a tool call's arguments against the
// tool's advertised input schema. A nil schema or empty arguments
// object passes: a tool with no declared arguments constrains nothing.
func validateArgsAgainstSchema(schema map[string]any, raw json.RawMessage) error {
	if len(schema) == 0 {
		return nil
	}
	body := raw
	if len(body) == 0 {
		body = json.RawMessage("{}")
	}
	var decoded any
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&decoded); err != nil {
		return argumentsInvalid("", "not valid JSON")
	}
	return validateValue(schema, decoded, "")
}

// validateValue applies one schema node to one decoded JSON value.
func validateValue(schema map[string]any, value any, path string) error {
	// A JSON null is how a client spells "not supplied" for an optional
	// argument; the tool bodies decode those into nil pointers and treat
	// them as absent, so the schema must too.
	if value == nil {
		return nil
	}
	typ, _ := schema["type"].(string)
	switch typ {
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return argumentsInvalid(path, "expected an object")
		}
		return validateObject(schema, obj, path)
	case "array":
		arr, ok := value.([]any)
		if !ok {
			return argumentsInvalid(path, "expected an array")
		}
		// The length is checked before anything descends into the
		// elements. An array's length is work the tool then does per
		// element — a child task inserted inside the transaction holding
		// the project row, an id fetched and pasted into an LLM prompt —
		// so it is the argument that costs, and a caller sending ten
		// thousand of them has to be refused for the count rather than
		// after ten thousand element validations. The bounds apply
		// whether or not an item schema is declared, for the same reason:
		// what is being bounded is the number of elements, not their
		// shape.
		if minItems, ok := schemaInt(schema, "minItems"); ok && len(arr) < minItems {
			return argumentsInvalid(path, "must have at least %d items", minItems)
		}
		if maxItems, ok := schemaInt(schema, "maxItems"); ok && len(arr) > maxItems {
			return argumentsInvalid(path, "must have at most %d items", maxItems)
		}
		items, _ := schema["items"].(map[string]any)
		if items == nil {
			return nil
		}
		for i, el := range arr {
			if err := validateValue(items, el, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	case "string":
		s, ok := value.(string)
		if !ok {
			return argumentsInvalid(path, "expected a string")
		}
		return validateString(schema, s, path)
	case "integer", "number":
		num, ok := value.(json.Number)
		if !ok {
			return argumentsInvalid(path, "expected a number")
		}
		return validateNumber(schema, typ, num, path)
	case "boolean":
		if _, ok := value.(bool); !ok {
			return argumentsInvalid(path, "expected a boolean")
		}
		return nil
	default:
		// Untyped node: nothing is declared, so nothing is enforced.
		return nil
	}
}

// validateObject enforces required members and descends into the
// declared properties. Undeclared members are left alone — the tool
// bodies decode into structs and drop what they do not know, and
// rejecting extras would break a client that sends a field a newer
// server understands.
func validateObject(schema map[string]any, obj map[string]any, path string) error {
	for _, name := range requiredNames(schema) {
		v, present := obj[name]
		if !present || v == nil {
			return argumentsInvalid(joinPath(path, name), "is required")
		}
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return nil
	}
	// Sorted so a call breaking two rules is always refused for the same
	// one; an agent that retries otherwise sees the complaint move.
	names := make([]string, 0, len(obj))
	for name := range obj {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		prop, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		if err := validateValue(prop, obj[name], joinPath(path, name)); err != nil {
			return err
		}
	}
	return nil
}

func validateString(schema map[string]any, s, path string) error {
	if allowed, ok := enumValues(schema); ok && !containsString(allowed, s) {
		return argumentsInvalid(path, "must be one of: %s", strings.Join(allowed, ", "))
	}
	// Code points, not bytes: JSON Schema counts characters, and a byte
	// count would refuse a legal Japanese title three times shorter than
	// the limit it is measured against.
	n := utf8.RuneCountInString(s)
	if minLen, ok := schemaInt(schema, "minLength"); ok && n < minLen {
		return argumentsInvalid(path, "must be at least %d characters", minLen)
	}
	if maxLen, ok := schemaInt(schema, "maxLength"); ok && n > maxLen {
		return argumentsInvalid(path, "must be at most %d characters", maxLen)
	}
	if pat, ok := schema["pattern"].(string); ok && pat != "" {
		if re := compilePattern(pat); re != nil && !re.MatchString(s) {
			return argumentsInvalid(path, "must match %s", pat)
		}
	}
	return nil
}

func validateNumber(schema map[string]any, typ string, num json.Number, path string) error {
	if typ == "integer" {
		if _, err := num.Int64(); err != nil {
			return argumentsInvalid(path, "must be an integer")
		}
	}
	f, err := num.Float64()
	if err != nil {
		return argumentsInvalid(path, "is not a valid number")
	}
	if minV, ok := schemaInt(schema, "minimum"); ok && f < float64(minV) {
		return argumentsInvalid(path, "must be >= %d", minV)
	}
	if maxV, ok := schemaInt(schema, "maximum"); ok && f > float64(maxV) {
		return argumentsInvalid(path, "must be <= %d", maxV)
	}
	return nil
}

// requiredNames reads the "required" list, tolerating both the
// []string the schema helpers build and the []any a schema literal
// would produce.
func requiredNames(schema map[string]any) []string {
	switch v := schema["required"].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// enumValues reads the "enum" list. The second result distinguishes an
// absent enum from an empty one, so a schema that declares no permitted
// value refuses everything rather than nothing.
func enumValues(schema map[string]any) ([]string, bool) {
	switch v := schema["enum"].(type) {
	case []string:
		return v, true
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out, true
	default:
		return nil, false
	}
}

// schemaInt reads a numeric keyword. The helpers store plain ints; a
// schema decoded from JSON would carry float64 or json.Number, so all
// three are accepted.
func schemaInt(schema map[string]any, key string) (int, bool) {
	switch v := schema[key].(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func joinPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "." + name
}
