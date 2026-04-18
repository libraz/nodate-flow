package workspaces

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Register wires the workspace operations onto the given Huma API. The
// caller is responsible for attaching the appropriate ACL middleware to
// the underlying chi router for routes under /workspaces/{wsId}.
func Register(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "workspaces-create",
		Method:      http.MethodPost,
		Path:        "/workspaces",
		Summary:     "Create a workspace",
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "workspaces-list",
		Method:      http.MethodGet,
		Path:        "/workspaces",
		Summary:     "List workspaces visible to the caller",
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "workspaces-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}",
		Summary:     "Fetch a workspace",
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "workspaces-patch",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}",
		Summary:     "Patch a workspace",
	}, Patch(deps))

	huma.Register(api, huma.Operation{
		OperationID: "workspaces-disable",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}",
		Summary:     "Soft-disable a workspace",
	}, Disable(deps))

	huma.Register(api, huma.Operation{
		OperationID: "workspaces-users-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/users",
		Summary:     "List workspace users (minimal summary for actor pickers)",
	}, ListUsers(deps))

	huma.Register(api, huma.Operation{
		OperationID: "workspaces-members-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/members",
		Summary:     "List members of a workspace",
	}, ListMembers(deps))

	huma.Register(api, huma.Operation{
		OperationID: "workspaces-members-invite",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/members",
		Summary:     "Invite a user to a workspace",
	}, InviteMember(deps))

	huma.Register(api, huma.Operation{
		OperationID: "workspaces-members-update-role",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/members/{userId}",
		Summary:     "Change a member's role",
	}, UpdateMemberRole(deps))

	huma.Register(api, huma.Operation{
		OperationID: "workspaces-members-remove",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/members/{userId}",
		Summary:     "Remove a member from a workspace",
	}, RemoveMember(deps))
}

// RegisterInvites wires the workspace invite link routes onto the given
// Huma API. These routes require admin+ role and sit under
// /workspaces/{wsId}/invites. The AcceptInvite route is auth-only
// (no workspace scope) and must be registered separately.
func RegisterInvites(api huma.API, deps InviteDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "workspaces-invites-create",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/invites",
		Summary:     "Create a shareable invite link for a workspace",
	}, CreateInvite(deps))

	huma.Register(api, huma.Operation{
		OperationID: "workspaces-invites-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/invites",
		Summary:     "List active invite links for a workspace",
	}, ListInvites(deps))

	huma.Register(api, huma.Operation{
		OperationID: "workspaces-invites-revoke",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/invites/{inviteId}",
		Summary:     "Revoke an invite link",
	}, RevokeInvite(deps))
}

// RegisterInviteAccept wires the authenticated invite acceptance route.
// This endpoint requires auth but NOT workspace membership.
func RegisterInviteAccept(api huma.API, deps InviteDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "workspaces-invites-accept",
		Method:      http.MethodPost,
		Path:        "/invites/{token}/accept",
		Summary:     "Accept a workspace invite link",
	}, AcceptInvite(deps))
}

// RegisterPublicInvites wires the unauthenticated invite info route.
// The caller must attach per-IP rate limiting but NOT auth middleware.
func RegisterPublicInvites(api huma.API, deps InviteDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "workspaces-invites-info",
		Method:      http.MethodGet,
		Path:        "/invites/{token}/info",
		Summary:     "Fetch public info about a workspace invite (no auth required)",
	}, InviteInfo(deps))
}
