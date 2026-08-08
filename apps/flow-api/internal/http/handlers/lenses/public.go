package lenses

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
	sharedtoken "github.com/libraz/nodate-flow/packages/go-shared/token"
)

// Publish handles POST /workspaces/{wsId}/lenses/{lensId}/publish.
// It mints a capability token, stores only its SHA-256, and marks the
// lens as publicly accessible. Returns 409 if the lens is already
// public.
//
// The plaintext token is returned to the publisher exactly once and is
// not written anywhere else — not to the event payload, not to the
// audit log, not back out of the lens read endpoints. Those records
// outlive the share: the event log is append-only, so a token recorded
// at publish time stays readable to every workspace member after the
// lens is unpublished and after the recorder has left. lensId is the
// identifier those records need; the token is a credential and belongs
// only in the URL its holder was given.
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
		if err := requireLensShareOwner(ctx, deps, ws, actorID, lensRow.CreatorPublicID); err != nil {
			return nil, err
		}

		// Mint through the shared token package so lens shares stay in
		// lockstep with calendar shares and invite magic links: one
		// alphabet, one entropy budget, one hash encoding.
		token, tokenHash, err := sharedtoken.MintToken()
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// SetLensPublic is a no-op when is_public = TRUE (WHERE guard).
		// The already-public case is answered from lensRow just below, which
		// distinguishes it from a missing view; the count cannot, because it
		// is zero for both.
		if _, err := deps.Queries.SetLensPublic(ctx, generated.SetLensPublicParams{
			PublicTokenHash: sql.NullString{String: tokenHash, Valid: true},
			WorkspaceID:     ws.ID,
			PublicID:        lid,
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
				"lensId": lensRow.PublicID.String(),
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "lenses.Publish"),
				slog.String("event_type", string(eventbus.LensShared)),
				logutil.LogEntity("workspace", ws.PublicID),
				slog.String("lens_id", lensRow.PublicID.String()),
			)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "lens.publish",
			ActorID:      actorID,
			WorkspaceID:  ws.ID,
			ResourceType: "lens",
			ResourceID:   lensRow.PublicID.String(),
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
		if err := requireLensShareOwner(ctx, deps, ws, actorID, lensRow.CreatorPublicID); err != nil {
			return nil, err
		}

		// SetLensPrivate is a no-op when is_public = FALSE (WHERE guard).
		// See Publish: the already-private case is answered from lensRow,
		// which the count cannot distinguish from a missing view.
		if _, err := deps.Queries.SetLensPrivate(ctx, generated.SetLensPrivateParams{
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
				logutil.LogEntity("workspace", ws.PublicID),
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

// requireLensShareOwner gates publishing and unpublishing a lens to its
// creator and to workspace admins / owners.
//
// Publishing puts a projection of the workspace's tasks on an
// unauthenticated URL, so "any workspace member who can see the lens" is
// too wide a gate: the workspace role floor already keeps guests out, and
// this narrows the remaining members to the people who own the view or
// administer the workspace. It mirrors the calendar public-share rule
// (resolveWorkspaceNonGuest / resolveWorkspaceAdmin).
func requireLensShareOwner(
	ctx context.Context,
	deps Deps,
	ws middleware.WorkspaceContext,
	actorID uint32,
	creatorPublicID types.PublicID,
) error {
	if ws.Role.AtLeast(middleware.WorkspaceRoleAdmin) {
		return nil
	}
	profile, err := deps.Queries.FindUserProfileById(ctx, actorID)
	if err != nil {
		return httpErr(apierr.SpecForErrNoRows(err, apierrors.WsMemberRoleDenied, apierrors.InternalUnexpected))
	}
	if profile.PublicID != creatorPublicID {
		return httpErr(apierrors.WsMemberRoleDenied)
	}
	return nil
}

// GetPublic handles GET /public/lenses/{token}. This is an
// unauthenticated endpoint that returns a read-only view of a publicly
// shared lens together with the resolved task list. Rate-limited by
// per-IP middleware at the router level. The task list is hard-capped
// at publicLensTaskCap rows; public shares are not paginated because
// they are intentionally a small projection, not a free data dump.
//
// The token's shape is not prescribed here: the SHA-256 is computed
// over the raw path parameter, so anything that is not a live token —
// including the empty string — lands in the token-invalid path rather
// than in a validation error that would confirm the format.
func GetPublic(deps Deps) func(context.Context, *GetPublicLensInput) (*GetPublicLensOutput, error) {
	return func(ctx context.Context, in *GetPublicLensInput) (*GetPublicLensOutput, error) {
		if in.Token == "" {
			return nil, httpErr(apierrors.WsLensPublicTokenInvalid)
		}
		row, err := deps.Queries.FindLensByPublicTokenHash(ctx, sql.NullString{
			String: sharedtoken.HashToken(in.Token),
			Valid:  true,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsLensPublicTokenInvalid, apierrors.InternalUnexpected))
		}

		body := rowToPublicLens(row)

		tasks, err := resolvePublicLensTasks(ctx, deps.DB, row)
		if err != nil {
			slog.ErrorContext(ctx, "resolvePublicLensTasks failed",
				slog.Any("err", err),
				slog.String("handler", "lenses.GetPublic"),
				slog.String("lens_id", row.PublicID.String()),
			)
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		body.Tasks = tasks

		return &GetPublicLensOutput{Body: body}, nil
	}
}

// actorInt64Ptr converts a uint32 actor id to a *int64 for eventbus.Event.
func actorInt64Ptr(id uint32) *int64 {
	v := int64(id)
	return &v
}
