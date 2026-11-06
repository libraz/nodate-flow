// Package execution holds immutable authorization principals and the
// transaction-current task authorization used by taskservice commands.
package execution

import (
	"errors"
	"fmt"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

const (
	ScopeReadWorkspace  = "read:workspace"
	ScopeWriteWorkspace = "write:workspace"

	ToolReadTask    = "read_task"
	ToolSetPriority = "set_priority"
	ToolAddComment  = "add_comment"
)

var (
	ErrInvalidPrincipal  = errors.New("execution: invalid principal")
	ErrTargetNotCaptured = errors.New("execution: task target is outside the captured target set")
)

// Principal is an immutable, validated identity for one operation. A
// human principal has userID set and agentID clear. An agent principal
// has agentID set and userID clear; delegatedByUserID is optional and is
// an independently reauthorized human identity, never the agent's author.
//
// All fields are private so callers cannot turn an agent into a human or
// widen its captured targets after construction.
type Principal struct {
	workspaceID       uint32
	userID            uint32
	agentID           uint32
	delegatedByUserID uint32
	scopes            []string
	tools             []string
	targets           []types.PublicID
}

// NewUserPrincipal builds a human identity scoped to one workspace.
func NewUserPrincipal(workspaceID, userID uint32) (Principal, error) {
	if workspaceID == 0 || userID == 0 {
		return Principal{}, ErrInvalidPrincipal
	}
	return Principal{workspaceID: workspaceID, userID: userID}, nil
}

// NewAgentPrincipal builds an agent identity scoped to one workspace and
// a fixed set of tools, token scopes, and captured public task IDs. Empty
// capability lists are allowed and grant nothing. A zero delegator means
// the call is autonomous; a non-zero delegator is rechecked separately by
// AuthorizeTaskTx.
func NewAgentPrincipal(
	workspaceID, agentID uint32,
	scopes, tools []string,
	targets []types.PublicID,
	delegatedByUserID uint32,
) (Principal, error) {
	if workspaceID == 0 || agentID == 0 {
		return Principal{}, ErrInvalidPrincipal
	}
	if err := validateValues("scope", scopes, ScopeReadWorkspace, ScopeWriteWorkspace); err != nil {
		return Principal{}, err
	}
	if err := validateValues("tool", tools); err != nil {
		return Principal{}, err
	}
	seen := make(map[types.PublicID]struct{}, len(targets))
	for _, target := range targets {
		if target == (types.PublicID{}) {
			return Principal{}, fmt.Errorf("%w: empty task target", ErrInvalidPrincipal)
		}
		if _, ok := seen[target]; ok {
			return Principal{}, fmt.Errorf("%w: duplicate task target", ErrInvalidPrincipal)
		}
		seen[target] = struct{}{}
	}
	return Principal{
		workspaceID:       workspaceID,
		agentID:           agentID,
		delegatedByUserID: delegatedByUserID,
		scopes:            append([]string(nil), scopes...),
		tools:             append([]string(nil), tools...),
		targets:           append([]types.PublicID(nil), targets...),
	}, nil
}

func validateValues(label string, values []string, allowed ...string) error {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		allowedSet[value] = struct{}{}
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return fmt.Errorf("%w: empty %s", ErrInvalidPrincipal, label)
		}
		if _, ok := allowedSet[value]; len(allowedSet) != 0 && !ok {
			return fmt.Errorf("%w: unsupported %s %q", ErrInvalidPrincipal, label, value)
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("%w: duplicate %s %q", ErrInvalidPrincipal, label, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

// Validate reports whether the value was made by one of the constructors.
// It also protects service entry points from a zero value Principal.
func (p Principal) Validate() error {
	if p.workspaceID == 0 || (p.userID == 0) == (p.agentID == 0) {
		return ErrInvalidPrincipal
	}
	if p.userID != 0 && (p.delegatedByUserID != 0 || len(p.scopes) != 0 || len(p.tools) != 0 || len(p.targets) != 0) {
		return ErrInvalidPrincipal
	}
	if p.agentID != 0 {
		if err := validateValues("scope", p.scopes, ScopeReadWorkspace, ScopeWriteWorkspace); err != nil {
			return err
		}
		if err := validateValues("tool", p.tools); err != nil {
			return err
		}
		seen := make(map[types.PublicID]struct{}, len(p.targets))
		for _, target := range p.targets {
			if target == (types.PublicID{}) {
				return ErrInvalidPrincipal
			}
			if _, ok := seen[target]; ok {
				return ErrInvalidPrincipal
			}
			seen[target] = struct{}{}
		}
	}
	return nil
}

func (p Principal) WorkspaceID() uint32       { return p.workspaceID }
func (p Principal) UserID() uint32            { return p.userID }
func (p Principal) AgentID() uint32           { return p.agentID }
func (p Principal) DelegatedByUserID() uint32 { return p.delegatedByUserID }
func (p Principal) IsAgent() bool             { return p.agentID != 0 }

// Scopes returns a defensive copy of the principal's token scopes.
func (p Principal) Scopes() []string { return append([]string(nil), p.scopes...) }

// Tools returns a defensive copy of the principal's captured tool allowlist.
func (p Principal) Tools() []string { return append([]string(nil), p.tools...) }

// Targets returns a defensive copy of the captured public task targets.
func (p Principal) Targets() []types.PublicID {
	return append([]types.PublicID(nil), p.targets...)
}

// CapturedTarget reports whether the task was named in the principal's
// immutable bounded target set.
func (p Principal) CapturedTarget(target types.PublicID) bool {
	for _, candidate := range p.targets {
		if candidate == target {
			return true
		}
	}
	return false
}

func (p Principal) HasTool(tool string) bool { return contains(p.tools, tool) }

// HasScope applies the MCP scope widening rule: write also grants read.
func (p Principal) HasScope(required string) bool {
	if contains(p.scopes, required) {
		return true
	}
	return required == ScopeReadWorkspace && contains(p.scopes, ScopeWriteWorkspace)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
