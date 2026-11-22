package taskservice

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mentions"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

const (
	AuthorKindUser  = "user"
	AuthorKindAgent = "agent"
)

var (
	// ErrCommentAuthor is returned by [WriteComment] unless exactly one of
	// the human and agent author ids is set and in range.
	ErrCommentAuthor = errors.New("taskservice: a comment needs exactly one human or agent author")
	// ErrCommentAgentUnavailable is returned by [WriteComment] when the
	// agent author is not an enabled agent of the workspace.
	ErrCommentAgentUnavailable = errors.New("taskservice: comment agent author is not enabled in the workspace")
	// ErrCommentSourceTaken is returned by [WriteComment] when the task
	// already holds a live comment with the same source uid.
	ErrCommentSourceTaken = errors.New("taskservice: a comment with this source uid already exists on the task")
	// ErrCommentNotFound is returned by [DisableComment] when no live
	// comment matched, including when a concurrent delete won.
	ErrCommentNotFound = errors.New("taskservice: no live comment matched")
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

// WriteArgs describes one comment for [WriteComment].
type WriteArgs struct {
	// Actor makes the change and is attributed on the event; its
	// WorkspaceID scopes every write.
	Actor        mutationlog.Actor
	TaskID       uint32
	TaskPublicID types.PublicID
	// AuthorUserID and AuthorAgentID are the comment's author; exactly one
	// is set. The author is also the actor of the mentions the body makes.
	AuthorUserID  sql.NullInt64
	AuthorAgentID sql.NullInt64
	Body          string
	// CreatedAt is stored as the comment's creation time; zero means now.
	CreatedAt time.Time
	// SourceUID is the namespaced id of an imported comment; empty stores NULL.
	SourceUID string
	// Payload holds event payload fields beyond taskId and commentId, which
	// the kit sets.
	Payload map[string]any
}

// WrittenComment identifies the row [WriteComment] inserted.
type WrittenComment struct {
	ID       uint32
	PublicID types.PublicID
	// Payload is the event payload as appended, for the caller's audit row.
	Payload map[string]any
}

// EditArgs describes one body replacement for [EditComment].
type EditArgs struct {
	Actor           mutationlog.Actor
	CommentID       uint32
	CommentPublicID types.PublicID
	TaskID          uint32
	TaskPublicID    types.PublicID
	// AuthorUserID is the comment's stored human author; the UPDATE matches
	// on it and it is the actor of the re-derived mentions.
	AuthorUserID uint32
	Body         string
}

// DisableArgs describes one soft delete for [DisableComment].
type DisableArgs struct {
	Actor           mutationlog.Actor
	CommentID       uint32
	CommentPublicID types.PublicID
	TaskID          uint32
	TaskPublicID    types.PublicID
}

// WriteComment inserts a comment, syncs the mentions its body makes and
// appends task.comment.added on tx. It does not authorize; the caller
// records the audit row with RecordTxAudit after the commit.
func WriteComment(ctx context.Context, tx *dbretry.Tx, args WriteArgs) (WrittenComment, error) {
	if err := requireCommentBody(args.Body); err != nil {
		return WrittenComment{}, err
	}
	if args.AuthorUserID.Valid == args.AuthorAgentID.Valid {
		return WrittenComment{}, ErrCommentAuthor
	}
	q := generated.New(tx)
	pub := types.New()
	params := generated.AddCommentParams{
		PublicID:    pub,
		WorkspaceID: args.Actor.WorkspaceID,
		TaskID:      sql.NullInt32{Int32: int32(args.TaskID), Valid: true}, //#nosec G115 -- tasks.id is INT UNSIGNED and fits int32 within realistic deployments
		AuthorKind:  generated.CommentsAuthorKindUser,
		// The zero public id is stored as NULL for a human comment.
		AuthorAgentPublicID: types.PublicID{},
		Body:                args.Body,
	}
	var mentionUser, mentionAgent *int64
	if args.AuthorUserID.Valid {
		id, ok := int32ID(args.AuthorUserID.Int64)
		if !ok {
			return WrittenComment{}, ErrCommentAuthor
		}
		params.AuthorID = sql.NullInt32{Int32: id, Valid: true}
		v := args.AuthorUserID.Int64
		mentionUser = &v
	} else {
		id, ok := int32ID(args.AuthorAgentID.Int64)
		if !ok {
			return WrittenComment{}, ErrCommentAuthor
		}
		byline, err := q.GetCommentAgentByline(ctx, generated.GetCommentAgentBylineParams{
			WorkspaceID: args.Actor.WorkspaceID,
			ID:          uint32(id), //#nosec G115 -- int32ID admits only positive ids
		})
		if errors.Is(err, sql.ErrNoRows) {
			return WrittenComment{}, ErrCommentAgentUnavailable
		}
		if err != nil {
			return WrittenComment{}, err
		}
		params.AuthorKind = generated.CommentsAuthorKindAgent
		params.AuthorAgentID = sql.NullInt32{Int32: id, Valid: true}
		params.AuthorAgentPublicID = byline.PublicID
		params.AuthorAgentName = sql.NullString{String: byline.Name, Valid: true}
		v := args.AuthorAgentID.Int64
		mentionAgent = &v
	}
	if !args.CreatedAt.IsZero() {
		params.CreatedAt = args.CreatedAt
	}
	if args.SourceUID != "" {
		params.SourceUid = sql.NullString{String: args.SourceUID, Valid: true}
	}

	rowID, err := q.AddComment(ctx, params)
	if apierr.IsDuplicateEntry(err) {
		return WrittenComment{}, ErrCommentSourceTaken
	}
	if err != nil {
		return WrittenComment{}, err
	}
	if rowID <= 0 || rowID > math.MaxUint32 {
		return WrittenComment{}, apierrors.New(apierrors.InternalUnexpected)
	}
	commentID := uint32(rowID) //#nosec G115 -- validated within uint32 range above

	if err := mentions.SyncComment(ctx, tx, mentions.CommentArgs{
		WorkspaceID:     args.Actor.WorkspaceID,
		TaskID:          args.TaskID,
		TaskPublicID:    args.TaskPublicID,
		CommentID:       commentID,
		CommentPublicID: pub,
		ActorUserID:     mentionUser,
		ActorAgentID:    mentionAgent,
		Body:            args.Body,
	}); err != nil {
		return WrittenComment{}, err
	}

	payload := make(map[string]any, len(args.Payload)+2)
	for k, v := range args.Payload {
		payload[k] = v
	}
	payload["taskId"] = args.TaskPublicID.String()
	payload["commentId"] = pub.String()
	if err := appendCommentEvent(ctx, tx, eventbus.TaskCommentAdded, args.Actor, args.TaskID, payload); err != nil {
		return WrittenComment{}, err
	}
	return WrittenComment{ID: commentID, PublicID: pub, Payload: payload}, nil
}

// EditComment replaces a comment's body, re-derives its mentions and
// appends task.comment.edited on tx. It does not authorize. Re-submitting
// the current body is not an error: MySQL counts zero changed rows.
func EditComment(ctx context.Context, tx *dbretry.Tx, args EditArgs) error {
	if err := requireCommentBody(args.Body); err != nil {
		return err
	}
	author, ok := int32ID(int64(args.AuthorUserID))
	if !ok {
		return ErrCommentAuthor
	}
	q := generated.New(tx)
	if _, err := q.EditComment(ctx, generated.EditCommentParams{
		Body:        args.Body,
		WorkspaceID: args.Actor.WorkspaceID,
		PublicID:    args.CommentPublicID,
		AuthorID:    sql.NullInt32{Int32: author, Valid: true},
	}); err != nil {
		return err
	}
	mentionUser := int64(args.AuthorUserID)
	if err := mentions.SyncComment(ctx, tx, mentions.CommentArgs{
		WorkspaceID:     args.Actor.WorkspaceID,
		TaskID:          args.TaskID,
		TaskPublicID:    args.TaskPublicID,
		CommentID:       args.CommentID,
		CommentPublicID: args.CommentPublicID,
		ActorUserID:     &mentionUser,
		Body:            args.Body,
	}); err != nil {
		return err
	}
	return appendCommentEvent(ctx, tx, eventbus.TaskCommentEdited, args.Actor, args.TaskID, map[string]any{
		"taskId":    args.TaskPublicID.String(),
		"commentId": args.CommentPublicID.String(),
	})
}

// DisableComment soft-deletes a live comment, clears its mentions and
// appends task.comment.removed on tx. It does not authorize. The delete
// reaches no foreign key cascade, so the mention clear is what stops a
// removed comment from going on naming people.
func DisableComment(ctx context.Context, tx *dbretry.Tx, args DisableArgs) error {
	q := generated.New(tx)
	rows, err := q.DeleteComment(ctx, generated.DeleteCommentParams{
		WorkspaceID: args.Actor.WorkspaceID,
		PublicID:    args.CommentPublicID,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrCommentNotFound
	}
	if err := mentions.SyncComment(ctx, tx, mentions.CommentArgs{
		WorkspaceID:     args.Actor.WorkspaceID,
		TaskID:          args.TaskID,
		TaskPublicID:    args.TaskPublicID,
		CommentID:       args.CommentID,
		CommentPublicID: args.CommentPublicID,
		ActorUserID:     actorUserPtr(args.Actor),
		ActorAgentID:    actorAgentPtr(args.Actor),
		Body:            "",
	}); err != nil {
		return err
	}
	return appendCommentEvent(ctx, tx, eventbus.TaskCommentRemoved, args.Actor, args.TaskID, map[string]any{
		"taskId":    args.TaskPublicID.String(),
		"commentId": args.CommentPublicID.String(),
	})
}

func appendCommentEvent(ctx context.Context, tx *dbretry.Tx, kind eventbus.Kind, actor mutationlog.Actor, taskID uint32, payload map[string]any) error {
	task := internalTaskID(taskID)
	return eventbus.Append(ctx, tx, eventbus.Event{
		Type:         kind,
		WorkspaceID:  actor.WorkspaceID,
		ActorUserID:  actorUserPtr(actor),
		ActorAgentID: actorAgentPtr(actor),
		TaskID:       &task,
		Payload:      payload,
	})
}

func actorUserPtr(a mutationlog.Actor) *int64 {
	if a.UserID == 0 || a.AgentID != 0 {
		return nil
	}
	v := int64(a.UserID)
	return &v
}

func actorAgentPtr(a mutationlog.Actor) *int64 {
	if a.AgentID == 0 {
		return nil
	}
	v := int64(a.AgentID)
	return &v
}

func int32ID(id int64) (int32, bool) {
	if id <= 0 || id > math.MaxInt32 {
		return 0, false
	}
	return int32(id), true
}

// AddComment authorizes the principal, then writes the comment through
// [WriteComment] and records the audit entry after the commit.
func (s *Service) AddComment(ctx context.Context, principal execution.Principal, target types.PublicID, body string) (Comment, error) {
	if err := requireCommentBody(body); err != nil {
		return Comment{}, err
	}
	if err := s.checkReadiness(true); err != nil {
		return Comment{}, err
	}
	var (
		out     Comment
		actor   mutationlog.Actor
		written WrittenComment
	)
	err := dbretry.InTx(ctx, s.DB, "taskservice.AddComment", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		authz, err := execution.AuthorizeTaskTx(ctx, tx, principal, target, execution.CapabilityAddComment)
		if err != nil {
			return err
		}
		qtx := s.Queries.WithTx(tx.RawTx())
		actor = mutationlog.Actor{UserID: principal.UserID(), WorkspaceID: principal.WorkspaceID()}
		args := WriteArgs{
			TaskID:       authz.Task.ID,
			TaskPublicID: target,
			Body:         body,
			Payload:      map[string]any{"authorKind": AuthorKindUser},
		}
		var authorPublic, authorName string
		if authz.Agent != nil {
			actor = mutationlog.Actor{AgentID: authz.Agent.ID, WorkspaceID: principal.WorkspaceID()}
			args.AuthorAgentID = sql.NullInt64{Int64: int64(authz.Agent.ID), Valid: true}
			args.Payload["authorKind"] = AuthorKindAgent
			args.Payload["authorAgentId"] = authz.Agent.PublicID.String()
			if authz.DelegatedByPublicID != (types.PublicID{}) {
				args.Payload["delegatedByUserId"] = authz.DelegatedByPublicID.String()
			}
			authorPublic = authz.Agent.PublicID.String()
			authorName = authz.Agent.Name
		} else {
			userID := principal.UserID()
			args.AuthorUserID = sql.NullInt64{Int64: int64(userID), Valid: true}
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
		args.Actor = actor

		written, err = WriteComment(ctx, tx, args)
		if err != nil {
			return err
		}
		createdAt, err := qtx.GetCommentCreatedAt(ctx, generated.GetCommentCreatedAtParams{
			WorkspaceID: principal.WorkspaceID(),
			PublicID:    written.PublicID,
		})
		if err != nil {
			return err
		}
		out = Comment{
			ID:                written.PublicID,
			AuthorID:          authorPublic,
			AuthorKind:        AuthorKindUser,
			AuthorDisplayName: authorName,
			Body:              body,
			CreatedAt:         createdAt,
		}
		if authz.Agent != nil {
			out.AuthorKind = AuthorKindAgent
			out.AuthorID = ""
			out.AuthorAgentID = authorPublic
		}
		return nil
	})
	if err != nil {
		return Comment{}, err
	}
	s.Mutations.RecordTxAudit(ctx, actor, mutationlog.Mutation{
		AuditAction:  "comment.create",
		ResourceType: "comment",
		ResourceID:   written.PublicID.String(),
		Payload:      written.Payload,
		CallSite:     "taskservice.AddComment (WriteComment appended the event)",
	})
	return out, nil
}
