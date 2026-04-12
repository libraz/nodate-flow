// Package tasks contains Huma operation handlers for the /tasks endpoints
// (CRUD plus constraints, dependencies, actors, comments, and attachments).
package tasks

import (
	"database/sql"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai/embed"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai/nlconstraint"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/storage"
)

// Deps is the dependency bundle passed to each handler in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	// Embedder upserts task embeddings on Create / Patch when title or
	// description changes (ADR 0003). Optional: nil disables write-time
	// embedding, which is fine currently and the weekly reindex cron
	// will catch up.
	Embedder *embed.Client
	// NlConstraint is the natural-language-to-DSL compiler (3.AI-1).
	// Optional: nil causes the compile endpoint to return
	// AI.PROVIDER.NOT_CONFIGURED.
	NlConstraint *nlconstraint.Compiler
	// Storage is the S3-compatible object store client for file
	// uploads and downloads. Optional: nil causes the presign
	// endpoints to return INTERNAL.NOT_CONFIGURED.
	Storage *storage.Client
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

func nullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	tt := t.Time
	return &tt
}

// timePtr returns a pointer to a time.Time value, for assigning non-null
// times into DTO fields declared as *time.Time.
func timePtr(t time.Time) *time.Time {
	return &t
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
	WorkspaceID              string    `json:"workspaceId" format:"uuid"`
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
	CompletedAt              *time.Time `json:"completedAt,omitempty"`
	ConstraintCount          int64      `json:"constraintCount"`
	ConstraintSatisfiedCount int64      `json:"constraintSatisfiedCount"`
	DependencyCount          int64      `json:"dependencyCount"`
	ActorCount               int64      `json:"actorCount"`
	SortWeight               int32      `json:"sortWeight"`
	UpdatedAt                *time.Time `json:"updatedAt,omitempty"`
	CreatedAt                time.Time  `json:"createdAt"`
}

