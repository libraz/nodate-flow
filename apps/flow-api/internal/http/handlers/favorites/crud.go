package favorites

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/resolve"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// Create handles POST /me/favorites.
func Create(deps Deps) func(context.Context, *CreateFavoriteInput) (*CreateFavoriteOutput, error) {
	return func(ctx context.Context, in *CreateFavoriteInput) (*CreateFavoriteOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}

		wsID, err := resolve.WorkspaceMember(ctx, deps.DB, in.Body.WorkspaceID, actorID)
		if err != nil {
			return nil, err
		}

		targetPub, err := types.Parse(in.Body.TargetID)
		if err != nil {
			return nil, httpErr(apierrors.WsFavoriteNotFound)
		}
		targetType := generated.UserFavoritesTargetType(in.Body.TargetType)
		if err := ensureFavoriteTargetExists(ctx, deps, wsID, actorID, targetType, targetPub); err != nil {
			return nil, httpErr(apierrors.WsFavoriteNotFound)
		}

		// Check for duplicate favorite on the same target.
		_, dupErr := deps.Queries.FindFavoriteByTarget(ctx, generated.FindFavoriteByTargetParams{
			WorkspaceID:    wsID,
			UserID:         actorID,
			TargetType:     targetType,
			TargetPublicID: targetPub,
		})
		if dupErr == nil {
			return nil, httpErr(apierrors.WsFavoriteAlreadyExists)
		}
		if !errors.Is(dupErr, sql.ErrNoRows) {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		pub := types.New()
		folderName := sql.NullString{}
		if in.Body.FolderName != "" {
			folderName = sql.NullString{String: in.Body.FolderName, Valid: true}
		}

		if _, err := deps.Queries.CreateFavorite(ctx, generated.CreateFavoriteParams{
			PublicID:       pub,
			WorkspaceID:    wsID,
			UserID:         actorID,
			TargetType:     targetType,
			TargetPublicID: targetPub,
			FolderName:     folderName,
		}); err != nil {
			if isDuplicateEntry(err) {
				return nil, httpErr(apierrors.WsFavoriteAlreadyExists)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.FavoriteAdded,
			WorkspaceID: wsID,
			ActorUserID: actorPtr(ctx),
			Payload: map[string]any{
				"favoriteId": pub.String(),
				"targetType": in.Body.TargetType,
				"targetId":   in.Body.TargetID,
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "favorites.Create"),
				slog.String("event_type", string(eventbus.FavoriteAdded)),
				slog.String("workspace_public_id", in.Body.WorkspaceID),
				slog.String("favorite_public_id", pub.String()),
			)
		}

		if deps.Audit != nil {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "favorite.create",
				ActorID:      actorID,
				WorkspaceID:  wsID,
				ResourceType: "favorite",
				ResourceID:   pub.String(),
				Metadata:     map[string]any{"targetType": in.Body.TargetType, "targetId": in.Body.TargetID},
			})
		}

		row, err := deps.Queries.FindFavoriteByPublicId(ctx, generated.FindFavoriteByPublicIdParams{
			WorkspaceID: wsID,
			PublicID:    pub,
			UserID:      actorID,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return &CreateFavoriteOutput{Body: mapFindRow(row)}, nil
	}
}

