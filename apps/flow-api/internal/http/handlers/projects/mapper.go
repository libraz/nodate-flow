package projects

import (
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

func rowToProjectFromFind(r generated.FindProjectByPublicIdGlobalRow) Project {
	return Project{
		ID:          r.PublicID.String(),
		WorkspaceID: r.WorkspacePublicID.String(),
		Slug:        r.Slug,
		Name:        r.Name,
		Description: nullStr(r.Description),
		Color:       nullStr(r.Color),
		IsArchived:  r.IsArchived,
		StartedOn:   nullTime(r.StartedOn),
		EndedOn:     nullTime(r.EndedOn),
		UpdatedAt:   nullTime(r.UpdatedAt),
		CreatedAt:   r.CreatedAt,
	}
}

// rowToProjectFromList builds a Project DTO from a list row. The workspace
// public id is threaded in from the caller because v_projects does not
// expose it (the list query is already workspace-scoped via the path).
func rowToProjectFromList(r generated.ListProjectsForWorkspaceRow, workspacePublicID string) Project {
	return Project{
		ID:          r.PublicID.String(),
		WorkspaceID: workspacePublicID,
		Slug:        r.Slug,
		Name:        r.Name,
		Description: nullStr(r.Description),
		Color:       nullStr(r.Color),
		IsArchived:  r.IsArchived,
		StartedOn:   nullTime(r.StartedOn),
		EndedOn:     nullTime(r.EndedOn),
		UpdatedAt:   nullTime(r.UpdatedAt),
		CreatedAt:   r.CreatedAt,
	}
}

func rowToProjectMember(r generated.ListProjectMembersRow) ProjectMember {
	return ProjectMember{
		ID:          r.PublicID.String(),
		UserID:      r.UserPublicID.String(),
		Email:       r.Email,
		DisplayName: r.DisplayName,
		AvatarURL:   nullStr(r.AvatarUrl),
		Role:        string(r.Role),
		AddedAt:     nullTime(r.AddedAt),
		CreatedAt:   r.CreatedAt,
	}
}

// totalAsInt64 delegates to handlerutil.TotalAsInt64.
var totalAsInt64 = handlerutil.TotalAsInt64
