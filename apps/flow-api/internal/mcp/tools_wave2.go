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

// reactionListLimit bounds the reactions returned by the MCP
// list_reactions tool. Reactions are a small per-task collection, so a
// fixed upper bound keeps the result set bounded without a paging cursor.
const reactionListLimit int32 = 200

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
	if err := ensureMCPFavoriteTargetExists(ctx, deps.Queries, s.workspaceID, tt, targetPub); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
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
	actor := int64(s.userID)
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.FavoriteAdded,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		Payload:     map[string]any{"targetType": in.TargetType, "targetId": in.TargetID, "via": "mcp"},
	})
	return map[string]any{"ok": true, "id": pub.String()}, nil
}

func ensureMCPFavoriteTargetExists(
	ctx context.Context,
	q *generated.Queries,
	workspaceID uint32,
	targetType generated.UserFavoritesTargetType,
	targetPublicID types.PublicID,
) error {
	switch targetType {
	case generated.UserFavoritesTargetTypeProject:
		_, err := q.FindProjectByPublicId(ctx, generated.FindProjectByPublicIdParams{WorkspaceID: workspaceID, PublicID: targetPublicID})
		return err
	case generated.UserFavoritesTargetTypeTask:
		_, err := loadTaskRow(ctx, q, workspaceID, targetPublicID)
		return err
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

func runAddReaction(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		TaskID string `json:"taskId"`
		Emoji  string `json:"emoji"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Emoji == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	taskInternal, _, err := resolveTask(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	taskNullID := sql.NullInt32{Int32: int32(taskInternal), Valid: true} //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	// Check for existing reaction to avoid duplicates.
	_, err = deps.Queries.FindExistingReaction(ctx, generated.FindExistingReactionParams{
		UserID: s.userID,
		TaskID: taskNullID,
		Emoji:  in.Emoji,
	})
	if err == nil {
		return map[string]any{"ok": true, "alreadyReacted": true}, nil
	}
	if !stderrors.Is(err, sql.ErrNoRows) {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	pub := newPublicID()
	if _, err := deps.Queries.CreateReaction(ctx, generated.CreateReactionParams{
		PublicID:    pub,
		WorkspaceID: s.workspaceID,
		UserID:      s.userID,
		TaskID:      taskNullID,
		CommentID:   sql.NullInt32{},
		Emoji:       in.Emoji,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	taskID64 := int64(taskInternal)
	actor := int64(s.userID)
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.ReactionAdded,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		TaskID:      &taskID64,
		Payload:     map[string]any{"taskId": in.TaskID, "emoji": in.Emoji, "via": "mcp"},
	})
	return map[string]any{"ok": true, "id": pub.String()}, nil
}

func runListReactions(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		TaskID string `json:"taskId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	taskInternal, _, err := resolveTask(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	rows, err := deps.Queries.ListReactionsForTask(ctx, generated.ListReactionsForTaskParams{
		TaskID: sql.NullInt32{Int32: int32(taskInternal), Valid: true}, //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		Limit:  reactionListLimit,
		Offset: 0,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	type reactionOut struct {
		ID          string `json:"id"`
		Emoji       string `json:"emoji"`
		UserID      string `json:"userId"`
		DisplayName string `json:"displayName"`
		CreatedAt   int64  `json:"createdAt"`
	}
	out := make([]reactionOut, 0, len(rows))
	for _, r := range rows {
		out = append(out, reactionOut{
			ID:          r.PublicID.String(),
			Emoji:       r.Emoji,
			UserID:      r.UserPublicID.String(),
			DisplayName: r.DisplayName,
			CreatedAt:   r.CreatedAt.Unix(),
		})
	}
	return map[string]any{"reactions": out}, nil
}

func runListRecent(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		Limit int32 `json:"limit"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Limit <= 0 || in.Limit > 20 {
		in.Limit = 20
	}
	rows, err := deps.Queries.ListRecentVisitsForUser(ctx, generated.ListRecentVisitsForUserParams{
		WorkspaceID: s.workspaceID,
		UserID:      s.userID,
		Limit:       in.Limit,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	type visitOut struct {
		ID         string `json:"id"`
		EntityType string `json:"entityType"`
		EntityID   string `json:"entityId"`
		Title      string `json:"title,omitempty"`
		VisitedAt  int64  `json:"visitedAt"`
	}
	out := make([]visitOut, 0, len(rows))
	for _, r := range rows {
		v := visitOut{
			ID:         r.PublicID.String(),
			EntityType: string(r.EntityType),
			EntityID:   r.EntityPublicID.String(),
		}
		if r.EntityTitle.Valid {
			v.Title = r.EntityTitle.String
		}
		if r.UpdatedAt.Valid {
			v.VisitedAt = r.UpdatedAt.Time.Unix()
		} else {
			v.VisitedAt = r.CreatedAt.Unix()
		}
		out = append(out, v)
	}
	return map[string]any{"recentVisits": out}, nil
}