// ensureFavoriteTargetExists refuses a favorite whose target the caller
// cannot reach. For tasks that means the Layer-4 visibility ACL, not just
// workspace scope: a workspace-scoped lookup answers "does this id name a
// task here?" for a task the caller may not see, and the caller learns the
// answer from whether the favorite was accepted. Every rejection surfaces as
// WS.FAVORITE.NOT_FOUND at the call site, so invisible and absent are the
// same response.
//
// The other target types carry no per-row visibility, so workspace scope is
// the entire rule for them.
func ensureFavoriteTargetExists(
	ctx context.Context,
	deps Deps,
	workspaceID uint32,
	actorID uint32,
	targetType generated.UserFavoritesTargetType,
	targetPublicID types.PublicID,
) error {
	q := deps.Queries
	switch targetType {
	case generated.UserFavoritesTargetTypeProject:
		_, err := q.FindProjectByPublicId(ctx, generated.FindProjectByPublicIdParams{WorkspaceID: workspaceID, PublicID: targetPublicID})
		return err
	case generated.UserFavoritesTargetTypeTask:
		access, err := acl.AuthorizeTaskAccess(ctx, deps.DB, targetPublicID.UUID(), actorID)
		if err != nil {
			return err
		}
		if access.Task.WorkspaceID != workspaceID {
			return sql.ErrNoRows
		}
		return nil
	case generated.UserFavoritesTargetTypePage:
		_, err := q.GetPageByPublicId(ctx, generated.GetPageByPublicIdParams{WorkspaceID: workspaceID, PublicID: targetPublicID})
		return err
	case generated.UserFavoritesTargetTypeLens:
		_, err := q.GetLensByPublicID(ctx, generated.GetLensByPublicIDParams{WorkspaceID: workspaceID, PublicID: targetPublicID})
		return err
	case generated.UserFavoritesTargetTypeTimebox:
		_, err := q.GetTimeboxByPublicId(ctx, generated.GetTimeboxByPublicIdParams{WorkspaceID: workspaceID, PublicID: targetPublicID})
		return err
	default:
		return sql.ErrNoRows
	}
}

// List handles GET /me/favorites.
func List(deps Deps) func(context.Context, *ListFavoritesInput) (*ListFavoritesOutput, error) {
	return func(ctx context.Context, in *ListFavoritesInput) (*ListFavoritesOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}

		wsID, err := resolve.WorkspaceMember(ctx, deps.DB, in.WorkspaceID, actorID)
		if err != nil {
			return nil, err
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		rows, err := deps.Queries.ListFavoritesForUser(ctx, generated.ListFavoritesForUserParams{
			WorkspaceID: wsID,
			UserID:      actorID,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListFavoritesOutput{}
		out.Body.Favorites = make([]Favorite, 0, len(rows))
		for _, r := range rows {
			out.Body.Favorites = append(out.Body.Favorites, mapListRow(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// Delete handles DELETE /me/favorites/{id}.
func Delete(deps Deps) func(context.Context, *DeleteFavoriteInput) (*DeleteFavoriteOutput, error) {
	return func(ctx context.Context, in *DeleteFavoriteInput) (*DeleteFavoriteOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}

		wsID, err := resolve.WorkspaceMember(ctx, deps.DB, in.WorkspaceID, actorID)
		if err != nil {
			return nil, err
		}

		pub, err := types.Parse(in.ID)
		if err != nil {
			return nil, httpErr(apierrors.WsFavoriteNotFound)
		}

		// Look up the favorite to get the workspace for the event.
		row, err := deps.Queries.FindFavoriteByPublicId(ctx, generated.FindFavoriteByPublicIdParams{
			WorkspaceID: wsID,
			PublicID:    pub,
			UserID:      actorID,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsFavoriteNotFound, apierrors.InternalUnexpected))
		}

		// Scoped to this user's live favorites, so a zero count means there
		// is nothing here to un-favorite.
		rows, err := deps.Queries.DisableFavorite(ctx, generated.DisableFavoriteParams{
			WorkspaceID: wsID,
			PublicID:    pub,
			UserID:      actorID,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if rows == 0 {
			return nil, httpErr(apierrors.WsFavoriteNotFound)
		}

		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.FavoriteRemoved,
			WorkspaceID: row.WorkspaceID,
			ActorUserID: actorPtr(ctx),
			Payload: map[string]any{
				"favoriteId": pub.String(),
				"targetType": string(row.TargetType),
				"targetId":   row.TargetPublicID.String(),
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "favorites.Delete"),
				slog.String("event_type", string(eventbus.FavoriteRemoved)),
				slog.String("workspace_public_id", in.WorkspaceID),
				slog.String("favorite_public_id", pub.String()),
			)
		}

		if deps.Audit != nil {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "favorite.delete",
				ActorID:      actorID,
				WorkspaceID:  row.WorkspaceID,
				ResourceType: "favorite",
				ResourceID:   pub.String(),
			})
		}

		out := &DeleteFavoriteOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// actorPtr delegates to handlerutil.ActorPtr.
var actorPtr = handlerutil.ActorPtr
