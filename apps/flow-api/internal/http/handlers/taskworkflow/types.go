// Package taskworkflow contains review-gated task completion and accepted
// human responsibility handoffs.
package taskworkflow

import (
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
)

// Deps is the dependency bundle for task workflow operations.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	Events  *mutationlog.Recorder
}

// CompletionPolicyBody is the JSON body for POST
// /tasks/{id}/completion-policy.
type CompletionPolicyBody struct {
	Policy                string   `json:"policy" enum:"manual,review_required"`
	RequiredConstraintIDs []string `json:"requiredConstraintIds,omitempty" maxItems:"50"`
}

// CompletionPolicyInput is the request for the policy endpoint.
type CompletionPolicyInput struct {
	ID   string `path:"id" format:"uuid"`
	Body CompletionPolicyBody
}

// CompletionPolicy is the task's persisted completion policy.
type CompletionPolicy struct {
	Policy                string   `json:"policy"`
	WorkRevision          uint32   `json:"workRevision"`
	RequiredConstraintIDs []string `json:"requiredConstraintIds"`
}

// CompletionPolicyOutput is the response from the policy endpoint.
type CompletionPolicyOutput struct {
	Body CompletionPolicy
}

// GetWorkflowInput is the request for GET /tasks/{id}/workflow.
type GetWorkflowInput struct {
	ID string `path:"id" format:"uuid"`
}

// WorkflowDecision is the current immutable decision on a submission.
type WorkflowDecision struct {
	Decision        string `json:"decision"`
	Reason          string `json:"reason,omitempty"`
	DecidedByUserID string `json:"decidedByUserId,omitempty" format:"uuid"`
	DecidedAt       *int64 `json:"decidedAt,omitempty"`
}

// WorkflowSubmission is one immutable review snapshot.
type WorkflowSubmission struct {
	ID                 string                 `json:"id" format:"uuid"`
	Revision           uint32                 `json:"revision"`
	CreatedAt          int64                  `json:"createdAt"`
	SubmitterUserID    string                 `json:"submitterUserId,omitempty" format:"uuid"`
	ReviewerUserID     string                 `json:"reviewerUserId,omitempty" format:"uuid"`
	AcceptanceCriteria []string               `json:"acceptanceCriteria"`
	EvidenceURLs       []string               `json:"evidenceUrls"`
	Attachments        []SubmissionAttachment `json:"attachments"`
	Decision           *WorkflowDecision      `json:"decision,omitempty"`
}

// WorkflowHandoff is a recent or pending human handoff.
type WorkflowHandoff struct {
	ID         string `json:"id" format:"uuid"`
	Role       string `json:"role"`
	FromUserID string `json:"fromUserId,omitempty" format:"uuid"`
	ToUserID   string `json:"toUserId,omitempty" format:"uuid"`
	DueAt      *int64 `json:"dueAt,omitempty"`
	Status     string `json:"status"`
	CreatedAt  int64  `json:"createdAt"`
	ResolvedAt *int64 `json:"resolvedAt,omitempty"`
	CanDecide  bool   `json:"canDecide"`
	CanCancel  bool   `json:"canCancel"`
}

// WorkflowCapabilities summarizes which workflow actions the current task
// reader may perform. Handlers still recheck every capability on write.
type WorkflowCapabilities struct {
	CanSetPolicy               bool     `json:"canSetPolicy"`
	CanSubmit                  bool     `json:"canSubmit"`
	CanDecideCurrentSubmission bool     `json:"canDecideCurrentSubmission"`
	CanRequestHandoffRoles     []string `json:"canRequestHandoffRoles"`
}

// WorkflowSnapshot is persisted workflow state plus current-reader actions.
type WorkflowSnapshot struct {
	CompletionPolicy      string               `json:"completionPolicy"`
	WorkRevision          uint32               `json:"workRevision"`
	RequiredConstraintIDs []string             `json:"requiredConstraintIds"`
	CurrentSubmission     *WorkflowSubmission  `json:"currentSubmission,omitempty"`
	RecentSubmissions     []WorkflowSubmission `json:"recentSubmissions"`
	Handoffs              []WorkflowHandoff    `json:"handoffs"`
	Capabilities          WorkflowCapabilities `json:"capabilities"`
}

// GetWorkflowOutput is the response for the workflow read endpoint.
type GetWorkflowOutput struct {
	Body WorkflowSnapshot
}

