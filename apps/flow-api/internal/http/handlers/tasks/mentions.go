package tasks

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mentions"
)

// syncCommentMentions re-derives the mentions a comment names, on the
// boundary the comment body itself commits through.
//
// A deleted comment passes an empty body: the delete is a soft one, so no
// foreign key cascade reaches the mention rows and a comment removed from
// the thread would otherwise keep naming people it no longer shows.
func syncCommentMentions(ctx context.Context, db dbretry.CommitBoundary, workspaceID uint32, task middleware.TaskContext, commentID uint32, commentPub types.PublicID, actor *int64, body string) error {
	return mentions.SyncComment(ctx, db, mentions.CommentArgs{
		WorkspaceID:     workspaceID,
		TaskID:          task.ID,
		TaskPublicID:    types.FromUUID(task.PublicID),
		CommentID:       commentID,
		CommentPublicID: commentPub,
		ActorUserID:     actor,
		Body:            body,
	})
}
