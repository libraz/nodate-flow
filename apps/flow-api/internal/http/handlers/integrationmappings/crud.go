package integrationmappings

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// List handles GET /workspaces/{wsId}/integration-mappings.
func List(deps Deps) func(context.Context, *ListInput) (*ListOutput, error) {
	return func(ctx context.Context, _ *ListInput) (*ListOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		rows, err := deps.Queries.ListIntegrationSourceMappings(ctx, ws.ID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListOutput{}
		out.Body.Mappings = make([]IntegrationMapping, len(rows))
		for i, r := range rows {
			out.Body.Mappings[i] = mapListRow(r)
		}
		out.Body.Total = int64(len(rows))
		return out, nil
	}
}

// Create handles POST /workspaces/{wsId}/integration-mappings. It claims
// an external webhook source for the caller's workspace.
func Create(deps Deps) func(context.Context, *CreateInput) (*CreateOutput, error) {
	return func(ctx context.Context, in *CreateInput) (*CreateOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		provider, ok := parseProvider(in.Body.Provider)
		if !ok {
			return nil, httpErr(apierrors.IntegrationOauthProviderUnsupported)
		}
		if !validExternalKey(provider, in.Body.ExternalKey) {
			return nil, httpErr(apierrors.IntegrationMappingExternalKeyInvalid)
		}

		pub := types.New()
		if _, err := deps.Queries.CreateIntegrationSourceMapping(ctx, generated.CreateIntegrationSourceMappingParams{
			PublicID:    pub,
			WorkspaceID: ws.ID,
			Provider:    provider,
			ExternalKey: in.Body.ExternalKey,
			Label:       in.Body.Label,
		}); err != nil {
			// The (provider, external_key) UNIQUE key spans the whole
			// instance, so this also fires when another workspace already
			// holds the claim. Which workspace that is stays undisclosed.
			if isDuplicateEntry(err) {
				return nil, httpErr(apierrors.IntegrationMappingSourceAlreadyMapped)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if deps.Audit != nil {
			if actorID, ok := middleware.ActorFromContext(ctx); ok {
				deps.Audit.Record(ctx, audit.Entry{
					Action:       "integration.mapping.create",
					ActorID:      actorID,
					WorkspaceID:  ws.ID,
					ResourceType: "integration_source_mapping",
					ResourceID:   pub.String(),
					Metadata: map[string]any{
						"provider":    string(provider),
						"externalKey": in.Body.ExternalKey,
					},
				})
			}
		}

		row, err := deps.Queries.FindIntegrationSourceMappingByPublicId(ctx, generated.FindIntegrationSourceMappingByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &CreateOutput{Body: mapFindRow(row)}, nil
	}
}

// Patch handles PATCH /workspaces/{wsId}/integration-mappings/{id}.
func Patch(deps Deps) func(context.Context, *PatchInput) (*PatchOutput, error) {
	return func(ctx context.Context, in *PatchInput) (*PatchOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		pub, err := types.Parse(in.ID)
		if err != nil {
			return nil, httpErr(apierrors.IntegrationMappingNotFound)
		}
		// Existence is checked workspace-scoped first so a public id
		// belonging to another tenant is a 404, not a silent no-op UPDATE
		// followed by a 404 from the re-read.
		if _, err := deps.Queries.FindIntegrationSourceMappingByPublicId(ctx, generated.FindIntegrationSourceMappingByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		}); err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.IntegrationMappingNotFound, apierrors.InternalUnexpected))
		}

		params := generated.UpdateIntegrationSourceMappingParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		}
		if in.Body.Label != nil {
			params.Label = sql.NullString{String: *in.Body.Label, Valid: true}
		}
		if in.Body.Enabled != nil {
			params.Enabled = sql.NullBool{Bool: *in.Body.Enabled, Valid: true}
		}
		if err := deps.Queries.UpdateIntegrationSourceMapping(ctx, params); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if deps.Audit != nil {
			if actorID, ok := middleware.ActorFromContext(ctx); ok {
				meta := map[string]any{}
				if in.Body.Enabled != nil {
					meta["enabled"] = *in.Body.Enabled
				}
				deps.Audit.Record(ctx, audit.Entry{
					Action:       "integration.mapping.patch",
					ActorID:      actorID,
					WorkspaceID:  ws.ID,
					ResourceType: "integration_source_mapping",
					ResourceID:   in.ID,
					Metadata:     meta,
				})
			}
		}

		row, err := deps.Queries.FindIntegrationSourceMappingByPublicId(ctx, generated.FindIntegrationSourceMappingByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.IntegrationMappingNotFound, apierrors.InternalUnexpected))
		}
		return &PatchOutput{Body: mapFindRow(row)}, nil
	}
}

