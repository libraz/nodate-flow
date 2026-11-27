package ai

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/errormodel"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// errAnswered rolls a transaction back once the handler has decided the
// response itself. It is not transient, so dbretry does not retry it.
var errAnswered = errors.New("ai: answered")

const (
	// maxModelNameBytes matches ai_models.name and ai_providers.default_model.
	maxModelNameBytes = 128
	// maxModelDisplayNameChars matches ai_models.display_name.
	maxModelDisplayNameChars = 255
	// maxEnabledModelsPerWorkspace is the cap the model picker pages within.
	maxEnabledModelsPerWorkspace = 200
)

// validModelName trims surrounding whitespace and accepts 1..128 bytes of
// printable ASCII (0x21..0x7E). It returns the trimmed name.
func validModelName(s string) (string, bool) {
	n := strings.TrimSpace(s)
	if n == "" || len(n) > maxModelNameBytes {
		return "", false
	}
	for i := 0; i < len(n); i++ {
		if c := n[i]; c < 0x21 || c > 0x7E {
			return "", false
		}
	}
	return n, true
}

// validModelDisplayName trims surrounding whitespace and accepts 1..255
// characters. It returns the trimmed value.
func validModelDisplayName(s string) (string, bool) {
	n := strings.TrimSpace(s)
	if n == "" || utf8.RuneCountInString(n) > maxModelDisplayNameChars {
		return "", false
	}
	return n, true
}

// insertModelParams builds the ai_models insert for a model under a provider.
func insertModelParams(pub types.PublicID, workspaceID, providerID uint32, kind, name, displayName string) generated.InsertAIModelParams {
	d := defaultsForModel(kind, name)
	return generated.InsertAIModelParams{
		PublicID:                   pub,
		WorkspaceID:                workspaceID,
		ProviderID:                 providerID,
		Name:                       name,
		DisplayName:                displayName,
		ContextWindow:              d.contextWindow,
		MaxOutputTokens:            sql.NullInt32{Int32: int32(d.maxOutputTokens), Valid: true}, //#nosec G115 -- catalog constants, all far below MaxInt32
		InputPriceMicroUsdPerMtok:  uint64(d.inputPriceMicroUSDPerMTok),                         //#nosec G115 -- catalog prices are non-negative constants
		OutputPriceMicroUsdPerMtok: uint64(d.outputPriceMicroUSDPerMTok),                        //#nosec G115 -- catalog prices are non-negative constants
		SupportsTools:              d.supportsTools,
		SupportsVision:             d.supportsVision,
	}
}

// enabledModelLimitReached reports whether the workspace already holds the
// maximum number of enabled models.
func enabledModelLimitReached(ctx context.Context, q *generated.Queries, workspaceID uint32) (bool, error) {
	n, err := q.CountEnabledModelsForWorkspace(ctx, workspaceID)
	if err != nil {
		return false, err
	}
	return n >= maxEnabledModelsPerWorkspace, nil
}

// lookupEnabledProvider resolves the path provider id to an enabled provider
// of the workspace. It runs outside any transaction.
func lookupEnabledProvider(ctx context.Context, deps Deps, workspaceID uint32, providerID string) (generated.GetEnabledProviderForWorkspaceRow, error) {
	pub, err := types.Parse(providerID)
	if err != nil {
		return generated.GetEnabledProviderForWorkspaceRow{}, httpErr(apierrors.ValidationPathParamInvalid)
	}
	row, err := deps.Queries.GetEnabledProviderForWorkspace(ctx, generated.GetEnabledProviderForWorkspaceParams{
		WorkspaceID: workspaceID,
		PublicID:    pub,
	})
	if err != nil {
		return generated.GetEnabledProviderForWorkspaceRow{}, httpErr(apierr.SpecForErrNoRows(err, apierrors.AiProviderNotFound, apierrors.InternalUnexpected))
	}
	return row, nil
}

// recordAudit writes an audit entry attributed to the request actor.
func recordAudit(ctx context.Context, deps Deps, workspaceID uint32, action, resourceType, resourceID string, metadata map[string]any) {
	actorID, ok := middleware.ActorFromContext(ctx)
	if !ok {
		return
	}
	deps.Audit.Record(ctx, audit.Entry{
		Action:       action,
		ActorID:      actorID,
		WorkspaceID:  workspaceID,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Metadata:     metadata,
	})
}

