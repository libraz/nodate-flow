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

// TestMCPFindTaskByPublicIdCentralized proves that no MCP tool bypasses the
// Layer-4 task-visibility ACL by loading a task row directly. Every
// tasks-by-public-id lookup must go through the resolveTask / resolveTaskRow
// helpers in acl.go, so a tool can never read or mutate a task the caller is
// not permitted to see. The guard fails if any non-test file other than
// acl.go references FindTaskByPublicId.
func TestMCPFindTaskByPublicIdCentralized(t *testing.T) {
	t.Parallel()

	const owner = "acl.go"
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == owner {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(b), "FindTaskByPublicId") {
			t.Fatalf("%s calls FindTaskByPublicId directly; route task lookups through resolveTask/resolveTaskRow in %s so Layer-4 task visibility cannot be bypassed", name, owner)
		}
	}

	// The centralized helpers must exist and authorize through the shared ACL.
	src := readMCPSource(t, owner)
	for _, want := range []string{
		"func resolveTaskRow(",
		"func loadTaskRow(",
		"acl.AuthorizeTaskAccess",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("%s must define %s so task lookups stay behind the visibility ACL", owner, want)
		}
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
