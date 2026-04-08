// Package tasks contains Huma operation handlers for the /tasks endpoints
// (CRUD plus constraints, dependencies, actors, comments, and attachments).
package tasks

import (
	"database/sql"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
)

// Deps is the dependency bundle passed to each handler in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
}

func httpErr(spec *apierrors.Spec) error {
	return huma.NewError(spec.Status, spec.Code+": "+spec.Message)
}

func nullStr(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}

func nullTime(t sql.NullTime) time.Time {
	if t.Valid {
		return t.Time
	}
	return time.Time{}
}

func nullDate(t sql.NullTime) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format("2006-01-02")
}

// totalAsInt64 normalizes the COUNT(*) OVER() return type into int64.
func totalAsInt64(v interface{}) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case uint64:
		return int64(x)
	case []byte:
		var n int64
		for _, c := range x {
			if c < '0' || c > '9' {
				return n
			}
			n = n*10 + int64(c-'0')
		}
		return n
	}
	return 0
}

// Task is the public DTO for a task row.
type Task struct {
	ID                       string    `json:"id"`
	ProjectID                string    `json:"projectId"`
	ProjectName              string    `json:"projectName,omitempty"`
	ParentTaskID             string    `json:"parentTaskId,omitempty"`
	CreatedByUserID          string    `json:"createdByUserId,omitempty"`
	Title                    string    `json:"title"`
	Description              string    `json:"description,omitempty"`
	DerivedState             string    `json:"derivedState"`
	Priority                 int32     `json:"priority"`
	DueOn                    string    `json:"dueOn,omitempty"`
	StartedOn                string    `json:"startedOn,omitempty"`
	CompletedAt              time.Time `json:"completedAt,omitempty"`
	ConstraintCount          int64     `json:"constraintCount"`
	ConstraintSatisfiedCount int64     `json:"constraintSatisfiedCount"`
	DependencyCount          int64     `json:"dependencyCount"`
	ActorCount               int64     `json:"actorCount"`
	SortWeight               int32     `json:"sortWeight"`
	UpdatedAt                time.Time `json:"updatedAt,omitempty"`
	CreatedAt                time.Time `json:"createdAt"`
}

// TaskListItem is the public DTO for a task row in list responses.
type TaskListItem struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"projectId"`
	ProjectName  string    `json:"projectName,omitempty"`
	ParentTaskID string    `json:"parentTaskId,omitempty"`
	Title        string    `json:"title"`
	DerivedState string    `json:"derivedState"`
	Priority     int32     `json:"priority"`
	DueOn        string    `json:"dueOn,omitempty"`
	StartedOn    string    `json:"startedOn,omitempty"`
	CompletedAt  time.Time `json:"completedAt,omitempty"`
	SortWeight   int32     `json:"sortWeight"`
	UpdatedAt    time.Time `json:"updatedAt,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

// Constraint is the public DTO for a task_constraints row.
type Constraint struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Expression  string    `json:"expression"`
	SatisfiedAt time.Time `json:"satisfiedAt,omitempty"`
	FailedAt    time.Time `json:"failedAt,omitempty"`
	SortWeight  int32     `json:"sortWeight"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Dependency is the public DTO for a task_dependencies row.
type Dependency struct {
	ID                 string    `json:"id"`
	Kind               string    `json:"kind"`
	FromTaskID         string    `json:"fromTaskId"`
	ToTaskID           string    `json:"toTaskId"`
	ToTaskTitle        string    `json:"toTaskTitle"`
	ToTaskDerivedState string    `json:"toTaskDerivedState"`
	UpdatedAt          time.Time `json:"updatedAt,omitempty"`
	CreatedAt          time.Time `json:"createdAt"`
}

// Actor is the public DTO for a task_actors row.
type Actor struct {
	ID          string    `json:"id"`
	UserID      string    `json:"userId"`
	Email       string    `json:"email"`
	DisplayName string    `json:"displayName"`
	AvatarURL   string    `json:"avatarUrl,omitempty"`
	Role        string    `json:"role"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Comment is the public DTO for a comments row.
