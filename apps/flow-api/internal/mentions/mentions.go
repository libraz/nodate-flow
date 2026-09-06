// Package mentions keeps the mentions table in step with the body each
// mention was written in. Every writer of a task description or a comment
// goes through [SyncTaskDescription] or [SyncComment], so the table says
// what the current bodies say rather than what some of them once said.
//
// The table is a cache: every row is re-derivable from the body it came
// from, which is why a sync clears before it re-extracts. The clear runs
// even when the body is empty. A body edited down to nothing has to lose
// its mentions, and a person still being notified about a mention that is
// no longer written anywhere has no way to make it stop.
//
// See [Extract] for the notation and for what it deliberately does not
// treat as a mention.
package mentions

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// TaskDescriptionArgs describes the task description being synced.
type TaskDescriptionArgs struct {
	// WorkspaceID bounds both the clear and the resolution. It is the
	// tenant boundary, not a filter: see [syncBody].
	WorkspaceID uint32
	// TaskID is the internal id of the task carrying the description.
	TaskID uint32
	// TaskPublicID names the task in the event payload.
	TaskPublicID types.PublicID
	// ActorUserID is the internal id of whoever wrote the body, nil for a
	// body a process produced.
	ActorUserID *int64
	// Body is the description as it is being stored.
	Body string
}

// CommentArgs describes the comment being synced.
type CommentArgs struct {
	WorkspaceID uint32
	// TaskID is the internal id of the task the comment belongs to. It is
	// recorded on the comment's mentions as well as the description's,
	// because ListMentionsForTask reads mentions by task_id and a row that
	// left it null is a mention that query cannot see.
	TaskID uint32
	// TaskPublicID names the task in the event payload.
	TaskPublicID types.PublicID
	// CommentID is the internal id of the comment carrying the body.
	CommentID uint32
	// CommentPublicID names the comment in the event payload.
	CommentPublicID types.PublicID
	// ActorUserID is the internal id of the comment's author, nil for a
	// comment a process produced.
	ActorUserID *int64
	// Body is the comment as it is being stored.
	Body string
}

// SyncTaskDescription replaces the task_description mentions recorded for
// a task with the ones args.Body names.
//
// db must be the commit boundary the description itself is written
// through: the clear and the re-extraction have to become durable with
// the body they describe, and the event this appends fans out only once
// that commit is observable.
func SyncTaskDescription(ctx context.Context, db dbretry.CommitBoundary, args TaskDescriptionArgs) error {
	if args.TaskID == 0 {
		return fmt.Errorf("mentions: task description sync needs a task id")
	}
	return syncBody(ctx, db, bodyParams{
		workspaceID: args.WorkspaceID,
		source:      generated.MentionsSourceTaskDescription,
		taskID:      nullInt32(args.TaskID),
		actorUserID: args.ActorUserID,
		body:        args.Body,
		payload: map[string]any{
			"taskId": args.TaskPublicID.String(),
			"source": string(generated.MentionsSourceTaskDescription),
		},
	})
}

// SyncComment replaces the mentions recorded for a comment with the ones
// args.Body names.
//
// db carries the same requirement as in [SyncTaskDescription].
func SyncComment(ctx context.Context, db dbretry.CommitBoundary, args CommentArgs) error {
	if args.TaskID == 0 || args.CommentID == 0 {
		return fmt.Errorf("mentions: comment sync needs a task id and a comment id")
	}
	return syncBody(ctx, db, bodyParams{
		workspaceID: args.WorkspaceID,
		source:      generated.MentionsSourceComment,
		taskID:      nullInt32(args.TaskID),
		commentID:   nullInt32(args.CommentID),
		actorUserID: args.ActorUserID,
		body:        args.Body,
		payload: map[string]any{
			"taskId":    args.TaskPublicID.String(),
			"commentId": args.CommentPublicID.String(),
			"source":    string(generated.MentionsSourceComment),
		},
	})
}

// bodyParams is what the two entry points differ by, reduced to what the
// shared sequence needs.
type bodyParams struct {
	workspaceID uint32
	source      generated.MentionsSource
	taskID      sql.NullInt32
	commentID   sql.NullInt32
	actorUserID *int64
	body        string
	// payload names the subject of the event; syncBody adds the mentioned
	// users to it.
	payload map[string]any
}

