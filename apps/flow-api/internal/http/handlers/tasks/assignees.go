package tasks

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// attachAssignees fills TaskListItem.Assignees for one page of items with a
// single query. Assignees is always non-nil so the wire shape is [] rather
// than null.
func attachAssignees(ctx context.Context, q *generated.Queries, workspaceID uint32, items []TaskListItem) error {
	for i := range items {
		items[i].Assignees = []TaskAssigneeRef{}
	}
	if len(items) == 0 {
		return nil
	}
	ids := make([]types.PublicID, 0, len(items))
	index := make(map[string][]int, len(items))
	for i, it := range items {
		pid, err := types.Parse(it.ID)
		if err != nil {
			return err
		}
		ids = append(ids, pid)
		index[it.ID] = append(index[it.ID], i)
	}
	rows, err := q.ListTaskAssigneesForTasks(ctx, generated.ListTaskAssigneesForTasksParams{
		WorkspaceID:   workspaceID,
		TaskPublicIds: ids,
	})
	if err != nil {
		return err
	}
	for _, r := range rows {
		ref := TaskAssigneeRef{ID: r.UserPublicID.String(), DisplayName: r.DisplayName}
		for _, i := range index[r.TaskPublicID.String()] {
			items[i].Assignees = append(items[i].Assignees, ref)
		}
	}
	return nil
}
