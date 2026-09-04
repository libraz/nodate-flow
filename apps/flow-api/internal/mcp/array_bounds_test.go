package mcp

import (
	"sort"
	"testing"
)

// TestEveryArraySchemaIsBounded fails on an array argument that declares no
// maxItems.
//
// It is the mirror of [TestToolSchemaKeywordsAreEnforced]: that one fails
// when a schema declares a constraint the validator ignores, this one when
// a schema declares nothing where a bound is required. An array's length is
// not like a string's: the tool does work per element inside the one call —
// a row inserted under a lock held for the whole batch, an id fetched and
// pasted into a prompt the workspace pays for — so an unbounded array is
// the caller choosing how much of the server a single request uses.
//
// The set is walked out of the registry rather than listed, because the
// omission it guards against is not in any array anybody wrote down: both
// arrays this repository had were unbounded, and neither was a decision.
// The next one is covered without anybody remembering this file.
func TestEveryArraySchemaIsBounded(t *testing.T) {
	t.Parallel()

	h := NewHandler(Deps{})
	var checked []string

	// The descent follows every properties map and every items node
	// whatever its type, rather than only descending into objects: an
	// array whose elements are arrays is the shape a walk that stops at
	// the first one would report as bounded.
	var walk func(toolName, path string, node map[string]any)
	walk = func(toolName, path string, node map[string]any) {
		if typ, _ := node["type"].(string); typ == "array" {
			checked = append(checked, toolName+" "+path)
			if _, ok := schemaInt(node, "maxItems"); !ok {
				t.Errorf("tool %q argument %q is an array declaring no maxItems, so its length is "+
					"whatever the caller sends; declare one through arraySchema's Constraints, "+
					"because the elements are work this tool does inside one request",
					toolName, path)
			}
		}
		if props, ok := node["properties"].(map[string]any); ok {
			for name, raw := range props {
				child, isNode := raw.(map[string]any)
				if !isNode {
					continue
				}
				walk(toolName, joinPath(path, name), child)
			}
		}
		if items, ok := node["items"].(map[string]any); ok {
			walk(toolName, path+"[]", items)
		}
	}

	for name, tl := range h.tools {
		walk(name, "", tl.inputSchema)
	}

	if len(checked) == 0 {
		t.Fatal("no array argument was found in any registered tool; the walk stopped matching " +
			"the schemas rather than the arrays having gone away, and a guard that reads nothing " +
			"reports success for every array added after it")
	}
	sort.Strings(checked)
	t.Logf("%d array arguments checked for a length bound: %v", len(checked), checked)
}