// syncBody clears the rows this body owns, re-extracts them and records
// the event, all on the caller's commit boundary.
func syncBody(ctx context.Context, db dbretry.CommitBoundary, p bodyParams) error {
	q := generated.New(db)
	// The clear runs before the body is looked at. "No mentions" is a
	// state the table has to be able to reach, and it is reached by a body
	// that no longer names anyone — including an empty one.
	if err := clearRows(ctx, q, p); err != nil {
		return err
	}
	ids := Extract(p.body)
	if len(ids) == 0 {
		return nil
	}
	// Resolution is batched and workspace-scoped. A body may name any
	// UUID, so an id belonging to another tenant, to a former member, or
	// to nobody at all simply comes back absent — and because the caller
	// sees the same nothing in all three cases, the table cannot be used
	// to ask whether a given user exists.
	members, err := q.FindWorkspaceMemberUserInternalIdsByPublicIds(ctx, generated.FindWorkspaceMemberUserInternalIdsByPublicIdsParams{
		WorkspaceID: p.workspaceID,
		PublicIds:   ids,
	})
	if err != nil {
		return fmt.Errorf("mentions: resolve mentioned users: %w", err)
	}
	if len(members) == 0 {
		return nil
	}
	actor := sql.NullInt32{}
	if p.actorUserID != nil {
		actor = sql.NullInt32{Int32: int32(*p.actorUserID), Valid: true} //#nosec G115 -- actor user id sourced from the session, fits int32 within realistic deployments
	}
	mentioned := make([]string, 0, len(members))
	for _, m := range members {
		// A self-mention is written like any other row. The notification
		// fan-out excludes the actor, so it costs no delivery, and skipping
		// it here would instead leave "who is named in this body" answered
		// differently depending on who wrote it.
		if _, err := q.CreateMention(ctx, generated.CreateMentionParams{
			PublicID:        types.New(),
			WorkspaceID:     p.workspaceID,
			MentionedUserID: m.ID,
			ActorUserID:     actor,
			TaskID:          p.taskID,
			CommentID:       p.commentID,
			Source:          p.source,
		}); err != nil {
			return fmt.Errorf("mentions: create mention: %w", err)
		}
		mentioned = append(mentioned, m.PublicID.String())
	}
	// Public ids only: eventlog.ValidatePayloadIDs refuses an append whose
	// payload carries an internal key. The fan-out resolves these back
	// through the same workspace-scoped lookup used above, so it inherits
	// the membership check rather than repeating it.
	p.payload["mentionedUserIds"] = mentioned
	var taskID *int64
	if p.taskID.Valid {
		id := int64(p.taskID.Int32)
		taskID = &id
	}
	if err := eventbus.Append(ctx, db, eventbus.Event{
		Type:        eventbus.MentionCreated,
		WorkspaceID: p.workspaceID,
		ActorUserID: p.actorUserID,
		TaskID:      taskID,
		Payload:     p.payload,
	}); err != nil {
		return fmt.Errorf("mentions: append event: %w", err)
	}
	return nil
}

// clearRows removes the rows the body owns, which is what makes the sync
// a replacement rather than an accumulation. The scope follows the
// source: a description owns the task's task_description rows, a comment
// owns its own, and neither clears the other's.
func clearRows(ctx context.Context, q *generated.Queries, p bodyParams) error {
	switch p.source {
	case generated.MentionsSourceComment:
		if err := q.DeleteMentionsForComment(ctx, generated.DeleteMentionsForCommentParams{
			CommentID:   p.commentID,
			WorkspaceID: p.workspaceID,
		}); err != nil {
			return fmt.Errorf("mentions: clear comment mentions: %w", err)
		}
	default:
		if err := q.DeleteMentionsForTaskDescription(ctx, generated.DeleteMentionsForTaskDescriptionParams{
			TaskID:      p.taskID,
			WorkspaceID: p.workspaceID,
		}); err != nil {
			return fmt.Errorf("mentions: clear task description mentions: %w", err)
		}
	}
	return nil
}

// nullInt32 renders an internal id as the nullable column type the
// generated params take. Zero means "no row": a task description has no
// comment, and the column is NULL for it.
func nullInt32(id uint32) sql.NullInt32 {
	if id == 0 {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: int32(id), Valid: true} //#nosec G115 -- internal ids are INT UNSIGNED, fit int32 within realistic deployments
}
