package mcp

import (
	"os"
	"strings"
	"testing"
)

func TestMCPResolveTaskUsesSharedVisibilityACL(t *testing.T) {
	t.Parallel()

	src := readMCPSource(t, "acl.go")
	for _, want := range []string{
		"acl.AuthorizeTaskAccess",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("resolveTask must keep using shared ACL helper %s", want)
		}
	}
}

func TestMCPListTasksUsesVisibilityFilter(t *testing.T) {
	t.Parallel()

	src := readMCPSource(t, "tools.go")
	for _, want := range []string{
		"listMCPTasks",
		"acl.TaskVisibilityFilter",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("MCP list_tasks must keep applying task visibility filter %s", want)
		}
	}
}

func TestMCPCalendarResolverRequiresSubscription(t *testing.T) {
	t.Parallel()

	src := readMCPSource(t, "acl.go")
	if !strings.Contains(src, "FindCalendarSubscription") {
		t.Fatalf("resolveCalendar must require a calendar subscription")
	}
}

func readMCPSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}
