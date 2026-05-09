// Package tasks contains Huma operation handlers for the /tasks endpoints
// (CRUD plus constraints, dependencies, actors, comments, and attachments).
package tasks

import (
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/nlconstraint"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/storage"
)

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// Deps is the dependency bundle passed to each handler in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	// Embedder upserts task embeddings on Create / Patch when title or
	// description changes (ADR 0003). Optional: nil disables write-time
	// embedding, which is fine currently and the weekly reindex cron
	// will catch up.
	Embedder *embed.Client
	// NlConstraint is the natural-language-to-DSL compiler.
	// Optional: nil causes the compile endpoint to return
	// AI.PROVIDER.NOT_CONFIGURED.
	NlConstraint *nlconstraint.Compiler
	// Storage is the S3-compatible object store client for file
	// uploads and downloads. Optional: nil causes the presign
	// endpoints to return INTERNAL.NOT_CONFIGURED.
	Storage *storage.Client
	// Audit records audit log entries for task mutations.
	// Optional: nil disables audit logging.
	Audit *audit.Recorder
}

// nullStr delegates to handlerutil.NullStr.
var nullStr = handlerutil.NullStr

// nullTimeUnix delegates to handlerutil.NullTimeUnix (returns *int64, nil for NULL).
var nullTimeUnix = handlerutil.NullTimeUnix

// int64Ptr returns a pointer to an int64 value, for assigning non-null
// unix-seconds timestamps into DTO fields declared as *int64.
func int64Ptr(v int64) *int64 {
	return &v
}

// nullTimeDate delegates to handlerutil.NullTimeDate (returns *string YYYY-MM-DD, nil for NULL).
var nullTimeDate = handlerutil.NullTimeDate

// nullDate delegates to handlerutil.NullTimeDateStr (returns string YYYY-MM-DD, "" for NULL).
// Used by Task / TaskListItem DTOs whose `dueOn` / `startedOn` fields are
// declared as `string` with `omitempty` rather than `*string`.
var nullDate = handlerutil.NullTimeDateStr

// totalAsInt64 delegates to handlerutil.TotalAsInt64.
var totalAsInt64 = handlerutil.TotalAsInt64

// Task is the public DTO for a task row.
type Task struct {
	ID                       string `json:"id"`
	WorkspaceID              string `json:"workspaceId" format:"uuid"`
	ProjectID                string `json:"projectId"`
	ProjectName              string `json:"projectName,omitempty"`
	ParentTaskID             string `json:"parentTaskId,omitempty"`
	CreatedByUserID          string `json:"createdByUserId,omitempty"`
	Title                    string `json:"title"`
	Description              string `json:"description,omitempty"`
	Visibility               string `json:"visibility"`
	DerivedState             string `json:"derivedState"`
	Priority                 int32  `json:"priority"`
	DueOn                    string `json:"dueOn,omitempty"`
	StartedOn                string `json:"startedOn,omitempty"`
	CompletedAt              *int64 `json:"completedAt,omitempty"`
	ProjectIdentifier        string `json:"projectIdentifier,omitempty"`
	TaskNumber               int32  `json:"taskNumber"`
	ArchivedAt               *int64 `json:"archivedAt,omitempty"`
	LabelCount               int64  `json:"labelCount"`
	ConstraintCount          int64  `json:"constraintCount"`
	ConstraintSatisfiedCount int64  `json:"constraintSatisfiedCount"`
	DependencyCount          int64  `json:"dependencyCount"`
	ActorCount               int64  `json:"actorCount"`
	SortWeight               int32  `json:"sortWeight"`
	UpdatedAt                *int64 `json:"updatedAt,omitempty"`
	CreatedAt                int64  `json:"createdAt"`
}

// TaskListItem is the public DTO for a task row in list responses.
type TaskListItem struct {
	ID                string  `json:"id"`
	ProjectID         string  `json:"projectId"`
	ProjectName       string  `json:"projectName,omitempty"`
	ParentTaskID      string  `json:"parentTaskId,omitempty"`
	Title             string  `json:"title"`
	Visibility        string  `json:"visibility"`
	DerivedState      string  `json:"derivedState"`
	Priority          int32   `json:"priority"`
	DueOn             string  `json:"dueOn,omitempty"`
	StartedOn         string  `json:"startedOn,omitempty"`
	CompletedAt       *int64  `json:"completedAt,omitempty"`
	ProjectIdentifier string  `json:"projectIdentifier,omitempty"`
	TaskNumber        int32   `json:"taskNumber"`
	ArchivedAt        *int64  `json:"archivedAt,omitempty"`
	LabelIDs          string  `json:"labelIds,omitempty"`
	SortWeight        int32   `json:"sortWeight"`
	PrimaryAssigneeID *string `json:"primaryAssigneeId"`
	AssigneeCount     int64   `json:"assigneeCount"`
	UpdatedAt         *int64  `json:"updatedAt,omitempty"`
	CreatedAt         int64   `json:"createdAt"`
}

