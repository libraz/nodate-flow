package tasks

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
)

func TestParentAndSubtaskProjectionsGateByLayer4(t *testing.T) {
	t.Parallel()

	flag, count, args := parentAndSubtaskProjections(7, middleware.WorkspaceRoleAdmin)
	if len(args) != 0 || strings.Contains(count, "visibility") || flag != "CAST(1 AS SIGNED)" {
		t.Fatalf("elevated actor must get ungated projections, got flag=%s args=%v count=%s", flag, args, count)
	}

	flag, count, args = parentAndSubtaskProjections(7, middleware.WorkspaceRoleMember)
	if len(args) != 6 {
		t.Fatalf("want 2 gates x 3 binds, got %d", len(args))
	}
	for _, a := range args {
		if a != uint32(7) {
			t.Fatalf("bind %v is not the actor id", a)
		}
	}
	if !strings.HasPrefix(flag, "CAST(CASE WHEN ") || !strings.HasSuffix(flag, " THEN 1 ELSE 0 END AS SIGNED)") ||
		!strings.Contains(flag, "v.parent_task_visibility") {
		t.Errorf("flag is not gated on the parent columns: %s", flag)
	}
	if strings.Contains(flag, "v.visibility") || strings.Contains(flag, "v.task_internal_id") {
		t.Errorf("flag still reads the child's own columns: %s", flag)
	}
	if !strings.Contains(count, "cv.visibility") || !strings.Contains(count, "cv.archived_at IS NULL") {
		t.Errorf("count is not gated on the children: %s", count)
	}
	if strings.Contains(strings.ReplaceAll(count, "cv.", ""), " v.visibility") {
		t.Errorf("count still reads the outer row's visibility: %s", count)
	}
	if got := strings.Count(flag+count, "?"); got != len(args) {
		t.Errorf("placeholders=%d args=%d", got, len(args))
	}
}

const parentUUID = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

// parentRaw is parentUUID as the raw BINARY(16) bytes the queries return.
var parentRaw = string([]byte{0x01, 0x90, 0xa1, 0xb2, 0xc3, 0xd4, 0x7e, 0x5f, 0x8a, 0x9b, 0x0c, 0x1d, 0x2e, 0x3f, 0x4a, 0x5b})

func TestListMappersGateParentOnVisibility(t *testing.T) {
	t.Parallel()

	parent := sql.NullString{String: parentRaw, Valid: true}
	title := sql.NullString{String: "secret parent", Valid: true}

	cases := map[string]func(visible int64) TaskListItem{
		"row": func(v int64) TaskListItem {
			return taskListRowToDTO(taskListRow{ParentTaskPublicID: parent, ParentTaskTitle: title, ParentVisible: v == 1})
		},
		"workspace": func(v int64) TaskListItem {
			return taskListRowToDTO(taskListRowFromWorkspace(generated.ListTasksForWorkspaceRow{
				ParentTaskPublicID: parent, ParentTaskTitle: title, ParentVisible: v}))
		},
		"archived": func(v int64) TaskListItem {
			return mapArchivedTaskListItem(generated.ListArchivedTasksForWorkspaceRow{
				ParentTaskPublicID: parent, ParentTaskTitle: title, ParentVisible: v})
		},
		"archived keyset": func(v int64) TaskListItem {
			return mapArchivedTaskListItemKeyset(generated.ListArchivedTasksForWorkspaceKeysetRow{
				ParentTaskPublicID: parent, ParentTaskTitle: title, ParentVisible: v})
		},
	}
	for name, run := range cases {
		hidden := run(0)
		if hidden.ParentTaskID != "" || hidden.ParentTaskTitle != "" {
			t.Errorf("%s: invisible parent leaked id=%q title=%q", name, hidden.ParentTaskID, hidden.ParentTaskTitle)
		}
		shown := run(1)
		if shown.ParentTaskID != parentUUID || shown.ParentTaskTitle != "secret parent" {
			t.Errorf("%s: visible parent dropped id=%q title=%q", name, shown.ParentTaskID, shown.ParentTaskTitle)
		}
	}
}
