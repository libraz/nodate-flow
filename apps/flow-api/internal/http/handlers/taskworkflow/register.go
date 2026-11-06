package taskworkflow

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterTaskScopedReads wires the bounded persisted workflow snapshot. The
// caller must attach RequireTaskAccess; it is the only gate so current task
// readers can see the selected reviewer, decisions and pending handoffs.
func RegisterTaskScopedReads(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "tasks-workflow-get",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/workflow",
		Summary:     "Get review and human handoff state for a task",
		Description: "Returns persisted completion policy, current-revision submission and decision, pending/recent human handoffs, and capabilities derived from the current task roles.",
		Tags:        []string{"Tasks"},
	}, GetWorkflow(deps))
}

// RegisterTaskScopedEditorWrites wires workflow mutations that require the
// caller to pass RequireTaskAccess and the project editor floor. Review
// decision routes have their own scoped authority and must use
// RegisterTaskScopedDecisions instead of this group.
func RegisterTaskScopedEditorWrites(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "tasks-completion-policy-set",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/completion-policy",
		Summary:     "Set the completion policy for a task",
		Description: "Enables manual or review-required completion. Only explicitly selected required constraints gate completion; other task constraints remain advisory.",
		Tags:        []string{"Tasks"},
	}, SetCompletionPolicy(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-submissions-create",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/submissions",
		Summary:     "Submit the current task work for review",
		Description: "Creates an immutable snapshot of acceptance criteria, evidence links and attachment digests for the current task work revision. The selected reviewer must already hold a human reviewer or approver role and be able to read the task.",
		Tags:        []string{"Tasks"},
	}, CreateSubmission(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-human-handoffs-create",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/handoffs",
		Summary:     "Request a human task-role handoff",
		Description: "Creates one pending handoff per role. The recipient must already be able to read the task; responsibility remains with the current holder until the recipient accepts.",
		Tags:        []string{"Tasks"},
	}, CreateHandoff(deps))
}

// RegisterTaskScopedDecisions wires decision routes that require only
// RequireTaskAccess. The handlers enforce current reviewer / approver role,
// pending recipient or requester identity directly, allowing an assigned
// project viewer to act without granting a general project-editor floor.
func RegisterTaskScopedDecisions(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "tasks-submissions-decide",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/submissions/{submissionId}/decision",
		Summary:     "Approve or reject a task submission",
		Description: "Only the selected reviewer, who must still hold the task reviewer or approver role, may decide a submission. Decisions are immutable.",
		Tags:        []string{"Tasks"},
	}, DecideSubmission(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-human-handoffs-decide",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/handoffs/{handoffId}/decision",
		Summary:     "Accept, decline or cancel a human task-role handoff",
		Description: "Only the pending recipient may accept or decline; the requester, the source holder, a project lead or a workspace admin may cancel. Current task visibility is rechecked. Accepting at or after the due time expires the handoff instead; accepting atomically transfers the role and appends the actor and handoff events.",
		Tags:        []string{"Tasks"},
	}, DecideHandoff(deps))
}
