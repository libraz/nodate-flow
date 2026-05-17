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
		Description: "Creates a task in the project supplied in the body. Validates project membership server-side and emits a task.created event so timelines, AI pipelines, and webhooks see it.",
		Tags:        []string{"Tasks"},
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-list",
		Method:      http.MethodGet,
		Path:        "/tasks",
		Summary:     "List tasks for a project or workspace",
		Description: "Returns a cursor-paginated page of tasks filtered by project or workspace plus optional status / assignee / label filters. Backs every task list view (board, list, etc.).\n\nLayer 4 task visibility (public / project / private) is enforced as a SQL filter rather than a per-row 403, so tasks the actor cannot see are silently excluded from both the rows and the `total` count. This is asymmetric with the single-task endpoint (which returns 404 on visibility denial); see `docs/conventions/acl.md` for the rationale.",
		Tags:        []string{"Tasks"},
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-reorder",
		Method:      http.MethodPost,
		Path:        "/tasks/reorder",
		Summary:     "Bulk-update sort weights for tasks within a project",
		Description: "Updates many tasks' sort_order in one request after a drag-and-drop. Atomic per project so no other client sees a partially reordered list.",
		Tags:        []string{"Tasks"},
	}, Reorder(deps))

	huma.Register(api, huma.Operation{
		OperationID: "me-tasks-list",
		Method:      http.MethodGet,
		Path:        "/me/tasks",
		Summary:     "List tasks assigned to the authenticated user across every workspace",
		Description: "Returns the caller's open assigned tasks across every workspace they belong to so the global My Tasks view can render without per-workspace round trips.",
		Tags:        []string{"Tasks"},
	}, ListMyTasks(deps))

	huma.Register(api, huma.Operation{
		OperationID: "me-tasks-with-dates-list",
		Method:      http.MethodGet,
		Path:        "/me/tasks-with-dates",
		Summary:     "List tasks with due_on in range across every workspace",
		Description: "Returns the caller's tasks whose due_on falls inside the supplied date range. Used by calendar overlays and weekly digest generation.",
		Tags:        []string{"Tasks"},
	}, ListMyTasksWithDates(deps))
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
		Description: "Returns a single task with its core fields, derived state, labels, and assignee. Sub-resources (comments, attachments, dependencies) live on dedicated endpoints.",
		Tags:        []string{"Tasks"},
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-patch",
		Method:      http.MethodPatch,
		Path:        "/tasks/{id}",
		Summary:     "Patch a task",
		Description: "Updates editable task fields (title, description, due_on, priority, assignee). Description edits append a description-version row so history is queryable.",
		Tags:        []string{"Tasks"},
	}, Patch(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-disable",
		Method:      http.MethodDelete,
		Path:        "/tasks/{id}",
		Summary:     "Soft-disable a task",
		Description: "Marks the task as removed so it disappears from active views without erasing data. Idempotent. Use /unarchive on archived tasks; this endpoint is the destructive variant.",
		Tags:        []string{"Tasks"},
	}, Disable(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-duplicates-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/duplicates",
		Summary:     "List likely-duplicate tasks by embedding similarity",
		Description: "Returns tasks similar to this one ranked by cosine distance over the description embedding. Used by the duplicate-warning chip on the task editor.",
		Tags:        []string{"Tasks"},
	}, ListDuplicates(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-infer-state",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/infer-state",
		Summary:     "Propose the next likely state transition for a task",
		Description: "Runs the deterministic state-inference engine and returns the proposed next state plus the matching rule and rationale. Read-only; apply with /transitions.",
		Tags:        []string{"Tasks"},
	}, InferState(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-ai-invocations-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/ai/invocations",
		Summary:     "List recent AI invocations scoped to this task",
		Description: "Returns redacted ai_invocations rows scoped to the task so the AI reasoning panel can show which prompts the LLM saw and what it decided.",
		Tags:        []string{"Tasks"},
	}, ListAiInvocations(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-transitions-apply",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/transitions",
		Summary:     "Apply a state machine transition to a task",
		Description: "Applies the named transition to the task. Validates against the state machine and emits a task.transitioned event. Refuses transitions that violate attached constraints.",
		Tags:        []string{"Tasks"},
	}, Transition(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-constraints-add",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/constraints",
		Summary:     "Attach a constraint to a task",
		Description: "Persists a constraint DSL expression on the task. Subsequent transitions and the constraint engine evaluate against this rule.",
		Tags:        []string{"Tasks"},
	}, AddConstraint(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-replay",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/replay",
		Summary:     "Replay transition events and report drift vs stored derived_state",
		Description: "Replays every state-changing event in the task timeline and reports whether the recomputed derived_state matches the row. Diagnostic; does not mutate state.",
		Tags:        []string{"Tasks"},
	}, Replay(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-constraints-evaluate",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/constraints/evaluate",
		Summary:     "Run the constraint engine for a task and persist satisfied/failed markers",
		Description: "Evaluates every constraint attached to the task and persists the satisfied / failed markers so the UI can render the result without re-running rules client-side.",
		Tags:        []string{"Tasks"},
	}, EvaluateConstraints(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-constraints-compile",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/constraints/compile",
		Summary:     "Compile a natural-language prompt into a constraint DSL expression",
		Description: "Asks the AI compiler to translate a natural-language constraint description into a validated DSL expression. Returns the expression plus the compiler's confidence so the UI can confirm before /add.",
		Tags:        []string{"Tasks"},
	}, CompileConstraint(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-constraints-explain",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/constraints/explain",
		Summary:     "Explain a constraint DSL expression in human-readable form",
		Description: "Renders a stored constraint DSL back into prose so non-technical users can review what was attached. Used by the constraint detail panel.",
		Tags:        []string{"Tasks"},
	}, ExplainConstraint(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-constraints-remove",
		Method:      http.MethodDelete,
		Path:        "/tasks/{id}/constraints/{cid}",
		Summary:     "Remove a constraint from a task",
		Description: "Detaches the named constraint from the task. Subsequent evaluations no longer consider it. Idempotent.",
		Tags:        []string{"Tasks"},
	}, RemoveConstraint(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-dependencies-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/dependencies",
		Summary:     "List incoming and outgoing dependency edges for a task",
		Description: "Returns both blocks and blocked-by edges connected to the task. Used by the dependency panel and by the project graph view's per-task drilldown.",
		Tags:        []string{"Tasks"},
	}, ListDependencies(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-dependencies-add",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/dependencies",
		Summary:     "Add a dependency edge from a task",
		Description: "Creates a blocks / blocked-by edge from this task to another. Refuses cycles so the graph stays a DAG.",
		Tags:        []string{"Tasks"},
	}, AddDependency(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-dependencies-remove",
		Method:      http.MethodDelete,
		Path:        "/tasks/{id}/dependencies/{depId}",
		Summary:     "Remove a dependency edge",
		Description: "Removes the named dependency edge. Idempotent.",
		Tags:        []string{"Tasks"},
	}, RemoveDependency(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-linked-events-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/linked-events",
		Summary:     "List calendar events a task is linked to via task_event_links",
		Description: "Returns the calendar events linked to this task with the link kind (contributes_to / blocks / etc.). Used by the task detail's calendar section.",
		Tags:        []string{"Tasks"},
	}, ListLinkedEvents(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-event-links-create",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/links",
		Summary:     "Link a task to a calendar event (contributes_to / blocks / ...)",
		Description: "Creates a task_event_link row with the supplied kind so calendar shifts and task changes can propagate per the link semantics.",
		Tags:        []string{"Tasks"},
	}, CreateTaskEventLink(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-event-links-remove",
		Method:      http.MethodDelete,
		Path:        "/tasks/{id}/links/{linkId}",
		Summary:     "Soft-disable a task↔event link",
		Description: "Marks the named task↔event link as removed so propagation rules stop firing. Audit history is preserved.",
		Tags:        []string{"Tasks"},
	}, DeleteTaskEventLink(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-actors-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/actors",
		Summary:     "List actors on a task",
		Description: "Returns the human actors (assignees, collaborators, watchers) attached to the task with their role.",
		Tags:        []string{"Tasks"},
	}, ListActors(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-actors-add",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/actors",
		Summary:     "Attach an actor to a task",
		Description: "Adds a human actor to the task at the requested role (assignee / collaborator / watcher). Triggers the assignment notification pipeline when the role is assignee.",
		Tags:        []string{"Tasks"},
	}, AddActor(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-actors-remove",
		Method:      http.MethodDelete,
		Path:        "/tasks/{id}/actors/{actorId}",
		Summary:     "Remove an actor from a task",
		Description: "Detaches the named actor from the task. Idempotent.",
		Tags:        []string{"Tasks"},
	}, RemoveActor(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-agents-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/agents",
		Summary:     "List AI agent actors on a task",
		Description: "Returns the AI agent actors currently attached to the task. Distinguished from human actors so UI can render differently and quotas can apply per-agent.",
		Tags:        []string{"Tasks"},
	}, ListAgentActors(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-agents-add",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/agents",
		Summary:     "Attach an AI agent to a task",
		Description: "Attaches an AI agent so the agent's runtime can be scheduled on or triggered for the task. Idempotent per (task, agent).",
		Tags:        []string{"Tasks"},
	}, AddAgentActor(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-handoff-to-agent",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/handoff/to-agent",
		Summary:     "Promote an AI agent to the task's current assignee",
		Description: "Disables any current agent assignee on the task and attaches the supplied agent in its place. Emits an agent.task.handoff_to_agent event so the orchestrator can pick up the new assignment.",
		Tags:        []string{"Tasks"},
	}, HandoffToAgent(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-handoff-to-user",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/handoff/to-user",
		Summary:     "Hand a task back to a human user",
		Description: "Disables the current agent assignee, optionally upserts a target user as the new assignee, and stamps tasks.agent_memo with the handoff reason. Emits agent.task.handoff_to_user tagged with the prior agent as actor.",
		Tags:        []string{"Tasks"},
	}, HandoffToUser(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-agent-runs-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/agent-runs",
		Summary:     "List recent agent run events scoped to a task",
		Description: "Returns the agent run lifecycle events (started / completed / failed) the orchestrator has appended for this task. Most recent first. Empty until the orchestrator stamps task_id and actor_agent_id on its ai.agent.run.* events.",
		Tags:        []string{"Tasks"},
	}, ListAgentRuns(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-comments-add",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/comments",
		Summary:     "Add a comment to a task",
		Description: "Appends a comment from the caller. Mentions inside the body are parsed and routed through the notifications pipeline.",
		Tags:        []string{"Tasks"},
	}, AddComment(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-comments-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/comments",
		Summary:     "List comments on a task",
		Description: "Returns the comments on the task in chronological order with reaction counts. Cursor-paginated for long threads.",
		Tags:        []string{"Tasks"},
	}, ListComments(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-comments-edit",
		Method:      http.MethodPatch,
		Path:        "/tasks/{id}/comments/{cid}",
		Summary:     "Edit a comment (author only)",
		Description: "Replaces the body of the named comment. Only the original author may edit; an edited_at timestamp is set.",
		Tags:        []string{"Tasks"},
	}, EditComment(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-comments-delete",
		Method:      http.MethodDelete,
		Path:        "/tasks/{id}/comments/{cid}",
		Summary:     "Delete a comment (author or workspace admin)",
		Description: "Removes the named comment. Permitted to the comment author or any workspace admin. Idempotent.",
		Tags:        []string{"Tasks"},
	}, DeleteComment(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-attachments-presign",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/attachments/presign",
		Summary:     "Reserve an attachment row and (if needed) get a presigned PUT URL",
		Description: "Single entry point for adding an attachment to a task. The client supplies the file's SHA-256; the server runs content-addressed dedup and either bumps the ref count on an existing storage_objects row (deduplicated=true, no upload) or returns a presigned PUT URL the client streams the bytes to. The attachment row is always created in the same transaction.",
		Tags:        []string{"Tasks"},
	}, PresignUpload(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-attachments-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/attachments",
		Summary:     "List attachments on a task",
		Description: "Returns the attachments registered on the task with metadata only — bytes are fetched separately via /download.",
		Tags:        []string{"Tasks"},
	}, ListAttachments(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-attachments-download",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/attachments/{aid}/download",
		Summary:     "Get a presigned GET URL for downloading an attachment",
		Description: "Returns a short-lived presigned GET URL so the client can stream the file straight from object storage. The API never proxies the bytes.",
		Tags:        []string{"Tasks"},
	}, DownloadAttachment(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-attachments-delete",
		Method:      http.MethodDelete,
		Path:        "/tasks/{id}/attachments/{aid}",
		Summary:     "Soft-delete an attachment from a task",
		Description: "Marks the attachment as removed and best-effort deletes the underlying object. Idempotent.",
		Tags:        []string{"Tasks"},
	}, DeleteAttachment(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-archive",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/archive",
		Summary:     "Archive a task",
		Description: "Moves the task into the archive so it disappears from active views without removing it. Idempotent.",
		Tags:        []string{"Tasks"},
	}, Archive(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-unarchive",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/unarchive",
		Summary:     "Unarchive a task",
		Description: "Restores an archived task to active state. Idempotent.",
		Tags:        []string{"Tasks"},
	}, Unarchive(deps))

	// Description version history.
	huma.Register(api, huma.Operation{
		OperationID: "tasks-description-history-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/description-history",
		Summary:     "List description version history for a task",
		Description: "Returns a chronological list of description revisions (id, author, edited_at) without the full body so the version chooser is cheap.",
		Tags:        []string{"Tasks"},
	}, ListDescriptionVersions(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-description-history-get",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/description-history/{versionId}",
		Summary:     "Get a specific description version with full body",
		Description: "Returns the full body of one description revision so the diff viewer can render the historical content.",
		Tags:        []string{"Tasks"},
	}, GetDescriptionVersion(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-description-history-restore",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/description-history/{versionId}/restore",
		Summary:     "Restore a previous description version",
		Description: "Replaces the task's current description with the named historical version, appending a new revision so the restore itself is recorded.",
		Tags:        []string{"Tasks"},
	}, RestoreDescriptionVersion(deps))
}

