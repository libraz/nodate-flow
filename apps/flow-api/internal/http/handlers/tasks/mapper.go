package tasks

import (
	"database/sql"

	"github.com/google/uuid"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
)

func bytesToUUIDString(b []byte) string {
	if len(b) != 16 {
		return ""
	}
	var u uuid.UUID
	copy(u[:], b)
	return u.String()
}

// nullBytesToUUIDString converts a sql.NullString whose underlying string
// is a raw BINARY(16) UUID (as returned by sqlc for nullable BINARY(16)
// columns) into a canonical hyphenated UUID v7 string. Returns "" when
// NULL or when the value is not exactly 16 bytes.
//
// Without this helper, handlers that assign the NullString directly into
// a string DTO field (e.g. via handlerutil.NullStr) leak the raw binary
// bytes through the JSON boundary, violating docs/requirements.md §11.9.
func nullBytesToUUIDString(s sql.NullString) string {
	if !s.Valid || len(s.String) != 16 {
		return ""
	}
	var u uuid.UUID
	copy(u[:], s.String)
	return u.String()
}

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
		ProjectIdentifier:        r.ProjectIdentifier,
		TaskNumber:               int32(r.TaskNumber),
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

// rawBytesToUUIDPtr converts a BINARY(16) column to a UUID string pointer.
// It accepts interface{} because sqlc may expose the column as either []byte
// or interface{} depending on the query. Returns nil when the value is not
// a []byte of exactly 16 bytes.
func rawBytesToUUIDPtr(v interface{}) *string {
	b, ok := v.([]byte)
	if !ok || len(b) != 16 {
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
		ProjectIdentifier: r.ProjectIdentifier,
		TaskNumber:        int32(r.TaskNumber),
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
		ProjectIdentifier: r.ProjectIdentifier,
		TaskNumber:        int32(r.TaskNumber),
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
