package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 1. Self-reference CHECK constraints
// ---------------------------------------------------------------------------
// These tests verify that MySQL enforces the CHECK constraints that prevent
// a row from pointing to itself. The API layer should also block these
// operations, but the DB constraints are the last line of defense. We test
// them via direct SQL to ensure they work independently of the API logic.

// TestCheckConstraintTasksNoSelfParent verifies that setting a task's
// parent_task_id to its own id is rejected by the
// chk_tasks_no_self_parent CHECK constraint.
func TestCheckConstraintTasksNoSelfParent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Create a task via the API.
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Self-parent test"},
		&task)
	require.NotEmpty(t, task.ID, "task creation must return a public id")

	// Resolve the internal id from the public id.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var internalID uint32
	err := testDB.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`, task.ID,
	).Scan(&internalID)
	require.NoError(t, err, "must resolve internal task id")
	require.NotZero(t, internalID)

	// Attempt to set parent_task_id = id (self-reference). The CHECK
	// constraint chk_tasks_no_self_parent must reject this.
	_, err = testDB.ExecContext(ctx,
		`UPDATE tasks SET parent_task_id = ? WHERE id = ?`,
		internalID, internalID)
	require.Error(t, err, "UPDATE setting parent_task_id to own id must be rejected by CHECK constraint")
	require.Contains(t, err.Error(), "chk_tasks_no_self_parent",
		"error must reference the CHECK constraint name")
}

// TestCheckConstraintPagesNoSelfParent verifies that setting a page's
// parent_page_id to its own id is rejected by the
// chk_pages_no_self_parent CHECK constraint.
func TestCheckConstraintPagesNoSelfParent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	wsURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID

	// Create a page via the API.
	var page struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, wsURL+"/pages", tt.AccessToken,
		map[string]any{"title": "Self-parent page test", "body": "body"},
		&page)
	require.NotEmpty(t, page.ID, "page creation must return a public id")

	// Resolve the internal id.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var internalID uint32
	err := testDB.QueryRowContext(ctx,
		`SELECT id FROM pages WHERE public_id = UUID_TO_BIN(?, 0)`, page.ID,
	).Scan(&internalID)
	require.NoError(t, err, "must resolve internal page id")
	require.NotZero(t, internalID)

	// Attempt to set parent_page_id = id (self-reference). The CHECK
	// constraint chk_pages_no_self_parent must reject this.
	_, err = testDB.ExecContext(ctx,
		`UPDATE pages SET parent_page_id = ? WHERE id = ?`,
		internalID, internalID)
	require.Error(t, err, "UPDATE setting parent_page_id to own id must be rejected by CHECK constraint")
	require.Contains(t, err.Error(), "chk_pages_no_self_parent",
		"error must reference the CHECK constraint name")
}

// TestCheckConstraintTaskDependenciesNoSelf verifies that inserting a
// task_dependencies row where from_task_id = to_task_id is rejected by
// the chk_task_dependencies_no_self CHECK constraint.
func TestCheckConstraintTaskDependenciesNoSelf(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Create a task via the API.
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Self-dep test"},
		&task)
	require.NotEmpty(t, task.ID)

	// Resolve internal ids for the task and its workspace.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var taskInternalID, workspaceID uint32
	err := testDB.QueryRowContext(ctx,
		`SELECT id, workspace_id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`, task.ID,
	).Scan(&taskInternalID, &workspaceID)
	require.NoError(t, err, "must resolve internal task id")

	// Attempt to INSERT a self-referencing dependency. The CHECK
	// constraint chk_task_dependencies_no_self must reject this.
	_, err = testDB.ExecContext(ctx,
		`INSERT INTO task_dependencies (public_id, workspace_id, from_task_id, to_task_id, kind)
		 VALUES (UUID_TO_BIN(UUID(), 0), ?, ?, ?, 'blocks')`,
		workspaceID, taskInternalID, taskInternalID)
	require.Error(t, err, "INSERT with from_task_id = to_task_id must be rejected by CHECK constraint")
	require.Contains(t, err.Error(), "chk_task_dependencies_no_self",
		"error must reference the CHECK constraint name")
}

// TestCheckConstraintRelationSuggestionsNoSelf verifies that inserting a
// relation_suggestions row where source_task_id = target_task_id is
// rejected by the chk_relation_suggestions_no_self CHECK constraint.
func TestCheckConstraintRelationSuggestionsNoSelf(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Create a task via the API.
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Self-relation test"},
		&task)
	require.NotEmpty(t, task.ID)

	// Resolve internal ids.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var taskInternalID, workspaceID uint32
	err := testDB.QueryRowContext(ctx,
		`SELECT id, workspace_id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`, task.ID,
	).Scan(&taskInternalID, &workspaceID)
	require.NoError(t, err, "must resolve internal task id")

	// Attempt to INSERT a self-referencing relation suggestion. The CHECK
	// constraint chk_relation_suggestions_no_self must reject this.
	_, err = testDB.ExecContext(ctx,
		`INSERT INTO relation_suggestions
		   (public_id, workspace_id, source_task_id, target_task_id, suggested_kind, confidence)
		 VALUES (UUID_TO_BIN(UUID(), 0), ?, ?, ?, 'duplicates', 0.9500)`,
		workspaceID, taskInternalID, taskInternalID)
	require.Error(t, err, "INSERT with source_task_id = target_task_id must be rejected by CHECK constraint")
	require.Contains(t, err.Error(), "chk_relation_suggestions_no_self",
		"error must reference the CHECK constraint name")
}

// ---------------------------------------------------------------------------
// 2. Soft-delete UNIQUE constraint behavior
// ---------------------------------------------------------------------------
// These tests verify that the composite UNIQUE constraints include the
// `enabled` column, so soft-deleted rows (enabled=FALSE) do not block
// the creation of new enabled rows with the same natural key.

// TestSoftDeleteUniqueProjectSlug verifies that disabling a project
// (enabled=FALSE) frees its slug for reuse within the same workspace.
// The UNIQUE KEY uniq_projects_workspace_id_slug_enabled includes the
// enabled column, so (workspace_id, slug, TRUE) and
// (workspace_id, slug, FALSE) are distinct entries.
func TestSoftDeleteUniqueProjectSlug(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	wsURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID

	slug := "reusable-slug-" + randomHex(4)

	// Create a project with the slug.
	var proj1 struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	doJSON(t, http.MethodPost, wsURL+"/projects", tt.AccessToken,
		map[string]any{"slug": slug, "name": "First project"}, &proj1)
	require.Equal(t, slug, proj1.Slug)

	// Soft-delete (disable) the project via direct SQL. We set enabled=FALSE
	// to simulate the soft-delete behavior. Using direct SQL here because
	// the API may or may not expose a project delete endpoint yet.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := testDB.ExecContext(ctx,
		`UPDATE projects SET enabled = FALSE
		 WHERE public_id = UUID_TO_BIN(?, 0)`, proj1.ID)
	require.NoError(t, err, "soft-deleting the first project must succeed")

	// Create a second project with the same slug. Because the first project
	// is now enabled=FALSE, the UNIQUE KEY (workspace_id, slug, enabled)
	// should allow this insertion with enabled=TRUE.
	var proj2 struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	doJSON(t, http.MethodPost, wsURL+"/projects", tt.AccessToken,
		map[string]any{"slug": slug, "name": "Second project"}, &proj2)
	require.Equal(t, slug, proj2.Slug)
	require.NotEqual(t, proj1.ID, proj2.ID,
		"second project must be a distinct row from the soft-deleted one")
}

// TestSoftDeleteUniqueTaskDependencyEdge verifies that disabling a
// task dependency (enabled=FALSE) frees the (from, to, kind) edge for
// re-creation. The UNIQUE KEY uniq_task_dependencies_edge includes the
// enabled column.
func TestSoftDeleteUniqueTaskDependencyEdge(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Create two tasks.
	var task1, task2 struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Dep edge source"}, &task1)
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Dep edge target"}, &task2)
	require.NotEmpty(t, task1.ID)
	require.NotEmpty(t, task2.ID)

	// Create a dependency via the API.
	var dep1 struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task1.ID+"/dependencies",
		tt.AccessToken, map[string]any{
			"toTaskId": task2.ID,
			"kind":     "blocks",
		}, &dep1)
	require.NotEmpty(t, dep1.ID)

	// Soft-delete the dependency via direct SQL (set enabled=FALSE).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := testDB.ExecContext(ctx,
		`UPDATE task_dependencies SET enabled = FALSE
		 WHERE public_id = UUID_TO_BIN(?, 0)`, dep1.ID)
	require.NoError(t, err, "soft-deleting the dependency must succeed")

	// Re-create the same edge. Because the old row is enabled=FALSE,
	// the UNIQUE KEY (from_task_id, to_task_id, kind, enabled) should
	// allow this new row with enabled=TRUE.
	var dep2 struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task1.ID+"/dependencies",
		tt.AccessToken, map[string]any{
			"toTaskId": task2.ID,
			"kind":     "blocks",
		}, &dep2)
	require.NotEmpty(t, dep2.ID)
	require.NotEqual(t, dep1.ID, dep2.ID,
		"re-created dependency must be a distinct row from the soft-deleted one")
}

// ---------------------------------------------------------------------------
// 3. Positive self-reference sanity checks
// ---------------------------------------------------------------------------
// Verify that legitimate parent references (pointing to a *different* row)
// still work correctly, ensuring the CHECK constraints do not over-block.

// TestTaskParentRefToDifferentTask verifies that setting parent_task_id
// to another task's id is accepted (the CHECK constraint only blocks
// self-references).
func TestTaskParentRefToDifferentTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Create parent and child tasks.
	var parent, child struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Parent task"}, &parent)
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Child task"}, &child)

	// Set parent_task_id on the child to point to the parent via direct SQL.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var parentInternal, childInternal uint32
	err := testDB.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`, parent.ID,
	).Scan(&parentInternal)
	require.NoError(t, err)

	err = testDB.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`, child.ID,
	).Scan(&childInternal)
	require.NoError(t, err)

	// This must succeed: parent_task_id != id.
	_, err = testDB.ExecContext(ctx,
		`UPDATE tasks SET parent_task_id = ? WHERE id = ?`,
		parentInternal, childInternal)
	require.NoError(t, err, "setting parent_task_id to a different task must succeed")

	// Verify the update stuck.
	var actualParent uint32
	err = testDB.QueryRowContext(ctx,
		`SELECT parent_task_id FROM tasks WHERE id = ?`, childInternal,
	).Scan(&actualParent)
	require.NoError(t, err)
	require.Equal(t, parentInternal, actualParent,
		"parent_task_id must be set to the parent's internal id")
}

// TestPageParentRefToDifferentPage verifies that setting parent_page_id
// to another page's id is accepted.
func TestPageParentRefToDifferentPage(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	wsURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID

	// Create parent and child pages.
	var parent, child struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, wsURL+"/pages", tt.AccessToken,
		map[string]any{"title": "Parent page", "body": "p"}, &parent)
	doJSON(t, http.MethodPost, wsURL+"/pages", tt.AccessToken,
		map[string]any{"title": "Child page", "body": "c"}, &child)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var parentInternal, childInternal uint32
	err := testDB.QueryRowContext(ctx,
		`SELECT id FROM pages WHERE public_id = UUID_TO_BIN(?, 0)`, parent.ID,
	).Scan(&parentInternal)
	require.NoError(t, err)

	err = testDB.QueryRowContext(ctx,
		`SELECT id FROM pages WHERE public_id = UUID_TO_BIN(?, 0)`, child.ID,
	).Scan(&childInternal)
	require.NoError(t, err)

	// This must succeed: parent_page_id != id.
	_, err = testDB.ExecContext(ctx,
		`UPDATE pages SET parent_page_id = ? WHERE id = ?`,
		parentInternal, childInternal)
	require.NoError(t, err, "setting parent_page_id to a different page must succeed")

	var actualParent uint32
	err = testDB.QueryRowContext(ctx,
		`SELECT parent_page_id FROM pages WHERE id = ?`, childInternal,
	).Scan(&actualParent)
	require.NoError(t, err)
	require.Equal(t, parentInternal, actualParent,
		"parent_page_id must be set to the parent's internal id")
}
