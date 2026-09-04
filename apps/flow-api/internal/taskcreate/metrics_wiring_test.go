package taskcreate

import (
	"os"
	"strings"
	"testing"
)

// TestNewCountsExactlyOneCreation pins that New records the creation counter
// once per inserted row.
//
// Placement carries the guarantee here. TestCreateTaskCentralized proves that
// no other file in the module may name the generated task insert, so this one
// call covers the REST handlers, the MCP tools, the importer, the intake path
// and the signal judge's applier alike. A second call would double-count every
// task; a missing one would leave nf_tasks_created_total flat while tasks were
// being created, which reads as an idle system rather than a broken metric.
func TestNewCountsExactlyOneCreation(t *testing.T) {
	t.Parallel()

	src := readCreateSource(t)

	const call = "obs.IncTaskCreated()"
	if n := strings.Count(src, call); n != 1 {
		t.Errorf("taskcreate.New must contain exactly one %s; found %d", call, n)
	}
}

// TestCreationCountedOnlyAfterTheInsert pins that a failed insert records
// nothing. New returns early on the insert error, so the counter must sit
// after that check rather than before it.
func TestCreationCountedOnlyAfterTheInsert(t *testing.T) {
	t.Parallel()

	src := readCreateSource(t)

	insertAt := strings.Index(src, "q.CreateTask(")
	if insertAt < 0 {
		t.Fatal("could not locate the task insert in New")
	}
	countAt := strings.Index(src, "obs.IncTaskCreated()")
	if countAt < 0 {
		t.Fatal("could not locate the obs.IncTaskCreated call in New")
	}
	if countAt < insertAt {
		t.Error("obs.IncTaskCreated must come after the insert: a row that failed to insert is not a created task")
	}
}

func readCreateSource(t *testing.T) string {
	t.Helper()

	b, err := os.ReadFile("taskcreate.go")
	if err != nil {
		t.Fatalf("read taskcreate.go: %v", err)
	}
	return string(b)
}
