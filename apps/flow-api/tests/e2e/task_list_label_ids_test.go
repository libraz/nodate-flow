package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestTaskListLabelIdsAreUUIDStrings pins the wire shape of a task list
// row's labelIds.
//
// The column behind it is a GROUP_CONCAT, and the concatenation used to
// run over raw BINARY(16) public ids. That shape is unrecoverable twice
// over: 0x2C is a legal byte inside a UUIDv7 so a reader cannot tell a
// separator from payload, and the bytes are not valid UTF-8 so JSON
// encoding replaces them with U+FFFD before any reader sees them.
//
// Two labels, not one: with a single label the concatenation has no
// separator, so a broken join still yields something that superficially
// parses. The separator is the part that has to work.
func TestTaskListLabelIdsAreUUIDStrings(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)

	first := seedWorkspaceLabel(t, owner, "label-ids-first")
	second := seedWorkspaceLabel(t, owner, "label-ids-second")

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId": owner.ProjectPublicID,
			"title":     "Task carrying two labels",
		}, &task)
	require.NotEmpty(t, task.ID)

	for _, labelID := range []string{first, second} {
		doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/labels",
			owner.AccessToken, map[string]any{"labelId": labelID}, nil)
	}

	var list struct {
		Tasks []struct {
			ID       string   `json:"id"`
			LabelIDs []string `json:"labelIds"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks?projectId="+owner.ProjectPublicID,
		owner.AccessToken, nil, &list)

	var got []string
	found := false
	for _, row := range list.Tasks {
		if row.ID == task.ID {
			got = row.LabelIDs
			found = true
			break
		}
	}
	require.True(t, found, "the task under test must appear in its project's task list")

	require.Len(t, got, 2, "both labels must survive the aggregate, split apart")
	for _, id := range got {
		_, err := uuid.Parse(id)
		require.NoErrorf(t, err, "labelIds entry %q must be a UUID string", id)
	}
	require.ElementsMatch(t, []string{first, second}, got,
		"labelIds must carry the public ids the labels were created with")
}

// taskListLabelIDCap mirrors the aggregate cap in _v_task_list_all.sql.
const taskListLabelIDCap = 20

// TestTaskListLabelIdsCapIsWholeEntries pins the truncation boundary.
//
// GROUP_CONCAT clips its result at group_concat_max_len (1024 bytes by
// default) with no error and no marker. At 37 bytes per UUID entry the
// clip would land mid-id somewhere past the 27th label and publish a
// malformed id. The view caps the aggregate below that point instead, so
// the boundary is a whole number of entries: a task with more labels
// than the cap reports exactly the cap, every entry intact.
func TestTaskListLabelIdsCapIsWholeEntries(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId": owner.ProjectPublicID,
			"title":     "Task carrying more labels than the cap",
		}, &task)
	require.NotEmpty(t, task.ID)

	for i := 0; i < taskListLabelIDCap+1; i++ {
		labelID := seedWorkspaceLabel(t, owner, fmt.Sprintf("cap-label-%02d", i))
		doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/labels",
			owner.AccessToken, map[string]any{"labelId": labelID}, nil)
	}

	var list struct {
		Tasks []struct {
			ID       string   `json:"id"`
			LabelIDs []string `json:"labelIds"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks?projectId="+owner.ProjectPublicID,
		owner.AccessToken, nil, &list)

	var got []string
	found := false
	for _, row := range list.Tasks {
		if row.ID == task.ID {
			got = row.LabelIDs
			found = true
			break
		}
	}
	require.True(t, found, "the task under test must appear in its project's task list")

	require.Len(t, got, taskListLabelIDCap,
		"a task past the cap reports exactly the cap, not a byte-clipped list")
	for _, id := range got {
		_, err := uuid.Parse(id)
		require.NoErrorf(t, err, "every entry up to the cap must be a whole UUID, got %q", id)
	}
}
