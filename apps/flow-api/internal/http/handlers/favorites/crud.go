package favorites

import (
	"context"
	"database/sql"
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/resolve"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
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

		if err := dbretry.InTx(ctx, deps.DB, "favorites.Create", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			if _, err := generated.New(tx).CreateFavorite(ctx, generated.CreateFavoriteParams{
				PublicID:       pub,
				WorkspaceID:    wsID,
				UserID:         actorID,
				TargetType:     targetType,
				TargetPublicID: targetPub,
				FolderName:     folderName,
			}); err != nil {
				return err
			}
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: wsID}, mutationlog.Mutation{
				EventType:    eventbus.FavoriteAdded,
				AuditAction:  "favorite.create",
				ResourceType: "favorite",
				ResourceID:   pub.String(),
				Payload: map[string]any{
					"favoriteId": pub.String(),
					"targetType": in.Body.TargetType,
					"targetId":   in.Body.TargetID,
				},
				CallSite: "favorites.Create",
			})
		}); err != nil {
			if isDuplicateEntry(err) {
				return nil, httpErr(apierrors.WsFavoriteAlreadyExists)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
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
		access, err := acl.AuthorizeTaskAccess(ctx, deps.DB, targetPublicID.UUID(), actorID, apierrors.WsTaskAccessDenied)
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
		var missing bool
		if err := dbretry.InTx(ctx, deps.DB, "favorites.Delete", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			rows, err := generated.New(tx).DisableFavorite(ctx, generated.DisableFavoriteParams{
				WorkspaceID: wsID,
				PublicID:    pub,
				UserID:      actorID,
			})
			if err != nil {
				return err
			}
			if missing = rows == 0; missing {
				return nil
			}
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: row.WorkspaceID}, mutationlog.Mutation{
				EventType:    eventbus.FavoriteRemoved,
				AuditAction:  "favorite.delete",
				ResourceType: "favorite",
				ResourceID:   pub.String(),
				Payload: map[string]any{
					"favoriteId": pub.String(),
					"targetType": string(row.TargetType),
					"targetId":   row.TargetPublicID.String(),
				},
				CallSite: "favorites.Delete",
			})
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if missing {
			return nil, httpErr(apierrors.WsFavoriteNotFound)
		}

		out := &DeleteFavoriteOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