// TaskListItem is the public DTO for a task row in list responses.
type TaskListItem struct {
	ID                 string    `json:"id"`
	ProjectID          string    `json:"projectId"`
	ProjectName        string    `json:"projectName,omitempty"`
	ParentTaskID       string    `json:"parentTaskId,omitempty"`
	Title              string    `json:"title"`
	DerivedState       string    `json:"derivedState"`
	Priority           int32     `json:"priority"`
	DueOn              string    `json:"dueOn,omitempty"`
	StartedOn          string    `json:"startedOn,omitempty"`
	CompletedAt        *time.Time `json:"completedAt,omitempty"`
	SortWeight         int32      `json:"sortWeight"`
	PrimaryAssigneeID  *string    `json:"primaryAssigneeId"`
	AssigneeCount      int64      `json:"assigneeCount"`
	UpdatedAt          *time.Time `json:"updatedAt,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
}

// TaskConstraint is the public DTO for a task_constraints row.
type TaskConstraint struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Expression  string    `json:"expression"`
	SatisfiedAt *time.Time `json:"satisfiedAt,omitempty"`
	FailedAt    *time.Time `json:"failedAt,omitempty"`
	SortWeight  int32      `json:"sortWeight"`
	UpdatedAt   *time.Time `json:"updatedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// TaskDependency is the public DTO for a task_dependencies row.
type TaskDependency struct {
	ID                 string     `json:"id"`
	Kind               string     `json:"kind"`
	FromTaskID         string     `json:"fromTaskId"`
	ToTaskID           string     `json:"toTaskId"`
	ToTaskTitle        string     `json:"toTaskTitle"`
	ToTaskDerivedState string     `json:"toTaskDerivedState"`
	UpdatedAt          *time.Time `json:"updatedAt,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
}

// TaskActor is the public DTO for a task_actors row.
type TaskActor struct {
	ID          string     `json:"id"`
	UserID      string     `json:"userId"`
	Email       string     `json:"email"`
	DisplayName string     `json:"displayName"`
	AvatarURL   string     `json:"avatarUrl,omitempty"`
	Role        string     `json:"role"`
	UpdatedAt   *time.Time `json:"updatedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// TaskComment is the public DTO for a comments row attached to a task.
type TaskComment struct {
	ID                string     `json:"id"`
	AuthorID          string     `json:"authorId"`
	AuthorDisplayName string     `json:"authorDisplayName"`
	AuthorAvatarURL   string     `json:"authorAvatarUrl,omitempty"`
	Body              string     `json:"body"`
	EditedAt          *time.Time `json:"editedAt,omitempty"`
	UpdatedAt         *time.Time `json:"updatedAt,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
}

// TaskAttachment is the public DTO for an attachments row attached to a task.
type TaskAttachment struct {
	ID                  string    `json:"id"`
	UploaderID          string    `json:"uploaderId"`
	UploaderDisplayName string    `json:"uploaderDisplayName"`
	Filename            string    `json:"filename"`
	ContentType         string    `json:"contentType"`
	ByteSize            uint64    `json:"byteSize"`
	StorageKey          string    `json:"storageKey"`
	ChecksumSHA256      string     `json:"checksumSha256,omitempty"`
	UpdatedAt           *time.Time `json:"updatedAt,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
}

// ---- Task CRUD I/O ---------------------------------------------------------

// CreateTaskBody is the JSON body for POST /tasks.
type CreateTaskBody struct {
	ProjectID   string `json:"projectId" doc:"Project public id (UUID v7)"`
	Title       string `json:"title" minLength:"1" maxLength:"500"`
	Description string `json:"description,omitempty" maxLength:"50000"`
	Priority    int32  `json:"priority,omitempty" minimum:"0" maximum:"4"`
	DueOn       string `json:"dueOn,omitempty" doc:"YYYY-MM-DD"`
	StartOn     string `json:"startOn,omitempty" doc:"YYYY-MM-DD"`
}

// CreateTaskInput is the request for POST /tasks.
type CreateTaskInput struct {
	Body CreateTaskBody
}

// CreateTaskOutput is the response for POST /tasks.
type CreateTaskOutput struct {
	Body Task
}

// ListTasksInput is the query for GET /tasks.
type ListTasksInput struct {
	ProjectID   string   `query:"projectId" doc:"Optional project public id (UUID v7) to scope the list"`
	WorkspaceID string   `query:"workspaceId" doc:"Workspace public id (UUID v7); required when projectId is not given"`
	Q           string   `query:"q" doc:"Case-insensitive substring match on title"`
	State       []string `query:"state" doc:"Filter by derived_state; repeat to OR multiple values"`
	Assignee    string   `query:"assignee" doc:"Filter to tasks with this user as an assignee (user public id UUID v7)"`
	Limit       int32    `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset      int32    `query:"offset" minimum:"0" default:"0"`
}

// ListTasksBody is the response payload for GET /tasks.
type ListTasksBody struct {
	Total      int64          `json:"total"`
	Tasks      []TaskListItem `json:"tasks"`
	NextCursor *string        `json:"nextCursor"`
}

// ListTasksOutput is the response for GET /tasks.
type ListTasksOutput struct {
	Body ListTasksBody
}

// MyTaskListItem is the public DTO for a single row in the
// cross-workspace /me/tasks response. It carries workspace context
// on each row so the caller can group/filter client-side without a
// second round-trip per workspace.
type MyTaskListItem struct {
	ID            string     `json:"id"`
	WorkspaceID   string     `json:"workspaceId"`
	WorkspaceName string     `json:"workspaceName"`
	ProjectID     string     `json:"projectId"`
	ProjectName   string     `json:"projectName,omitempty"`
	Title         string     `json:"title"`
	DerivedState  string     `json:"derivedState"`
	Priority      int32      `json:"priority"`
	DueOn         string     `json:"dueOn,omitempty"`
	ActorRole     string     `json:"actorRole"`
	UpdatedAt     *time.Time `json:"updatedAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// ListMyTasksInput is the query for GET /me/tasks.
type ListMyTasksInput struct {
	Limit  int32 `query:"limit" minimum:"1" maximum:"500" default:"200"`
	Offset int32 `query:"offset" minimum:"0" default:"0"`
}

// ListMyTasksBody is the response payload for GET /me/tasks.
type ListMyTasksBody struct {
	Total int64            `json:"total"`
	Tasks []MyTaskListItem `json:"tasks"`
}

// ListMyTasksOutput is the response for GET /me/tasks.
type ListMyTasksOutput struct {
	Body ListMyTasksBody
}

// GetTaskInput is the path for GET /tasks/{id}.
type GetTaskInput struct {
	ID string `path:"id"`
}

// GetTaskOutput is the response for GET /tasks/{id}.
type GetTaskOutput struct {
	Body Task
}

// PatchTaskBody is the JSON body for PATCH /tasks/{id}.
type PatchTaskBody struct {
	Title       *string `json:"title,omitempty" minLength:"1" maxLength:"500"`
	Description *string `json:"description,omitempty" maxLength:"50000"`
	Priority    *int32  `json:"priority,omitempty" minimum:"0" maximum:"4"`
	DueOn       *string `json:"dueOn,omitempty" doc:"YYYY-MM-DD or empty string to clear"`
	StartOn     *string `json:"startOn,omitempty" doc:"YYYY-MM-DD or empty string to clear"`
	SortWeight  *int32  `json:"sortWeight,omitempty" doc:"Display order weight. Lower values sort first."`
}

// PatchTaskInput is the request for PATCH /tasks/{id}.
type PatchTaskInput struct {
	ID   string `path:"id"`
	Body PatchTaskBody
}

// PatchTaskOutput is the response for PATCH /tasks/{id}.
type PatchTaskOutput struct {
	Body Task
}

// DisableTaskInput is the path for DELETE /tasks/{id}.
type DisableTaskInput struct {
	ID string `path:"id"`
}

// DisableTaskBody is the response payload for DELETE /tasks/{id}.
type DisableTaskBody struct {
	Ok bool `json:"ok"`
}

// DisableTaskOutput is the response for DELETE /tasks/{id}.
type DisableTaskOutput struct {
	Body DisableTaskBody
}

// ---- Reorder I/O -----------------------------------------------------------

// ReorderItem is a single task with its new sort weight for the bulk
// reorder endpoint.
type ReorderItem struct {
	ID         string `json:"id" required:"true" doc:"Task public ID (UUID v7)"`
	SortWeight int32  `json:"sortWeight" required:"true" doc:"New display order weight"`
}

// ReorderTasksBody is the JSON body for POST /tasks/reorder.
type ReorderTasksBody struct {
	ProjectID string        `json:"projectId" required:"true" doc:"Project public ID (UUID v7); all tasks must belong to this project"`
	Items     []ReorderItem `json:"items" required:"true" minItems:"1" doc:"Tasks with new sort weights"`
}

// ReorderTasksInput is the request for POST /tasks/reorder.
type ReorderTasksInput struct {
	Body ReorderTasksBody
}

// ReorderTasksOkBody is the response payload for POST /tasks/reorder.
type ReorderTasksOkBody struct {
	Ok bool `json:"ok"`
}

// ReorderTasksOutput is the response for POST /tasks/reorder.
type ReorderTasksOutput struct {
	Body ReorderTasksOkBody
}

// ---- Transitions I/O -------------------------------------------------------

// TransitionTaskBody is the JSON body for POST /tasks/{id}/transitions.
type TransitionTaskBody struct {
	Transition string `json:"transition" enum:"start,block,unblock,submit,complete,reopen,cancel" doc:"State machine transition name"`
	Reason     string `json:"reason,omitempty" maxLength:"2000" doc:"Optional human-readable reason recorded on the event"`
	OccurredAt int64  `json:"occurredAt,omitempty" doc:"Optional client-provided unix seconds timestamp; ignored for storage"`
}

// TransitionTaskInput is the request for POST /tasks/{id}/transitions.
type TransitionTaskInput struct {
	ID   string `path:"id"`
	Body TransitionTaskBody
}

// TransitionTaskOutput is the response for POST /tasks/{id}/transitions.
type TransitionTaskOutput struct {
	Body Task
}

// ---- Constraints I/O -------------------------------------------------------

// AddTaskConstraintBody is the JSON body for POST /tasks/{id}/constraints.
type AddTaskConstraintBody struct {
	Kind       string `json:"kind" enum:"deadline,dependency,approval,signal,custom"`
	Expression string `json:"expression" minLength:"1" maxLength:"4000"`
}

// AddTaskConstraintInput is the request for POST /tasks/{id}/constraints.
type AddTaskConstraintInput struct {
	ID   string `path:"id"`
	Body AddTaskConstraintBody
}

// AddTaskConstraintOutput is the response for POST /tasks/{id}/constraints.
type AddTaskConstraintOutput struct {
	Body TaskConstraint
}

// RemoveTaskConstraintInput is the path for DELETE /tasks/{id}/constraints/{cid}.
type RemoveTaskConstraintInput struct {
	ID  string `path:"id"`
	CID string `path:"cid"`
}

// RemoveTaskConstraintBody is the response payload for DELETE /tasks/{id}/constraints/{cid}.
type RemoveTaskConstraintBody struct {
	Ok bool `json:"ok"`
}

// RemoveTaskConstraintOutput is the response for DELETE /tasks/{id}/constraints/{cid}.
type RemoveTaskConstraintOutput struct {
	Body RemoveTaskConstraintBody
}

// ---- NL Constraint Compile I/O --------------------------------------------

// CompileConstraintBody is the JSON body for POST /tasks/{id}/constraints/compile.
type CompileConstraintBody struct {
	Prompt string `json:"prompt" required:"true" minLength:"1" doc:"Natural language description of the constraint"`
}

// CompileConstraintInput is the request for POST /tasks/{id}/constraints/compile.
type CompileConstraintInput struct {
	ID   string `path:"id"`
	Body CompileConstraintBody
}

// CompileConstraintOutput is the response for POST /tasks/{id}/constraints/compile.
type CompileConstraintOutput struct {
	Body struct {
		Kind       string `json:"kind" doc:"Inferred constraint kind"`
		Expression string `json:"expression" doc:"DSL expression"`
	}
}

// ---- Constraint Explain I/O -----------------------------------------------

// ExplainConstraintBody is the JSON body for POST /tasks/{id}/constraints/explain.
type ExplainConstraintBody struct {
	Expression string `json:"expression" required:"true" minLength:"1" doc:"DSL expression to explain"`
}

// ExplainConstraintInput is the request for POST /tasks/{id}/constraints/explain.
type ExplainConstraintInput struct {
	ID   string `path:"id"`
	Body ExplainConstraintBody
}

// ExplainConstraintOutput is the response for POST /tasks/{id}/constraints/explain.
type ExplainConstraintOutput struct {
	Body struct {
		Explanation string `json:"explanation" doc:"Human-readable explanation"`
	}
}

// ---- Dependencies I/O ------------------------------------------------------

// AddTaskDependencyBody is the JSON body for POST /tasks/{id}/dependencies.
type AddTaskDependencyBody struct {
	ToTaskID string `json:"toTaskId" doc:"Target task public id (UUID v7)"`
	Kind     string `json:"kind" enum:"blocks,relates,duplicates,subtask_of"`
}

// AddTaskDependencyInput is the request for POST /tasks/{id}/dependencies.
type AddTaskDependencyInput struct {
	ID   string `path:"id"`
	Body AddTaskDependencyBody
}

// AddTaskDependencyOutput is the response for POST /tasks/{id}/dependencies.
type AddTaskDependencyOutput struct {
	Body TaskDependency
}

// RemoveTaskDependencyInput is the path for DELETE /tasks/{id}/dependencies/{depId}.
type RemoveTaskDependencyInput struct {
	ID    string `path:"id"`
	DepID string `path:"depId"`
}

// RemoveTaskDependencyBody is the response payload for DELETE /tasks/{id}/dependencies/{depId}.
type RemoveTaskDependencyBody struct {
	Ok bool `json:"ok"`
}

// RemoveTaskDependencyOutput is the response for DELETE /tasks/{id}/dependencies/{depId}.
type RemoveTaskDependencyOutput struct {
	Body RemoveTaskDependencyBody
}

// TaskDependencyEdge is a directional edge entry returned by
// GET /tasks/{id}/dependencies. The "other" task is the one at the
// non-current end of the edge (target for outgoing, source for incoming).
type TaskDependencyEdge struct {
	ID                    string    `json:"id"`
	Kind                  string    `json:"kind"`
	OtherTaskID           string    `json:"otherTaskId"`
	OtherTaskTitle        string    `json:"otherTaskTitle"`
	OtherTaskDerivedState string    `json:"otherTaskDerivedState"`
	CreatedAt             time.Time `json:"createdAt"`
}

// ListTaskDependenciesInput is the path for GET /tasks/{id}/dependencies.
type ListTaskDependenciesInput struct {
	ID string `path:"id"`
}

// ListTaskDependenciesBody is the response payload for GET /tasks/{id}/dependencies.
type ListTaskDependenciesBody struct {
	Outgoing []TaskDependencyEdge `json:"outgoing"`
	Incoming []TaskDependencyEdge `json:"incoming"`
}

// ListTaskDependenciesOutput is the response for GET /tasks/{id}/dependencies.
type ListTaskDependenciesOutput struct {
	Body ListTaskDependenciesBody
}

// ---- Actors I/O ------------------------------------------------------------

// AddTaskActorBody is the JSON body for POST /tasks/{id}/actors.
type AddTaskActorBody struct {
	UserID string `json:"userId" doc:"User public id (UUID v7)"`
	Role   string `json:"role" enum:"assignee,reviewer,watcher,approver"`
}

// AddTaskActorInput is the request for POST /tasks/{id}/actors.
type AddTaskActorInput struct {
	ID   string `path:"id"`
	Body AddTaskActorBody
}

// AddTaskActorOutput is the response for POST /tasks/{id}/actors.
type AddTaskActorOutput struct {
	Body TaskActor
}

// ListTaskActorsInput is the query for GET /tasks/{id}/actors.
type ListTaskActorsInput struct {
	ID     string `path:"id"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"100"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListTaskActorsBody is the response payload for GET /tasks/{id}/actors.
type ListTaskActorsBody struct {
	Total      int64       `json:"total"`
	Actors     []TaskActor `json:"actors"`
	NextCursor *string     `json:"nextCursor"`
}

// ListTaskActorsOutput is the response for GET /tasks/{id}/actors.
type ListTaskActorsOutput struct {
	Body ListTaskActorsBody
}

// RemoveTaskActorInput is the path for DELETE /tasks/{id}/actors/{actorId}.
type RemoveTaskActorInput struct {
	ID      string `path:"id"`
	ActorID string `path:"actorId"`
}

// RemoveTaskActorBody is the response payload for DELETE /tasks/{id}/actors/{actorId}.
type RemoveTaskActorBody struct {
	Ok bool `json:"ok"`
}

// RemoveTaskActorOutput is the response for DELETE /tasks/{id}/actors/{actorId}.
type RemoveTaskActorOutput struct {
	Body RemoveTaskActorBody
}

// TaskAgentActor is the public DTO for an AI agent attached to a task
// via task_actors (kind='agent'). Shape is deliberately distinct from
// TaskActor so the frontend can render agents differently. (2.MCP-2)
type TaskAgentActor struct {
	ID        string     `json:"id"`
	AgentID   string     `json:"agentId"`
	AgentName string     `json:"agentName"`
	Role      string     `json:"role"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

// AddTaskAgentActorBody is the JSON body for POST /tasks/{id}/agents.
type AddTaskAgentActorBody struct {
	AgentID string `json:"agentId" doc:"AI agent public id (UUID v7)"`
	Role    string `json:"role" enum:"assignee,reviewer,watcher,approver"`
}

// AddTaskAgentActorInput is the request for POST /tasks/{id}/agents.
type AddTaskAgentActorInput struct {
	ID   string `path:"id"`
	Body AddTaskAgentActorBody
}

// AddTaskAgentActorOutput is the response for POST /tasks/{id}/agents.
type AddTaskAgentActorOutput struct {
	Body TaskAgentActor
}

// ListTaskAgentActorsInput is the query for GET /tasks/{id}/agents.
type ListTaskAgentActorsInput struct {
	ID     string `path:"id"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"100"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListTaskAgentActorsBody is the response payload for GET /tasks/{id}/agents.
type ListTaskAgentActorsBody struct {
	Total  int64            `json:"total"`
	Agents []TaskAgentActor `json:"agents"`
}

// ListTaskAgentActorsOutput is the response for GET /tasks/{id}/agents.
type ListTaskAgentActorsOutput struct {
	Body ListTaskAgentActorsBody
}

// ---- Comments I/O ----------------------------------------------------------

// AddTaskCommentBody is the JSON body for POST /tasks/{id}/comments.
type AddTaskCommentBody struct {
	Body string `json:"body" minLength:"1" maxLength:"50000"`
}

// AddTaskCommentInput is the request for POST /tasks/{id}/comments.
type AddTaskCommentInput struct {
	ID   string `path:"id"`
	Body AddTaskCommentBody
}

// AddTaskCommentOutput is the response for POST /tasks/{id}/comments.
type AddTaskCommentOutput struct {
	Body TaskComment
}

// ListTaskCommentsInput is the query for GET /tasks/{id}/comments.
type ListTaskCommentsInput struct {
	ID     string `path:"id"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListTaskCommentsBody is the response payload for GET /tasks/{id}/comments.
type ListTaskCommentsBody struct {
	Total      int64         `json:"total"`
	Comments   []TaskComment `json:"comments"`
	NextCursor *string       `json:"nextCursor"`
}

// ListTaskCommentsOutput is the response for GET /tasks/{id}/comments.
type ListTaskCommentsOutput struct {
	Body ListTaskCommentsBody
}

// EditTaskCommentBody is the JSON body for PATCH /tasks/{id}/comments/{cid}.
type EditTaskCommentBody struct {
	Body string `json:"body" minLength:"1" maxLength:"50000"`
}

// EditTaskCommentInput is the request for PATCH /tasks/{id}/comments/{cid}.
type EditTaskCommentInput struct {
	ID   string `path:"id"`
	CID  string `path:"cid"`
	Body EditTaskCommentBody
}

// EditTaskCommentOutput is the response for PATCH /tasks/{id}/comments/{cid}.
type EditTaskCommentOutput struct {
	Body TaskComment
}

// DeleteTaskCommentInput is the path for DELETE /tasks/{id}/comments/{cid}.
type DeleteTaskCommentInput struct {
	ID  string `path:"id"`
	CID string `path:"cid"`
}

// DeleteTaskCommentBody is the response payload for DELETE /tasks/{id}/comments/{cid}.
type DeleteTaskCommentBody struct {
	Ok bool `json:"ok"`
}

// DeleteTaskCommentOutput is the response for DELETE /tasks/{id}/comments/{cid}.
type DeleteTaskCommentOutput struct {
	Body DeleteTaskCommentBody
}

// ---- Attachments I/O -------------------------------------------------------

// AddTaskAttachmentBody is the JSON body for POST /tasks/{id}/attachments.
type AddTaskAttachmentBody struct {
	Filename    string `json:"filename" minLength:"1" maxLength:"512"`
	ContentType string `json:"contentType" minLength:"1" maxLength:"255"`
	ByteSize    uint64 `json:"byteSize" minimum:"0"`
	StorageKey  string `json:"storageKey" minLength:"1" maxLength:"1024"`
}

// AddTaskAttachmentInput is the request for POST /tasks/{id}/attachments.
type AddTaskAttachmentInput struct {
	ID   string `path:"id"`
	Body AddTaskAttachmentBody
}

// AddTaskAttachmentOutput is the response for POST /tasks/{id}/attachments.
type AddTaskAttachmentOutput struct {
	Body TaskAttachment
}

// ListTaskAttachmentsInput is the query for GET /tasks/{id}/attachments.
type ListTaskAttachmentsInput struct {
	ID     string `path:"id"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListTaskAttachmentsBody is the response payload for GET /tasks/{id}/attachments.
type ListTaskAttachmentsBody struct {
	Total       int64            `json:"total"`
	Attachments []TaskAttachment `json:"attachments"`
	NextCursor  *string          `json:"nextCursor"`
}

// ListTaskAttachmentsOutput is the response for GET /tasks/{id}/attachments.
type ListTaskAttachmentsOutput struct {
	Body ListTaskAttachmentsBody
}

// DeleteTaskAttachmentInput is the path for DELETE /tasks/{id}/attachments/{aid}.
type DeleteTaskAttachmentInput struct {
	ID  string `path:"id"`
	AID string `path:"aid"`
}

// DeleteTaskAttachmentBody is the response payload for DELETE /tasks/{id}/attachments/{aid}.
type DeleteTaskAttachmentBody struct {
	Ok bool `json:"ok"`
}

// DeleteTaskAttachmentOutput is the response for DELETE /tasks/{id}/attachments/{aid}.
type DeleteTaskAttachmentOutput struct {
	Body DeleteTaskAttachmentBody
}

// PresignUploadBody is the JSON body for POST /tasks/{id}/attachments/presign.
type PresignUploadBody struct {
	Filename    string `json:"filename" minLength:"1" maxLength:"512" doc:"Original filename"`
	ContentType string `json:"contentType" minLength:"1" maxLength:"255" doc:"MIME type"`
	ByteSize    uint64 `json:"byteSize" minimum:"1" doc:"File size in bytes"`
}

// PresignUploadInput is the request for POST /tasks/{id}/attachments/presign.
type PresignUploadInput struct {
	ID   string `path:"id"`
	Body PresignUploadBody
}

// PresignUploadOutputBody is the response body for presign upload.
type PresignUploadOutputBody struct {
	UploadURL    string `json:"uploadUrl" doc:"Presigned PUT URL"`
	StorageKey   string `json:"storageKey" doc:"Object key to confirm after upload"`
	AttachmentID string `json:"attachmentId" doc:"Public ID of the created attachment row"`
}

// PresignUploadOutput is the response for POST /tasks/{id}/attachments/presign.
type PresignUploadOutput struct {
	Body PresignUploadOutputBody
}

// DownloadAttachmentInput is the path for GET /tasks/{id}/attachments/{aid}/download.
type DownloadAttachmentInput struct {
	ID  string `path:"id"`
	AID string `path:"aid"`
}

// DownloadAttachmentOutputBody is the response body for download.
type DownloadAttachmentOutputBody struct {
	DownloadURL string `json:"downloadUrl" doc:"Presigned GET URL with Content-Disposition: attachment"`
}

// DownloadAttachmentOutput is the response for GET /tasks/{id}/attachments/{aid}/download.
type DownloadAttachmentOutput struct {
	Body DownloadAttachmentOutputBody
}
