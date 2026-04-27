package mcp

import (
	"strings"
	"testing"
)

// TestNumericPropertiesHaveMinimum walks every registered MCP tool and
// asserts that every property whose JSON-Schema "type" is "integer" or
// "number" carries a "minimum". MCP clients (Claude Desktop, Claude
// Code) use these to validate input client-side before the round-trip.
func TestNumericPropertiesHaveMinimum(t *testing.T) {
	h := NewHandler(Deps{})
	if len(h.tools) == 0 {
		t.Fatal("no tools registered")
	}
	walkProperties(t, h, func(t *testing.T, toolName, path string, prop map[string]any) {
		typ, _ := prop["type"].(string)
		if typ != "integer" && typ != "number" {
			return
		}
		if _, ok := prop["minimum"]; !ok {
			t.Errorf("tool %q property %q (type %s) is missing JSONSchema minimum",
				toolName, path, typ)
		}
	})
}

// TestPublicIDPropertiesHavePattern asserts that every string property
// whose camelCase name ends in "Id" (e.g. taskId, projectId,
// parentTaskId), as well as the items schema of a string array whose
// name ends in "Ids" (e.g. taskIds), declares a JSONSchema pattern
// matching the canonical public-id form emitted by
// types.PublicID.String().
func TestPublicIDPropertiesHavePattern(t *testing.T) {
	h := NewHandler(Deps{})
	walkProperties(t, h, func(t *testing.T, toolName, path string, prop map[string]any) {
		typ, _ := prop["type"].(string)
		if typ != "string" {
			return
		}
		leaf := path
		if i := strings.LastIndex(path, "."); i >= 0 {
			leaf = path[i+1:]
		}
		// "fooId" property, or the synthetic "fooIds[]" produced by
		// walkSchema when descending into a string-array's items.
		isPublicID := strings.HasSuffix(leaf, "Id") || strings.HasSuffix(leaf, "Ids[]")
		if !isPublicID {
			return
		}
		pat, ok := prop["pattern"].(string)
		if !ok {
			t.Errorf("tool %q property %q is a public-id string but has no JSONSchema pattern",
				toolName, path)
			return
		}
		if pat != publicIDPattern {
			t.Errorf("tool %q property %q has pattern %q, want %q",
				toolName, path, pat, publicIDPattern)
		}
	})
}

// walkProperties iterates every tool's inputSchema, descending into
// nested object schemas (under "properties") and array item schemas
// (under "items"), invoking visit on every leaf property descriptor.
// Paths are dotted; array items are reported under the parent
// property name suffixed with "[]" so the leaf still encodes its
// enclosing field's semantic.
func walkProperties(t *testing.T, h *Handler, visit func(t *testing.T, toolName, path string, prop map[string]any)) {
	t.Helper()
	for name, tl := range h.tools {
		walkSchema(t, name, "", tl.inputSchema, visit)
	}
}

func walkSchema(t *testing.T, toolName, path string, schema map[string]any, visit func(t *testing.T, toolName, path string, prop map[string]any)) {
	t.Helper()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return
	}
	for propName, raw := range props {
		prop, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		childPath := propName
		if path != "" {
			childPath = path + "." + propName
		}
		visit(t, toolName, childPath, prop)
		if typ, _ := prop["type"].(string); typ == "object" {
			walkSchema(t, toolName, childPath, prop, visit)
		}
		if items, ok := prop["items"].(map[string]any); ok {
			itemPath := childPath + "[]"
			visit(t, toolName, itemPath, items)
			if typ, _ := items["type"].(string); typ == "object" {
				walkSchema(t, toolName, itemPath, items, visit)
			}
		}
	}
}