// CreateSubmissionBody is the JSON body for POST /tasks/{id}/submissions.
type CreateSubmissionBody struct {
	ReviewerUserID     string   `json:"reviewerUserId" format:"uuid"`
	AcceptanceCriteria []string `json:"acceptanceCriteria" minItems:"1" maxItems:"30"`
	EvidenceURLs       []string `json:"evidenceUrls,omitempty" maxItems:"20"`
	AttachmentIDs      []string `json:"attachmentIds,omitempty" maxItems:"20"`
}

// CreateSubmissionInput is the request for creating a submission.
type CreateSubmissionInput struct {
	ID   string `path:"id" format:"uuid"`
	Body CreateSubmissionBody
}

// SubmissionBody is an immutable submission snapshot returned by create.
type SubmissionBody struct {
	ID                 string                 `json:"id" format:"uuid"`
	TaskID             string                 `json:"taskId" format:"uuid"`
	Revision           uint32                 `json:"revision"`
	SubmitterUserID    string                 `json:"submitterUserId" format:"uuid"`
	ReviewerUserID     string                 `json:"reviewerUserId" format:"uuid"`
	AcceptanceCriteria []string               `json:"acceptanceCriteria"`
	EvidenceURLs       []string               `json:"evidenceUrls"`
	Attachments        []SubmissionAttachment `json:"attachments"`
	CreatedAt          int64                  `json:"createdAt"`
}

// SubmissionAttachment is an immutable attachment digest captured when the
// submission was created.
type SubmissionAttachment struct {
	ID     string `json:"id" format:"uuid"`
	SHA256 string `json:"sha256"`
}

// CreateSubmissionOutput is the response to creating a submission.
type CreateSubmissionOutput struct {
	Body SubmissionBody
}

// ReviewDecisionBody is the JSON body for a submission decision.
type ReviewDecisionBody struct {
	Decision string `json:"decision" enum:"approve,reject"`
	Reason   string `json:"reason,omitempty" maxLength:"2000"`
}

// ReviewDecisionInput is the request for POST
// /tasks/{id}/submissions/{submissionId}/decision.
type ReviewDecisionInput struct {
	ID           string `path:"id" format:"uuid"`
	SubmissionID string `path:"submissionId" format:"uuid"`
	Body         ReviewDecisionBody
}

// DecisionBody is a persisted review decision.
type DecisionBody struct {
	ID           string `json:"id" format:"uuid"`
	SubmissionID string `json:"submissionId" format:"uuid"`
	Decision     string `json:"decision"`
	Reason       string `json:"reason,omitempty"`
	DecidedAt    int64  `json:"decidedAt"`
}

// ReviewDecisionOutput is the response to a review decision.
type ReviewDecisionOutput struct {
	Body DecisionBody
}

// CreateHandoffBody is the JSON body for POST /tasks/{id}/handoffs.
type CreateHandoffBody struct {
	Role       string `json:"role" enum:"assignee,reviewer,approver"`
	FromUserID string `json:"fromUserId,omitempty" format:"uuid"`
	ToUserID   string `json:"toUserId" format:"uuid"`
	DueAt      *int64 `json:"dueAt,omitempty" doc:"Optional Unix timestamp in seconds"`
}

// CreateHandoffInput is the request for creating a handoff.
type CreateHandoffInput struct {
	ID   string `path:"id" format:"uuid"`
	Body CreateHandoffBody
}

// HandoffBody is a persisted human responsibility handoff.
type HandoffBody struct {
	ID         string `json:"id" format:"uuid"`
	TaskID     string `json:"taskId" format:"uuid"`
	Role       string `json:"role"`
	FromUserID string `json:"fromUserId,omitempty" format:"uuid"`
	ToUserID   string `json:"toUserId" format:"uuid"`
	DueAt      *int64 `json:"dueAt,omitempty"`
	Status     string `json:"status"`
	CreatedAt  int64  `json:"createdAt"`
	ResolvedAt *int64 `json:"resolvedAt,omitempty"`
}

// CreateHandoffOutput is the response to creating a handoff.
type CreateHandoffOutput struct {
	Body HandoffBody
}

// HandoffDecisionBody is the JSON body for a handoff decision.
type HandoffDecisionBody struct {
	Decision string `json:"decision" enum:"accept,decline,cancel"`
}

// HandoffDecisionInput is the request for POST
// /tasks/{id}/handoffs/{handoffId}/decision.
type HandoffDecisionInput struct {
	ID        string `path:"id" format:"uuid"`
	HandoffID string `path:"handoffId" format:"uuid"`
	Body      HandoffDecisionBody
}

// HandoffDecisionOutput is the response to resolving a pending handoff.
type HandoffDecisionOutput struct {
	Body HandoffBody
}
