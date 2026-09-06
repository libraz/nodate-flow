package admin

import (
	"encoding/json"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

var (
	nullStr      = dbtype.PtrFromNullString
	nullTimeUnix = dbtype.UnixSecondsFromNullTime
)

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

func rowToAdminUserDetail(r generated.AdminGetUserRow) User {
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

func rowToAdminWorkspaceList(r generated.AdminListWorkspacesRow) AdminWorkspace {
	return AdminWorkspace{
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

func rowToAdminWorkspaceDetail(r generated.AdminGetWorkspaceRow) AdminWorkspace {
	pc := r.ProjectCount
	return AdminWorkspace{
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
		IPAddress:  dbtype.IPStringFromNullString(r.IpAddress),
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

// rowToOAuthSignInAllowlistEntry maps one allowlist entry. added_by is
// exposed as the adder's public id, and comes back nil once that account
// is gone: the FK is ON DELETE SET NULL, and the LEFT JOIN then scans the
// zero UUID.
func rowToOAuthSignInAllowlistEntry(r generated.FindOauthSigninAllowlistEntryRow) OAuthSignInAllowlistEntry {
	return OAuthSignInAllowlistEntry{
		ID:                 r.PublicID.String(),
		Kind:               string(r.EntryKind),
		Value:              r.EntryValue,
		Notes:              nullStr(r.Notes),
		Enabled:            r.Enabled,
		AddedByID:          nullPubID(r.AddedByPublicID),
		AddedByDisplayName: nullStr(r.AddedByDisplayName),
		CreatedAt:          r.CreatedAt.Unix(),
		UpdatedAt:          nullTimeUnix(r.UpdatedAt),
	}
}

// listRowToOAuthSignInAllowlistEntry maps a list row through the single
// mapping above. The two statements select the same columns, so one
// mapping serves both and the list and the write response can never
// describe the same entry differently.
func listRowToOAuthSignInAllowlistEntry(r generated.ListOauthSigninAllowlistEntriesRow) OAuthSignInAllowlistEntry {
	return rowToOAuthSignInAllowlistEntry(generated.FindOauthSigninAllowlistEntryRow{
		PublicID:           r.PublicID,
		EntryKind:          r.EntryKind,
		EntryValue:         r.EntryValue,
		Notes:              r.Notes,
		Enabled:            r.Enabled,
		AddedByPublicID:    r.AddedByPublicID,
		AddedByDisplayName: r.AddedByDisplayName,
		UpdatedAt:          r.UpdatedAt,
		CreatedAt:          r.CreatedAt,
	})
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
		IPAddress:              dbtype.IPPtrFromNullString(r.IpAddress),
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
