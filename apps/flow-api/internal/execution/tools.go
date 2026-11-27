package execution

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Tool names one task operation an agent can be granted. The set is
// closed; reading a task is not a tool because assignment alone permits it.
// The constants are untyped so they also fill a principal's []string tool list.
type Tool string

const (
	ToolAddComment     = "add_comment"
	ToolSetPriority    = "set_priority"
	ToolSetDueOn       = "set_due_on"
	ToolTransitionTask = "transition_task"
	ToolCreateSubtask  = "create_subtask"

	// ToolReadTask names the read capability. It is never a grant key.
	ToolReadTask = "read_task"
)

// GrantMode is how a granted tool is applied: directly or via a proposal.
type GrantMode string

const (
	GrantModeAuto     GrantMode = "auto"
	GrantModeApproval GrantMode = "approval"
)

// ToolGrants is the decoded ai_agents.tools_json: tool -> mode. A tool
// without a key is not permitted.
type ToolGrants map[Tool]GrantMode

// ErrInvalidToolGrants reports a tools value that is not an object of known
// tools to known modes.
var ErrInvalidToolGrants = errors.New("execution: invalid tool grants")

var grantableTools = map[Tool]struct{}{
	ToolAddComment:     {},
	ToolSetPriority:    {},
	ToolSetDueOn:       {},
	ToolTransitionTask: {},
	ToolCreateSubtask:  {},
}

// Capability returns the capability AuthorizeTaskTx checks for the tool.
func (t Tool) Capability() Capability { return Capability(t) }

// DefaultTaskAgentTools is the grant set written for a new task agent:
// every tool, each requiring approval.
func DefaultTaskAgentTools() ToolGrants {
	grants := make(ToolGrants, len(grantableTools))
	for tool := range grantableTools {
		grants[tool] = GrantModeApproval
	}
	return grants
}

// ValidateToolGrants checks an API-supplied tool -> mode map. Unknown tools
// and unknown or empty modes (a JSON null decodes to "") are rejected.
func ValidateToolGrants(raw map[string]string) (ToolGrants, error) {
	grants := make(ToolGrants, len(raw))
	for key, mode := range raw {
		tool := Tool(key)
		if _, ok := grantableTools[tool]; !ok {
			return nil, fmt.Errorf("%w: unknown tool %q", ErrInvalidToolGrants, key)
		}
		switch GrantMode(mode) {
		case GrantModeAuto, GrantModeApproval:
		default:
			return nil, fmt.Errorf("%w: unknown mode %q for tool %q", ErrInvalidToolGrants, mode, key)
		}
		grants[tool] = GrantMode(mode)
	}
	return grants, nil
}

// ParseToolGrants decodes a stored tools_json value. SQL NULL (empty input)
// and JSON null mean no grants and return nil. Anything other than an object
// of known tools to known modes is ErrInvalidToolGrants.
func ParseToolGrants(raw []byte) (ToolGrants, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToolGrants, err)
	}
	if entries == nil {
		return nil, nil
	}
	modes := make(map[string]string, len(entries))
	for key, value := range entries {
		var mode string
		if err := json.Unmarshal(value, &mode); err != nil || string(value) == "null" {
			return nil, fmt.Errorf("%w: mode for tool %q is not a string", ErrInvalidToolGrants, key)
		}
		modes[key] = mode
	}
	return ValidateToolGrants(modes)
}

// MarshalToolGrants encodes grants for tools_json after validating them.
// Nil grants encode as {} (no tools permitted).
func MarshalToolGrants(grants ToolGrants) ([]byte, error) {
	modes := make(map[string]string, len(grants))
	for tool, mode := range grants {
		modes[string(tool)] = string(mode)
	}
	if _, err := ValidateToolGrants(modes); err != nil {
		return nil, err
	}
	return json.Marshal(modes)
}
