package mcp

import "sync"

// ToolDescription is one catalogue entry: the name a caller invokes, the
// prose that explains what the tool does, and the JSON Schema its
// arguments must satisfy.
//
// It exists so surfaces outside the MCP transport can describe the tool
// set without keeping a second copy of it. The natural-language command
// resolver is the one such surface today: it needs a tool catalogue for
// the LLM prompt, and a hand-written catalogue drifts from the registry
// silently — a tool whose required arguments differ between the two is
// still a valid tool name, so nothing fails until a user's command
// resolves to arguments the executor cannot use.
//
// registerTools is therefore the only place a tool's name, prose, or
// argument shape is written down.
type ToolDescription struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// catalogue builds the tool registry once, with zero Deps. Registration
// only records descriptors and run function values — it never touches
// h.deps — so a dependency-free handler is enough to read names and
// schemas back out.
var catalogue = sync.OnceValue(func() map[string]tool {
	h := &Handler{tools: make(map[string]tool)}
	registerTools(h)
	return h.tools
})

// DescribeTools returns the catalogue entry for each requested tool, in
// the order given. A name with no registered tool is omitted rather than
// returned empty, so a caller can detect the mismatch by comparing
// lengths instead of shipping a tool description the server cannot run.
func DescribeTools(names []string) []ToolDescription {
	reg := catalogue()
	out := make([]ToolDescription, 0, len(names))
	for _, name := range names {
		t, ok := reg[name]
		if !ok {
			continue
		}
		out = append(out, ToolDescription{
			Name:        t.name,
			Description: t.description,
			InputSchema: t.inputSchema,
		})
	}
	return out
}
