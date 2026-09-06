package mcp

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mentions"
)

// syncTaskDescriptionMentions re-derives the mentions a task's description
// names, on the boundary the description itself commits through.
//
// The session supplies the workspace and the actor, so a tool states only
// the task whose body moved.
func syncTaskDescriptionMentions(ctx context.Context, db dbretry.CommitBoundary, s *session, taskID uint32, taskPub types.PublicID, body string) error {
	return mentions.SyncTaskDescription(ctx, db, mentions.TaskDescriptionArgs{
		WorkspaceID:  s.workspaceID,
		TaskID:       taskID,
		TaskPublicID: taskPub,
		ActorUserID:  sessionActor(s),
		Body:         body,
	})
}

// syncCommentMentions re-derives the mentions a comment names, on the
// boundary the comment body itself commits through.
func syncCommentMentions(ctx context.Context, db dbretry.CommitBoundary, s *session, taskID uint32, taskPub types.PublicID, commentID uint32, commentPub types.PublicID, body string) error {
	return mentions.SyncComment(ctx, db, mentions.CommentArgs{
		WorkspaceID:     s.workspaceID,
		TaskID:          taskID,
		TaskPublicID:    taskPub,
		CommentID:       commentID,
		CommentPublicID: commentPub,
		ActorUserID:     sessionActor(s),
		Body:            body,
	})
}

// sessionActor widens the session's user id onto the optional actor shape
// the mention sync takes. A session always carries one, so the pointer is
// never nil here; the parameter is optional for the paths where a process
// rather than a person wrote the body.
func sessionActor(s *session) *int64 {
	id := int64(s.userID)
	return &id
}
