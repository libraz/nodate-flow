package mcp_test

import (
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/nlcommand"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
)

// Every tool the natural-language resolver is allowed to emit must exist
// in the MCP registry. If it does not, the resolver hands the client a
// tool name nothing can execute — and because the name still passes the
// allowlist check, the failure surfaces as a confident, successful
// resolution that does nothing.
func TestAllowedNLToolsExistInRegistry(t *testing.T) {
	names := nlcommand.AllowedToolNames()
	descs := mcp.DescribeTools(names)
	if len(descs) != len(names) {
		found := make(map[string]bool, len(descs))
		for _, d := range descs {
			found[d.Name] = true
		}
		for _, n := range names {
			if !found[n] {
				t.Errorf("nlcommand allows %q but the MCP registry has no such tool", n)
			}
		}
	}
}

// The catalogue the LLM is shown has to carry the argument schema, not
// just the name: an empty schema lets the model shape arguments freely,
// which is exactly the drift the shared registry is here to prevent.
func TestDescribedToolsCarryTheirSchema(t *testing.T) {
	for _, d := range mcp.DescribeTools(nlcommand.AllowedToolNames()) {
		if d.Description == "" {
			t.Errorf("tool %q has no description", d.Name)
		}
		if len(d.InputSchema) == 0 {
			t.Errorf("tool %q has no input schema", d.Name)
		}
	}
}

// Unknown names are dropped rather than described, so a caller comparing
// lengths can tell the two lists apart.
func TestDescribeToolsSkipsUnknownNames(t *testing.T) {
	descs := mcp.DescribeTools([]string{"list_projects", "no_such_tool"})
	if len(descs) != 1 {
		t.Fatalf("want 1 description, got %d", len(descs))
	}
	if descs[0].Name != "list_projects" {
		t.Fatalf("want list_projects, got %q", descs[0].Name)
	}
}
