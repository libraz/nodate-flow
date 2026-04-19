package workspaces

import (
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
)

// rowToWorkspaceFromFind builds a public Workspace DTO from the
// FindWorkspaceByPublicId row.
func rowToWorkspaceFromFind(r generated.FindWorkspaceByPublicIdRow) Workspace {
	return Workspace{
		ID:          r.PublicID.String(),
		Slug:        r.Slug,
		Name:        r.Name,
		Description: nullStr(r.Description),
		IconURL:     nullStr(r.IconUrl),
		UpdatedAt:   nullTimeUnix(r.UpdatedAt),
		CreatedAt:   r.CreatedAt.Unix(),
	}
}

// rowToWorkspaceFromList builds a Workspace DTO from a list-for-user row.
func rowToWorkspaceFromList(r generated.ListWorkspacesForUserRow) Workspace {
	return Workspace{
		ID:          r.PublicID.String(),
		Slug:        r.Slug,
		Name:        r.Name,
		Description: nullStr(r.Description),
		IconURL:     nullStr(r.IconUrl),
		Role:        string(r.Role),
		MemberCount: r.MemberCount,
		UpdatedAt:   nullTimeUnix(r.UpdatedAt),
		CreatedAt:   r.CreatedAt.Unix(),
	}
}

// rowToMember builds a WorkspaceMember DTO from a list row.
func rowToMember(r generated.ListWorkspaceMembersRow) WorkspaceMember {
	return WorkspaceMember{
		ID:          r.PublicID.String(),
		UserID:      r.UserPublicID.String(),
		Email:       r.Email,
		DisplayName: r.DisplayName,
		AvatarURL:   nullStr(r.AvatarUrl),
		Role:        string(r.Role),
		InvitedAt:   nullTimeUnix(r.InvitedAt),
		JoinedAt:    nullTimeUnix(r.JoinedAt),
		UpdatedAt:   nullTimeUnix(r.UpdatedAt),
		CreatedAt:   r.CreatedAt.Unix(),
	}
}
