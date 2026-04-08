package tasks

import (
	"github.com/google/uuid"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
)

func bytesToUUIDString(b []byte) string {
	if len(b) != 16 {
		return ""
	}
	var u uuid.UUID
	copy(u[:], b)
	return u.String()
}

func rowToTaskFromFind(r generated.FindTaskByPublicIdRow) Task {
	return Task{
		ID:                       r.PublicID.String(),
		ProjectID:                bytesToUUIDString(r.ProjectPublicID),
		ProjectName:              r.ProjectName,
		ParentTaskID:             nullStr(r.ParentTaskPublicID),
		CreatedByUserID:          nullStr(r.CreatedByUserPublicID),
		Title:                    r.Title,
		Description:              nullStr(r.Description),
		DerivedState:             string(r.DerivedState),
		Priority:                 r.Priority,
		DueOn:                    nullDate(r.DueOn),
		StartedOn:                nullDate(r.StartedOn),
		CompletedAt:              nullTime(r.CompletedAt),
		ConstraintCount:          r.ConstraintCount,
		ConstraintSatisfiedCount: r.ConstraintSatisfiedCount,
		DependencyCount:          r.DependencyCount,
		ActorCount:               r.ActorCount,
		SortWeight:               r.SortWeight,
		UpdatedAt:                nullTime(r.UpdatedAt),
		CreatedAt:                r.CreatedAt,
	}
}

func rowToTaskListItemFromProject(r generated.ListTasksForProjectRow) TaskListItem {
	return TaskListItem{
		ID:           r.PublicID.String(),
		ProjectID:    bytesToUUIDString(r.ProjectPublicID),
		ProjectName:  r.ProjectName,
		ParentTaskID: nullStr(r.ParentTaskPublicID),
		Title:        r.Title,
		DerivedState: string(r.DerivedState),
		Priority:     r.Priority,
		DueOn:        nullDate(r.DueOn),
		StartedOn:    nullDate(r.StartedOn),
		CompletedAt:  nullTime(r.CompletedAt),
		SortWeight:   r.SortWeight,
		UpdatedAt:    nullTime(r.UpdatedAt),
		CreatedAt:    r.CreatedAt,
	}
}

func rowToTaskListItemFromWorkspace(r generated.ListTasksForWorkspaceRow) TaskListItem {
	return TaskListItem{
		ID:           r.PublicID.String(),
		ProjectID:    bytesToUUIDString(r.ProjectPublicID),
		ProjectName:  r.ProjectName,
		ParentTaskID: nullStr(r.ParentTaskPublicID),
		Title:        r.Title,
		DerivedState: string(r.DerivedState),
		Priority:     r.Priority,
		DueOn:        nullDate(r.DueOn),
		StartedOn:    nullDate(r.StartedOn),
		CompletedAt:  nullTime(r.CompletedAt),
		SortWeight:   r.SortWeight,
		UpdatedAt:    nullTime(r.UpdatedAt),
		CreatedAt:    r.CreatedAt,
	}
}

func rowToConstraint(r generated.ListConstraintsForTaskRow) Constraint {
	return Constraint{
		ID:          r.PublicID.String(),
		Kind:        string(r.Kind),
		Expression:  r.Expression,
		SatisfiedAt: nullTime(r.SatisfiedAt),
		FailedAt:    nullTime(r.FailedAt),
		SortWeight:  r.SortWeight,
		UpdatedAt:   nullTime(r.UpdatedAt),
		CreatedAt:   r.CreatedAt,
	}
}

func rowToDependency(r generated.ListDependenciesForTaskRow) Dependency {
	return Dependency{
		ID:                 r.PublicID.String(),
		Kind:               string(r.Kind),
		FromTaskID:         r.FromTaskPublicID.String(),
		ToTaskID:           r.ToTaskPublicID.String(),
		ToTaskTitle:        r.ToTaskTitle,
		ToTaskDerivedState: string(r.ToTaskDerivedState),
		UpdatedAt:          nullTime(r.UpdatedAt),
		CreatedAt:          r.CreatedAt,
	}
}

func rowToActor(r generated.ListActorsForTaskRow) Actor {
	return Actor{
		ID:          r.PublicID.String(),
		UserID:      r.UserPublicID.String(),
		Email:       r.Email,
		DisplayName: r.DisplayName,
		AvatarURL:   nullStr(r.AvatarUrl),
		Role:        string(r.Role),
		UpdatedAt:   nullTime(r.UpdatedAt),
		CreatedAt:   r.CreatedAt,
	}
}

func rowToComment(r generated.ListCommentsForTaskRow) Comment {
	return Comment{
		ID:                r.PublicID.String(),
		AuthorID:          r.AuthorPublicID.String(),
		AuthorDisplayName: r.AuthorDisplayName,
		AuthorAvatarURL:   nullStr(r.AuthorAvatarUrl),
		Body:              r.Body,
		EditedAt:          nullTime(r.EditedAt),
		UpdatedAt:         nullTime(r.UpdatedAt),
		CreatedAt:         r.CreatedAt,
	}
}

func rowToAttachment(r generated.ListAttachmentsForTaskRow) Attachment {
	return Attachment{
		ID:                  r.PublicID.String(),
		UploaderID:          r.UploaderPublicID.String(),
		UploaderDisplayName: r.UploaderDisplayName,
		Filename:            r.Filename,
		ContentType:         r.ContentType,
		ByteSize:            r.ByteSize,
		StorageKey:          r.StorageKey,
		ChecksumSHA256:      nullStr(r.ChecksumSha256),
		UpdatedAt:           nullTime(r.UpdatedAt),
		CreatedAt:           r.CreatedAt,
	}
}
