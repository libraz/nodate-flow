package tasks

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterCollection wires the unscoped task collection routes
// (POST /tasks and GET /tasks). The caller must attach RequireAuth
// to the underlying chi router; the handlers perform their own
// project / workspace membership checks because there is no path
// parameter for ACL middleware to bind onto.
func RegisterCollection(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "tasks-create",
		Method:      http.MethodPost,
		Path:        "/tasks",
		Summary:     "Create a task",
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-list",
		Method:      http.MethodGet,
		Path:        "/tasks",
		Summary:     "List tasks for a project or workspace",
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-reorder",
		Method:      http.MethodPost,
		Path:        "/tasks/reorder",
		Summary:     "Bulk-update sort weights for tasks within a project",
	}, Reorder(deps))

	huma.Register(api, huma.Operation{
		OperationID: "me-tasks-list",
		Method:      http.MethodGet,
		Path:        "/me/tasks",
		Summary:     "List tasks assigned to the authenticated user across every workspace",
	}, ListMyTasks(deps))
}

// RegisterTaskScoped wires the per-task routes that operate on a single
// {id}. The caller must attach RequireTaskAccess to the underlying chi
// router so the task / project / workspace contexts are populated.
func RegisterTaskScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "tasks-get",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}",
		Summary:     "Fetch a task",
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-patch",
		Method:      http.MethodPatch,
		Path:        "/tasks/{id}",
		Summary:     "Patch a task",
	}, Patch(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-disable",
		Method:      http.MethodDelete,
		Path:        "/tasks/{id}",
		Summary:     "Soft-disable a task",
	}, Disable(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-duplicates-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/duplicates",
		Summary:     "List likely-duplicate tasks by embedding similarity",
	}, ListDuplicates(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-infer-state",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/infer-state",
		Summary:     "Propose the next likely state transition for a task",
	}, InferState(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-ai-invocations-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/ai/invocations",
		Summary:     "List recent AI invocations scoped to this task",
	}, ListAiInvocations(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-transitions-apply",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/transitions",
		Summary:     "Apply a state machine transition to a task",
	}, Transition(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-constraints-add",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/constraints",
		Summary:     "Attach a constraint to a task",
	}, AddConstraint(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-replay",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/replay",
		Summary:     "Replay transition events and report drift vs stored derived_state (3.ENG-1)",
	}, Replay(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-constraints-evaluate",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/constraints/evaluate",
		Summary:     "Run the Phase 3 constraint engine for a task and persist satisfied/failed markers",
	}, EvaluateConstraints(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-constraints-compile",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/constraints/compile",
		Summary:     "Compile a natural-language prompt into a constraint DSL expression",
	}, CompileConstraint(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-constraints-explain",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/constraints/explain",
		Summary:     "Explain a constraint DSL expression in human-readable form",
	}, ExplainConstraint(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-constraints-remove",
		Method:      http.MethodDelete,
		Path:        "/tasks/{id}/constraints/{cid}",
		Summary:     "Remove a constraint from a task",
	}, RemoveConstraint(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-dependencies-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/dependencies",
		Summary:     "List incoming and outgoing dependency edges for a task",
	}, ListDependencies(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-dependencies-add",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/dependencies",
		Summary:     "Add a dependency edge from a task",
	}, AddDependency(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-dependencies-remove",
		Method:      http.MethodDelete,
		Path:        "/tasks/{id}/dependencies/{depId}",
		Summary:     "Remove a dependency edge",
	}, RemoveDependency(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-actors-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/actors",
		Summary:     "List actors on a task",
	}, ListActors(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-actors-add",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/actors",
		Summary:     "Attach an actor to a task",
	}, AddActor(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-actors-remove",
		Method:      http.MethodDelete,
		Path:        "/tasks/{id}/actors/{actorId}",
		Summary:     "Remove an actor from a task",
	}, RemoveActor(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-agents-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/agents",
		Summary:     "List AI agent actors on a task",
	}, ListAgentActors(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-agents-add",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/agents",
		Summary:     "Attach an AI agent to a task",
	}, AddAgentActor(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-comments-add",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/comments",
		Summary:     "Add a comment to a task",
	}, AddComment(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-comments-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/comments",
		Summary:     "List comments on a task",
	}, ListComments(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-comments-edit",
		Method:      http.MethodPatch,
		Path:        "/tasks/{id}/comments/{cid}",
		Summary:     "Edit a comment (author only)",
	}, EditComment(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-comments-delete",
		Method:      http.MethodDelete,
		Path:        "/tasks/{id}/comments/{cid}",
		Summary:     "Delete a comment (author or workspace admin)",
	}, DeleteComment(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-attachments-add",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/attachments",
		Summary:     "Register an attachment metadata row on a task",
	}, AddAttachment(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-attachments-presign",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/attachments/presign",
		Summary:     "Get a presigned PUT URL for uploading an attachment",
	}, PresignUpload(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-attachments-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/attachments",
		Summary:     "List attachments on a task",
	}, ListAttachments(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-attachments-download",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/attachments/{aid}/download",
		Summary:     "Get a presigned GET URL for downloading an attachment",
	}, DownloadAttachment(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-attachments-delete",
		Method:      http.MethodDelete,
		Path:        "/tasks/{id}/attachments/{aid}",
		Summary:     "Soft-delete an attachment from a task",
	}, DeleteAttachment(deps))
}
