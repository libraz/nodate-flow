package workspace

import (
	"database/sql"
	"strconv"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
)

// nullStr converts a sql.NullString to a plain string (empty when NULL).
func nullStr(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}

// nullTimeUnix converts a sql.NullTime to *int64 unix seconds (nil when NULL).
func nullTimeUnix(t sql.NullTime) *int64 {
	if !t.Valid {
		return nil
	}
	v := t.Time.Unix()
	return &v
}

// nullInt32Ptr converts a sql.NullInt32 to *int32 (nil when NULL).
func nullInt32Ptr(n sql.NullInt32) *int32 {
	if n.Valid {
		v := n.Int32
		return &v
	}
	return nil
}

// totalAsInt64 converts the MySQL COUNT(*) OVER() result (which the
// Go driver delivers as []uint8) to a plain int64.
func totalAsInt64(v interface{}) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case uint64:
		return int64(x)
	case []byte:
		n, _ := strconv.ParseInt(string(x), 10, 64)
		return n
	default:
		return 0
	}
}

// rowToWorkspaceFromFind builds a public Workspace DTO from the
// FindWorkspaceByPublicId row.
func rowToWorkspaceFromFind(r generated.FindWorkspaceByPublicIdRow) Workspace {
	return Workspace{
		ID:          r.PublicID.String(),
		Slug:        r.Slug,
		Name:        r.Name,
		Description: nullStr(r.Description),
		IconURL:     nullStr(r.IconUrl),
		Timezone:    r.Timezone,
		Country:     nullStr(r.Country),
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
		Timezone:    r.Timezone,
		Country:     nullStr(r.Country),
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

// rowToInvite converts a ListWorkspaceInvitesRow to the public DTO.
func rowToInvite(r generated.ListWorkspaceInvitesRow) WorkspaceInvite {
	return WorkspaceInvite{
		ID:            r.PublicID.String(),
		Role:          string(r.Role),
		MaxUses:       nullInt32Ptr(r.MaxUses),
		UseCount:      r.UseCount,
		Label:         nullStr(r.Label),
		CreatedByName: r.CreatedByName,
		ExpiresAt:     nullTimeUnix(r.ExpiresAt),
		CreatedAt:     r.CreatedAt.Unix(),
	}
}
