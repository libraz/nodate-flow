package openapiutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// schemasField is the one huma.Components field this package cannot fold
// through reflection: it holds a huma.Registry rather than a name-keyed
// map, so it is merged by name through the registry's own Map().
const schemasField = "Schemas"

// MergeAPIs merges every sub-API's OpenAPI document into a single
// normalized OpenAPI 3.1 document.
//
// Both services split their operations across multiple huma adapter
// instances so each middleware chain lives in its own router group. Each
// group carries its own OpenAPI document sharing nothing with the others
// — including its own schema registry — so the same Go type registered in
// two groups is rendered twice, independently, and a component declared
// by one group exists nowhere else.
//
// This is the only fold over huma.API values in the repository. flow-api's
// live /openapi.json route, flow-api's cmd/dump-openapi and auth-api's
// cmd/dump-openapi all call it rather than each keeping a copy: two
// hand-written folds can only be compared after they have already
// drifted, and the comparison cannot say which one was right. It also
// agrees with scripts/merge-openapi.go, which folds the two dumped
// documents one layer further out, about what a merge may do: combine
// every components section, union the tag list, and stop rather than
// silently pick a winner when two declarations of one name genuinely
// disagree.
//
// The result is a fresh document. Nothing here writes into a sub-API's
// own OpenAPI document, so callers may fold the same slice repeatedly
// and the per-group operation inventory stays intact whenever it is
// taken.
func MergeAPIs(apis []huma.API) (*huma.OpenAPI, error) {
	if len(apis) == 0 {
		return &huma.OpenAPI{OpenAPI: "3.1.0"}, nil
	}
	root := freshRoot(apis[0].OpenAPI())
	schemas := root.Components.Schemas.Map()
	for _, a := range apis {
		spec := a.OpenAPI()
		if spec == nil {
			continue
		}
		if err := mergeDocument(root, spec); err != nil {
			return nil, err
		}
		for path, item := range spec.Paths {
			existing, ok := root.Paths[path]
			if !ok {
				root.Paths[path] = item
				continue
			}
			// Copy before merging. The entry is still the sub-API's own
			// PathItem, and merging into it would hand that group the other
			// group's verbs permanently — which the per-group ACL checks
			// then read back as routes their group never registered.
			merged := *existing
			mergePathItem(&merged, item)
			root.Paths[path] = &merged
		}
		if spec.Components == nil {
			continue
		}
		if spec.Components.Schemas != nil {
			if err := mergeNamed(schemas, spec.Components.Schemas.Map(), "schemas"); err != nil {
				return nil, err
			}
		}
		if err := mergeComponents(root.Components, spec.Components); err != nil {
			return nil, err
		}
	}

	// Normalization is part of producing the document, not a step callers
	// remember to add — the ErrorModel the SDK is generated from has to be
	// the ErrorModel the server answers with. It runs on a detached copy
	// because the registry entry is still a sub-API's own schema object,
	// and rewriting it in place would leave that one group's rendering
	// permanently different from every other group's.
	if em := schemas["ErrorModel"]; em != nil {
		schemas["ErrorModel"] = detachSchema(em)
	}
	PatchErrorModelSchema(root)
	return root, nil
}

// freshRoot returns an empty document seeded with src's document-level
// fields (info, servers, security, tags) and given its own paths map, its
// own schema registry, its own tag list and its own copy of every
// components section, so folding into it never touches src.
//
// The tag list is copied rather than shared because the fold appends to
// it: appending to a slice that still has spare capacity writes into the
// backing array src is serving from, which would publish another group's
// tags under apis[0]'s document.
func freshRoot(src *huma.OpenAPI) *huma.OpenAPI {
	root := &huma.OpenAPI{OpenAPI: "3.1.0"}
	if src != nil {
		*root = *src
	}
	root.Paths = map[string]*huma.PathItem{}
	root.Tags = append([]*huma.Tag(nil), root.Tags...)
	components := &huma.Components{}
	if src != nil && src.Components != nil {
		*components = *src.Components
		detachComponentMaps(components)
	}
	components.Schemas = huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	root.Components = components
	return root
}

// detachComponentMaps replaces every name-keyed section of c with a copy
// holding the same entries, so writing a name into the merged document
// cannot reach the map apis[0] is still serving from. The entries
// themselves are shared, which is safe because the fold only ever
// replaces map values and never edits one in place.
func detachComponentMaps(c *huma.Components) {
	v := reflect.ValueOf(c).Elem()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		if field.Kind() != reflect.Map || field.IsNil() {
			continue
		}
		clone := reflect.MakeMapWithSize(field.Type(), field.Len())
		iter := field.MapRange()
		for iter.Next() {
			clone.SetMapIndex(iter.Key(), iter.Value())
		}
		field.Set(clone)
	}
}

