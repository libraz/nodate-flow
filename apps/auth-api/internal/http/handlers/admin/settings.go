package admin

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// settingValidators maps every instance setting key the admin API accepts
// to its value check. A key is added here only together with the code path
// that reads it: registration, MFA policy and workspace/member limits are
// governed by deployment config, so no key is accepted yet. Unknown keys
// are rejected with 400.
var settingValidators = map[string]func(value string) bool{}

// ListSettings handles GET /admin/settings. Returns the active instance
// settings whose key is accepted by settingValidators; a stored row no code
// path reads is not shown as if it took effect.
func ListSettings(deps Deps) func(context.Context, *struct{}) (*ListSettingsOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*ListSettingsOutput, error) {
		rows, err := deps.Queries.AdminListInstanceSettings(ctx)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListSettingsOutput{}
		out.Body.Items = make([]InstanceSetting, 0, len(rows))
		for _, r := range rows {
			if _, ok := settingValidators[r.SettingKey]; !ok {
				continue
			}
			out.Body.Items = append(out.Body.Items, rowToSetting(r))
		}
		return out, nil
	}
}

// PatchSettings handles PATCH /admin/settings. Validates each key against
// the allowed list, then upserts every setting in the request body.
func PatchSettings(deps Deps) func(context.Context, *PatchSettingsInput) (*PatchSettingsOutput, error) {
	return func(ctx context.Context, in *PatchSettingsInput) (*PatchSettingsOutput, error) {
		uid, _ := authn.ActorFromContext(ctx)

		// Validate every key and value before writing anything.
		for key, value := range in.Body.Settings {
			valid, ok := settingValidators[key]
			if !ok {
				return nil, httpErr(apierrors.InstanceSettingsInvalidKey)
			}
			if !valid(value) {
				return nil, httpErr(apierrors.InstanceSettingsInvalidValue)
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

// settingsMetadata builds the audit metadata map from the updated settings.
func settingsMetadata(settings map[string]string) map[string]any {
	m := make(map[string]any, len(settings))
	for k, v := range settings {
		m[k] = v
	}
	return m
}
