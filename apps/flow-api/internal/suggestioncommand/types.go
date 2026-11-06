// Package suggestioncommand implements atomic, workspace-scoped commands for
// persisted AI inbox suggestions.
package suggestioncommand

import (
	"encoding/json"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// ExecuteCommand is the authenticated command context and normalized request
// body for executing or opening one persisted suggestion.
type ExecuteCommand struct {
	WorkspaceID       uint32
	WorkspacePublicID types.PublicID
	ActorUserID       uint32
	InboxItemID       types.PublicID
	OperationKey      string
	ProposalID        *types.PublicID
	ExpectedRevision  uint32
	Action            string
	Reasoning         string
	SnoozeUntil       *int64
}

// ExecuteResult is persisted in the operation receipt and returned verbatim
// on a successful retry, with Replayed changed to true.
type ExecuteResult struct {
	OperationID string `json:"operationId"`
	ProposalID  string `json:"proposalId"`
	Revision    uint32 `json:"revision"`
	Action      string `json:"action"`
	Status      string `json:"status"`
	InboxItemID string `json:"inboxItemId"`
	TaskID      string `json:"taskId,omitempty"`
	SnoozeUntil *int64 `json:"snoozeUntil,omitempty"`
	ExecutedAt  *int64 `json:"executedAt,omitempty"`
	Replayed    bool   `json:"replayed"`
}

// DecisionCommand is an actor-scoped edit, dismissal, or deferral of a
// persisted proposal. DeferUntil is required only for state=deferred.
type DecisionCommand struct {
	WorkspaceID       uint32
	WorkspacePublicID types.PublicID
	ActorUserID       uint32
	InboxItemID       types.PublicID
	ProposalID        types.PublicID
	ExpectedRevision  uint32
	State             string
	Action            string
	Reasoning         string
	DeferUntil        *int64
}

// DecisionResult is the current actor-scoped state returned after a decision
// write. DecisionExpiresAt is Unix seconds and is always present.
type DecisionResult struct {
	ProposalID        string `json:"proposalId"`
	Revision          uint32 `json:"revision"`
	State             string `json:"state"`
	Action            string `json:"action,omitempty"`
	Reasoning         string `json:"reasoning,omitempty"`
	DecisionExpiresAt int64  `json:"decisionExpiresAt"`
}

type parsedProposal struct {
	EventID       uint64
	PublicID      types.PublicID
	InboxItemID   types.PublicID
	BatchID       types.PublicID
	HasBatch      bool
	Action        string
	Reasoning     string
	Score         float32
	SourceVersion []byte
	HasVersion    bool
	OccurredAt    time.Time
}

type proposalEvent struct {
	ID          uint64
	PublicID    types.PublicID
	OccurredAt  time.Time
	PayloadJSON json.RawMessage
}
