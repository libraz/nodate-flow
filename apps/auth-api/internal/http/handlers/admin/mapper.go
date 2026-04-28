package admin

import (
	"database/sql"
	"encoding/json"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
)

func nullStr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	s := ns.String
	return &s
}

func nullTimeUnix(nt sql.NullTime) *int64 {
	if !nt.Valid {
		return nil
	}
	v := nt.Time.Unix()
	return &v
}

// nullPubID converts a PublicID to a *string, returning nil for the zero UUID.
func nullPubID(pid types.PublicID) *string {
	s := pid.String()
	if s == "00000000-0000-0000-0000-000000000000" {
		return nil
	}
	return &s
}

// totalAsInt64 extracts the total count from a COUNT(*) OVER() column,
// which sqlc types as interface{}.
func totalAsInt64(v interface{}) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case []uint8:
		// MySQL driver may return []uint8 for COUNT(*) OVER()
		var n int64
		for _, b := range t {
			n = n*10 + int64(b-'0')
		}
		return n
	default:
		return 0
	}
}

func rowToAdminUser(r generated.AdminListUsersRow) User {
	return User{
		ID:              r.PublicID.String(),
		Email:           r.Email,
		DisplayName:     r.DisplayName,
		AvatarURL:       nullStr(r.AvatarUrl),
		Locale:          r.Locale,
		LastLoginAt:     nullTimeUnix(r.LastLoginAt),
		EmailVerifiedAt: nullTimeUnix(r.EmailVerifiedAt),
		Enabled:         r.Enabled,
		WorkspaceCount:  r.WorkspaceCount,
		IsInstanceAdmin: r.IsInstanceAdmin,
		CreatedAt:       r.CreatedAt.Unix(),
		UpdatedAt:       nullTimeUnix(r.UpdatedAt),
	}
}

func rowToAdminUserDetail(r generated.VAdminUser) User {
	return User{
		ID:              r.PublicID.String(),
		Email:           r.Email,
		DisplayName:     r.DisplayName,
		AvatarURL:       nullStr(r.AvatarUrl),
		Locale:          r.Locale,
		LastLoginAt:     nullTimeUnix(r.LastLoginAt),
		EmailVerifiedAt: nullTimeUnix(r.EmailVerifiedAt),
		Enabled:         r.Enabled,
		WorkspaceCount:  r.WorkspaceCount,
		IsInstanceAdmin: r.IsInstanceAdmin,
		CreatedAt:       r.CreatedAt.Unix(),
		UpdatedAt:       nullTimeUnix(r.UpdatedAt),
	}
}

func rowToAdminWorkspaceList(r generated.AdminListWorkspacesRow) Workspace {
	return Workspace{
		ID:          r.PublicID.String(),
		Slug:        r.Slug,
		Name:        r.Name,
		Description: nullStr(r.Description),
		IconURL:     nullStr(r.IconUrl),
		Enabled:     r.Enabled,
		MemberCount: r.MemberCount,
		CreatedAt:   r.CreatedAt.Unix(),
		UpdatedAt:   nullTimeUnix(r.UpdatedAt),
	}
}

func rowToAdminWorkspaceDetail(r generated.AdminGetWorkspaceRow) Workspace {
	pc := r.ProjectCount
	return Workspace{
		ID:           r.PublicID.String(),
		Slug:         r.Slug,
		Name:         r.Name,
		Description:  nullStr(r.Description),
		IconURL:      nullStr(r.IconUrl),
		Enabled:      r.Enabled,
		MemberCount:  r.MemberCount,
		ProjectCount: &pc,
		CreatedAt:    r.CreatedAt.Unix(),
		UpdatedAt:    nullTimeUnix(r.UpdatedAt),
	}
}

func rowToAdminSession(r generated.AdminListUserSessionsRow) Session {
	return Session{
		ID:         r.PublicID.String(),
		UserAgent:  r.UserAgent.String,
		IPAddress:  r.IpAddress.String,
		ExpiresAt:  r.ExpiresAt.Unix(),
		LastUsedAt: nullTimeUnix(r.LastUsedAt),
		Active:     r.Enabled && !r.RevokedAt.Valid,
		CreatedAt:  r.CreatedAt.Unix(),
	}
}

func rowToInstanceAdmin(r generated.AdminListInstanceAdminsRow) InstanceAdmin {
	return InstanceAdmin{
		ID:                   r.PublicID.String(),
		UserID:               r.UserPublicID.String(),
		Email:                r.Email,
		DisplayName:          r.DisplayName,
		AvatarURL:            nullStr(r.AvatarUrl),
		GrantedAt:            r.GrantedAt.Unix(),
		GrantedByID:          nullPubID(r.GrantedByPublicID),
		GrantedByDisplayName: nullStr(r.GrantedByDisplayName),
	}
}

func rowToAuditEntry(r generated.AdminListInstanceAuditLogsRow) AuditEntry {
	var payload json.RawMessage
	if len(r.PayloadJson) > 0 {
		payload = r.PayloadJson
	}
	return AuditEntry{
		ID:                     r.PublicID.String(),
		ActorUserID:            nullPubID(r.ActorUserPublicID),
		ActorDisplayName:       nullStr(r.ActorDisplayName),
		Action:                 r.Action,
		TargetWorkspaceID:      nullPubID(r.TargetWorkspacePublicID),
		TargetWorkspaceName:    nullStr(r.TargetWorkspaceName),
		TargetResourceType:     nullStr(r.TargetResourceType),
		TargetResourcePublicID: nullPubID(r.TargetResourcePublicID),
		IPAddress:              nullStr(r.IpAddress),
		UserAgent:              nullStr(r.UserAgent),
		Payload:                payload,
		OccurredAt:             r.OccurredAt.Unix(),
	}
}

func rowToSetting(r generated.AdminListInstanceSettingsRow) InstanceSetting {
	return InstanceSetting{
		Key:       r.SettingKey,
		Value:     r.SettingValue,
		UpdatedAt: nullTimeUnix(r.UpdatedAt),
	}
}
