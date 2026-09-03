// MCP tools over a user's favorites: listing them and adding one for a
// project, task, page, lens or timebox.

package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// validFavoriteTargetTypes enumerates the accepted target_type values for
// the add_favorite tool.
var validFavoriteTargetTypes = map[string]generated.UserFavoritesTargetType{
	"project": generated.UserFavoritesTargetTypeProject,
	"task":    generated.UserFavoritesTargetTypeTask,
	"page":    generated.UserFavoritesTargetTypePage,
	"lens":    generated.UserFavoritesTargetTypeLens,
	"timebox": generated.UserFavoritesTargetTypeTimebox,
}

func runListFavorites(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		Limit  int32 `json:"limit"`
		Offset int32 `json:"offset"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Limit <= 0 || in.Limit > 50 {
		in.Limit = 50
	}
	rows, err := deps.Queries.ListFavoritesForUser(ctx, generated.ListFavoritesForUserParams{
		WorkspaceID: s.workspaceID,
		UserID:      s.userID,
		Limit:       in.Limit,
		Offset:      in.Offset,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	type favOut struct {
		ID         string `json:"id"`
		TargetType string `json:"targetType"`
		TargetID   string `json:"targetId"`
		CreatedAt  int64  `json:"createdAt"`
	}
	out := make([]favOut, 0, len(rows))
	for _, r := range rows {
		out = append(out, favOut{
			ID:         r.PublicID.String(),
			TargetType: string(r.TargetType),
			TargetID:   r.TargetPublicID.String(),
			CreatedAt:  r.CreatedAt.Unix(),
		})
	}
	return map[string]any{"favorites": out}, nil
}

func runAddFavorite(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		TargetType string `json:"targetType"`
		TargetID   string `json:"targetId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	tt, ok := validFavoriteTargetTypes[in.TargetType]
	if !ok {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	targetPub, err := types.Parse(in.TargetID)
	if err != nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if err := ensureMCPFavoriteTargetExists(ctx, deps, s, tt, in.TargetID, targetPub); err != nil {
		return nil, err
	}
	// Check for existing favorite to avoid duplicates.
	_, err = deps.Queries.FindFavoriteByTarget(ctx, generated.FindFavoriteByTargetParams{
		WorkspaceID:    s.workspaceID,
		UserID:         s.userID,
		TargetType:     tt,
		TargetPublicID: targetPub,
	})
	if err == nil {
		// Already favorited, return idempotent success.
		return map[string]any{"ok": true, "alreadyFavorited": true}, nil
	}
	if !stderrors.Is(err, sql.ErrNoRows) {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	pub := newPublicID()
	if _, err := deps.Queries.CreateFavorite(ctx, generated.CreateFavoriteParams{
		PublicID:       pub,
		WorkspaceID:    s.workspaceID,
		UserID:         s.userID,
		TargetType:     tt,
		TargetPublicID: targetPub,
		FolderName:     sql.NullString{},
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	// The favorite row is committed, and the tool short-circuits on an
	// existing favorite, so a retry returns early without reaching this
	// append: propagating would report a failure nothing can repair.
	recordMutation(ctx, deps, s, mutation{
		EventType:    eventbus.FavoriteAdded,
		AuditAction:  "favorite.create",
		ResourceType: "favorite",
		ResourceID:   pub.String(),
		Payload:      map[string]any{"targetType": in.TargetType, "targetId": in.TargetID, "via": "mcp"},
		CallSite:     "mcp.add_favorite",
	})
	return map[string]any{"ok": true, "id": pub.String()}, nil
}

// ensureMCPFavoriteTargetExists refuses a favorite whose target the caller
// cannot reach. Tenancy alone is not enough for tasks: task rows carry a
// Layer-4 visibility of their own, so probing them with a workspace-scoped
// lookup answers "does this id name a task in your workspace?" for a task
// the caller is not allowed to see. A favorite is a per-user row that grants
// nothing, but the accept/reject split still reports existence, and one bit
// per guess is all an oracle needs.
//
// The task branch therefore goes through resolveTask, the same gate every
// other task-touching tool uses, and returns its WS.TASK.NOT_FOUND verbatim
// — the invisible and the absent answer identically. The other target types
// have no per-row visibility, so workspace scope is the whole rule for them.
func ensureMCPFavoriteTargetExists(
	ctx context.Context,
	deps Deps,
	s *session,
	targetType generated.UserFavoritesTargetType,
	targetID string,
	targetPublicID types.PublicID,
) error {
	q := deps.Queries
	var err error
	switch targetType {
	case generated.UserFavoritesTargetTypeProject:
		_, err = q.FindProjectByPublicId(ctx, generated.FindProjectByPublicIdParams{WorkspaceID: s.workspaceID, PublicID: targetPublicID})
	case generated.UserFavoritesTargetTypeTask:
		_, _, taskErr := resolveTask(ctx, deps, s, targetID)
		return taskErr
	case generated.UserFavoritesTargetTypePage:
		_, err = q.GetPageByPublicId(ctx, generated.GetPageByPublicIdParams{WorkspaceID: s.workspaceID, PublicID: targetPublicID})
	case generated.UserFavoritesTargetTypeLens:
		_, err = q.GetLensByPublicID(ctx, generated.GetLensByPublicIDParams{WorkspaceID: s.workspaceID, PublicID: targetPublicID})
	case generated.UserFavoritesTargetTypeTimebox:
		_, err = q.GetTimeboxByPublicId(ctx, generated.GetTimeboxByPublicIdParams{WorkspaceID: s.workspaceID, PublicID: targetPublicID})
	default:
		err = sql.ErrNoRows
	}
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return apierrors.New(apierrors.McpToolArgumentsInvalid)
		}
		return apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return nil
}
