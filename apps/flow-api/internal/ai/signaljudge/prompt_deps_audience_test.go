package signaljudge

import (
	"strings"
	"testing"
)

// The judge's context lookups have no actor. Whether that is safe rests
// entirely on the audience bound written into their SQL, and that bound
// is one clause anyone can delete while every other test in the package
// stays green: the fakes in prompt_test.go never touch the statements,
// and the integration test that does runs against tasks it created
// itself, which are public by default.
//
// So the bound is held to here, structurally rather than by matching the
// statement text: every relation the two lookups read `tasks` through
// must carry it. A third UNION branch, or a second FROM tasks added to
// either statement, fails until it does.

// tasksReads counts the places a statement reads the tasks table.
func tasksReads(stmt string) int {
	return strings.Count(strings.ToUpper(stmt), "FROM TASKS")
}

// TestJudgeContextLookupsBoundEveryTasksReadToItsAudience is the check.
func TestJudgeContextLookupsBoundEveryTasksReadToItsAudience(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		sql  string
		// wantReads is stated rather than derived so that a branch
		// removed from a statement is a change somebody has to look at
		// here, not one that silently shrinks what is being proved.
		wantReads int
	}{
		{name: "recent tasks", sql: recentTasksQuery, wantReads: 1},
		{name: "linked tasks", sql: linkedTasksQuery, wantReads: 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reads := tasksReads(tc.sql)
			if reads != tc.wantReads {
				t.Fatalf("the statement reads tasks %d times, expected %d; "+
					"a read added or removed changes what this proves and has to be stated here",
					reads, tc.wantReads)
			}
			bounds := strings.Count(tc.sql, publicOnly)
			if bounds != reads {
				t.Fatalf("%d of the %d reads of tasks carry %q. A judge run has no actor, and "+
					"the prompt built from these rows is stored in ai_invocations.prompt_redacted, "+
					"which every member of the workspace can read — so a read without the bound "+
					"hands a task's title to people its visibility keeps it from:\n%s",
					bounds, reads, publicOnly, tc.sql)
			}
			if !strings.Contains(tc.sql, "workspace_id = ?") {
				t.Errorf("the statement is not bound to a workspace:\n%s", tc.sql)
			}
		})
	}
}

// TestPublicOnlyNamesTheWidestAudience pins what the bound is, not just
// that it is present.
//
// Widening it to include 'project' would read as the same kind of clause
// and pass every count above, while putting a project's tasks in front of
// the workspace members who are not in that project.
func TestPublicOnlyNamesTheWidestAudience(t *testing.T) {
	t.Parallel()

	if publicOnly != "visibility = 'public'" {
		t.Fatalf("the audience bound is %q. Only 'public' means "+
			"\"every member of this workspace already sees this\"; 'project' and 'private' "+
			"are narrower audiences that these lookups have no actor to check against",
			publicOnly)
	}
	for _, narrower := range []string{"project", "private"} {
		if strings.Contains(recentTasksQuery, "'"+narrower+"'") ||
			strings.Contains(linkedTasksQuery, "'"+narrower+"'") {
			t.Errorf("a judge context lookup admits %q-visibility tasks; the run has no actor "+
				"to decide who among them may see one", narrower)
		}
	}
}
