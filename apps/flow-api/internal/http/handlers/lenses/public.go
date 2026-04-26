package lenses

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
)

// Publish handles POST /workspaces/{wsId}/lenses/{lensId}/publish.
// It generates a random 32-char hex share token and marks the lens
// as publicly accessible. Returns 409 if the lens is already public.
func Publish(deps Deps) func(context.Context, *PublishLensInput) (*PublishLensOutput, error) {
	return func(ctx context.Context, in *PublishLensInput) (*PublishLensOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsMemberRoleDenied)
		}

		lid, err := types.Parse(in.LensID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		// Verify the lens exists in this workspace.
		lensRow, err := deps.Queries.GetLensByPublicID(ctx, generated.GetLensByPublicIDParams{
			WorkspaceID: ws.ID,
			PublicID:    lid,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsLensNotFound, apierrors.InternalUnexpected))
		}

		// Generate a cryptographically random 32-char hex token.
		tokenBytes := make([]byte, 16)
		if _, err := rand.Read(tokenBytes); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		token := hex.EncodeToString(tokenBytes)

		// SetLensPublic is a no-op when is_public = TRUE (WHERE guard).
		if err := deps.Queries.SetLensPublic(ctx, generated.SetLensPublicParams{
			PublicToken: sql.NullString{String: token, Valid: true},
			WorkspaceID: ws.ID,
			PublicID:    lid,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// If the lens was already public the UPDATE matched zero rows.
		// Detect by checking the existing row's state.
		if lensRow.IsPublic {
			return nil, httpErr(apierrors.WsLensAlreadyPublic)
		}

		// Append event for the state change.
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.LensShared,
			WorkspaceID: ws.ID,
			ActorUserID: actorInt64Ptr(actorID),
			Payload: map[string]any{
				"lensId":      lensRow.PublicID.String(),
				"publicToken": token,
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "lenses.Publish"),
				slog.String("event_type", string(eventbus.LensShared)),
				slog.Int64("workspace_id", int64(ws.ID)),
				slog.Int64("actor_id", int64(actorID)),
				slog.String("lens_id", lensRow.PublicID.String()),
			)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "lens.publish",
			ActorID:      actorID,
			WorkspaceID:  ws.ID,
			ResourceType: "lens",
			ResourceID:   lensRow.PublicID.String(),
			Metadata:     map[string]any{"publicToken": token},
		})

		return &PublishLensOutput{Body: PublishLensBody{
			PublicToken: token,
		}}, nil
	}
}

// Unpublish handles POST /workspaces/{wsId}/lenses/{lensId}/unpublish.
// It revokes public access and clears the share token. Returns 409 if
// the lens is already private.
func Unpublish(deps Deps) func(context.Context, *UnpublishLensInput) (*UnpublishLensOutput, error) {
	return func(ctx context.Context, in *UnpublishLensInput) (*UnpublishLensOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsMemberRoleDenied)
		}

		lid, err := types.Parse(in.LensID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		// Verify the lens exists in this workspace.
		lensRow, err := deps.Queries.GetLensByPublicID(ctx, generated.GetLensByPublicIDParams{
			WorkspaceID: ws.ID,
			PublicID:    lid,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsLensNotFound, apierrors.InternalUnexpected))
		}

		// SetLensPrivate is a no-op when is_public = FALSE (WHERE guard).
		if err := deps.Queries.SetLensPrivate(ctx, generated.SetLensPrivateParams{
			WorkspaceID: ws.ID,
			PublicID:    lid,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// If the lens was already private the UPDATE matched zero rows.
		if !lensRow.IsPublic {
			return nil, httpErr(apierrors.WsLensAlreadyPrivate)
		}

		// Append event for the state change.
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.LensUnshared,
			WorkspaceID: ws.ID,
			ActorUserID: actorInt64Ptr(actorID),
			Payload: map[string]any{
				"lensId": lensRow.PublicID.String(),
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "lenses.Unpublish"),
				slog.String("event_type", string(eventbus.LensUnshared)),
				slog.Int64("workspace_id", int64(ws.ID)),
				slog.Int64("actor_id", int64(actorID)),
				slog.String("lens_id", lensRow.PublicID.String()),
			)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "lens.unpublish",
			ActorID:      actorID,
			WorkspaceID:  ws.ID,
			ResourceType: "lens",
			ResourceID:   lensRow.PublicID.String(),
		})

		out := &UnpublishLensOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// GetPublic handles GET /public/lenses/{token}. This is an
// unauthenticated endpoint that returns a read-only view of a publicly
// shared lens. Rate-limited by per-IP middleware at the router level.
func GetPublic(deps Deps) func(context.Context, *GetPublicLensInput) (*GetPublicLensOutput, error) {
	return func(ctx context.Context, in *GetPublicLensInput) (*GetPublicLensOutput, error) {
		row, err := deps.Queries.FindLensByPublicToken(ctx, sql.NullString{
			String: in.Token,
			Valid:  true,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsLensPublicTokenInvalid, apierrors.InternalUnexpected))
		}

		return &GetPublicLensOutput{Body: rowToPublicLens(row)}, nil
	}
}

// actorInt64Ptr converts a uint32 actor id to a *int64 for eventbus.Event.
func actorInt64Ptr(id uint32) *int64 {
	v := int64(id)
	return &v
}
