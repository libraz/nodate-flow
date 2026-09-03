// Command merge-openapi merges multiple OpenAPI 3.1 JSON files into one.
// It combines paths and every components sub-map, with earlier files
// taking precedence on collisions.
//
// What it refuses to do is drop things. An earlier version merged only
// `paths` and `components.schemas` and discarded everything else in the
// second and later specs without a word — so the day a service starts
// emitting `components.securitySchemes`, or tags, or servers, that part
// of its contract would vanish from the merged spec and from every
// client generated off it, with the build green throughout. Sections
// this program does not know how to combine now stop the merge instead.
//
// Usage:
//
//	go run merge-openapi.go -o merged.json flow.json auth.json
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
)

// Top-level keys taken from the first spec and expected to be identical
// (or absent) in the rest. A later spec that disagrees is reported
// rather than silently losing.
var singletonKeys = []string{"openapi", "info"}

// Top-level keys this program knows how to combine.
var mergeableKeys = map[string]bool{
	"paths":      true,
	"components": true,
	"tags":       true,
	"security":   true,
	"servers":    true,
}

func main() {
	out := flag.String("o", "openapi.json", "output path")
	flag.Parse()
	files := flag.Args()
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "merge-openapi: no input files")
		os.Exit(1)
	}

	var root map[string]any
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "merge-openapi: read %s: %v\n", f, err)
			os.Exit(1)
		}
		var spec map[string]any
		if err := json.Unmarshal(data, &spec); err != nil {
			fmt.Fprintf(os.Stderr, "merge-openapi: parse %s: %v\n", f, err)
			os.Exit(1)
		}
		if root == nil {
			root = spec
			continue
		}
		if err := merge(root, spec, f); err != nil {
			fmt.Fprintf(os.Stderr, "merge-openapi: %v\n", err)
			os.Exit(1)
		}
	}

	buf, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "merge-openapi: marshal: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, append(buf, '\n'), 0o644); err != nil { //nolint:gosec // codegen artifact
		fmt.Fprintf(os.Stderr, "merge-openapi: write: %v\n", err)
		os.Exit(1)
	}

	pathCount := 0
	if p, ok := root["paths"].(map[string]any); ok {
		pathCount = len(p)
	}
	fmt.Printf("merge-openapi: wrote %s (%d paths)\n", *out, pathCount)
}

func mergePaths(dst, src map[string]any) {
	srcPaths, ok := src["paths"].(map[string]any)
	if !ok {
		return
	}
	dstPaths, ok := dst["paths"].(map[string]any)
	if !ok {
		dstPaths = map[string]any{}
		dst["paths"] = dstPaths
	}
	for path, item := range srcPaths {
		if _, exists := dstPaths[path]; !exists {
			dstPaths[path] = item
		} else {
			// Merge HTTP methods within the same path.
			dstItem, _ := dstPaths[path].(map[string]any)
			srcItem, _ := item.(map[string]any)
			if dstItem != nil && srcItem != nil {
				for method, op := range srcItem {
					if _, exists := dstItem[method]; !exists {
						dstItem[method] = op
					}
				}
			}
		}
	}
}

// merge folds src into dst, refusing anything it cannot account for.
func merge(dst, src map[string]any, srcName string) error {
	for _, key := range singletonKeys {
		sv, present := src[key]
		if !present {
			continue
		}
		dv, exists := dst[key]
		if !exists {
			dst[key] = sv
			continue
		}
		if key == "openapi" && !equalJSON(dv, sv) {
			return fmt.Errorf("%s: openapi version %v disagrees with %v", srcName, sv, dv)
		}
	}

	mergePaths(dst, src)
	if err := mergeComponents(dst, src, srcName); err != nil {
		return err
	}
	mergeTagList(dst, src)

	// `security` and `servers` are document-wide and cannot be combined
	// meaningfully when two specs disagree: whichever won would apply to
	// the other service's paths as well.
	for _, key := range []string{"security", "servers"} {
		sv, present := src[key]
		if !present {
			continue
		}
		dv, exists := dst[key]
		if !exists {
			dst[key] = sv
			continue
		}
		if !equalJSON(dv, sv) {
			return fmt.Errorf(
				"%s: %q differs between specs and cannot be merged; the merged document would apply one service's setting to the other's paths",
				srcName, key)
		}
	}

	// Anything left is a section this program cannot vouch for. Dropping
	// it silently is the bug this check exists to prevent.
	unknown := make([]string, 0)
	for key := range src {
		if mergeableKeys[key] {
			continue
		}
		if contains(singletonKeys, key) {
			continue
		}
		unknown = append(unknown, key)
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf(
			"%s: unhandled top-level section(s) %v — teach merge-openapi how to combine them rather than shipping a spec that omits them",
			srcName, unknown)
	}
	return nil
}

