package events

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterWorkspaceScoped wires POST /workspaces/{wsId}/events/{eventPublicId}/reverse.
// The caller must attach RequireWorkspaceMember to the underlying chi
// router so the workspace context is populated before the handler runs.
func RegisterWorkspaceScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID:   "events-reverse",
		Method:        http.MethodPost,
		Path:          "/workspaces/{wsId}/events/{eventPublicId}/reverse",
		Summary:       "Reverse an LLM-origin event with a compensating event",
		Description:   "Appends a same-type compensating event whose reverses_event_id points back at the target. Only events with actor_agent_id set (LLM-origin) may be reversed; double-reversal is rejected. The events log stays immutable; the derived_state projection cancels the pair out for the timeline UI.",
		DefaultStatus: http.StatusCreated,
		Tags:          []string{"Tasks"},
	}, Reverse(deps))
}
