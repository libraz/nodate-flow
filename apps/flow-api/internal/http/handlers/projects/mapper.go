package projects

import (
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
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

// totalAsInt64 normalizes the COUNT(*) OVER() return type into int64.
func totalAsInt64(v interface{}) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case uint64:
		return int64(x)
	case []byte:
		var n int64
		for _, c := range x {
			if c < '0' || c > '9' {
				return n
			}
			n = n*10 + int64(c-'0')
		}
		return n
	}
	return 0
}
