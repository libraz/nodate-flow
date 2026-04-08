package tasks

import (
	"database/sql"

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
		WorkspaceID:              bytesToUUIDString(r.WorkspacePublicID),
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

// nullBytesToUUIDPtr converts a sql.NullString carrying raw BINARY(16) bytes
// into a UUID string pointer; returns nil when NULL or wrong length.
func nullBytesToUUIDPtr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	if len(s.String) != 16 {
		return nil
	}
	var u uuid.UUID
	copy(u[:], s.String)
	out := u.String()
	return &out
}

// rawBytesToUUIDPtr is the []byte variant for columns that sqlc exposes
// as a raw BINARY(16) slice rather than sql.NullString. Returns nil
// when the slice is empty or not exactly 16 bytes.
func rawBytesToUUIDPtr(b []byte) *string {
	if len(b) != 16 {
		return nil
	}
	var u uuid.UUID
	copy(u[:], b)
	out := u.String()
	return &out
}

func rowToTaskListItemFromProject(r generated.ListTasksForProjectRow) TaskListItem {
	return TaskListItem{
		ID:                r.PublicID.String(),
		ProjectID:         bytesToUUIDString(r.ProjectPublicID),
		ProjectName:       r.ProjectName,
		ParentTaskID:      nullStr(r.ParentTaskPublicID),
		Title:             r.Title,
		DerivedState:      string(r.DerivedState),
		Priority:          r.Priority,
		DueOn:             nullDate(r.DueOn),
		StartedOn:         nullDate(r.StartedOn),
		CompletedAt:       nullTime(r.CompletedAt),
		SortWeight:        r.SortWeight,
		PrimaryAssigneeID: rawBytesToUUIDPtr(r.PrimaryAssigneePublicID),
		AssigneeCount:     r.AssigneeCount,
		UpdatedAt:         nullTime(r.UpdatedAt),
		CreatedAt:         r.CreatedAt,
	}
}

func rowToTaskListItemFromWorkspace(r generated.ListTasksForWorkspaceRow) TaskListItem {
	return TaskListItem{
		ID:                r.PublicID.String(),
		ProjectID:         bytesToUUIDString(r.ProjectPublicID),
		ProjectName:       r.ProjectName,
		ParentTaskID:      nullStr(r.ParentTaskPublicID),
		Title:             r.Title,
		DerivedState:      string(r.DerivedState),
		Priority:          r.Priority,
		DueOn:             nullDate(r.DueOn),
		StartedOn:         nullDate(r.StartedOn),
		CompletedAt:       nullTime(r.CompletedAt),
		SortWeight:        r.SortWeight,
		PrimaryAssigneeID: rawBytesToUUIDPtr(r.PrimaryAssigneePublicID),
		AssigneeCount:     r.AssigneeCount,
		UpdatedAt:         nullTime(r.UpdatedAt),
		CreatedAt:         r.CreatedAt,
	}
}

func rowToConstraint(r generated.ListConstraintsForTaskRow) TaskConstraint {
	return TaskConstraint{
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

func rowToDependency(r generated.ListDependenciesForTaskRow) TaskDependency {
	return TaskDependency{
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

func rowToActor(r generated.ListActorsForTaskRow) TaskActor {
	return TaskActor{
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

func rowToComment(r generated.ListCommentsForTaskRow) TaskComment {
	return TaskComment{
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
		UpdatedAt:           nullTime(r.UpdatedAt),
		CreatedAt:           r.CreatedAt,
	}
}
