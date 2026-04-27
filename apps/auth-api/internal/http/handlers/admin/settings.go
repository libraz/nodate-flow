package admin

import (
	"context"
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// allowedSettingKeys defines the set of instance setting keys that may be
// created or updated via the admin API. Unknown keys are rejected with 400.
var allowedSettingKeys = map[string]bool{
	"registration_open":         true,
	"mfa_enforcement":           true,
	"max_workspaces_per_user":   true,
	"max_members_per_workspace": true,
}

// ListSettings handles GET /admin/settings. Returns all active instance
// settings as a flat list.
func ListSettings(deps Deps) func(context.Context, *struct{}) (*ListSettingsOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*ListSettingsOutput, error) {
		rows, err := deps.Queries.AdminListInstanceSettings(ctx)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListSettingsOutput{}
		out.Body.Items = make([]InstanceSetting, len(rows))
		for i, r := range rows {
			out.Body.Items[i] = rowToSetting(r)
		}
		return out, nil
	}
}

// PatchSettings handles PATCH /admin/settings. Validates each key against
// the allowed list, then upserts every setting in the request body.
func PatchSettings(deps Deps) func(context.Context, *PatchSettingsInput) (*PatchSettingsOutput, error) {
	return func(ctx context.Context, in *PatchSettingsInput) (*PatchSettingsOutput, error) {
		uid, _ := authn.ActorFromContext(ctx)

		// Validate all keys before writing anything.
		for key := range in.Body.Settings {
			if !allowedSettingKeys[key] {
				return nil, httpErr(apierrors.InstanceSettingsInvalidKey)
			}
		}

		// Validate values.
		for key, value := range in.Body.Settings {
			if err := validateSettingValue(key, value); err != nil {
				return nil, err
			}
		}

		updatedBy := sql.NullInt32{Int32: int32(uid), Valid: uid > 0} //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments

		for key, value := range in.Body.Settings {
			err := deps.Queries.AdminUpsertInstanceSetting(ctx, generated.AdminUpsertInstanceSettingParams{
				PublicID:        types.New(),
				SettingKey:      key,
				SettingValue:    value,
				UpdatedByUserID: updatedBy,
			})
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "admin.settings.update",
			ActorID:      uid,
			ResourceType: "settings",
			Metadata:     settingsMetadata(in.Body.Settings),
		})

		out := &PatchSettingsOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// validateSettingValue checks that the value is acceptable for the given key.
// Returns nil on success or an error suitable for returning from the handler.
func validateSettingValue(key, value string) error {
	switch key {
	case "registration_open", "mfa_enforcement":
		if value != "true" && value != "false" {
			return httpErr(apierrors.InstanceSettingsInvalidValue)
		}
	case "max_workspaces_per_user", "max_members_per_workspace":
		// Must be a positive integer string.
		if len(value) == 0 {
			return httpErr(apierrors.InstanceSettingsInvalidValue)
		}
		for _, c := range value {
			if c < '0' || c > '9' {
				return httpErr(apierrors.InstanceSettingsInvalidValue)
			}
		}
	}
	return nil
}

// settingsMetadata builds the audit metadata map from the updated settings.
func settingsMetadata(settings map[string]string) map[string]any {
	m := make(map[string]any, len(settings))
	for k, v := range settings {
		m[k] = v
	}
	return m
}
