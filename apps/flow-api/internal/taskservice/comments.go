package taskservice

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mentions"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
)

const (
	AuthorKindUser  = "user"
	AuthorKindAgent = "agent"
)

// Comment is the public service result for an appended comment. AuthorID is
// populated for a human comment; AuthorAgentID is populated for an agent
// comment. Both are public UUIDs.
type Comment struct {
	ID                types.PublicID
	AuthorID          string
	AuthorKind        string
	AuthorAgentID     string
	AuthorDisplayName string
	Body              string
	CreatedAt         time.Time
}

// AddComment creates a comment, refreshes its mention cache, and records the
// comment event and audit entry in one retryable transaction.
func (s *Service) AddComment(ctx context.Context, principal execution.Principal, target types.PublicID, body string) (Comment, error) {
	if err := requireCommentBody(body); err != nil {
		return Comment{}, err
	}
	if err := s.checkReadiness(true); err != nil {
		return Comment{}, err
	}
	var out Comment
	err := dbretry.InTx(ctx, s.DB, "taskservice.AddComment", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		authz, err := execution.AuthorizeTaskTx(ctx, tx, principal, target, execution.CapabilityAddComment)
		if err != nil {
			return err
		}
		qtx := s.Queries.WithTx(tx.RawTx())
		commentID := types.New()
		params := generated.AddCommentParams{
			PublicID:    commentID,
			WorkspaceID: principal.WorkspaceID(),
			TaskID:      sql.NullInt32{Int32: int32(authz.Task.ID), Valid: true}, //#nosec G115 -- tasks.id is INT UNSIGNED and fits int32 within realistic deployments
			AuthorKind:  generated.CommentsAuthorKindUser,
			// The zero public id is stored as NULL for a human comment.
			AuthorAgentPublicID: types.PublicID{},
			Body:                body,
		}
		actor := mutationlog.Actor{UserID: principal.UserID(), WorkspaceID: principal.WorkspaceID()}
		var (
			actorUserID  *int64
			authorPublic string
			authorName   string
		)
		if authz.Agent != nil {
			params.AuthorKind = generated.CommentsAuthorKindAgent
			params.AuthorAgentID = sql.NullInt32{Int32: int32(authz.Agent.ID), Valid: true} //#nosec G115 -- ai_agents.id is INT UNSIGNED and fits int32 within realistic deployments
			params.AuthorAgentPublicID = authz.Agent.PublicID
			params.AuthorAgentName = sql.NullString{String: authz.Agent.Name, Valid: true}
			actor.UserID = 0
			authorPublic = authz.Agent.PublicID.String()
			authorName = authz.Agent.Name
		} else {
			userID := principal.UserID()
			params.AuthorID = sql.NullInt32{Int32: int32(userID), Valid: true} //#nosec G115 -- users.id is INT UNSIGNED and fits int32 within realistic deployments
			userActor := int64(userID)
			actorUserID = &userActor
			author, err := qtx.LockCommentHumanAuthor(ctx, userID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return apierrors.New(apierrors.WsTaskAccessDenied)
				}
				return err
			}
			authorPublic = author.PublicID.String()
			authorName = author.DisplayName
		}

		commentInternalID, err := qtx.AddComment(ctx, params)
		if err != nil {
			return err
		}
		if commentInternalID <= 0 || commentInternalID > int64(^uint32(0)) {
			return apierrors.New(apierrors.InternalUnexpected)
		}

		if err := mentions.SyncComment(ctx, tx, mentions.CommentArgs{
			WorkspaceID:     principal.WorkspaceID(),
			TaskID:          authz.Task.ID,
			TaskPublicID:    target,
			CommentID:       uint32(commentInternalID), //#nosec G115 -- validated within uint32 range above
			CommentPublicID: commentID,
			ActorUserID:     actorUserID,
			ActorAgentID:    actorAgentIDPointer(authz.Agent),
			Body:            body,
		}); err != nil {
			return err
		}

		taskID := internalTaskID(authz.Task.ID)
		mutation := mutationlog.Mutation{
			EventType:    eventbus.TaskCommentAdded,
			AuditAction:  "comment.create",
			ResourceType: "comment",
			ResourceID:   commentID.String(),
			TaskID:       &taskID,
			Payload: map[string]any{
				"taskId":     target.String(),
				"commentId":  commentID.String(),
				"authorKind": string(params.AuthorKind),
			},
			CallSite: "taskservice.AddComment",
		}
		if authz.Agent != nil {
			agentID := internalAgentID(authz.Agent.ID)
			mutation.ActorAgentID = &agentID
			mutation.Payload["authorAgentId"] = authz.Agent.PublicID.String()
			if authz.DelegatedByPublicID != (types.PublicID{}) {
				mutation.Payload["delegatedByUserId"] = authz.DelegatedByPublicID.String()
			}
		}
		if err := s.Mutations.RecordInTx(ctx, tx, actor, mutation); err != nil {
			return err
		}

		createdAt, err := qtx.GetCommentCreatedAt(ctx, generated.GetCommentCreatedAtParams{
			WorkspaceID: principal.WorkspaceID(),
			PublicID:    commentID,
		})
		if err != nil {
			return err
		}
		out = Comment{
			ID:                commentID,
			AuthorID:          authorPublic,
			AuthorKind:        string(params.AuthorKind),
			AuthorAgentID:     "",
			AuthorDisplayName: authorName,
			Body:              body,
			CreatedAt:         createdAt,
		}
		if authz.Agent != nil {
			out.AuthorID = ""
			out.AuthorAgentID = authorPublic
		}
		return nil
	})
	if err != nil {
		return Comment{}, err
	}
	return out, nil
}

func actorAgentIDPointer(agent *execution.AgentIdentity) *int64 {
	if agent == nil {
		return nil
	}
	id := internalAgentID(agent.ID)
	return &id
}