// RegisterWorkspaceScoped wires workspace-level task routes that don't
// require a specific task context.
func RegisterWorkspaceScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "tasks-archived-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/tasks/archived",
		Summary:     "List archived tasks in a workspace",
		Description: "Returns archived tasks in the workspace with cursor pagination. Used by the archive panel and bulk-restore tooling.",
		Tags:        []string{"Tasks"},
	}, ListArchived(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-drafts-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/tasks/drafts",
		Summary:     "List draft tasks in a workspace by reason (currently only retro)",
		Description: "Returns draft tasks of the requested reason for review. Currently the only supported reason is 'retro' — retrospective drafts created by the signal_judge Applier when an event-day signal triggers action=generate_retro. Each row carries the source task back-reference plus optional agent attribution sourced from the task.retro.drafted event. The retro draft queue UI (Phase 6 / L2) renders Accept / Discard against this feed.",
		Tags:        []string{"Tasks"},
	}, ListRetroDrafts(deps))

	huma.Register(api, huma.Operation{
		OperationID: "calendar-events-linked-tasks-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/calendar-events/{evtId}/linked-tasks",
		Summary:     "List tasks linked to a calendar event via task_event_links",
		Description: "Returns the tasks linked to the named calendar event with the link kind. Inverse of /tasks/{id}/linked-events.",
		Tags:        []string{"Tasks"},
	}, ListLinkedTasks(deps))

	huma.Register(api, huma.Operation{
		OperationID: "calendar-events-shift-propose",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/calendar-events/{evtId}/propose-shift",
		Summary:     "Propose shifting an umbrella event and partition linked tasks into safe vs conflict",
		Description: "Dry-runs an umbrella event shift and returns which contributes_to-linked tasks would move cleanly versus which would conflict with constraints. Read-only; apply with /apply-shift.",
		Tags:        []string{"Tasks"},
	}, ProposeShift(deps))

	huma.Register(api, huma.Operation{
		OperationID: "calendar-events-shift-apply",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/calendar-events/{evtId}/apply-shift",
		Summary:     "Shift an umbrella event and move confirmed contributes_to-linked tasks by the same day delta",
		Description: "Applies the previously proposed shift: moves the umbrella event and the confirmed task subset by the same day delta. Conflicting tasks are left in place; their conflict reasons are returned.",
		Tags:        []string{"Tasks"},
	}, ApplyShift(deps))
}
