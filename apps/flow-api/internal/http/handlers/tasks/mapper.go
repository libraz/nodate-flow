package tasks

import (
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// bytesToUUIDString / nullBytesToUUIDString / nullBytesToUUIDPtr /
// rawBytesToUUIDPtr delegate to handlerutil so the four byte→UUID
// conversion shapes share a single implementation across handler
// packages. Aliases keep the call sites readable.
var (
	bytesToUUIDString     = handlerutil.BytesToUUIDString
	nullBytesToUUIDString = handlerutil.NullBytesToUUIDString
	nullBytesToUUIDPtr    = handlerutil.NullBytesToUUIDPtr
	rawBytesToUUIDPtr     = handlerutil.RawBytesToUUIDPtr
)

func rowToTaskFromFind(r generated.FindTaskByPublicIdRow) Task {
	return Task{
		ID:                       r.PublicID.String(),
		WorkspaceID:              bytesToUUIDString(r.WorkspacePublicID),
		ProjectID:                bytesToUUIDString(r.ProjectPublicID),
		ProjectName:              r.ProjectName,
		ParentTaskID:             nullBytesToUUIDString(r.ParentTaskPublicID),
		CreatedByUserID:          nullBytesToUUIDString(r.CreatedByUserPublicID),
		Title:                    r.Title,
		Description:              nullStr(r.Description),
		Visibility:               string(r.Visibility),
		DerivedState:             string(r.DerivedState),
		Priority:                 r.Priority,
		DueOn:                    nullDate(r.DueOn),
		StartedOn:                nullDate(r.StartedOn),
		CompletedAt:              nullTimeUnix(r.CompletedAt),
		ProjectIdentifier:        r.ProjectIdentifier.String,
		TaskNumber:               int32(r.TaskNumber), //#nosec G115 -- task_number is per-project sequence (uint32), fits int32 within realistic deployments
		ArchivedAt:               nullTimeUnix(r.ArchivedAt),
		LabelCount:               r.LabelCount,
		ConstraintCount:          r.ConstraintCount,
		ConstraintSatisfiedCount: r.ConstraintSatisfiedCount,
		DependencyCount:          r.DependencyCount,
		ActorCount:               r.ActorCount,
		SortWeight:               r.SortWeight,
		UpdatedAt:                nullTimeUnix(r.UpdatedAt),
		CreatedAt:                r.CreatedAt.Unix(),
	}
}

func rowToTaskListItemFromProject(r generated.ListTasksForProjectRow) TaskListItem {
	return TaskListItem{
		ID:                r.PublicID.String(),
		ProjectID:         bytesToUUIDString(r.ProjectPublicID),
		ProjectName:       r.ProjectName,
		ProjectIdentifier: r.ProjectIdentifier.String,
		TaskNumber:        int32(r.TaskNumber), //#nosec G115 -- task_number is per-project sequence (uint32), fits int32 within realistic deployments
		ParentTaskID:      nullBytesToUUIDString(r.ParentTaskPublicID),
		Title:             r.Title,
		Visibility:        string(r.Visibility),
		DerivedState:      string(r.DerivedState),
		Priority:          r.Priority,
		DueOn:             nullDate(r.DueOn),
		StartedOn:         nullDate(r.StartedOn),
		CompletedAt:       nullTimeUnix(r.CompletedAt),
		ArchivedAt:        nullTimeUnix(r.ArchivedAt),
		LabelIDs:          nullStr(r.LabelIds),
		SortWeight:        r.SortWeight,
		PrimaryAssigneeID: rawBytesToUUIDPtr(r.PrimaryAssigneePublicID),
		AssigneeCount:     r.AssigneeCount,
		UpdatedAt:         nullTimeUnix(r.UpdatedAt),
		CreatedAt:         r.CreatedAt.Unix(),
	}
}

// rowToTaskListItemFromProjectKeyset is the keyset-pagination twin of
// rowToTaskListItemFromProject. The Row shape differs only in that it
// drops the COUNT(*) OVER() Total column (keyset queries never carry
// total since the response is "more pages or not", not "page X of Y"),
// so the projection is otherwise identical.
func rowToTaskListItemFromProjectKeyset(r generated.ListTasksForProjectKeysetRow) TaskListItem {
	return TaskListItem{
		ID:                r.PublicID.String(),
		ProjectID:         bytesToUUIDString(r.ProjectPublicID),
		ProjectName:       r.ProjectName,
		ProjectIdentifier: r.ProjectIdentifier.String,
		TaskNumber:        int32(r.TaskNumber), //#nosec G115 -- task_number is per-project sequence (uint32), fits int32 within realistic deployments
		ParentTaskID:      nullBytesToUUIDString(r.ParentTaskPublicID),
		Title:             r.Title,
		Visibility:        string(r.Visibility),
		DerivedState:      string(r.DerivedState),
		Priority:          r.Priority,
		DueOn:             nullDate(r.DueOn),
		StartedOn:         nullDate(r.StartedOn),
		CompletedAt:       nullTimeUnix(r.CompletedAt),
		ArchivedAt:        nullTimeUnix(r.ArchivedAt),
		LabelIDs:          nullStr(r.LabelIds),
		SortWeight:        r.SortWeight,
		PrimaryAssigneeID: rawBytesToUUIDPtr(r.PrimaryAssigneePublicID),
		AssigneeCount:     r.AssigneeCount,
		UpdatedAt:         nullTimeUnix(r.UpdatedAt),
		CreatedAt:         r.CreatedAt.Unix(),
	}
}

// rowToTaskListItemFromWorkspaceKeyset is the keyset-pagination twin of
// rowToTaskListItemFromWorkspace, structurally identical bar the Total
// column.
func rowToTaskListItemFromWorkspaceKeyset(r generated.ListTasksForWorkspaceKeysetRow) TaskListItem {
	return TaskListItem{
		ID:                r.PublicID.String(),
		ProjectID:         bytesToUUIDString(r.ProjectPublicID),
		ProjectName:       r.ProjectName,
		ProjectIdentifier: r.ProjectIdentifier.String,
		TaskNumber:        int32(r.TaskNumber), //#nosec G115 -- task_number is per-project sequence (uint32), fits int32 within realistic deployments
		ParentTaskID:      nullBytesToUUIDString(r.ParentTaskPublicID),
		Title:             r.Title,
		Visibility:        string(r.Visibility),
		DerivedState:      string(r.DerivedState),
		Priority:          r.Priority,
		DueOn:             nullDate(r.DueOn),
		StartedOn:         nullDate(r.StartedOn),
		CompletedAt:       nullTimeUnix(r.CompletedAt),
		ArchivedAt:        nullTimeUnix(r.ArchivedAt),
		LabelIDs:          nullStr(r.LabelIds),
		SortWeight:        r.SortWeight,
		PrimaryAssigneeID: rawBytesToUUIDPtr(r.PrimaryAssigneePublicID),
		AssigneeCount:     r.AssigneeCount,
		UpdatedAt:         nullTimeUnix(r.UpdatedAt),
		CreatedAt:         r.CreatedAt.Unix(),
	}
}

func rowToTaskListItemFromWorkspace(r generated.ListTasksForWorkspaceRow) TaskListItem {
	return TaskListItem{
		ID:                r.PublicID.String(),
		ProjectID:         bytesToUUIDString(r.ProjectPublicID),
		ProjectName:       r.ProjectName,
		ProjectIdentifier: r.ProjectIdentifier.String,
		TaskNumber:        int32(r.TaskNumber), //#nosec G115 -- task_number is per-project sequence (uint32), fits int32 within realistic deployments
		ParentTaskID:      nullBytesToUUIDString(r.ParentTaskPublicID),
		Title:             r.Title,
		Visibility:        string(r.Visibility),
		DerivedState:      string(r.DerivedState),
		Priority:          r.Priority,
		DueOn:             nullDate(r.DueOn),
		StartedOn:         nullDate(r.StartedOn),
		CompletedAt:       nullTimeUnix(r.CompletedAt),
		ArchivedAt:        nullTimeUnix(r.ArchivedAt),
		LabelIDs:          nullStr(r.LabelIds),
		SortWeight:        r.SortWeight,
		PrimaryAssigneeID: rawBytesToUUIDPtr(r.PrimaryAssigneePublicID),
		AssigneeCount:     r.AssigneeCount,
		UpdatedAt:         nullTimeUnix(r.UpdatedAt),
		CreatedAt:         r.CreatedAt.Unix(),
	}
}

func rowToConstraint(r generated.ListConstraintsForTaskRow) TaskConstraint {
	return TaskConstraint{
		ID:          r.PublicID.String(),
		Kind:        string(r.Kind),
		Expression:  r.Expression,
		SatisfiedAt: nullTimeUnix(r.SatisfiedAt),
		FailedAt:    nullTimeUnix(r.FailedAt),
		SortWeight:  r.SortWeight,
		UpdatedAt:   nullTimeUnix(r.UpdatedAt),
		CreatedAt:   r.CreatedAt.Unix(),
	}
}

func rowToDependency(r generated.ListDependenciesForTaskRow) TaskDependency {
	return TaskDependency{
		ID:                 r.PublicID.String(),
		Kind:               string(r.Kind),
		FromTaskID:         r.FromTaskPublicID.String(),
		ToTaskID:           r.ToTaskPublicID.String(),
		ToTaskTitle:        r.ToTaskTitle,
		ToTaskDerivedState: string(r.ToTaskDerivedState),
		UpdatedAt:          nullTimeUnix(r.UpdatedAt),
		CreatedAt:          r.CreatedAt.Unix(),
	}
}

func rowToActor(r generated.ListActorsForTaskRow) TaskActor {
	return TaskActor{
		ID:          r.PublicID.String(),
		UserID:      r.UserPublicID.String(),
		Email:       r.Email,
		DisplayName: r.DisplayName,
		AvatarURL:   nullStr(r.AvatarUrl),
		Role:        string(r.Role),
		UpdatedAt:   nullTimeUnix(r.UpdatedAt),
		CreatedAt:   r.CreatedAt.Unix(),
	}
}

func rowToComment(r generated.ListCommentsForTaskRow) TaskComment {
	return TaskComment{
		ID:                r.PublicID.String(),
		AuthorID:          r.AuthorPublicID.String(),
		AuthorDisplayName: r.AuthorDisplayName,
		AuthorAvatarURL:   nullStr(r.AuthorAvatarUrl),
		Body:              r.Body,
		EditedAt:          nullTimeUnix(r.EditedAt),
		UpdatedAt:         nullTimeUnix(r.UpdatedAt),
		CreatedAt:         r.CreatedAt.Unix(),
	}
}

func rowToAttachment(r generated.ListAttachmentsForTaskRow) TaskAttachment {
	return TaskAttachment{
		ID:                  r.PublicID.String(),
		UploaderID:          r.UploaderPublicID.String(),
		UploaderDisplayName: r.UploaderDisplayName,
		Filename:            r.Filename,
		ContentType:         r.ContentType,
		ByteSize:            r.ByteSize,
		StorageKey:          r.StorageKey,
		ChecksumSHA256:      nullStr(r.ChecksumSha256),
		UpdatedAt:           nullTimeUnix(r.UpdatedAt),
		CreatedAt:           r.CreatedAt.Unix(),
	}
}