// Delete handles DELETE /workspaces/{wsId}/integration-mappings/{id}. The
// row is removed rather than disabled so the external source can be
// claimed again — see the query's comment for why a tombstone would be a
// permanent lock-out.
func Delete(deps Deps) func(context.Context, *DeleteInput) (*DeleteOutput, error) {
	return func(ctx context.Context, in *DeleteInput) (*DeleteOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		pub, err := types.Parse(in.ID)
		if err != nil {
			return nil, httpErr(apierrors.IntegrationMappingNotFound)
		}
		affected, err := deps.Queries.DeleteIntegrationSourceMapping(ctx, generated.DeleteIntegrationSourceMappingParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if affected == 0 {
			return nil, httpErr(apierrors.IntegrationMappingNotFound)
		}

		if deps.Audit != nil {
			if actorID, ok := middleware.ActorFromContext(ctx); ok {
				deps.Audit.Record(ctx, audit.Entry{
					Action:       "integration.mapping.delete",
					ActorID:      actorID,
					WorkspaceID:  ws.ID,
					ResourceType: "integration_source_mapping",
					ResourceID:   in.ID,
				})
			}
		}

		out := &DeleteOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// parseProvider maps the request enum onto the DB enum. Huma has already
// rejected values outside the tag's enum list; the check is repeated here
// so a future tag edit cannot smuggle an unmapped value into the column.
func parseProvider(v string) (generated.IntegrationSourceMappingsProvider, bool) {
	switch v {
	case string(generated.IntegrationSourceMappingsProviderGithub):
		return generated.IntegrationSourceMappingsProviderGithub, true
	case string(generated.IntegrationSourceMappingsProviderSlack):
		return generated.IntegrationSourceMappingsProviderSlack, true
	case string(generated.IntegrationSourceMappingsProviderGoogle):
		return generated.IntegrationSourceMappingsProviderGoogle, true
	default:
		return "", false
	}
}

// validExternalKey checks the key against the shape the matching webhook
// receiver will actually present. Getting this wrong produces a mapping
// that silently never matches, which is indistinguishable from having no
// mapping at all — so the shape is enforced at the point the operator can
// still fix it. Written as explicit scans rather than regular expressions
// to match the rest of the webhook path.
func validExternalKey(provider generated.IntegrationSourceMappingsProvider, key string) bool {
	if key == "" || len(key) > 255 {
		return false
	}
	switch provider {
	case generated.IntegrationSourceMappingsProviderGithub:
		// repository.id serialised as decimal digits, as
		// signals.githubSenderKey renders it. Leading zeros would never
		// match, and neither would 0 itself.
		if len(key) > 19 || key[0] == '0' {
			return false
		}
		for i := 0; i < len(key); i++ {
			if key[i] < '0' || key[i] > '9' {
				return false
			}
		}
		return true
	case generated.IntegrationSourceMappingsProviderSlack:
		// Slack team ids ("T0123ABCD") and Enterprise Grid org ids
		// ("E0123ABCD") are uppercase alphanumeric.
		if len(key) < 3 || len(key) > 32 {
			return false
		}
		for i := 0; i < len(key); i++ {
			c := key[i]
			if (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
				return false
			}
		}
		return true
	case generated.IntegrationSourceMappingsProviderGoogle:
		// Google caps a push channel id at 64 characters drawn from the
		// unreserved URI set plus a few extras.
		if len(key) > 64 {
			return false
		}
		for i := 0; i < len(key); i++ {
			c := key[i]
			switch {
			case c >= 'A' && c <= 'Z',
				c >= 'a' && c <= 'z',
				c >= '0' && c <= '9',
				c == '-', c == '_', c == '.', c == '~', c == '+', c == '/', c == '=':
			default:
				return false
			}
		}
		return true
	default:
		return false
	}
}
