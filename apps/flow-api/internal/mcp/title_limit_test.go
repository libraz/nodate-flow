package mcp

import (
	"regexp"
	"strings"
	"testing"
)

// TestTitleLimitsComeFromOneDeclaration holds the title widths to a single
// declaration: the tool schemas read them from the constants.
func TestTitleLimitsComeFromOneDeclaration(t *testing.T) {
	t.Parallel()

	src := readMCPToolSources(t)
	if m := regexp.MustCompile(`(?m)^\s*\w*TitleMaxLen\s*=`).FindString(src); m != "" {
		t.Errorf("a local title width is back in the MCP tool sources (%q); use the shared title limit constants", strings.TrimSpace(m))
	}

	h := NewHandler(Deps{})
	for tool, want := range map[string]int{
		"create_task":           taskTitleLimit,
		"update_task":           taskTitleLimit,
		"smart_create_task":     taskTitleLimit,
		"create_page":           pageTitleLimit,
		"update_page":           pageTitleLimit,
		"create_calendar_event": eventTitleLimit,
		"update_calendar_event": eventTitleLimit,
	} {
		tl, ok := h.tools[tool]
		if !ok {
			t.Errorf("tool %q is not registered", tool)
			continue
		}
		props, _ := tl.inputSchema["properties"].(map[string]any)
		title, _ := props["title"].(map[string]any)
		if got := title["maxLength"]; got != want {
			t.Errorf("tool %q declares title maxLength %v, want %d", tool, got, want)
		}
	}
}
