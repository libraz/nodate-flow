package ai

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// --- DTOs ---

// AutoActionSettingsOutput is the response DTO for the auto-action settings
// endpoints. It exposes only the three auto-action knobs, not the full
// ai_settings row.
type AutoActionSettingsOutput struct {
	Body AutoActionSettingsBody
}

// AutoActionSettingsBody carries the auto-action tuning knobs.
type AutoActionSettingsBody struct {
	Enabled         bool    `json:"enabled"`
	IntervalMinutes int     `json:"intervalMinutes"`
	Threshold       float64 `json:"threshold"`
}

// GetAutoActionSettingsInput is the path-only input for
// GET /workspaces/{wsId}/ai/auto-action-settings.
type GetAutoActionSettingsInput struct {
	WsID string `path:"wsId"`
}

// PatchAutoActionSettingsInput is the body for
// PATCH /workspaces/{wsId}/ai/auto-action-settings.
type PatchAutoActionSettingsInput struct {
	WsID string `path:"wsId"`
	Body struct {
		Enabled         *bool    `json:"enabled,omitempty"`
		IntervalMinutes *int     `json:"intervalMinutes,omitempty" minimum:"0" maximum:"1440"`
		Threshold       *float64 `json:"threshold,omitempty" minimum:"0" maximum:"1"`
	}
}

// --- defaults ---

const (
	defaultAutoActionEnabled         = true
	defaultAutoActionIntervalMinutes = 5
	defaultAutoActionThreshold       = 0.80
)

// --- handlers ---

// GetAutoActionSettings returns the workspace's auto-action executor
// settings. When no ai_settings row exists the column defaults are
// returned.
func GetAutoActionSettings(deps Deps) func(context.Context, *GetAutoActionSettingsInput) (*AutoActionSettingsOutput, error) {
	return func(ctx context.Context, _ *GetAutoActionSettingsInput) (*AutoActionSettingsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		row, err := deps.Queries.GetAiSettings(ctx, ws.ID)
		if err != nil && err != sql.ErrNoRows {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &AutoActionSettingsOutput{}
		if err == sql.ErrNoRows {
			out.Body = AutoActionSettingsBody{
				Enabled:         defaultAutoActionEnabled,
				IntervalMinutes: defaultAutoActionIntervalMinutes,
				Threshold:       defaultAutoActionThreshold,
			}
		} else {
			out.Body = autoActionBodyFromRow(row)
		}
		return out, nil
	}
}

// PatchAutoActionSettings applies a partial update to the workspace's
// auto-action executor settings. Fields not present in the request body
// are left unchanged.
func PatchAutoActionSettings(deps Deps) func(context.Context, *PatchAutoActionSettingsInput) (*AutoActionSettingsOutput, error) {
	return func(ctx context.Context, in *PatchAutoActionSettingsInput) (*AutoActionSettingsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		// Read the full ai_settings row (or defaults) so the upsert
		// preserves fields that are not being patched.
		row, err := deps.Queries.GetAiSettings(ctx, ws.ID)
		if err != nil && err != sql.ErrNoRows {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Start from DB values or column defaults.
		var params generated.UpsertAiSettingsParams
		if err == sql.ErrNoRows {
			params = generated.UpsertAiSettingsParams{
				WorkspaceID:               ws.ID,
				EmbedModel:                "mock-768",
				EmbedBudgetCentsDay:       100,
				DuplicateThresholdHigh:    "0.870",
				DuplicateThresholdLow:     "0.750",
				AutoActionEnabled:         defaultAutoActionEnabled,
				AutoActionIntervalMinutes: uint32(defaultAutoActionIntervalMinutes),
				AutoActionThreshold:       fmt.Sprintf("%.2f", defaultAutoActionThreshold),
			}
		} else {
			params = generated.UpsertAiSettingsParams{
				WorkspaceID:               ws.ID,
				EmbedModel:                row.EmbedModel,
				EmbedBudgetCentsDay:       row.EmbedBudgetCentsDay,
				DuplicateThresholdHigh:    row.DuplicateThresholdHigh,
				DuplicateThresholdLow:     row.DuplicateThresholdLow,
				AutoActionEnabled:         row.AutoActionEnabled,
				AutoActionIntervalMinutes: row.AutoActionIntervalMinutes,
				AutoActionThreshold:       row.AutoActionThreshold,
			}
		}

		// Merge patch fields.
		if in.Body.Enabled != nil {
			params.AutoActionEnabled = *in.Body.Enabled
		}
		if in.Body.IntervalMinutes != nil {
			params.AutoActionIntervalMinutes = uint32(*in.Body.IntervalMinutes)
		}
		if in.Body.Threshold != nil {
			params.AutoActionThreshold = fmt.Sprintf("%.2f", *in.Body.Threshold)
		}

		if err := deps.Queries.UpsertAiSettings(ctx, params); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &AutoActionSettingsOutput{
			Body: AutoActionSettingsBody{
				Enabled:         params.AutoActionEnabled,
				IntervalMinutes: int(params.AutoActionIntervalMinutes),
				Threshold:       parseThreshold(params.AutoActionThreshold),
			},
		}
		return out, nil
	}
}

// RegisterAutoActionSettings wires the auto-action settings GET and PATCH
// endpoints. The caller MUST attach RequireWorkspaceMember +
// RequireWorkspaceRole(Admin) to the underlying chi router.
func RegisterAutoActionSettings(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "ai-auto-action-settings-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/ai/auto-action-settings",
		Summary:     "Get auto-action executor settings for a workspace",
	}, GetAutoActionSettings(deps))

	huma.Register(api, huma.Operation{
		OperationID: "ai-auto-action-settings-update",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/ai/auto-action-settings",
		Summary:     "Patch auto-action executor settings for a workspace",
	}, PatchAutoActionSettings(deps))
}

// --- mapper helpers ---

// autoActionBodyFromRow converts the auto-action columns of an AiSetting
// DB row into the API response body.
func autoActionBodyFromRow(row generated.AiSetting) AutoActionSettingsBody {
	return AutoActionSettingsBody{
		Enabled:         row.AutoActionEnabled,
		IntervalMinutes: int(row.AutoActionIntervalMinutes),
		Threshold:       parseThreshold(row.AutoActionThreshold),
	}
}

// parseThreshold converts the DECIMAL string stored in the DB to float64.
// On parse failure it returns the default threshold so the API never
// returns a zero value.
func parseThreshold(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return defaultAutoActionThreshold
	}
	return v
}