type Comment struct {
	ID                string    `json:"id"`
	AuthorID          string    `json:"authorId"`
	AuthorDisplayName string    `json:"authorDisplayName"`
	AuthorAvatarURL   string    `json:"authorAvatarUrl,omitempty"`
	Body              string    `json:"body"`
	EditedAt          time.Time `json:"editedAt,omitempty"`
	UpdatedAt         time.Time `json:"updatedAt,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
}

// Attachment is the public DTO for an attachments row.
type Attachment struct {
	ID                  string    `json:"id"`
	UploaderID          string    `json:"uploaderId"`
	UploaderDisplayName string    `json:"uploaderDisplayName"`
	Filename            string    `json:"filename"`
	ContentType         string    `json:"contentType"`
	ByteSize            uint64    `json:"byteSize"`
	StorageKey          string    `json:"storageKey"`
	ChecksumSHA256      string    `json:"checksumSha256,omitempty"`
	UpdatedAt           time.Time `json:"updatedAt,omitempty"`
	CreatedAt           time.Time `json:"createdAt"`
}

// ---- Task CRUD I/O ---------------------------------------------------------

// CreateInput is the body for POST /tasks.
type CreateInput struct {
	Body struct {
		ProjectID   string `json:"projectId" doc:"Project public id (UUID v7)"`
		Title       string `json:"title" minLength:"1" maxLength:"500"`
		Description string `json:"description,omitempty" maxLength:"50000"`
		Priority    int32  `json:"priority,omitempty" minimum:"0" maximum:"4"`
		DueOn       string `json:"dueOn,omitempty" doc:"YYYY-MM-DD"`
		StartOn     string `json:"startOn,omitempty" doc:"YYYY-MM-DD"`
	}
}

// CreateOutput is the response for POST /tasks.
type CreateOutput struct {
	Body Task
}

// ListInput is the query for GET /tasks.
type ListInput struct {
	ProjectID   string `query:"projectId" doc:"Optional project public id (UUID v7) to scope the list"`
	WorkspaceID string `query:"workspaceId" doc:"Workspace public id (UUID v7); required when projectId is not given"`
	Limit       int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset      int32  `query:"offset" minimum:"0" default:"0"`
}

// ListOutput is the response for GET /tasks.
type ListOutput struct {
	Body struct {
		Total      int64          `json:"total"`
		Tasks      []TaskListItem `json:"tasks"`
		NextCursor *string        `json:"nextCursor"`
	}
}

// GetInput is the path for GET /tasks/{id}.
type GetInput struct {
	ID string `path:"id"`
}

// GetOutput is the response for GET /tasks/{id}.
type GetOutput struct {
	Body Task
}

// PatchInput is the body for PATCH /tasks/{id}.
type PatchInput struct {
	ID   string `path:"id"`
	Body struct {
		Title       *string `json:"title,omitempty" minLength:"1" maxLength:"500"`
		Description *string `json:"description,omitempty" maxLength:"50000"`
		Priority    *int32  `json:"priority,omitempty" minimum:"0" maximum:"4"`
		DueOn       *string `json:"dueOn,omitempty" doc:"YYYY-MM-DD or empty string to clear"`
		StartOn     *string `json:"startOn,omitempty" doc:"YYYY-MM-DD or empty string to clear"`
	}
}

// PatchOutput is the response for PATCH /tasks/{id}.
type PatchOutput struct {
	Body Task
}

// DisableInput is the path for DELETE /tasks/{id}.
type DisableInput struct {
	ID string `path:"id"`
}

// DisableOutput is the response for DELETE /tasks/{id}.
type DisableOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// ---- Constraints I/O -------------------------------------------------------

// AddConstraintInput is the body for POST /tasks/{id}/constraints.
type AddConstraintInput struct {
	ID   string `path:"id"`
	Body struct {
		Kind       string `json:"kind" enum:"deadline,dependency,approval,signal,custom"`
		Expression string `json:"expression" minLength:"1" maxLength:"4000"`
	}
}

// AddConstraintOutput is the response for POST /tasks/{id}/constraints.
type AddConstraintOutput struct {
	Body Constraint
}

// RemoveConstraintInput is the path for DELETE /tasks/{id}/constraints/{cid}.
type RemoveConstraintInput struct {
	ID  string `path:"id"`
	CID string `path:"cid"`
}

// RemoveConstraintOutput is the response for DELETE /tasks/{id}/constraints/{cid}.
type RemoveConstraintOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// ---- Dependencies I/O ------------------------------------------------------

// AddDependencyInput is the body for POST /tasks/{id}/dependencies.
type AddDependencyInput struct {
	ID   string `path:"id"`
	Body struct {
		ToTaskID string `json:"toTaskId" doc:"Target task public id (UUID v7)"`
		Kind     string `json:"kind" enum:"blocks,relates,duplicates,subtask_of"`
	}
}

// AddDependencyOutput is the response for POST /tasks/{id}/dependencies.
type AddDependencyOutput struct {
	Body Dependency
}

// RemoveDependencyInput is the path for DELETE /tasks/{id}/dependencies/{depId}.
type RemoveDependencyInput struct {
	ID    string `path:"id"`
	DepID string `path:"depId"`
}

// RemoveDependencyOutput is the response for DELETE /tasks/{id}/dependencies/{depId}.
type RemoveDependencyOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// ---- Actors I/O ------------------------------------------------------------

// AddActorInput is the body for POST /tasks/{id}/actors.
type AddActorInput struct {
	ID   string `path:"id"`
	Body struct {
		UserID string `json:"userId" doc:"User public id (UUID v7)"`
		Role   string `json:"role" enum:"assignee,reviewer,watcher,approver"`
	}
}

// AddActorOutput is the response for POST /tasks/{id}/actors.
type AddActorOutput struct {
	Body Actor
}

// RemoveActorInput is the path for DELETE /tasks/{id}/actors/{actorId}.
type RemoveActorInput struct {
	ID      string `path:"id"`
	ActorID string `path:"actorId"`
}

// RemoveActorOutput is the response for DELETE /tasks/{id}/actors/{actorId}.
type RemoveActorOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// ---- Comments I/O ----------------------------------------------------------

// AddCommentInput is the body for POST /tasks/{id}/comments.
type AddCommentInput struct {
	ID   string `path:"id"`
	Body struct {
		Body string `json:"body" minLength:"1" maxLength:"50000"`
	}
}

// AddCommentOutput is the response for POST /tasks/{id}/comments.
type AddCommentOutput struct {
	Body Comment
}

// ListCommentsInput is the query for GET /tasks/{id}/comments.
type ListCommentsInput struct {
	ID     string `path:"id"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListCommentsOutput is the response for GET /tasks/{id}/comments.
type ListCommentsOutput struct {
	Body struct {
		Total      int64     `json:"total"`
		Comments   []Comment `json:"comments"`
		NextCursor *string   `json:"nextCursor"`
	}
}

// EditCommentInput is the body for PATCH /tasks/{id}/comments/{cid}.
type EditCommentInput struct {
	ID   string `path:"id"`
	CID  string `path:"cid"`
	Body struct {
		Body string `json:"body" minLength:"1" maxLength:"50000"`
	}
}

// EditCommentOutput is the response for PATCH /tasks/{id}/comments/{cid}.
type EditCommentOutput struct {
	Body Comment
}

// DeleteCommentInput is the path for DELETE /tasks/{id}/comments/{cid}.
type DeleteCommentInput struct {
	ID  string `path:"id"`
	CID string `path:"cid"`
}

// DeleteCommentOutput is the response for DELETE /tasks/{id}/comments/{cid}.
type DeleteCommentOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// ---- Attachments I/O -------------------------------------------------------

// AddAttachmentInput is the body for POST /tasks/{id}/attachments.
type AddAttachmentInput struct {
	ID   string `path:"id"`
	Body struct {
		Filename    string `json:"filename" minLength:"1" maxLength:"512"`
		ContentType string `json:"contentType" minLength:"1" maxLength:"255"`
		ByteSize    uint64 `json:"byteSize" minimum:"0"`
		StorageKey  string `json:"storageKey" minLength:"1" maxLength:"1024"`
	}
}

// AddAttachmentOutput is the response for POST /tasks/{id}/attachments.
type AddAttachmentOutput struct {
	Body Attachment
}

// ListAttachmentsInput is the query for GET /tasks/{id}/attachments.
type ListAttachmentsInput struct {
	ID     string `path:"id"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListAttachmentsOutput is the response for GET /tasks/{id}/attachments.
type ListAttachmentsOutput struct {
	Body struct {
		Total       int64        `json:"total"`
		Attachments []Attachment `json:"attachments"`
		NextCursor  *string      `json:"nextCursor"`
	}
}

// DeleteAttachmentInput is the path for DELETE /tasks/{id}/attachments/{aid}.
type DeleteAttachmentInput struct {
	ID  string `path:"id"`
	AID string `path:"aid"`
}

// DeleteAttachmentOutput is the response for DELETE /tasks/{id}/attachments/{aid}.
type DeleteAttachmentOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}
