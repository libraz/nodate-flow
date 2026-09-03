package router

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/danielgtaylor/huma/v2"

	"github.com/libraz/nodate-flow/packages/go-shared/openapiutil"
)

// MergeAPIs merges every sub-API's OpenAPI document into a single
// normalized OpenAPI 3.1 spec: the exact document flow-api serves at
// /openapi.json and writes out for SDK codegen.
//
// The nodate-flow router splits operations across multiple humachi.New
// instances so each middleware chain lives in its own chi group, which
// means each group carries its own OpenAPI document sharing nothing with
// the others — including its own schema registry. The same Go type
// registered in two groups is therefore rendered twice, independently.
//
// This is the only fold in flow-api. The live route and
// cmd/dump-openapi both call it rather than each keeping a copy: two
// hand-written folds can only be compared after they have already
// drifted, and the comparison cannot say which one was right.
//
// The result is a fresh document. Nothing here writes into a sub-API's
// own OpenAPI document, so callers may fold the same slice repeatedly
// and the per-builder operation inventory stays intact whenever it is
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
		for path, item := range spec.Paths {
			existing, ok := root.Paths[path]
			if !ok {
				root.Paths[path] = item
				continue
			}
			// Copy before merging. The entry is still the sub-API's own
			// PathItem, and merging into it would hand that group the other
			// group's verbs permanently — which the per-builder ACL checks
			// then read back as routes their builder never registered.
			merged := *existing
			mergePathItem(&merged, item)
			root.Paths[path] = &merged
		}
		if spec.Components == nil || spec.Components.Schemas == nil {
			continue
		}
		if err := mergeSchemaRegistry(schemas, spec.Components.Schemas.Map()); err != nil {
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
	openapiutil.PatchErrorModelSchema(root)
	return root, nil
}

// freshRoot returns an empty document carrying src's document-level
// fields (info, servers, security, tags, the non-schema components) with
// its own paths map and its own schema registry, so folding into it never
// touches src.
func freshRoot(src *huma.OpenAPI) *huma.OpenAPI {
	root := &huma.OpenAPI{OpenAPI: "3.1.0"}
	if src != nil {
		*root = *src
	}
	root.Paths = map[string]*huma.PathItem{}
	components := &huma.Components{}
	if src != nil && src.Components != nil {
		*components = *src.Components
	}
	components.Schemas = huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	root.Components = components
	return root
}

// detachSchema copies s far enough that rewriting the copy cannot reach
// the original: the schema value, its property map, and the property
// values. That is the whole reach of the normalization applied above; a
// normalization that edited deeper would need this to go deeper with it.
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

// mergeSchemaRegistry folds src's named schemas into dst.
//
// A name present on both sides is not resolved by keeping whichever
// registry was registered first. Registration order is an artifact of the
// order the router happens to call its Register* functions in, so
// first-wins silently pins the contract to that order: the same Go type
// rendered by two registries can differ, and the arbitrary winner is the
// one the SDK is generated from. Instead:
//
//   - when one rendering structurally contains the other, the fuller one
//     wins, deterministically and regardless of order. This is what the
//     same type being rendered twice actually looks like: huma only adds
//     the `$schema` self-description property to a type when a registry
//     uses it as an operation's direct request or response body, not when
//     that registry only ever nests it inside another schema.
//   - anything else is a real name collision — two different Go types
//     claiming one schema name — and stops the merge. Nothing downstream
//     can recover from it; one of the types has to be renamed.
func mergeSchemaRegistry(dst, src map[string]*huma.Schema) error {
	for name, schema := range src {
		existing, ok := dst[name]
		if !ok {
			dst[name] = schema
			continue
		}
		winner, err := preferSupersetSchema(existing, schema)
		if err != nil {
			return fmt.Errorf(
				"openapi merge: components.schemas.%s is defined differently by two sub-APIs and neither definition contains the other (%w) — two distinct Go types share this schema name; rename one so each keeps its own schema",
				name, err)
		}
		dst[name] = winner
	}
	return nil
}

// preferSupersetSchema returns whichever of a and b structurally contains
// the other, so two renderings of the same underlying type resolve to the
// more complete one instead of to whichever registry was folded first. It
// reports an error when neither contains the other, since that means the
// two definitions disagree about what the schema is rather than about how
// completely it was rendered.
func preferSupersetSchema(a, b *huma.Schema) (*huma.Schema, error) {
	aJSON, err := toJSONValue(a)
	if err != nil {
		return nil, err
	}
	bJSON, err := toJSONValue(b)
	if err != nil {
		return nil, err
	}
	if isSupersetOf(aJSON, bJSON) {
		return a, nil
	}
	if isSupersetOf(bJSON, aJSON) {
		return b, nil
	}
	return nil, errors.New("neither definition is a superset of the other")
}

// toJSONValue renders a schema as the generic JSON value it serializes
// to, so the comparison sees the shipped document rather than Go struct
// fields that never reach it.
func toJSONValue(s *huma.Schema) (any, error) {
	buf, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(buf, &v); err != nil {
		return nil, err
	}
	return v, nil
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

// mergePathItem copies operations from src into dst for HTTP verbs that
// dst does not already define.
//
// Paths really are shared between groups — a resource whose reads sit in
// one chi group and whose writes sit in another appears in both
// documents, each carrying only its own verbs — so this runs often, and
// what it produces is the union. Two groups claiming the same verb on the
// same path cannot happen: huma panics on the duplicate operation ID
// while the second one is being registered, long before any of this.
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