// mergeComponents merges every components sub-map, not just schemas.
// securitySchemes, responses, parameters and the rest are as much a part
// of the contract as the schemas are.
//
// A name that exists on both sides is not resolved by keeping whichever
// spec was read first: that is the same silent overwrite this program
// exists to refuse, just moved one level down. Instead:
//
//   - identical entries merge trivially.
//   - when one entry is a structural superset of the other (every
//     key/value the smaller one carries also appears, unchanged, in the
//     larger one) the superset wins. This is what lets the same Go type
//     survive being rendered slightly differently by two independent
//     huma registries — e.g. huma only adds the `$schema` self-description
//     property to a type when that registry uses it as an operation's
//     direct body, not when the same type is only ever nested inside
//     another schema — without losing the more complete rendering.
//   - anything else is a genuine name collision: two different
//     declarations share a component name and neither is a fuller
//     version of the other. Reporting it here, with the file and the
//     name, is the whole point; picking one arbitrarily is the bug this
//     program was written to stop.
func mergeComponents(dst, src map[string]any, srcName string) error {
	srcComp, ok := src["components"].(map[string]any)
	if !ok {
		return nil
	}
	dstComp, ok := dst["components"].(map[string]any)
	if !ok {
		dstComp = map[string]any{}
		dst["components"] = dstComp
	}
	for section, entries := range srcComp {
		srcEntries, ok := entries.(map[string]any)
		if !ok {
			// Not a name→object map; keep the first spec's value.
			if _, exists := dstComp[section]; !exists {
				dstComp[section] = entries
			}
			continue
		}
		dstEntries, ok := dstComp[section].(map[string]any)
		if !ok {
			dstEntries = map[string]any{}
			dstComp[section] = dstEntries
		}
		for name, entry := range srcEntries {
			existing, exists := dstEntries[name]
			if !exists {
				dstEntries[name] = entry
				continue
			}
			merged, err := preferSuperset(existing, entry)
			if err != nil {
				return fmt.Errorf(
					"%s: components.%s.%s disagrees with the same name already merged from an earlier spec, and neither definition is a superset of the other (%w) — two different declarations share this component name; rename one of the underlying Go types so each keeps its own schema",
					srcName, section, name, err)
			}
			dstEntries[name] = merged
		}
	}
	return nil
}

// preferSuperset returns whichever of a and b structurally contains the
// other, so a name collision between two renderings of the same
// underlying type resolves to the more complete rendering instead of
// whichever side happened to be read first. It reports an error when
// neither is a superset of the other, since that means the two values
// disagree about what the component actually is.
func preferSuperset(a, b any) (any, error) {
	if isSupersetOf(a, b) {
		return a, nil
	}
	if isSupersetOf(b, a) {
		return b, nil
	}
	return nil, errors.New("neither value is a superset of the other")
}

// isSupersetOf reports whether every key/value that b carries also
// appears, unchanged, in a. Maps are compared key by key so a can carry
// extra keys b does not have; anything else (scalars, arrays) must match
// exactly, since there is no meaningful "more complete" ordering for a
// JSON Schema array like `required` or `enum`.
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

// mergeTagList unions the document tag lists by tag name.
func mergeTagList(dst, src map[string]any) {
	srcTags, ok := src["tags"].([]any)
	if !ok {
		return
	}
	dstTags, _ := dst["tags"].([]any)
	seen := map[string]bool{}
	for _, t := range dstTags {
		if m, ok := t.(map[string]any); ok {
			if name, ok := m["name"].(string); ok {
				seen[name] = true
			}
		}
	}
	for _, t := range srcTags {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		if seen[name] {
			continue
		}
		seen[name] = true
		dstTags = append(dstTags, t)
	}
	dst["tags"] = dstTags
}

func equalJSON(a, b any) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(ab) == string(bb)
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
