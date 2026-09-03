// MCP tools over emoji reactions on a task: adding one and listing the
// reactions a task carries.

package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// reactionListLimit bounds the reactions returned by the MCP
// list_reactions tool. Reactions are a small per-task collection, so a
// fixed upper bound keeps the result set bounded without a paging cursor.
const reactionListLimit int32 = 200

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
	taskInternal, _, err := resolveTaskForWrite(ctx, deps, s, in.TaskID)
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
	// Same shape as add_favorite: committed row, and a retry returns on
	// the existing-reaction check before it could re-append.
	recordMutation(ctx, deps, s, mutation{
		EventType:    eventbus.ReactionAdded,
		AuditAction:  "reaction.create",
		ResourceType: "reaction",
		ResourceID:   pub.String(),
		TaskID:       &taskID64,
		Payload:      map[string]any{"taskId": in.TaskID, "emoji": in.Emoji, "via": "mcp"},
		CallSite:     "mcp.add_reaction",
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