// accountedDocumentFields are the huma.OpenAPI fields this fold knows
// how to combine. Every other field that reaches the emitted document is
// held to the check in refuseUnaccountedDocumentFields, so a huma release
// that adds a document-level section cannot pass through it unnoticed.
var accountedDocumentFields = map[string]bool{
	"OpenAPI":    true,
	"Info":       true,
	"Servers":    true,
	"Paths":      true,
	"Components": true,
	"Security":   true,
	"Tags":       true,
}

// mergeDocument folds src's document-level fields into root, by the same
// rules scripts/merge-openapi.go applies one layer out. Two folds that
// answer differently for the same field are how a document comes to
// depend on which of them produced it.
//
//   - openapi: the version must agree. Two groups claiming different
//     versions of the specification describe the merged document's own
//     format, and no merged document can be both.
//   - info: the first group's wins, deliberately and silently. Title,
//     version and description name the service, not the router group, so
//     there is nothing to combine — a merged title assembled from two is
//     not a title anyone declared. Every group of one service is built
//     from one huma.Config, so they never differ here; the outer fold has
//     to tolerate genuinely different infos, because it combines two
//     services into one document, and following it rather than refusing
//     is what keeps the two layers from disagreeing.
//   - servers and security: adopted when root has none, required to
//     agree otherwise. They are document-wide, so whichever won a
//     disagreement would silently apply to the other group's paths too —
//     it would relax or tighten the declared auth of operations that
//     never asked for it.
//   - tags: unioned by name, since a tag list is additive and each group
//     declares the tags its own operations use.
func mergeDocument(root, src *huma.OpenAPI) error {
	if src.OpenAPI != "" && root.OpenAPI != src.OpenAPI {
		return fmt.Errorf(
			"openapi merge: sub-APIs declare different OpenAPI versions (%q and %q); the merged document can only be one of them",
			root.OpenAPI, src.OpenAPI)
	}
	if root.Info == nil {
		root.Info = src.Info
	}
	if len(src.Servers) > 0 {
		switch {
		case len(root.Servers) == 0:
			root.Servers = src.Servers
		case !equalJSON(root.Servers, src.Servers):
			return documentConflictError("servers", root.Servers, src.Servers)
		}
	}
	if len(src.Security) > 0 {
		switch {
		case len(root.Security) == 0:
			root.Security = src.Security
		case !equalJSON(root.Security, src.Security):
			return documentConflictError("security", root.Security, src.Security)
		}
	}
	mergeTagList(root, src)
	return refuseUnaccountedDocumentFields(root, src)
}

// mergeTagList appends the tags src declares that root does not already
// carry under the same name.
//
// The result is ordered: root's tags keep their declared order and each
// new name is appended in the order its own document declares it. The map
// holds membership only and is never iterated, so the emitted tag array
// is the same on every run — an array assembled in Go map order would
// differ between runs of the same generator and read as drift in a
// document nothing had changed.
func mergeTagList(root, src *huma.OpenAPI) {
	if len(src.Tags) == 0 {
		return
	}
	seen := make(map[string]bool, len(root.Tags))
	for _, tag := range root.Tags {
		if tag != nil {
			seen[tag.Name] = true
		}
	}
	for _, tag := range src.Tags {
		if tag == nil || seen[tag.Name] {
			continue
		}
		seen[tag.Name] = true
		root.Tags = append(root.Tags, tag)
	}
}

// refuseUnaccountedDocumentFields stops the merge when a sub-API carries
// a document-level field this fold has no rule for and root does not
// already carry the same value.
//
// Silently keeping root's copy is the loss this exists to prevent: the
// field is part of the contract, nothing downstream reads the sub-API
// documents, and the omission leaves every generated client short of it
// with the build green throughout. Fields the document does not carry at
// all (yaml:"-") are skipped, since nothing about them can be lost.
func refuseUnaccountedDocumentFields(root, src *huma.OpenAPI) error {
	rv := reflect.ValueOf(root).Elem()
	sv := reflect.ValueOf(src).Elem()
	t := sv.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if accountedDocumentFields[field.Name] || field.Tag.Get("yaml") == "-" {
			continue
		}
		srcField := sv.Field(i)
		if isAbsent(srcField) {
			continue
		}
		rootField := rv.Field(i)
		if equalJSON(rootField.Interface(), srcField.Interface()) {
			continue
		}
		return fmt.Errorf(
			"openapi merge: document-level %q differs between sub-APIs (%s vs %s) and this fold has no rule for combining it; teach mergeDocument how to combine it rather than shipping a document that omits one",
			sectionName(field), renderJSON(rootField.Interface()), renderJSON(srcField.Interface()))
	}
	return nil
}

