package eventlog

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Every identifier a workspace member can see is a public_id (UUID v7);
// the internal auto-increment keys stay inside the database. Event
// payloads are the one place that rule kept getting broken, because a
// payload is a free-form map: a builder that has the internal id in hand
// writes it without anything objecting, and it then surfaces verbatim
// through the timeline API to every member of the workspace.
//
// Beyond identifying users and tasks the caller has no business
// resolving, a monotonic key is a population counter — the highest id
// anyone observes tells them how many users, tasks or uploads exist on
// the whole instance. And because a leaked id is a number where the same
// field is a UUID string everywhere else, it also breaks the generated
// SDK's type for that field.
//
// ValidatePayloadIDs is the runtime rail for that rule. It is checked on
// the append path rather than by review or a source scan because a
// payload map is assembled at runtime from whatever the builder happens
// to hold: only the finished value says what will actually be stored.

// idKeySuffixes are the camelCase endings that mark a payload key as an
// identifier. Payload keys follow the JSON wire convention, so the
// database's snake_case never appears here.
var idKeySuffixes = []string{"Id", "ID", "Ids", "IDs"}

// ValidatePayloadIDs reports an error when payload carries a numeric
// value under an identifier-shaped key, at any depth.
//
// It is deliberately narrow. A number under `taskId` is always an
// internal key, since every public identifier is a UUID string; a number
// under `count` or `durationMs` is data. Nesting is walked because a
// payload often carries a list of changed rows rather than a flat map.
//
// What it cannot see: an internal id that was already formatted as a
// string. Nothing in the payload distinguishes "42" from a legitimate
// external identifier that happens to be digits, so the rail stops at
// the JSON type. Builders convert through public_id rather than
// strconv, which is what keeps that case out.
func ValidatePayloadIDs(payload any) error {
	if payload == nil {
		return nil
	}
	seen := map[string]struct{}{}
	walkPayloadIDs("", false, reflect.ValueOf(payload), seen)
	if len(seen) == 0 {
		return nil
	}
	offenders := make([]string, 0, len(seen))
	for path := range seen {
		offenders = append(offenders, path)
	}
	sort.Strings(offenders)
	return fmt.Errorf(
		"eventlog: payload carries internal numeric identifiers (%s): resolve them to public_id (UUID v7) before appending",
		strings.Join(offenders, ", "))
}

// walkPayloadIDs descends through maps, slices and structs, recording
// every id-shaped key that carries a number. path is the dotted key
// trail so the error names the exact field; underIDKey says whether the
// value being visited sits under an identifier key, which a list of ids
// inherits from the key naming the list.
func walkPayloadIDs(path string, underIDKey bool, v reflect.Value, seen map[string]struct{}) {
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return
	}
	if underIDKey && isNumeric(v) {
		seen[path] = struct{}{}
		return
	}

	switch v.Kind() {
	case reflect.Map:
		for _, key := range v.MapKeys() {
			name, ok := mapKeyString(key)
			if !ok {
				continue
			}
			walkPayloadIDs(join(path, name), isIDKey(name), v.MapIndex(key), seen)
		}
	case reflect.Slice, reflect.Array:
		// Elements inherit the key that named the list, so the offending
		// field reads "taskIds" rather than "taskIds.0".
		for i := 0; i < v.Len(); i++ {
			walkPayloadIDs(path, underIDKey, v.Index(i), seen)
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name := jsonFieldName(f.Name, f.Tag.Get("json"))
			if name == "" {
				continue
			}
			walkPayloadIDs(join(path, name), isIDKey(name), v.Field(i), seen)
		}
	default:
		// Scalars reached without an id-shaped key are data.
	}
}

// join appends a key to a dotted path.
func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// mapKeyString renders a map key as the string it will serialise to.
// Non-string keys cannot produce an id-shaped JSON field, so they are
// skipped rather than stringified.
func mapKeyString(key reflect.Value) (string, bool) {
	for key.Kind() == reflect.Interface || key.Kind() == reflect.Pointer {
		if key.IsNil() {
			return "", false
		}
		key = key.Elem()
	}
	if key.Kind() != reflect.String {
		return "", false
	}
	return key.String(), true
}

// jsonFieldName resolves the wire name of a struct field, honouring the
// json tag and its "-" opt-out.
func jsonFieldName(goName, tag string) string {
	name, _, _ := strings.Cut(tag, ",")
	switch name {
	case "-":
		return ""
	case "":
		return goName
	default:
		return name
	}
}

// isIDKey reports whether a payload key names an identifier.
func isIDKey(key string) bool {
	if key == "id" || key == "ids" {
		return true
	}
	for _, suffix := range idKeySuffixes {
		if len(key) > len(suffix) && strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

// isNumeric reports whether the value will serialise as a JSON number.
func isNumeric(v reflect.Value) bool {
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) {
		if v.IsNil() {
			return false
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return false
	}
	if v.Type() == reflect.TypeOf(json.Number("")) {
		return true
	}
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

// RedactPayloadIDs removes identifier-shaped keys carrying numbers from
// a stored payload.
//
// [ValidatePayloadIDs] keeps new rows clean, but the events table is
// append-only: rows written before the rail existed still name tasks and
// users by their internal keys, and the timeline hands payload_json to
// every workspace member verbatim. Redacting on the way out covers those
// rows, and covers any future writer that reaches the table without
// going through an append function.
//
// A dropped key is better than a corrected one: nothing here can resolve
// an internal id to its public form, and leaving a number in a field the
// rest of the API spells as a UUID string would hand the client a value
// it cannot use and a type the generated SDK does not expect.
//
// Unparseable input yields an empty object. The column is JSON-typed, so
// that case means the row is already unreadable, and guessing at partial
// content risks passing through the very keys this removes.
func RedactPayloadIDs(raw []byte) []byte {
	if len(raw) == 0 {
		return raw
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return []byte(`{}`)
	}
	redacted, _ := redactValue(doc, false)
	out, err := json.Marshal(redacted)
	if err != nil {
		return []byte(`{}`)
	}
	return out
}

// redactValue rebuilds v without the id-shaped numeric leaves. The
// boolean reports whether the value itself must be dropped by its
// parent, which is how a list of internal ids disappears along with the
// key that named it.
func redactValue(v any, underIDKey bool) (any, bool) {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for key, child := range t {
			redacted, drop := redactValue(child, isIDKey(key))
			if drop {
				continue
			}
			out[key] = redacted
		}
		return out, false
	case []any:
		out := make([]any, 0, len(t))
		for _, child := range t {
			redacted, drop := redactValue(child, underIDKey)
			if drop {
				continue
			}
			out = append(out, redacted)
		}
		// A list that held nothing but internal ids is dropped whole
		// rather than surfacing as an empty array the client would read
		// as "no related rows".
		if underIDKey && len(out) == 0 && len(t) > 0 {
			return nil, true
		}
		return out, false
	case json.Number:
		return t, underIDKey
	case float64:
		return t, underIDKey
	default:
		return v, false
	}
}
