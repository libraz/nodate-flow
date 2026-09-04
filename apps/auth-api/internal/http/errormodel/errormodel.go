// Package errormodel installs the request-validation error model the
// service answers with.
//
// Huma's default model echoes the rejected value back to the caller in
// errors[].value. For an ordinary field that is a debugging aid. For a
// credential it is a disclosure: a refused API key, password or one-time
// code travels back out in the response body and from there into proxy
// and access logs, browser history, error-reporting clients, and any
// support ticket the body is pasted into.
//
// [Install] keeps the refusal useful — message and location still name
// the field and say what was wrong with it — and drops only the value,
// on three structural grounds:
//
//   - The value is a composite (object or array). Huma reports a
//     missing required property against the enclosing object, so the
//     echoed value is the whole request body with every sibling field
//     in it. There is no way to know none of them is a secret, and the
//     message already names the property that was missing.
//   - The location is `body` itself. That is the body-parse failure
//     path, where the echoed value is the raw request payload.
//   - The field must not be reflected: its schema declares it
//     write-only, or the shared log redactor refuses to print a JSON
//     member under that name.
//
// A string value that survives all three is still passed through
// [logutil.Redact], so a token pasted into an ordinary field is scrubbed
// by the same prefix scanner that guards the logs.
//
// flow-api carries the same package. Neither service can import the
// other's internal packages, so the policy is stated once per service.
package errormodel

import (
	"reflect"
	"strings"
	"sync"
	"unicode"

	"github.com/danielgtaylor/huma/v2"

	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// Install replaces huma.NewError with a wrapper that sanitises the
// echoed values of every error detail Huma produces, including the
// validation failures raised before a handler ever runs.
//
// huma.NewError is process-global and huma.NewErrorWithContext delegates
// to it, so one call covers every sub-API the router mounts. Repeat
// calls are no-ops: wrapping the wrapper would work but would grow a
// chain per router built, and the test harness builds many.
func Install() {
	installOnce.Do(func() {
		inner := huma.NewError
		huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
			se := inner(status, msg, errs...)
			if em, ok := se.(*huma.ErrorModel); ok {
				sanitize(em.Errors)
			}
			return se
		}
	})
}

var installOnce sync.Once

// LearnWriteOnlyFields records the JSON member names that the given
// APIs' schemas declare write-only, so a validation failure on one of
// them does not echo the value back.
//
// writeOnly is the OpenAPI statement that a member is accepted in a
// request and never returned in a response. A value that must not appear
// in a success response must not appear in an error response either, so
// the tag on the DTO is what decides — a field gains the protection by
// declaring what it is, not by being added to a list here.
//
// Call this after every operation has been registered: Huma populates
// the schema registry lazily as it registers them.
func LearnWriteOnlyFields(apis []huma.API) {
	found := map[string]struct{}{}
	for _, a := range apis {
		if a == nil {
			continue
		}
		spec := a.OpenAPI()
		if spec == nil || spec.Components == nil || spec.Components.Schemas == nil {
			continue
		}
		for _, s := range spec.Components.Schemas.Map() {
			collectWriteOnly(s, found, 0)
		}
	}
	if len(found) == 0 {
		return
	}
	writeOnlyMu.Lock()
	for name := range found {
		writeOnlyFields[name] = struct{}{}
	}
	writeOnlyMu.Unlock()
	// Verdicts reached before this call were reached without these
	// names, so they are no longer answers to the same question.
	reflectCache.Clear()
}

var (
	writeOnlyMu     sync.RWMutex
	writeOnlyFields = map[string]struct{}{}
)

// maxSchemaDepth bounds the walk over a registered schema. Registered
// schemas reference each other through $ref rather than nesting, so the
// inline depth that actually occurs is one or two; the bound is here so
// a self-referential schema cannot spin.
const maxSchemaDepth = 8

func collectWriteOnly(s *huma.Schema, out map[string]struct{}, depth int) {
	if s == nil || depth > maxSchemaDepth {
		return
	}
	for name, prop := range s.Properties {
		if prop == nil {
			continue
		}
		if prop.WriteOnly {
			out[normalizeFieldName(name)] = struct{}{}
		}
		collectWriteOnly(prop, out, depth+1)
	}
	collectWriteOnly(s.Items, out, depth+1)
}