// TaskConstraint is the public DTO for a task_constraints row.
type TaskConstraint struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Expression  string `json:"expression"`
	SatisfiedAt *int64 `json:"satisfiedAt,omitempty"`
	FailedAt    *int64 `json:"failedAt,omitempty"`
	SortWeight  int32  `json:"sortWeight"`
	UpdatedAt   *int64 `json:"updatedAt,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
}

// TaskDependency is the public DTO for a task_dependencies row.
type TaskDependency struct {
	ID                 string `json:"id"`
	Kind               string `json:"kind"`
	FromTaskID         string `json:"fromTaskId"`
	ToTaskID           string `json:"toTaskId"`
	ToTaskTitle        string `json:"toTaskTitle"`
	ToTaskDerivedState string `json:"toTaskDerivedState"`
	UpdatedAt          *int64 `json:"updatedAt,omitempty"`
	CreatedAt          int64  `json:"createdAt"`
}

// TaskActor is the public DTO for a task_actors row.
type TaskActor struct {
	ID          string `json:"id"`
	UserID      string `json:"userId"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	AvatarURL   string `json:"avatarUrl,omitempty"`
	Role        string `json:"role"`
	UpdatedAt   *int64 `json:"updatedAt,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
}

// TaskComment is the public DTO for a comments row attached to a task.
type TaskComment struct {
	ID                string `json:"id"`
	AuthorID          string `json:"authorId"`
	AuthorDisplayName string `json:"authorDisplayName"`
	AuthorAvatarURL   string `json:"authorAvatarUrl,omitempty"`
	Body              string `json:"body"`
	EditedAt          *int64 `json:"editedAt,omitempty"`
	UpdatedAt         *int64 `json:"updatedAt,omitempty"`
	CreatedAt         int64  `json:"createdAt"`
}

// TaskAttachment is the public DTO for an attachments row attached to a task.
type TaskAttachment struct {
	ID                  string `json:"id"`
	UploaderID          string `json:"uploaderId"`
	UploaderDisplayName string `json:"uploaderDisplayName"`
	Filename            string `json:"filename"`
	ContentType         string `json:"contentType"`
	ByteSize            uint64 `json:"byteSize"`
	StorageKey          string `json:"storageKey"`
	ChecksumSHA256      string `json:"checksumSha256,omitempty"`
	UpdatedAt           *int64 `json:"updatedAt,omitempty"`
	CreatedAt           int64  `json:"createdAt"`
}

// ---- Task CRUD I/O ---------------------------------------------------------

// CreateTaskActorInput is one entry in the optional `actors` array on
// CreateTaskBody. UserID is a user public id (UUID v7).
type CreateTaskActorInput struct {
	UserID string `json:"userId" doc:"User public id (UUID v7)"`
	Role   string `json:"role" enum:"assignee,reviewer,watcher,approver" default:"assignee"`
}

// CreateTaskBody is the JSON body for POST /tasks.
//
// If Actors is omitted or empty, the authenticated caller is attached as
// the sole assignee automatically — matching the "event you create
// appears on your calendar" mental model used by the calendar
// quick-create flow. Pass an explicit non-empty Actors array (including
// the caller if desired) to opt out of the auto-attach.
type CreateTaskBody struct {
	ProjectID   string                 `json:"projectId" doc:"Project public id (UUID v7)"`
	Title       string                 `json:"title" minLength:"1" maxLength:"500"`
	Description string                 `json:"description,omitempty" maxLength:"50000"`
	Priority    int32                  `json:"priority,omitempty" minimum:"0" maximum:"4"`
	DueOn       string                 `json:"dueOn,omitempty" doc:"YYYY-MM-DD"`
	StartOn     string                 `json:"startOn,omitempty" doc:"YYYY-MM-DD"`
	Visibility  string                 `json:"visibility,omitempty" enum:"public,project,private" default:"public" doc:"Task visibility: public (workspace), project (project members), or private (task actors only)"`
	Actors      []CreateTaskActorInput `json:"actors,omitempty" doc:"Optional explicit actor list. When omitted or empty, the caller is auto-attached as the sole assignee."`
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
//
// Pagination has two modes that coexist for the v1.0 deprecation
// runway: when `cursor` is non-empty the handler uses the keyset path
// (ListTasksForProjectKeyset / ListTasksForWorkspaceKeyset) and emits
// `nextCursor` in the response; otherwise the historical OFFSET path
// is used and `nextCursor` is empty. Mixing `cursor` with filters
// (`q`, `state`, `assignee`) keeps the OFFSET path because the keyset
// queries do not express those predicates.
type ListTasksInput struct {
	ProjectID   string   `query:"projectId" doc:"Optional project public id (UUID v7) to scope the list"`
	WorkspaceID string   `query:"workspaceId" doc:"Workspace public id (UUID v7); required when projectId is not given"`
	Q           string   `query:"q" doc:"Case-insensitive substring match on title"`
	State       []string `query:"state" doc:"Filter by derived_state; repeat to OR multiple values"`
	Assignee    string   `query:"assignee" doc:"Filter to tasks with this user as an assignee (user public id UUID v7)"`
	Cursor      string   `query:"cursor" doc:"Opaque cursor returned by previous page; pass to fetch next page. Empty when at end."`
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
	ID            string `json:"id"`
	WorkspaceID   string `json:"workspaceId"`
	WorkspaceName string `json:"workspaceName"`
	ProjectID     string `json:"projectId"`
	ProjectName   string `json:"projectName,omitempty"`
	Title         string `json:"title"`
	DerivedState  string `json:"derivedState"`
	Priority      int32  `json:"priority"`
	DueOn         string `json:"dueOn,omitempty"`
	ActorRole     string `json:"actorRole"`
	UpdatedAt     *int64 `json:"updatedAt,omitempty"`
	CreatedAt     int64  `json:"createdAt"`
}

// ListMyTasksInput is the query for GET /me/tasks.
//
// `cursor` opt-in: when non-empty the handler uses
// ListMyTasksGlobalKeyset and emits `nextCursor`; the OFFSET path
// remains the default for backward compatibility.
type ListMyTasksInput struct {
	Cursor string `query:"cursor" doc:"Opaque cursor returned by previous page; pass to fetch next page. Empty when at end."`
	// Cross-workspace dashboard fan-out: power users span many workspaces; cap raised to 1000.
	Limit  int32 `query:"limit" minimum:"1" maximum:"1000" default:"100"`
	Offset int32 `query:"offset" minimum:"0" default:"0"`
}

// ListMyTasksBody is the response payload for GET /me/tasks.
type ListMyTasksBody struct {
	Total      int64            `json:"total"`
	Tasks      []MyTaskListItem `json:"tasks"`
	NextCursor *string          `json:"nextCursor"`
}

// ListMyTasksOutput is the response for GET /me/tasks.
type ListMyTasksOutput struct {
	Body ListMyTasksBody
}

// ListMyTasksWithDatesInput is the query for GET /me/tasks-with-dates.
// Pairs with GET /me/calendar-events to power the unified
// cross-workspace calendar. `from` / `to` are inclusive dates on the
// server-side clock; the client should send the widest range it plans
// to render.
type ListMyTasksWithDatesInput struct {
	From   string `query:"from" required:"true" minLength:"1" pattern:"^20\\d{2}-(0[1-9]|1[0-2])-(0[1-9]|1\\d|2[0-8])$" doc:"Range start YYYY-MM-DD (inclusive)"`
	To     string `query:"to" required:"true" minLength:"1" pattern:"^20\\d{2}-(0[1-9]|1[0-2])-(0[1-9]|1\\d|2[0-8])$" doc:"Range end YYYY-MM-DD (inclusive)"`
	Cursor string `query:"cursor" doc:"Opaque cursor returned by previous page; pass to fetch next page. Empty when at end."`
	// Cross-workspace calendar grid: month-range fetch may exceed handlerutil.MaxListLimit; cap raised to 1000.
	Limit  int32 `query:"limit" minimum:"1" maximum:"1000" default:"100"`
	Offset int32 `query:"offset" minimum:"0" default:"0"`
}

// ListMyTasksWithDatesBody is the response payload for GET /me/tasks-with-dates.
type ListMyTasksWithDatesBody struct {
	Total      int64            `json:"total"`
	Tasks      []MyTaskListItem `json:"tasks"`
	NextCursor *string          `json:"nextCursor"`
}

// ListMyTasksWithDatesOutput is the response for GET /me/tasks-with-dates.
type ListMyTasksWithDatesOutput struct {
	Body ListMyTasksWithDatesBody
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
	Visibility  *string `json:"visibility,omitempty" enum:"public,project,private" doc:"Task visibility: public (workspace), project (project members), or private (task actors only)"`
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
	ID                    string `json:"id"`
	Kind                  string `json:"kind"`
	OtherTaskID           string `json:"otherTaskId"`
	OtherTaskTitle        string `json:"otherTaskTitle"`
	OtherTaskDerivedState string `json:"otherTaskDerivedState"`
	CreatedAt             int64  `json:"createdAt"`
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
//
// Default override: the actor count per task is bounded (typically
// well under 50), so a default of 100 lets the UI render the full
// roster in one round-trip. Cap stays at handlerutil.MaxListLimit.
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
// TaskActor so the frontend can render agents differently.
type TaskAgentActor struct {
	ID        string `json:"id"`
	AgentID   string `json:"agentId"`
	AgentName string `json:"agentName"`
	Role      string `json:"role"`
	UpdatedAt *int64 `json:"updatedAt,omitempty"`
	CreatedAt int64  `json:"createdAt"`
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
//
// Default override: same reasoning as [ListTaskActorsInput] — bounded
// per-task population, so default 100 avoids forced pagination for the
// common roster-view use case. Cap stays at handlerutil.MaxListLimit.
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
//
// `cursor` opt-in routes through ListCommentsForTaskKeyset. The keyset
// variant orders newest-first (DESC), which is the inverse of the
// OFFSET path's chronological order — UI consumers that want oldest-
// first must reverse client-side, or stay on the OFFSET path.
type ListTaskCommentsInput struct {
	ID     string `path:"id"`
	Cursor string `query:"cursor" doc:"Opaque cursor returned by previous page; pass to fetch next page. Empty when at end."`
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

// ---- Archive I/O ----------------------------------------------------------

// ArchiveTaskInput is the path for POST /tasks/{id}/archive.
type ArchiveTaskInput struct {
	ID string `path:"id"`
}

// ArchiveTaskOutput is the response for POST /tasks/{id}/archive.
type ArchiveTaskOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// UnarchiveTaskInput is the path for POST /tasks/{id}/unarchive.
type UnarchiveTaskInput struct {
	ID string `path:"id"`
}

// UnarchiveTaskOutput is the response for POST /tasks/{id}/unarchive.
type UnarchiveTaskOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// ListArchivedTasksInput is the query for GET /workspaces/{wsId}/tasks/archived.
//
// `cursor` opt-in routes the request through
// ListArchivedTasksForWorkspaceKeyset, which keys on
// (archived_at, public_id) — note that's `archived_at`, not
// `created_at`, since archived rows are sorted newest-archived-first.
type ListArchivedTasksInput struct {
	WsID   string `path:"wsId"`
	Cursor string `query:"cursor" doc:"Opaque cursor returned by previous page; pass to fetch next page. Empty when at end."`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListArchivedTasksBody is the response payload for GET /workspaces/{wsId}/tasks/archived.
type ListArchivedTasksBody struct {
	Total      int64          `json:"total"`
	Tasks      []TaskListItem `json:"tasks"`
	NextCursor *string        `json:"nextCursor"`
}

// ListArchivedTasksOutput is the response for GET /workspaces/{wsId}/tasks/archived.
type ListArchivedTasksOutput struct {
	Body ListArchivedTasksBody
}

// ---- Description Version History I/O --------------------------------------

// DescriptionVersion is the public DTO for a description version row
// without the full body (used in list responses).
type DescriptionVersion struct {
	ID                string `json:"id"`
	VersionNumber     int    `json:"versionNumber"`
	AuthorID          string `json:"authorId,omitempty"`
	AuthorDisplayName string `json:"authorDisplayName,omitempty"`
	BodyLength        int    `json:"bodyLength"`
	CreatedAt         int64  `json:"createdAt"`
}

// DescriptionVersionFull is the public DTO for a description version
// including the full body content.
type DescriptionVersionFull struct {
	DescriptionVersion
	Body string `json:"body"`
}

// ListDescriptionVersionsInput is the path for GET /tasks/{id}/description-history.
type ListDescriptionVersionsInput struct {
	ID string `path:"id"`
}

// ListDescriptionVersionsBody is the response payload for GET /tasks/{id}/description-history.
type ListDescriptionVersionsBody struct {
	Versions []DescriptionVersion `json:"versions"`
}

// ListDescriptionVersionsOutput is the response for GET /tasks/{id}/description-history.
type ListDescriptionVersionsOutput struct {
	Body ListDescriptionVersionsBody
}

// GetDescriptionVersionInput is the path for GET /tasks/{id}/description-history/{versionId}.
type GetDescriptionVersionInput struct {
	ID        string `path:"id"`
	VersionID string `path:"versionId"`
}

// GetDescriptionVersionOutput is the response for GET /tasks/{id}/description-history/{versionId}.
type GetDescriptionVersionOutput struct {
	Body DescriptionVersionFull
}

// RestoreDescriptionVersionInput is the path for POST /tasks/{id}/description-history/{versionId}/restore.
type RestoreDescriptionVersionInput struct {
	ID        string `path:"id"`
	VersionID string `path:"versionId"`
}

// RestoreDescriptionVersionOutput is the response for POST /tasks/{id}/description-history/{versionId}/restore.
type RestoreDescriptionVersionOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// ---- Task ↔ Event M:N links -----------------------------------------------

// TaskEventLink is the public DTO for a task_event_links row seen from
// either side. Callers fill LinkedEvent or LinkedTask according to the
// direction of the listing query.
type TaskEventLink struct {
	ID         string `json:"id"`
	Relation   string `json:"relation"`
	SortWeight int32  `json:"sortWeight"`
	CreatedAt  int64  `json:"createdAt"`

	// Event side — populated by GET /tasks/{id}/linked-events.
	EventID       string `json:"eventId,omitempty"`
	EventTitle    string `json:"eventTitle,omitempty"`
	EventStartAt  *int64 `json:"eventStartAt,omitempty"`
	EventEndAt    *int64 `json:"eventEndAt,omitempty"`
	EventAllDay   bool   `json:"eventAllDay,omitempty"`
	EventTimezone string `json:"eventTimezone,omitempty"`
	CalendarID    string `json:"calendarId,omitempty"`
	CalendarName  string `json:"calendarName,omitempty"`

	// Task side — populated by GET /calendar-events/{evtId}/linked-tasks.
	TaskID           string `json:"taskId,omitempty"`
	TaskTitle        string `json:"taskTitle,omitempty"`
	TaskDerivedState string `json:"taskDerivedState,omitempty"`
	TaskDueOn        string `json:"taskDueOn,omitempty"`
}

// CreateTaskEventLinkBody is the request body for POST /tasks/{id}/links.
type CreateTaskEventLinkBody struct {
	EventID    string `json:"eventId" doc:"Target calendar event public id"`
	Relation   string `json:"relation" enum:"contributes_to,blocks,depends_on,prep_for" doc:"How the task relates to the event"`
	SortWeight int32  `json:"sortWeight,omitempty" doc:"Display order hint"`
}

// CreateTaskEventLinkInput is the request for POST /tasks/{id}/links.
type CreateTaskEventLinkInput struct {
	ID   string `path:"id"`
	Body CreateTaskEventLinkBody
}

// CreateTaskEventLinkOutput is the response for POST /tasks/{id}/links.
type CreateTaskEventLinkOutput struct {
	Body TaskEventLink
}

// DeleteTaskEventLinkInput is the path for DELETE /tasks/{id}/links/{linkId}.
type DeleteTaskEventLinkInput struct {
	ID     string `path:"id"`
	LinkID string `path:"linkId"`
}

// DeleteTaskEventLinkOutput is the response for DELETE /tasks/{id}/links/{linkId}.
type DeleteTaskEventLinkOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// ListLinkedEventsInput is the path for GET /tasks/{id}/linked-events.
type ListLinkedEventsInput struct {
	ID       string `path:"id"`
	Relation string `query:"relation" enum:"contributes_to,blocks,depends_on,prep_for" doc:"Optional filter; empty = all relations"`
	// Umbrella link graph: contributing-event fan-in is unbounded per task; cap raised to 1000.
	Limit  int32 `query:"limit" default:"100" minimum:"1" maximum:"1000"`
	Offset int32 `query:"offset" default:"0" minimum:"0"`
}

// ListLinkedEventsOutput is the response for GET /tasks/{id}/linked-events.
type ListLinkedEventsOutput struct {
	Body struct {
		Links []TaskEventLink `json:"links"`
		Total int64           `json:"total"`
	}
}

// ListLinkedTasksInput is the path for GET /calendar-events/{evtId}/linked-tasks.
type ListLinkedTasksInput struct {
	WsID     string `path:"wsId"`
	EventID  string `path:"evtId"`
	Relation string `query:"relation" enum:"contributes_to,blocks,depends_on,prep_for"`
	// Reverse umbrella graph: mirror of ListLinkedEventsInput; cap raised to 1000.
	Limit  int32 `query:"limit" default:"100" minimum:"1" maximum:"1000"`
	Offset int32 `query:"offset" default:"0" minimum:"0"`
}

// ListLinkedTasksOutput is the response for GET /calendar-events/{evtId}/linked-tasks.
type ListLinkedTasksOutput struct {
	Body struct {
		Links []TaskEventLink `json:"links"`
		Total int64           `json:"total"`
	}
}

// ---- Shift proposal / apply -----------------------------------------------

// OtherEventLinkDTO describes a contributes_to event the candidate
// task is ALSO linked to, beyond the umbrella being shifted. Surfaced
// so the UI can warn the user about cross-event impact when the task
// is bulk-shifted.
type OtherEventLinkDTO struct {
	EventID      string `json:"eventId"`
	EventTitle   string `json:"eventTitle"`
	EventStartAt *int64 `json:"eventStartAt,omitempty"`
}

// ShiftCandidateDTO is one task in a shift proposal. SafeTasks carry
// empty OtherLinks; ConflictTasks carry at least one.
type ShiftCandidateDTO struct {
	TaskID     string              `json:"taskId"`
	TaskTitle  string              `json:"taskTitle"`
	LinkID     string              `json:"linkId"`
	OtherLinks []OtherEventLinkDTO `json:"otherLinks,omitempty"`
}

// ProposeShiftBody is the JSON body for POST
// /workspaces/{wsId}/calendar-events/{evtId}/propose-shift.
type ProposeShiftBody struct {
	NewStartAt int64 `json:"newStartAt" required:"true" doc:"Target start time for the umbrella event (unix seconds)"`
}

// ProposeShiftInput is the request for POST propose-shift.
type ProposeShiftInput struct {
	WsID    string `path:"wsId"`
	EventID string `path:"evtId"`
	Body    ProposeShiftBody
}

// ProposeShiftOutput is the response for POST propose-shift.
type ProposeShiftOutput struct {
	Body struct {
		EventID       string              `json:"eventId"`
		OldStartAt    int64               `json:"oldStartAt"`
		NewStartAt    int64               `json:"newStartAt"`
		DeltaSeconds  int64               `json:"deltaSeconds"`
		SafeTasks     []ShiftCandidateDTO `json:"safeTasks"`
		ConflictTasks []ShiftCandidateDTO `json:"conflictTasks"`
	}
}

// ApplyShiftBody is the JSON body for POST
// /workspaces/{wsId}/calendar-events/{evtId}/apply-shift.
type ApplyShiftBody struct {
	NewStartAt       int64    `json:"newStartAt" required:"true" doc:"Target start time for the umbrella event (unix seconds)"`
	ConfirmedTaskIDs []string `json:"confirmedTaskIds" doc:"Public IDs of tasks the user agreed to shift along with the umbrella event"`
}

// ApplyShiftInput is the request for POST apply-shift.
type ApplyShiftInput struct {
	WsID    string `path:"wsId"`
	EventID string `path:"evtId"`
	Body    ApplyShiftBody
}

// ApplyShiftOutput is the response for POST apply-shift.
type ApplyShiftOutput struct {
	Body struct {
		Ok           bool  `json:"ok"`
		ShiftedTasks int32 `json:"shiftedTasks"`
		DeltaSeconds int64 `json:"deltaSeconds"`
		NewStartAt   int64 `json:"newStartAt"`
	}
}
