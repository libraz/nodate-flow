package generated

import (
	"strings"
	"testing"
)

func TestTaskNumberAllocationLocksProjectRow(t *testing.T) {
	t.Parallel()

	if !strings.Contains(lockProjectForTaskNumber, "FROM projects") {
		t.Fatalf("task-number allocation must lock the owning project row:\n%s", lockProjectForTaskNumber)
	}
	if !strings.Contains(lockProjectForTaskNumber, "workspace_id = ?") {
		t.Fatalf("project lock must stay scoped to the caller workspace:\n%s", lockProjectForTaskNumber)
	}
	if !strings.Contains(lockProjectForTaskNumber, "id = ?") {
		t.Fatalf("project lock must target the project being allocated:\n%s", lockProjectForTaskNumber)
	}
	if !strings.Contains(lockProjectForTaskNumber, "FOR UPDATE") {
		t.Fatalf("project lock must use SELECT ... FOR UPDATE:\n%s", lockProjectForTaskNumber)
	}
}
