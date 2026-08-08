package signals

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strconv"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// webhookSender is the sender identity a /webhooks/* receiver extracted
// from a delivery it has already authenticated. Key is the value matched
// against integration_source_mappings.external_key; an empty Key means
// the delivery carried nothing that identifies its origin.
type webhookSender struct {
	Provider generated.IntegrationSourceMappingsProvider
	Key      string
	// Source is the log-facing name of the receiver ("github", "slack",
	// "google"). It duplicates Provider deliberately: Provider is a DB
	// enum whose values are free to diverge from the log vocabulary.
	Source string
}

// githubSenderKey renders a GitHub repository id as the external key.
// GitHub sends repository.id as a JSON number; the mapping stores the
// decimal digits so the column can hold Slack team ids and Google
// channel ids in the same shape.
func githubSenderKey(repoID int64) string {
	if repoID <= 0 {
		return ""
	}
	return strconv.FormatInt(repoID, 10)
}

// resolveWebhookWorkspace decides which tenant an authenticated inbound
// delivery belongs to.
//
// The sender identity is the only trustworthy routing input: the shared
// webhook secret proves the delivery came from the configured provider
// app, not which workspace it is for. A mapping row is therefore
// required, and a delivery whose sender is unmapped is rejected rather
// than stored somewhere arbitrary.
//
// NF_FLOW_DEFAULT_WORKSPACE_ID remains as a convenience for single-tenant
// deployments, where "the workspace" is unambiguous. It is honoured only
// while the instance actually has one live workspace; the moment a second
// tenant exists the fallback would start filing one tenant's events under
// another, so it switches itself off and says so in the log instead of
// silently mis-routing.
func resolveWebhookWorkspace(ctx context.Context, deps Deps, sender webhookSender) (uint32, *apierrors.Spec) {
	if sender.Key != "" {
		row, err := deps.Queries.FindIntegrationSourceMapping(ctx, generated.FindIntegrationSourceMappingParams{
			Provider:    sender.Provider,
			ExternalKey: sender.Key,
		})
		switch {
		case err == nil:
			return row.WorkspaceID, nil
		case errors.Is(err, sql.ErrNoRows):
			// Fall through to the single-tenant fallback.
		default:
			slog.ErrorContext(ctx, "webhook: source mapping lookup failed",
				slog.Any("error", err),
				slog.String("source", sender.Source),
			)
			return 0, apierrors.InternalUnexpected
		}
	}

	if deps.DefaultWorkspaceID == "" {
		slog.WarnContext(ctx, "webhook: delivery from an unmapped source rejected",
			slog.String("source", sender.Source),
			slog.Bool("sender_identified", sender.Key != ""),
		)
		return 0, apierrors.IntegrationMappingWorkspaceUnresolved
	}

	total, err := deps.Queries.CountEnabledWorkspaces(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "webhook: workspace count failed",
			slog.Any("error", err),
			slog.String("source", sender.Source),
		)
		return 0, apierrors.InternalUnexpected
	}
	if total != 1 {
		slog.ErrorContext(ctx, "webhook: default-workspace fallback disabled on a multi-tenant instance",
			slog.String("source", sender.Source),
			slog.Bool("sender_identified", sender.Key != ""),
			logutil.LogNumber("enabled_workspaces", total),
		)
		return 0, apierrors.IntegrationMappingWorkspaceUnresolved
	}

	wsPub, err := types.Parse(deps.DefaultWorkspaceID)
	if err != nil {
		slog.ErrorContext(ctx, "webhook: default workspace id parse failed",
			slog.Any("error", err),
			slog.String("source", sender.Source),
		)
		return 0, apierrors.InternalUnexpected
	}
	wsID, err := deps.Queries.GetWorkspaceIdByPublicId(ctx, wsPub)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, apierrors.WsWorkspaceNotFound
		}
		slog.ErrorContext(ctx, "webhook: default workspace lookup failed",
			slog.Any("error", err),
			slog.String("source", sender.Source),
		)
		return 0, apierrors.InternalUnexpected
	}
	return wsID, nil
}

// CheckDefaultWorkspaceFallback reports whether the configured
// NF_FLOW_DEFAULT_WORKSPACE_ID can still act as the routing fallback for
// unmapped webhook senders, so a misconfiguration surfaces at boot rather
// than as deliveries that quietly stop arriving. It returns nil when the
// setting is empty (no fallback wanted) or when the instance has exactly
// one live workspace. A non-nil error is a configuration report, not a
// runtime failure: the caller decides whether to refuse to start or log
// it — the routing path enforces the same rule per delivery either way.
func CheckDefaultWorkspaceFallback(ctx context.Context, q *generated.Queries, defaultWorkspaceID string) error {
	if defaultWorkspaceID == "" {
		return nil
	}
	total, err := q.CountEnabledWorkspaces(ctx)
	if err != nil {
		return err
	}
	if total == 1 {
		return nil
	}
	return &MultiTenantFallbackError{EnabledWorkspaces: total}
}

// MultiTenantFallbackError reports that NF_FLOW_DEFAULT_WORKSPACE_ID is
// set on an instance that hosts more than one live workspace, where it
// cannot be applied without cross-tenant leakage.
type MultiTenantFallbackError struct {
	EnabledWorkspaces int64
}

func (e *MultiTenantFallbackError) Error() string {
	return "NF_FLOW_DEFAULT_WORKSPACE_ID is set on an instance with " +
		strconv.FormatInt(e.EnabledWorkspaces, 10) +
		" enabled workspaces; inbound webhook deliveries from unmapped sources are rejected instead of routed to it"
}
