package mcp

import (
	"regexp"
	"strings"
	"testing"
)

// TestTitleLimitsComeFromOneDeclaration holds the title widths to a single
// declaration: the tool schemas read them from the constants, and the cut
// applied to model-produced subtask titles counts characters at the same
// width the schema accepts.
func TestTitleLimitsComeFromOneDeclaration(t *testing.T) {
	t.Parallel()

	src := readMCPSource(t, "tools.go")
	if m := regexp.MustCompile(`(?m)^\s*\w*TitleMaxLen\s*=`).FindString(src); m != "" {
		t.Errorf("a local title width is back in tools.go (%q); use the shared title limit constants", strings.TrimSpace(m))
	}
	if !strings.Contains(src, "stringutil.TruncateRunes(proposal.Subtasks[i].Title, taskTitleLimit)") {
		t.Error("model-produced subtask titles must be cut by character at taskTitleLimit, so a title the schema accepts is never shortened")
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