// fromTo is the audit shape of a changed value.
func fromTo(from, to any) map[string]any {
	return map[string]any{"from": from, "to": to}
}

// ListProviderModels handles GET /workspaces/{wsId}/ai/providers/{providerId}/models.
func ListProviderModels(deps Deps) func(context.Context, *ListProviderModelsInput) (*ListProviderModelsOutput, error) {
	return func(ctx context.Context, in *ListProviderModelsInput) (*ListProviderModelsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		prov, err := lookupEnabledProvider(ctx, deps, ws.ID, in.ProviderID)
		if err != nil {
			return nil, err
		}
		rows, err := deps.Queries.ListProviderModels(ctx, generated.ListProviderModelsParams{
			WorkspaceID: ws.ID,
			ProviderID:  prov.ID,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListProviderModelsOutput{}
		out.Body.Models = make([]ProviderModel, 0, len(rows))
		for _, r := range rows {
			out.Body.Models = append(out.Body.Models, ProviderModel{
				ID:          r.PublicID.String(),
				Name:        r.Name,
				DisplayName: r.DisplayName,
				Enabled:     r.Enabled,
				IsDefault:   r.IsDefault,
				AgentCount:  int(r.AgentCount),
			})
		}
		return out, nil
	}
}

// CreateProviderModel handles POST /workspaces/{wsId}/ai/providers/{providerId}/models.
// A disabled model of the same name is re-enabled instead of inserted.
func CreateProviderModel(deps Deps) func(context.Context, *CreateProviderModelInput) (*CreateProviderModelOutput, error) {
	return func(ctx context.Context, in *CreateProviderModelInput) (*CreateProviderModelOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		name, ok := validModelName(in.Body.Name)
		if !ok {
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
		}
		var displayName *string
		if in.Body.DisplayName != nil {
			dn, ok := validModelDisplayName(*in.Body.DisplayName)
			if !ok {
				return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
			}
			displayName = &dn
		}
		prov, err := lookupEnabledProvider(ctx, deps, ws.ID, in.ProviderID)
		if err != nil {
			return nil, err
		}

		var (
			answered        error
			model           ProviderModel
			reenabled       bool
			prevDisplayName string
		)
		txErr := dbretry.InTx(ctx, deps.DB, "ai.CreateProviderModel", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			reenabled = false
			q := deps.Queries.WithTx(tx.RawTx())
			full, err := enabledModelLimitReached(ctx, q, ws.ID)
			if err != nil {
				return err
			}
			if full {
				answered = httpErr(apierrors.AiModelLimitReached)
				return errAnswered
			}
			insertDisplayName := name
			if displayName != nil {
				insertDisplayName = *displayName
			}
			modelPub := types.New()
			_, err = q.InsertAIModel(ctx, insertModelParams(modelPub, ws.ID, prov.ID, string(prov.Kind), name, insertDisplayName))
			if handlerutil.IsDuplicateEntry(err) {
				existing, gerr := q.GetAIModelByNameForUpdate(ctx, generated.GetAIModelByNameForUpdateParams{
					ProviderID: prov.ID,
					Name:       name,
				})
				if gerr != nil {
					return gerr
				}
				if existing.Enabled {
					answered = httpErr(apierrors.AiModelAlreadyExists)
					return errAnswered
				}
				dn := existing.DisplayName
				if displayName != nil {
					dn = *displayName
				}
				n, rerr := q.ReenableAIModel(ctx, generated.ReenableAIModelParams{
					Name:        name,
					DisplayName: dn,
					ID:          existing.ID,
				})
				if rerr != nil {
					return rerr
				}
				// The row is locked and disabled, so exactly one row changes.
				if n != 1 {
					return fmt.Errorf("ai: re-enable model affected %d rows", n)
				}
				modelPub = existing.PublicID
				prevDisplayName = existing.DisplayName
				reenabled = true
			} else if err != nil {
				return err
			}
			if !prov.DefaultModel.Valid {
				if err := q.SetProviderDefaultModelIfNull(ctx, generated.SetProviderDefaultModelIfNullParams{
					DefaultModel: nullableString(name),
					ID:           prov.ID,
				}); err != nil {
					return err
				}
			}
			row, err := q.GetProviderModelForUpdate(ctx, generated.GetProviderModelForUpdateParams{
				WorkspaceID: ws.ID,
				ProviderID:  prov.ID,
				PublicID:    modelPub,
			})
			if err != nil {
				return err
			}
			agents, err := q.CountEnabledAgentsForModel(ctx, row.ID)
			if err != nil {
				return err
			}
			model = ProviderModel{
				ID:          row.PublicID.String(),
				Name:        row.Name,
				DisplayName: row.DisplayName,
				Enabled:     row.Enabled,
				IsDefault:   row.IsDefault,
				AgentCount:  int(agents),
			}
			return nil
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if reenabled {
			meta := map[string]any{
				"providerId": prov.PublicID.String(),
				"name":       model.Name,
				"enabled":    fromTo(false, true),
				"reenabled":  true,
			}
			if prevDisplayName != model.DisplayName {
				meta["displayName"] = fromTo(prevDisplayName, model.DisplayName)
			}
			recordAudit(ctx, deps, ws.ID, "ai_model.update", "ai_model", model.ID, meta)
		} else {
			recordAudit(ctx, deps, ws.ID, "ai_model.create", "ai_model", model.ID, map[string]any{
				"providerId":  prov.PublicID.String(),
				"name":        model.Name,
				"displayName": model.DisplayName,
			})
		}
		return &CreateProviderModelOutput{Body: model}, nil
	}
}

// PatchProviderModel handles PATCH /workspaces/{wsId}/ai/providers/{providerId}/models/{modelId}.
// A refusal applies nothing, including a displayName sent alongside.
func PatchProviderModel(deps Deps) func(context.Context, *PatchProviderModelInput) (*PatchProviderModelOutput, error) {
	return func(ctx context.Context, in *PatchProviderModelInput) (*PatchProviderModelOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		modelPub, err := types.Parse(in.ModelID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		if in.Body.Enabled == nil && in.Body.DisplayName == nil {
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
		}
		var displayName *string
		if in.Body.DisplayName != nil {
			dn, ok := validModelDisplayName(*in.Body.DisplayName)
			if !ok {
				return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
			}
			displayName = &dn
		}
		prov, err := lookupEnabledProvider(ctx, deps, ws.ID, in.ProviderID)
		if err != nil {
			return nil, err
		}

		var (
			answered error
			meta     map[string]any
		)
		txErr := dbretry.InTx(ctx, deps.DB, "ai.PatchProviderModel", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			meta = nil
			q := deps.Queries.WithTx(tx.RawTx())
			row, err := q.GetProviderModelForUpdate(ctx, generated.GetProviderModelForUpdateParams{
				WorkspaceID: ws.ID,
				ProviderID:  prov.ID,
				PublicID:    modelPub,
			})
			if errors.Is(err, sql.ErrNoRows) {
				answered = httpErr(apierrors.AiModelNotFound)
				return errAnswered
			}
			if err != nil {
				return err
			}
			changes := map[string]any{}
			if in.Body.Enabled != nil && *in.Body.Enabled != row.Enabled {
				if row.Enabled {
					if row.IsDefault {
						answered = httpErr(apierrors.AiModelIsProviderDefault)
						return errAnswered
					}
					agents, err := q.CountEnabledAgentsForModel(ctx, row.ID)
					if err != nil {
						return err
					}
					if agents > 0 {
						answered = httpErr(apierrors.AiModelInUse)
						return errAnswered
					}
				} else {
					full, err := enabledModelLimitReached(ctx, q, ws.ID)
					if err != nil {
						return err
					}
					if full {
						answered = httpErr(apierrors.AiModelLimitReached)
						return errAnswered
					}
				}
				if err := q.SetAIModelEnabled(ctx, generated.SetAIModelEnabledParams{
					Enabled: *in.Body.Enabled,
					ID:      row.ID,
				}); err != nil {
					return err
				}
				changes["enabled"] = fromTo(row.Enabled, *in.Body.Enabled)
			}
			if displayName != nil && *displayName != row.DisplayName {
				if err := q.UpdateAIModelDisplayName(ctx, generated.UpdateAIModelDisplayNameParams{
					DisplayName: *displayName,
					ID:          row.ID,
				}); err != nil {
					return err
				}
				changes["displayName"] = fromTo(row.DisplayName, *displayName)
			}
			if len(changes) > 0 {
				changes["providerId"] = prov.PublicID.String()
				changes["name"] = row.Name
				meta = changes
			}
			return nil
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if meta != nil {
			recordAudit(ctx, deps, ws.ID, "ai_model.update", "ai_model", modelPub.String(), meta)
		}
		out := &PatchProviderModelOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// SetProviderDefaultModel handles PUT /workspaces/{wsId}/ai/providers/{providerId}/default-model.
func SetProviderDefaultModel(deps Deps) func(context.Context, *SetProviderDefaultModelInput) (*SetProviderDefaultModelOutput, error) {
	return func(ctx context.Context, in *SetProviderDefaultModelInput) (*SetProviderDefaultModelOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		modelPub, err := types.Parse(in.Body.ModelID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		prov, err := lookupEnabledProvider(ctx, deps, ws.ID, in.ProviderID)
		if err != nil {
			return nil, err
		}

		var (
			answered error
			changed  bool
			toName   string
		)
		txErr := dbretry.InTx(ctx, deps.DB, "ai.SetProviderDefaultModel", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			changed = false
			q := deps.Queries.WithTx(tx.RawTx())
			row, err := q.GetProviderModelForUpdate(ctx, generated.GetProviderModelForUpdateParams{
				WorkspaceID: ws.ID,
				ProviderID:  prov.ID,
				PublicID:    modelPub,
			})
			if errors.Is(err, sql.ErrNoRows) || (err == nil && !row.Enabled) {
				answered = httpErr(apierrors.AiModelNotFound)
				return errAnswered
			}
			if err != nil {
				return err
			}
			if row.IsDefault {
				return nil
			}
			if err := q.SetProviderDefaultModel(ctx, generated.SetProviderDefaultModelParams{
				DefaultModel: nullableString(row.Name),
				ID:           prov.ID,
			}); err != nil {
				return err
			}
			changed = true
			toName = row.Name
			return nil
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if changed {
			var from any
			if prov.DefaultModel.Valid {
				from = prov.DefaultModel.String
			}
			recordAudit(ctx, deps, ws.ID, "ai_provider.update_default_model", "ai_provider", prov.PublicID.String(), map[string]any{
				"from": from,
				"to":   toName,
			})
		}
		out := &SetProviderDefaultModelOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// ListAvailableModels handles GET /workspaces/{wsId}/ai/providers/{providerId}/available-models.
// It is a candidate list only: nothing is recorded and no cost guard applies.
func ListAvailableModels(deps Deps) func(context.Context, *ListAvailableModelsInput) (*ListAvailableModelsOutput, error) {
	return func(ctx context.Context, in *ListAvailableModelsInput) (*ListAvailableModelsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		prov, err := lookupEnabledProvider(ctx, deps, ws.ID, in.ProviderID)
		if err != nil {
			return nil, err
		}
		out := &ListAvailableModelsOutput{}
		out.Body.Models = []string{}
		if deps.Resolver == nil {
			return out, nil
		}
		ctx = providers.WithWorkspaceID(ctx, ws.ID)
		client, err := deps.Resolver.ForProvider(ctx, ws.ID, prov.PublicID)
		if err != nil {
			return nil, handlerutil.HTTPErrWithRetryAfter(errormodel.AIFailure(err))
		}
		lister, ok := client.(providers.ModelLister)
		if !ok {
			return out, nil
		}
		ids, err := lister.ListModels(ctx)
		if err != nil {
			return nil, handlerutil.HTTPErrWithRetryAfter(errormodel.AIFailure(err))
		}
		for _, id := range ids {
			if n, ok := validModelName(id); ok {
				out.Body.Models = append(out.Body.Models, n)
			}
		}
		return out, nil
	}
}