// isAbsent reports whether v contributes nothing to the emitted
// document: the zero value, or an empty map or slice, all of which huma
// leaves out.
func isAbsent(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Map, reflect.Slice:
		return v.Len() == 0
	default:
		return v.IsZero()
	}
}

// documentConflictError names the field and both values, so the reader
// can see which two groups disagree without reproducing the merge.
func documentConflictError(field string, existing, incoming any) error {
	return fmt.Errorf(
		"openapi merge: document-level %q differs between sub-APIs (%s vs %s) and cannot be combined; it is document-wide, so the winner would apply to every other group's paths as well",
		field, renderJSON(existing), renderJSON(incoming))
}

// renderJSON renders a value the way the document carries it, for error
// messages. A value that cannot be marshaled falls back to Go syntax
// rather than costing the reader the message.
func renderJSON(v any) string {
	buf, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%+v", v)
	}
	return string(buf)
}

// mergeComponents folds every components section of src into dst except
// schemas, which the caller merges through the registry.
//
// Dropping a section is the failure this walks the struct to avoid.
// securitySchemes, responses, parameters, requestBodies, headers,
// examples, links, callbacks, pathItems and the `x-` extensions are as
// much a part of the contract as the schemas are, and a sub-API is free
// to declare one the first sub-API does not. Reflection rather than a
// hand-written field list because the list is the part that goes stale:
// a huma upgrade that adds a section would silently reintroduce the same
// loss, and here it is either folded like its neighbours or reported.
func mergeComponents(dst, src *huma.Components) error {
	dv := reflect.ValueOf(dst).Elem()
	sv := reflect.ValueOf(src).Elem()
	t := sv.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.Name == schemasField {
			continue
		}
		srcField := sv.Field(i)
		if srcField.IsZero() {
			continue
		}
		// An empty section contributes nothing, and materializing a map
		// for it would put an empty object in the emitted document where
		// huma's omitempty had left the section out entirely.
		if srcField.Kind() == reflect.Map && srcField.Len() == 0 {
			continue
		}
		section := sectionName(field)
		if srcField.Kind() != reflect.Map || srcField.Type().Key().Kind() != reflect.String {
			return fmt.Errorf(
				"openapi merge: components.%s is not a name-keyed map, so this fold cannot combine two sub-APIs' copies of it; teach mergeComponents how to merge it rather than shipping a document that omits one",
				section)
		}
		if err := mergeSection(dv.Field(i), srcField, section); err != nil {
			return err
		}
	}
	return nil
}

// mergeSection folds one name-keyed components section into dst. Names
// are visited in sorted order so a document carrying more than one
// collision always reports the same one.
func mergeSection(dst, src reflect.Value, section string) error {
	if dst.IsNil() {
		dst.Set(reflect.MakeMapWithSize(dst.Type(), src.Len()))
	}
	keys := make([]reflect.Value, 0, src.Len())
	iter := src.MapRange()
	for iter.Next() {
		keys = append(keys, iter.Key())
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })

	for _, key := range keys {
		entry := src.MapIndex(key)
		existing := dst.MapIndex(key)
		if !existing.IsValid() {
			dst.SetMapIndex(key, entry)
			continue
		}
		preferSrc, err := preferSuperset(existing.Interface(), entry.Interface())
		if err != nil {
			return collisionError(section, key.String(), err)
		}
		if preferSrc {
			dst.SetMapIndex(key, entry)
		}
	}
	return nil
}

// sectionName returns the name a components field is serialized under, so
// an error points at the document the reader is looking at rather than at
// a Go field name. The extensions field is inlined and has no name of its
// own; its Go name is the closest thing to one.
func sectionName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
	if name != "" {
		return name
	}
	return f.Name
}

// mergeNamed folds src's named entries into dst under the same rule the
// reflected sections use. It exists separately because the schema
// registry hands out a typed map rather than a struct field.
func mergeNamed(dst, src map[string]*huma.Schema, section string) error {
	names := make([]string, 0, len(src))
	for name := range src {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		schema := src[name]
		existing, ok := dst[name]
		if !ok {
			dst[name] = schema
			continue
		}
		preferSrc, err := preferSuperset(existing, schema)
		if err != nil {
			return collisionError(section, name, err)
		}
		if preferSrc {
			dst[name] = schema
		}
	}
	return nil
}

