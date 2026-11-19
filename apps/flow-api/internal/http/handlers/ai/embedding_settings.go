package ai

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
)

// EmbeddingSettingsOutput returns a workspace's embedding mode and its
// effective instance-level provider status.
type EmbeddingSettingsOutput struct {
	Body EmbeddingSettingsBody
}

// EmbeddingSettingsBody contains no credentials. Endpoint contains only the
// scheme, hostname, and optional port of the instance provider.
type EmbeddingSettingsBody struct {
	Mode     string `json:"mode" enum:"inherit,disabled"`
	Status   string `json:"status" enum:"enabled,disabled,unconfigured"`
	Model    string `json:"model,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

// GetEmbeddingSettingsInput identifies the workspace in the route.
type GetEmbeddingSettingsInput struct {
	WsID string `path:"wsId"`
}

// PatchEmbeddingSettingsInput updates the workspace's embedding mode.
type PatchEmbeddingSettingsInput struct {
	WsID string `path:"wsId"`
	Body struct {
		Mode string `json:"mode" enum:"inherit,disabled"`
	}
}

// GetEmbeddingSettings returns the workspace embedding policy and the
// instance provider's non-secret metadata.
func GetEmbeddingSettings(deps Deps) func(context.Context, *GetEmbeddingSettingsInput) (*EmbeddingSettingsOutput, error) {
	return func(ctx context.Context, _ *GetEmbeddingSettingsInput) (*EmbeddingSettingsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		return embeddingSettingsOutput(ctx, deps, ws.ID)
	}
}

// PatchEmbeddingSettings persists the workspace's embedding policy without
// rewriting unrelated AI settings.
func PatchEmbeddingSettings(deps Deps) func(context.Context, *PatchEmbeddingSettingsInput) (*EmbeddingSettingsOutput, error) {
	return func(ctx context.Context, in *PatchEmbeddingSettingsInput) (*EmbeddingSettingsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		mode := generated.WorkspaceEmbeddingSettingsMode(in.Body.Mode)
		if mode != generated.WorkspaceEmbeddingSettingsModeInherit && mode != generated.WorkspaceEmbeddingSettingsModeDisabled {
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
		}

		var modifiedBy sql.NullInt32
		if userID, ok := middleware.ActorFromContext(ctx); ok {
			modifiedBy = sql.NullInt32{Int32: int32(userID), Valid: true} //#nosec G115 -- actor user id is users.id (INT UNSIGNED), fits int32 within realistic deployments
		}
		if err := deps.Queries.UpsertWorkspaceEmbeddingSettings(ctx, generated.UpsertWorkspaceEmbeddingSettingsParams{
			WorkspaceID:      ws.ID,
			ModifiedByUserID: modifiedBy,
			Mode:             mode,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return embeddingSettingsOutput(ctx, deps, ws.ID)
	}
}

func embeddingSettingsOutput(ctx context.Context, deps Deps, workspaceID uint32) (*EmbeddingSettingsOutput, error) {
	mode := generated.WorkspaceEmbeddingSettingsModeInherit
	storedMode, err := deps.Queries.GetWorkspaceEmbeddingSettings(ctx, workspaceID)
	if err != nil && err != sql.ErrNoRows {
		return nil, httpErr(apierrors.InternalUnexpected)
	}
	if err == nil {
		mode = storedMode
	}
	if mode != generated.WorkspaceEmbeddingSettingsModeInherit && mode != generated.WorkspaceEmbeddingSettingsModeDisabled {
		return nil, httpErr(apierrors.InternalUnexpected)
	}

	status := "unconfigured"
	if mode == generated.WorkspaceEmbeddingSettingsModeDisabled {
		status = "disabled"
	} else if deps.EmbedConfigured {
		status = "enabled"
	}

	body := EmbeddingSettingsBody{
		Mode:   string(mode),
		Status: status,
	}
	if deps.EmbedConfigured {
		body.Model = deps.EmbedModel
		body.Endpoint = deps.EmbedEndpoint
	}
	return &EmbeddingSettingsOutput{Body: body}, nil
}

// RegisterEmbeddingSettings wires the workspace-admin settings routes.
// The caller MUST attach RequireWorkspaceMember and RequireWorkspaceRole(Admin).
func RegisterEmbeddingSettings(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "ai-embedding-settings-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/ai/embedding-settings",
		Summary:     "Get embedding provider settings for a workspace",
		Description: "Returns the workspace opt-out mode and the effective instance embedding provider status without exposing credentials.",
		Tags:        []string{"AI"},
	}, GetEmbeddingSettings(deps))

	huma.Register(api, huma.Operation{
		OperationID: "ai-embedding-settings-update",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/ai/embedding-settings",
		Summary:     "Update embedding provider settings for a workspace",
		Description: "Sets the workspace embedding mode to inherit the instance provider or disable embeddings for this workspace.",
		Tags:        []string{"AI"},
	}, PatchEmbeddingSettings(deps))
}