// sanitize rewrites the error details in place.
func sanitize(details []*huma.ErrorDetail) {
	for _, d := range details {
		if d == nil || d.Value == nil {
			continue
		}
		if mustNotEcho(d.Location, d.Value) {
			d.Value = nil
			continue
		}
		if s, ok := d.Value.(string); ok {
			d.Value = logutil.Redact(s)
		}
	}
}

// mustNotEcho reports whether the value at loc has to be dropped from
// the response. See the package doc for the three grounds.
func mustNotEcho(loc string, v any) bool {
	switch reflect.ValueOf(v).Kind() {
	case reflect.Map, reflect.Slice, reflect.Array, reflect.Struct:
		return true
	}
	if loc == bodyLocation {
		return true
	}
	return mustNotReflect(leafField(loc))
}

// bodyLocation is the location Huma reports for a failure against the
// request payload as a whole rather than against one of its members.
const bodyLocation = "body"

// leafField extracts the member name from a Huma error location such as
// `body.rules[2].signalKind` or `path.wsId`, dropping the section prefix
// and any array index.
func leafField(loc string) string {
	if i := strings.LastIndexByte(loc, '.'); i >= 0 {
		loc = loc[i+1:]
	}
	if i := strings.IndexByte(loc, '['); i >= 0 {
		loc = loc[:i]
	}
	return loc
}

// mustNotReflect reports whether a member of this name carries something
// that may not come back out. Answers are memoised because the set of
// names is bounded by the DTOs and the checks below are pure.
func mustNotReflect(field string) bool {
	if field == "" {
		return false
	}
	if cached, ok := reflectCache.Load(field); ok {
		return cached.(bool)
	}
	verdict := isWriteOnly(field) || redactedByLogPolicy(field)
	reflectCache.Store(field, verdict)
	return verdict
}

var reflectCache sync.Map

func isWriteOnly(field string) bool {
	writeOnlyMu.RLock()
	defer writeOnlyMu.RUnlock()
	_, ok := writeOnlyFields[normalizeFieldName(field)]
	return ok
}

// redactedByLogPolicy reports whether the shared log redactor refuses to
// print a JSON member under this name, either whole (`apiKey`) or in one
// of its parts (`newPassword`, `clientSecret`).
//
// The redactor is asked rather than quoted. Its key set is already the
// project's single answer to "which member names carry a secret", and
// restating it here would produce a second copy that has to be kept true
// by hand — the failure mode this fix exists to remove. logutil exposes
// the set only through the redaction it performs, so the question is put
// as the smallest JSON object that would carry the member: if the
// redactor rewrites it, the name is one it will not print.
func redactedByLogPolicy(field string) bool {
	if probeRedactor(field) {
		return true
	}
	for _, part := range splitFieldName(field) {
		if probeRedactor(part) {
			return true
		}
	}
	return false
}

// probeValue is the placeholder member value used to ask the
// redactor about a name. It matches no secret prefix, so a rewrite can
// only have come from the name.
const probeValue = "v"

func probeRedactor(name string) bool {
	if name == "" || strings.ContainsAny(name, `"\`) {
		return false
	}
	probe := `{"` + name + `":"` + probeValue + `"}`
	return logutil.RedactJSONFields(probe) != probe
}

// splitFieldName breaks a JSON member name into its words so a compound
// name is checked part by part: `newPassword` is as much a password as
// `password`, and `refresh_token` as much a token as `token`.
func splitFieldName(field string) []string {
	var parts []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			parts = append(parts, cur.String())
			cur.Reset()
		}
	}
	for _, r := range field {
		switch {
		case r == '_' || r == '-':
			flush()
		case unicode.IsUpper(r):
			flush()
			cur.WriteRune(unicode.ToLower(r))
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	if len(parts) < 2 {
		return nil
	}
	return parts
}

// normalizeFieldName folds a member name the way the log redactor folds
// its keys, so `api_key` and `apiKey` are one name.
func normalizeFieldName(name string) string {
	return strings.ReplaceAll(strings.ToLower(name), "_", "")
}