func collisionError(section, name string, cause error) error {
	return fmt.Errorf(
		"openapi merge: components.%s.%s is defined differently by two sub-APIs and neither definition contains the other (%w) — two distinct declarations share this component name; rename one so each keeps its own definition",
		section, name, cause)
}

// preferSuperset decides which of two definitions of one component name
// survives, and reports whether that is b.
//
// A name present on both sides is not resolved by keeping whichever
// sub-API was folded first. Fold order is an artifact of the order the
// router happens to call its Register* functions in, so first-wins
// silently pins the contract to that order: the same Go type rendered by
// two groups can differ, and the arbitrary winner is the one the SDK is
// generated from. Instead:
//
//   - when one rendering structurally contains the other, the fuller one
//     wins, deterministically and regardless of order. This is what the
//     same type being rendered twice actually looks like: huma only adds
//     the `$schema` self-description property to a type when a registry
//     uses it as an operation's direct request or response body, not when
//     that registry only ever nests it inside another schema.
//   - anything else is a real name collision — two different declarations
//     claiming one component name — and stops the merge. Nothing
//     downstream can recover from it; one of them has to be renamed.
//
// Identical definitions take the first side, which is unobservable.
func preferSuperset(a, b any) (bool, error) {
	aJSON, err := toJSONValue(a)
	if err != nil {
		return false, err
	}
	bJSON, err := toJSONValue(b)
	if err != nil {
		return false, err
	}
	if isSupersetOf(aJSON, bJSON) {
		return false, nil
	}
	if isSupersetOf(bJSON, aJSON) {
		return true, nil
	}
	return false, errors.New("neither definition is a superset of the other")
}

// toJSONValue renders a component as the generic JSON value it
// serializes to, so the comparison sees the shipped document rather than
// Go struct fields that never reach it.
func toJSONValue(v any) (any, error) {
	buf, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(buf, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// isSupersetOf reports whether every key/value b carries also appears,
// unchanged, in a. Objects are compared key by key, so a may carry keys b
// lacks; every other JSON kind must match exactly, since there is no
// meaningful "more complete" ordering for an array like `required` or
// `enum`. The walk is recursive and reads only b's keys, so it never
// depends on Go map iteration order.
func isSupersetOf(a, b any) bool {
	bMap, ok := b.(map[string]any)
	if !ok {
		return equalJSON(a, b)
	}
	aMap, ok := a.(map[string]any)
	if !ok {
		return false
	}
	for key, bVal := range bMap {
		aVal, present := aMap[key]
		if !present || !isSupersetOf(aVal, bVal) {
			return false
		}
	}
	return true
}

// equalJSON compares two decoded JSON values by their canonical
// encoding. Object key order does not affect the result because
// encoding/json sorts map keys.
func equalJSON(a, b any) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(ab) == string(bb)
}

// detachSchema copies s far enough that rewriting the copy cannot reach
// the original: the schema value, its property map, and the property
// values. That is the whole reach of the normalization MergeAPIs applies;
// a normalization that edited deeper would need this to go deeper with it.
func detachSchema(s *huma.Schema) *huma.Schema {
	clone := *s
	if s.Properties != nil {
		props := make(map[string]*huma.Schema, len(s.Properties))
		for name, p := range s.Properties {
			if p == nil {
				props[name] = nil
				continue
			}
			pc := *p
			props[name] = &pc
		}
		clone.Properties = props
	}
	return &clone
}

// mergePathItem copies operations from src into dst for HTTP verbs that
// dst does not already define.
//
// Paths really are shared between groups — a resource whose reads sit in
// one group and whose writes sit in another appears in both documents,
// each carrying only its own verbs — so this runs often, and what it
// produces is the union. Two groups claiming the same verb on the same
// path cannot happen: huma panics on the duplicate operation ID while the
// second one is being registered, long before any of this.
func mergePathItem(dst, src *huma.PathItem) {
	if dst.Get == nil {
		dst.Get = src.Get
	}
	if dst.Put == nil {
		dst.Put = src.Put
	}
	if dst.Post == nil {
		dst.Post = src.Post
	}
	if dst.Delete == nil {
		dst.Delete = src.Delete
	}
	if dst.Patch == nil {
		dst.Patch = src.Patch
	}
	if dst.Head == nil {
		dst.Head = src.Head
	}
	if dst.Options == nil {
		dst.Options = src.Options
	}
	if dst.Trace == nil {
		dst.Trace = src.Trace
	}
}
